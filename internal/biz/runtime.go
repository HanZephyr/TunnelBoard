package biz

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/HanZephyr/TunnelBoard/internal/forward"
	"github.com/HanZephyr/TunnelBoard/internal/model"

	"golang.org/x/crypto/ssh"
)

// 运行时状态机的终态与中间态；STATUS 仅供展示，不持久化（Vault 不存运行时状态）。
const (
	RuntimeStateRunning      = "running"
	RuntimeStateReconnecting = "reconnecting"
	RuntimeStateStopped      = "stopped"
	RuntimeStateError        = "error"
)

// 指纹核验哨兵：未知指纹（需用户确认）与指纹变化（必须阻断）在 Start 与重连路径
// 都会出现，调用方可用 errors.Is 区分并映射到确认流程。
var (
	ErrHostKeyUnknown  = errors.New("biz: ssh host key unknown")
	ErrHostKeyMismatch = errors.New("biz: ssh host key mismatch")
	ErrRuntimeClosing  = errors.New("biz: forward runtime is closing")
	ErrForwardStopping = errors.New("biz: forward is stopping")
)

// RuntimeStatus 是一条 Forward 的运行时快照（只存在于内存）。
type RuntimeStatus struct {
	ForwardID int    `json:"forwardId"`
	Status    string `json:"status"` // running | reconnecting | stopped | error
	LastError string `json:"lastError,omitempty"`
	LatencyMs int64  `json:"latencyMs"`
}

// runHandle 是运行时实例的接缝；*forward.LocalForward 天然满足该接口。
type runHandle interface {
	Start() error
	Stop(context.Context) error
	Done() <-chan struct{}
	Events() <-chan forward.RuntimeEvent
	Err() error
	LastLatency() (time.Duration, bool)
}

type runPhase uint8

const (
	runStarting runPhase = iota
	runRunning
	runStopping
)

type runEntry struct {
	generation      uint64
	phase           runPhase
	run             runHandle
	listenerAddress string
}

// RuntimeBiz 是计划文档中的 Forward 运行时 Module：按 Vault 配置启停 Forward、
// 跟踪断线重连状态机，不承载目录 CRUD（复用 CatalogBiz）。
// pool 按首跳复用 SSH 连接：引用同一 SSH 主机的多条 Forward 共享一条首跳连接。
type RuntimeBiz struct {
	store   VaultStore
	catalog *CatalogBiz
	pool    *forward.SSHConnPool
	newRun  func(fw model.Forward, hosts []model.SSHHost, verifier forward.HostKeyVerifier) runHandle

	mu             sync.Mutex
	runs           map[int]*runEntry
	states         map[int]RuntimeStatus
	nextGeneration uint64
	closing        bool
}

// NewRuntimeBiz 以默认工厂（forward.NewLocalForward + 首跳连接池）组装运行时 Module。
func NewRuntimeBiz(store VaultStore) *RuntimeBiz {
	b := &RuntimeBiz{
		store:   store,
		catalog: NewCatalogBiz(store),
		pool:    forward.NewSSHConnPool(),
		runs:    map[int]*runEntry{},
		states:  map[int]RuntimeStatus{},
	}
	b.newRun = func(fw model.Forward, hosts []model.SSHHost, verifier forward.HostKeyVerifier) runHandle {
		return forward.NewLocalForward(fw, hosts, verifier, b.pool.LeaseChain)
	}
	return b
}

