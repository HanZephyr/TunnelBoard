package biz

import (
	"errors"
	"fmt"
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
	Stop() error
	Done() <-chan struct{}
	Events() <-chan forward.RuntimeEvent
	Err() error
	LastLatency() (time.Duration, bool)
}

// RuntimeBiz 是计划文档中的 Forward 运行时 Module：按 Vault 配置启停 Forward、
// 跟踪断线重连状态机，不承载目录 CRUD（复用 CatalogBiz）。
type RuntimeBiz struct {
	store   VaultStore
	catalog *CatalogBiz
	newRun  func(fw model.Forward, hosts []model.SSHHost, verifier forward.HostKeyVerifier) runHandle

	mu     sync.Mutex
	runs   map[int]runHandle
	states map[int]RuntimeStatus
}

// NewRuntimeBiz 以默认工厂（forward.NewLocalForward）组装运行时 Module。
func NewRuntimeBiz(store VaultStore) *RuntimeBiz {
	b := &RuntimeBiz{
		store:   store,
		catalog: NewCatalogBiz(store),
		runs:    map[int]runHandle{},
		states:  map[int]RuntimeStatus{},
	}
	b.newRun = func(fw model.Forward, hosts []model.SSHHost, verifier forward.HostKeyVerifier) runHandle {
		return forward.NewLocalForward(fw, hosts, verifier)
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
	_, running := b.runs[id]
	b.mu.Unlock()
	if running {
		return nil
	}

	data, err := b.store.Load()
	if err != nil {
		return err
	}
	fw, ok := findForwardByID(data, id)
	if !ok {
		return fmt.Errorf("forward %d not found", id)
	}
	hosts, err := b.catalog.ResolveChain(fw)
	if err != nil {
		return err
	}

	run := b.newRun(fw, hosts, b.hostKeyVerifier())
	if err := run.Start(); err != nil {
		b.setState(id, RuntimeStateError, err.Error())
		return err
	}

	b.mu.Lock()
	if _, ok := b.runs[id]; ok {
		// 并发 Start 同一 id：后到者停掉多余实例，先入者生效。
		b.mu.Unlock()
		_ = run.Stop()
		return nil
	}
	b.runs[id] = run
	st := RuntimeStatus{ForwardID: id, Status: RuntimeStateRunning}
	if latency, ok := run.LastLatency(); ok {
		st.LatencyMs = latency.Milliseconds()
	}
	b.states[id] = st
	b.mu.Unlock()

	go b.watch(id, run)
	return nil
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
	return b.StartMany(ids), nil
}

// Stop 停止一条 Forward；未运行时幂等。主动停止后状态为 stopped。
func (b *RuntimeBiz) Stop(id int) error {
	b.mu.Lock()
	run, ok := b.runs[id]
	if ok {
		delete(b.runs, id)
		b.states[id] = RuntimeStatus{ForwardID: id, Status: RuntimeStateStopped}
	}
	b.mu.Unlock()
	if !ok {
		return nil
	}
	return run.Stop()
}

// Shutdown 停止全部运行中的实例（应用显式退出路径由上层调用）。
func (b *RuntimeBiz) Shutdown() {
	b.mu.Lock()
	runs := make([]runHandle, 0, len(b.runs))
	for id, run := range b.runs {
		runs = append(runs, run)
		delete(b.runs, id)
		b.states[id] = RuntimeStatus{ForwardID: id, Status: RuntimeStateStopped}
	}
	b.mu.Unlock()
	for _, run := range runs {
		_ = run.Stop()
	}
}

// Status 返回单条 Forward 的运行时状态；从未启动过返回 false。
func (b *RuntimeBiz) Status(id int) (RuntimeStatus, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	st, ok := b.states[id]
	return st, ok
}

// Snapshot 返回 Vault 中全部 Forward 的运行时状态；未运行的给 stopped 默认。
func (b *RuntimeBiz) Snapshot() []RuntimeStatus {
	data, err := b.store.Load()
	if err != nil {
		return nil
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
	return out
}

func (b *RuntimeBiz) setState(id int, status string, lastError string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	st := b.states[id]
	st.ForwardID = id
	st.Status = status
	st.LastError = lastError
	b.states[id] = st
}

// watch 消费运行时事件驱动状态机；Done 关闭后按 Err 落终态并清理 runs 表。
// 主动 Stop 路径 Err() 为 nil，落 stopped，不会误覆盖为 error。
func (b *RuntimeBiz) watch(id int, run runHandle) {
	done := run.Done()
	events := run.Events()
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			b.handleEvent(id, run, ev)
		case <-done:
			b.mu.Lock()
			defer b.mu.Unlock()
			delete(b.runs, id)
			if err := run.Err(); err != nil {
				st := b.states[id]
				st.ForwardID = id
				st.Status = RuntimeStateError
				st.LastError = err.Error()
				b.states[id] = st
			} else {
				b.states[id] = RuntimeStatus{ForwardID: id, Status: RuntimeStateStopped}
			}
			return
		}
	}
}

func (b *RuntimeBiz) handleEvent(id int, run runHandle, ev forward.RuntimeEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()
	st := b.states[id]
	st.ForwardID = id
	switch ev.Type {
	case forward.RuntimeEventDisconnected:
		st.Status = RuntimeStateReconnecting
		if ev.Err != nil {
			st.LastError = ev.Err.Error()
		}
	case forward.RuntimeEventReconnected:
		st.Status = RuntimeStateRunning
		st.LastError = ""
		if latency, ok := run.LastLatency(); ok {
			st.LatencyMs = latency.Milliseconds()
		}
	}
	b.states[id] = st
}

func findForwardByID(data model.VaultData, id int) (model.Forward, bool) {
	for _, fw := range data.Forwards {
		if fw.ID == id {
			return fw, true
		}
	}
	return model.Forward{}, false
}
