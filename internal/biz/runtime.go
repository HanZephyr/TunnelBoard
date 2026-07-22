package biz

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sort"
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

// SuspendedForward 是 Runtime 捕获的不可伪造暂停事实；generation 仅由后端生成。
type SuspendedForward struct {
	ForwardID  int    `json:"forwardId"`
	Generation uint64 `json:"-"`
}

type RuntimeSuspendPlan struct {
	Entries []SuspendedForward `json:"entries"`
}

type RuntimeResumeResult struct {
	Started []int          `json:"started"`
	Errors  map[int]string `json:"errors,omitempty"`
}

type AffectedForward struct {
	ForwardID         int    `json:"forwardId"`
	RunningGeneration uint64 `json:"runningGeneration,omitempty"`
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
	store          VaultStore
	catalog        *CatalogBiz
	pool           *forward.SSHConnPool
	newRun         func(fw model.Forward, hosts []model.SSHHost, verifier forward.HostKeyVerifier) runHandle
	preflightChain func(context.Context, []model.SSHHost, forward.HostKeyVerifier) error

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
	b.preflightChain = forward.TestSSHChainConnection
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

// SuspendAll 可恢复地暂停当前运行集合。调用方提供一个总 deadline，所有 Stop
// 并行共享该 context；它不会设置永久 closing，也不会从 Vault 推导恢复列表。
func (b *RuntimeBiz) SuspendAll(ctx context.Context) (RuntimeSuspendPlan, error) {
	return b.Suspend(ctx, nil)
}

// Suspend 只暂停 ids 中当前存在的运行代；ids 为空表示全部。
func (b *RuntimeBiz) Suspend(ctx context.Context, ids []int) (RuntimeSuspendPlan, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	wanted := make(map[int]struct{}, len(ids))
	for _, id := range ids {
		wanted[id] = struct{}{}
	}
	b.mu.Lock()
	entries := make(map[int]*runEntry)
	plan := RuntimeSuspendPlan{}
	for id, entry := range b.runs {
		if len(wanted) != 0 {
			if _, ok := wanted[id]; !ok {
				continue
			}
		}
		if entry.phase == runStopping {
			continue
		}
		entry.phase = runStopping
		entries[id] = entry
		plan.Entries = append(plan.Entries, SuspendedForward{ForwardID: id, Generation: entry.generation})
	}
	b.mu.Unlock()
	sort.Slice(plan.Entries, func(i, j int) bool { return plan.Entries[i].ForwardID < plan.Entries[j].ForwardID })

	type stopResult struct {
		id    int
		entry *runEntry
		err   error
	}
	results := make(chan stopResult, len(entries))
	for id, entry := range entries {
		go func(id int, entry *runEntry) {
			if entry.run == nil {
				results <- stopResult{id: id, entry: entry}
				return
			}
			results <- stopResult{id: id, entry: entry, err: entry.run.Stop(ctx)}
		}(id, entry)
	}
	var failures []string
	for range entries {
		result := <-results
		b.mu.Lock()
		current := b.runs[result.id]
		if current == result.entry && current.generation == result.entry.generation {
			if result.err == nil {
				delete(b.runs, result.id)
				b.states[result.id] = RuntimeStatus{ForwardID: result.id, Status: RuntimeStateStopped}
			} else {
				b.states[result.id] = RuntimeStatus{ForwardID: result.id, Status: RuntimeStateError, LastError: result.err.Error()}
				if result.entry.run != nil {
					go b.finishStopInBackground(result.id, result.entry.generation, result.entry.run)
				}
			}
		}
		b.mu.Unlock()
		if result.err != nil {
			failures = append(failures, fmt.Sprintf("forward %d: %v", result.id, result.err))
		}
	}
	if len(failures) != 0 {
		sort.Strings(failures)
		return plan, fmt.Errorf("suspend runtime: %s", strings.Join(failures, "; "))
	}
	return plan, nil
}

// Resume 只恢复 Suspend 返回的后端计划，并通过 Start 分配全新 generation。
func (b *RuntimeBiz) Resume(ctx context.Context, plan RuntimeSuspendPlan) RuntimeResumeResult {
	result := RuntimeResumeResult{Errors: map[int]string{}}
	for _, entry := range plan.Entries {
		if err := ctx.Err(); err != nil {
			result.Errors[entry.ForwardID] = err.Error()
			continue
		}
		if err := b.Start(entry.ForwardID); err != nil {
			result.Errors[entry.ForwardID] = err.Error()
			continue
		}
		result.Started = append(result.Started, entry.ForwardID)
	}
	return result
}

// AffectedForHost 扫描整条 ChainHostIDs，并附带当前运行代供应用层做 stale 检查。
func (b *RuntimeBiz) AffectedForHost(hostID int) []AffectedForward {
	data, err := b.store.Load()
	if err != nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	result := make([]AffectedForward, 0)
	for _, fw := range data.Forwards {
		for _, id := range fw.ChainHostIDs {
			if id != hostID {
				continue
			}
			item := AffectedForward{ForwardID: fw.ID}
			if entry := b.runs[fw.ID]; entry != nil && entry.phase != runStopping {
				item.RunningGeneration = entry.generation
			}
			result = append(result, item)
			break
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ForwardID < result[j].ForwardID })
	return result
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

func (b *RuntimeBiz) RetireHost(hostID int) {
	b.pool.RetireHost(hostID)
}

// PreflightHostChange 使用候选 Host 替换每条受影响 Forward 链中的同 ID 节点，
// 通过独占短连接验证完整 SSH 握手、认证和逐跳指纹。它不停止 Runtime、不写
// Vault，也不把候选连接放入连接池；key 0 表示无 Forward 引用时的直接 Host 预检。
func (b *RuntimeBiz) PreflightHostChange(ctx context.Context, proposed model.SSHHost, affected []AffectedForward) map[int]string {
	data, err := b.store.Load()
	if err != nil {
		return map[int]string{0: err.Error()}
	}
	if len(affected) == 0 {
		if err := b.preflightChain(ctx, []model.SSHHost{proposed}, b.hostKeyVerifier()); err != nil {
			return map[int]string{0: err.Error()}
		}
		return nil
	}
	hostsByID := make(map[int]model.SSHHost, len(data.SSHHosts))
	for _, host := range data.SSHHosts {
		hostsByID[host.ID] = host
	}
	forwardsByID := make(map[int]model.Forward, len(data.Forwards))
	for _, fw := range data.Forwards {
		forwardsByID[fw.ID] = fw
	}
	result := map[int]string{}
	for _, item := range affected {
		if err := ctx.Err(); err != nil {
			result[item.ForwardID] = err.Error()
			continue
		}
		fw, ok := forwardsByID[item.ForwardID]
		if !ok {
			result[item.ForwardID] = fmt.Sprintf("forward %d not found", item.ForwardID)
			continue
		}
		chain := make([]model.SSHHost, 0, len(fw.ChainHostIDs))
		valid := true
		for _, hostID := range fw.ChainHostIDs {
			host, exists := hostsByID[hostID]
			if !exists {
				result[item.ForwardID] = fmt.Sprintf("ssh host %d not found", hostID)
				valid = false
				break
			}
			if hostID == proposed.ID {
				host = proposed
			}
			chain = append(chain, host)
		}
		if !valid {
			continue
		}
		if err := b.preflightChain(ctx, chain, b.hostKeyVerifier()); err != nil {
			result[item.ForwardID] = err.Error()
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// TestSSHHostConnection 使用独占短连接验证草稿主机的握手、认证和指纹；绝不进入连接池。
func (b *RuntimeBiz) TestSSHHostConnection(ctx context.Context, host model.SSHHost) error {
	return forward.TestSSHChainConnection(ctx, []model.SSHHost{host}, b.hostKeyVerifier())
}

// TestForwardConnection 使用独占短连接检查未持久化的 Forward 草稿；不修改 Runtime 状态。
func (b *RuntimeBiz) TestForwardConnection(ctx context.Context, fw model.Forward) (time.Duration, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	hosts, err := b.catalog.ResolveChain(fw)
	if err != nil {
		return 0, err
	}
	return forward.TestForwardConnection(fw, hosts, b.hostKeyVerifier())
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