// hostKeyVerifier 桥接 Vault 指纹库：每次核验现读 store（TOFU 确认后立即生效），
// trusted 放行；mismatch / unknown 分别以哨兵错误阻断。
func (b *RuntimeBiz) hostKeyVerifier() forward.HostKeyVerifier {
	return func(host string, port int, key ssh.PublicKey) error {
		data, err := b.store.Load()
		if err != nil {
			return err
		}
		fingerprint := ssh.FingerprintSHA256(key)
		entry, status := data.CheckHostKey(host, port, fingerprint)
		switch status {
		case model.TrustTrusted:
			return nil
		case model.TrustMismatch:
			return fmt.Errorf("%w: %s:%d fingerprint changed (stored %s, got %s)",
				ErrHostKeyMismatch, host, port, entry.FingerprintSHA256, fingerprint)
		default:
			return fmt.Errorf("%w: %s:%d fingerprint %s", ErrHostKeyUnknown, host, port, fingerprint)
		}
	}
}

// Start 启动一条 Forward：加载配置、解析主机链、创建并启动运行时实例。
// 已在运行时幂等返回；run.Start() 失败记 error 状态（不存 handle，可重试）。
func (b *RuntimeBiz) Start(id int) error {
	b.mu.Lock()
	if b.closing {
		b.mu.Unlock()
		return ErrRuntimeClosing
	}
	if entry := b.runs[id]; entry != nil {
		if entry.phase == runStopping {
			b.mu.Unlock()
			return ErrForwardStopping
		}
		b.mu.Unlock()
		return nil
	}
	b.nextGeneration++
	generation := b.nextGeneration
	b.runs[id] = &runEntry{generation: generation, phase: runStarting}
	b.mu.Unlock()

	data, err := b.store.Load()
	if err != nil {
		b.finishStartFailure(id, generation, err)
		return err
	}
	fw, ok := findForwardByID(data, id)
	if !ok {
		err = fmt.Errorf("forward %d not found", id)
		b.finishStartFailure(id, generation, err)
		return err
	}
	hosts, err := b.catalog.ResolveChain(fw)
	if err != nil {
		b.finishStartFailure(id, generation, err)
		return err
	}

	slog.Info("forward start requested", "forward_id", id, "name", fw.Name)
	run := b.newRun(fw, hosts, b.hostKeyVerifier())
	if err := run.Start(); err != nil {
		b.finishStartFailure(id, generation, err)
		slog.Error("forward start failed", "forward_id", id, "name", fw.Name, "err", err)
		return err
	}

	b.mu.Lock()
	entry := b.runs[id]
	if entry == nil || entry.generation != generation || entry.phase != runStarting || b.closing {
		b.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		stopErr := run.Stop(ctx)
		cancel()
		b.mu.Lock()
		entry = b.runs[id]
		if entry != nil && entry.generation == generation && entry.run == nil {
			delete(b.runs, id)
			if stopErr != nil {
				b.states[id] = RuntimeStatus{ForwardID: id, Status: RuntimeStateError, LastError: stopErr.Error()}
			} else {
				b.states[id] = RuntimeStatus{ForwardID: id, Status: RuntimeStateStopped}
			}
		}
		b.mu.Unlock()
		return ErrForwardStopping
	}
	entry.run = run
	entry.phase = runRunning
	entry.listenerAddress = localListenerAddress(fw)
	st := RuntimeStatus{ForwardID: id, Status: RuntimeStateRunning}
	if latency, ok := run.LastLatency(); ok {
		st.LatencyMs = latency.Milliseconds()
	}
	b.states[id] = st
	b.mu.Unlock()

	go b.watch(id, generation, run)
	slog.Info("forward started", "forward_id", id, "name", fw.Name)
	return nil
}

// LocalListenerOwner 只读返回当前运行代对本地监听地址的所有权。
// 它不尝试绑定端口，也不泄露可变的 runs 表，供端口预检区分自身监听与外部占用。
func (b *RuntimeBiz) LocalListenerOwner(host string, port int) (int, bool) {
	host = strings.TrimSpace(host)
	if host == "" {
		host = "127.0.0.1"
	}
	want := net.JoinHostPort(host, strconv.Itoa(port))
	b.mu.Lock()
	defer b.mu.Unlock()
	for id, entry := range b.runs {
		if entry.phase == runRunning && entry.run != nil && entry.listenerAddress == want {
			return id, true
		}
	}
	return 0, false
}

func localListenerAddress(fw model.Forward) string {
	mode := strings.TrimSpace(fw.Mode)
	if mode == "remote" {
		return ""
	}
	host := strings.TrimSpace(fw.LocalHost)
	if host == "" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, strconv.Itoa(fw.LocalPort))
}

// StartMany 批量启动，逐项返回错误（无错项不出现在 map 中）。
func (b *RuntimeBiz) StartMany(ids []int) map[int]error {
	errs := make(map[int]error)
	for _, id := range ids {
		if err := b.Start(id); err != nil {
			errs[id] = err
		}
	}
	return errs
}

// StartAutoStart 启动 Vault 中全部 AutoStart=true 的 Forward；单项失败只记
// 该项状态，不中断其他项。
func (b *RuntimeBiz) StartAutoStart() (map[int]error, error) {
	data, err := b.store.Load()
	if err != nil {
		return nil, err
	}
	ids := make([]int, 0, len(data.Forwards))
	for _, fw := range data.Forwards {
		if fw.AutoStart {
			ids = append(ids, fw.ID)
		}
	}
	slog.Info("auto start forwards", "count", len(ids))
	return b.StartMany(ids), nil
}

// Stop 停止一条 Forward；未运行时幂等。主动停止后状态为 stopped。
func (b *RuntimeBiz) Stop(id int) error {
	b.mu.Lock()
	entry := b.runs[id]
	if entry == nil {
		b.mu.Unlock()
		return nil
	}
	if entry.phase == runStopping {
		b.mu.Unlock()
		return ErrForwardStopping
	}
	entry.phase = runStopping
	generation := entry.generation
	run := entry.run
	b.mu.Unlock()
	if run == nil {
		return nil
	}
	slog.Info("forward stop requested", "forward_id", id)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := run.Stop(ctx)
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.isCurrentLocked(id, generation, run) {
		return err
	}
	if err != nil {
		st := b.states[id]
		st.ForwardID = id
		st.Status = RuntimeStateError
		st.LastError = err.Error()
		b.states[id] = st
		go b.finishStopInBackground(id, generation, run)
		return err
	}
	delete(b.runs, id)
	b.states[id] = RuntimeStatus{ForwardID: id, Status: RuntimeStateStopped}
	return nil
}

// Shutdown 停止全部运行中的实例（应用显式退出路径由上层调用），
// 随后关闭连接池中的全部池化首跳连接。
func (b *RuntimeBiz) Shutdown() {
	b.mu.Lock()
	b.closing = true
	entries := make(map[int]*runEntry, len(b.runs))
	for id, entry := range b.runs {
		entry.phase = runStopping
		entries[id] = entry
	}
	b.mu.Unlock()
	slog.Info("forward runtime shutdown", "count", len(entries))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for id, entry := range entries {
		if entry.run == nil {
			continue
		}
		wg.Add(1)
		go func(id int, entry *runEntry) {
			defer wg.Done()
			err := entry.run.Stop(ctx)
			b.mu.Lock()
			defer b.mu.Unlock()
			if !b.isCurrentLocked(id, entry.generation, entry.run) {
				return
			}
			if err != nil {
				b.states[id] = RuntimeStatus{ForwardID: id, Status: RuntimeStateError, LastError: err.Error()}
				return
			}
			delete(b.runs, id)
			b.states[id] = RuntimeStatus{ForwardID: id, Status: RuntimeStateStopped}
		}(id, entry)
	}
	wg.Wait()
	b.pool.CloseAll()
}

// PoolStats 返回 SSH 连接池快照（Overview 展示各首跳主机的连接复用情况）。
func (b *RuntimeBiz) PoolStats() []forward.PoolStat {
	return b.pool.Stats()
}

// Status 返回单条 Forward 的运行时状态；从未启动过返回 false。
func (b *RuntimeBiz) Status(id int) (RuntimeStatus, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	st, ok := b.states[id]
	return st, ok
}

// Snapshot 返回 Vault 中全部 Forward 的运行时状态；未运行的给 stopped 默认。
// 读取 Vault 失败时返回错误（调用方应让失败可见，而不是退化为"全部已停止"）。
func (b *RuntimeBiz) Snapshot() ([]RuntimeStatus, error) {
	data, err := b.store.Load()
	if err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]RuntimeStatus, 0, len(data.Forwards))
	for _, fw := range data.Forwards {
		if st, ok := b.states[fw.ID]; ok {
			out = append(out, st)
		} else {
			out = append(out, RuntimeStatus{ForwardID: fw.ID, Status: RuntimeStateStopped})
		}
	}
	return out, nil
}

func (b *RuntimeBiz) finishStartFailure(id int, generation uint64, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	entry := b.runs[id]
	if entry == nil || entry.generation != generation {
		return
	}
	delete(b.runs, id)
	b.states[id] = RuntimeStatus{ForwardID: id, Status: RuntimeStateError, LastError: err.Error()}
}

// watch 消费运行时事件驱动状态机；Done 关闭后按 Err 落终态并清理 runs 表。
// 主动 Stop 路径 Err() 为 nil，落 stopped，不会误覆盖为 error。
func (b *RuntimeBiz) watch(id int, generation uint64, run runHandle) {
	done := run.Done()
	events := run.Events()
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			b.handleEvent(id, generation, run, ev)
		case <-done:
			b.mu.Lock()
			if !b.isCurrentLocked(id, generation, run) {
				b.mu.Unlock()
				return
			}
			if b.runs[id].phase == runStopping {
				b.mu.Unlock()
				return
			}
			delete(b.runs, id)
			if err := run.Err(); err != nil {
				st := b.states[id]
				st.ForwardID = id
				st.Status = RuntimeStateError
				st.LastError = err.Error()
				b.states[id] = st
				slog.Warn("forward finalized with error", "forward_id", id, "err", err)
			} else {
				b.states[id] = RuntimeStatus{ForwardID: id, Status: RuntimeStateStopped}
				slog.Info("forward finalized stopped", "forward_id", id)
			}
			b.mu.Unlock()
			return
		}
	}
}

func (b *RuntimeBiz) finishStopInBackground(id int, generation uint64, run runHandle) {
	if err := run.Stop(context.Background()); err != nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.isCurrentLocked(id, generation, run) {
		return
	}
	delete(b.runs, id)
	b.states[id] = RuntimeStatus{ForwardID: id, Status: RuntimeStateStopped}
}

func (b *RuntimeBiz) handleEvent(id int, generation uint64, run runHandle, ev forward.RuntimeEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.isCurrentLocked(id, generation, run) {
		return
	}
	st := b.states[id]
	st.ForwardID = id
	switch ev.Type {
	case forward.RuntimeEventDisconnected:
		st.Status = RuntimeStateReconnecting
		if ev.Err != nil {
			st.LastError = ev.Err.Error()
		}
		slog.Warn("forward disconnected, reconnecting", "forward_id", id, "err", ev.Err)
	case forward.RuntimeEventReconnected:
		st.Status = RuntimeStateRunning
		st.LastError = ""
		if latency, ok := run.LastLatency(); ok {
			st.LatencyMs = latency.Milliseconds()
		}
		slog.Info("forward reconnected", "forward_id", id)
	}
	b.states[id] = st
}

func (b *RuntimeBiz) isCurrentLocked(id int, generation uint64, run runHandle) bool {
	entry := b.runs[id]
	return entry != nil && entry.generation == generation && entry.run == run
}

func findForwardByID(data model.VaultData, id int) (model.Forward, bool) {
	for _, fw := range data.Forwards {
		if fw.ID == id {
			return fw, true
		}
	}
	return model.Forward{}, false
}
