package biz

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/HanZephyr/TunnelBoard/internal/forward"
	"github.com/HanZephyr/TunnelBoard/internal/model"

	"golang.org/x/crypto/ssh"
)

// memStore 是 VaultStore 的内存实现（语义对齐 vault.Store：mutate 失败不落盘）。
// catalog_test.go 的 fakeStore 在外部测试包 biz_test 中，本文件为注入未导出的
// newRun 接缝使用包内测试，无法复用，故保留一份等价实现。
type memStore struct {
	mu   sync.Mutex
	data model.VaultData
}

func (s *memStore) Load() (model.VaultData, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data, nil
}

func (s *memStore) Update(mutate func(*model.VaultData) error) (model.VaultData, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.data
	if err := mutate(&d); err != nil {
		return model.VaultData{}, err
	}
	s.data = d
	return d, nil
}

// fakeRun 实现 runHandle：Start/Stop 记录调用，事件与终结由测试驱动。
type fakeRun struct {
	mu         sync.Mutex
	startErr   error
	started    bool
	stopCalled bool
	done       chan struct{}
	events     chan forward.RuntimeEvent
	err        error
	latency    time.Duration
	hasLatency bool
	fw         model.Forward
	hosts      []model.SSHHost
	doneOnce   sync.Once
}

func newFakeRun() *fakeRun {
	return &fakeRun{
		done:   make(chan struct{}),
		events: make(chan forward.RuntimeEvent, 8),
	}
}

func (f *fakeRun) Start() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.startErr != nil {
		return f.startErr
	}
	f.started = true
	return nil
}

func (f *fakeRun) Stop() error {
	f.doneOnce.Do(func() {
		f.mu.Lock()
		f.stopCalled = true
		f.mu.Unlock()
		close(f.done)
	})
	return nil
}

func (f *fakeRun) Done() <-chan struct{}               { return f.done }
func (f *fakeRun) Events() <-chan forward.RuntimeEvent { return f.events }
func (f *fakeRun) Err() error                          { f.mu.Lock(); defer f.mu.Unlock(); return f.err }
func (f *fakeRun) LastLatency() (time.Duration, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.latency, f.hasLatency
}

// kill 模拟运行实例终结（连接死亡或主动停止殊途同归：done 关闭）。
func (f *fakeRun) kill(err error) {
	f.doneOnce.Do(func() {
		f.mu.Lock()
		f.err = err
		f.mu.Unlock()
		close(f.done)
	})
}

func (f *fakeRun) setLatency(latency time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.latency = latency
	f.hasLatency = true
}

func (f *fakeRun) wasStopped() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.stopCalled
}

// runFactory 捕获每次创建的运行实例，供测试断言与驱动。
type runFactory struct {
	mu       sync.Mutex
	startErr error
	onMake   func(r *fakeRun)
	runs     []*fakeRun
}

func (f *runFactory) make(fw model.Forward, hosts []model.SSHHost, verifier forward.HostKeyVerifier) runHandle {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := newFakeRun()
	r.startErr = f.startErr
	r.fw = fw
	r.hosts = hosts
	if f.onMake != nil {
		f.onMake(r)
	}
	f.runs = append(f.runs, r)
	return r
}

func (f *runFactory) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.runs)
}

func (f *runFactory) last() *fakeRun {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.runs) == 0 {
		return nil
	}
	return f.runs[len(f.runs)-1]
}

func seedRuntimeVault() *memStore {
	return &memStore{data: model.VaultData{
		Version: 1,
		Folders: []model.Folder{{ID: 1, Name: "工作"}},
		SSHHosts: []model.SSHHost{
			{ID: 1, Name: "h1", Host: "10.0.0.1", Port: 22, User: "u", AuthType: "password", Password: "x"},
			{ID: 2, Name: "h2", Host: "10.0.0.2", Port: 2222, User: "u", AuthType: "password", Password: "x"},
		},
		Forwards: []model.Forward{
			{ID: 1, FolderID: 1, Name: "fw1", Mode: "local", ChainHostIDs: []int{1, 2},
				LocalHost: "127.0.0.1", LocalPort: 5001, RemoteHost: "db", RemotePort: 3306},
			{ID: 2, FolderID: 1, Name: "fw2", Mode: "local", ChainHostIDs: []int{2},
				LocalHost: "127.0.0.1", LocalPort: 5002, RemoteHost: "db", RemotePort: 3307, AutoStart: true},
			{ID: 3, FolderID: 1, Name: "fw3", Mode: "dynamic", ChainHostIDs: []int{1},
				LocalHost: "127.0.0.1", LocalPort: 1080, AutoStart: true},
		},
	}}
}

func newTestRuntime(store *memStore, factory *runFactory) *RuntimeBiz {
	b := NewRuntimeBiz(store)
	b.newRun = factory.make
	return b
}

func eventually(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("condition not met within 2s: %s", msg)
}

func testPublicKey(t *testing.T) (ssh.PublicKey, string) {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate ed25519 key failed: %v", err)
	}
	key, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("build ssh public key failed: %v", err)
	}
	return key, ssh.FingerprintSHA256(key)
}

func TestResolveChain(t *testing.T) {
	c := NewCatalogBiz(seedRuntimeVault())

	hosts, err := c.ResolveChain(model.Forward{ID: 1, Name: "fw1", ChainHostIDs: []int{2, 1}})
	if err != nil {
		t.Fatalf("ResolveChain failed: %v", err)
	}
	if len(hosts) != 2 || hosts[0].ID != 2 || hosts[1].ID != 1 {
		t.Fatalf("ResolveChain order = %+v, want [h2 h1]", hosts)
	}

	_, err = c.ResolveChain(model.Forward{ID: 9, Name: "bad", ChainHostIDs: []int{42}})
	if !errors.Is(err, model.ErrRefMissing) {
		t.Fatalf("ResolveChain err = %v, want errors.Is ErrRefMissing", err)
	}
}

func TestRuntimeStartRunning(t *testing.T) {
	factory := &runFactory{onMake: func(r *fakeRun) { r.setLatency(120 * time.Millisecond) }}
	b := newTestRuntime(seedRuntimeVault(), factory)

	if err := b.Start(1); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	if factory.count() != 1 {
		t.Fatalf("factory count = %d, want 1", factory.count())
	}
	run := factory.last()
	if run.fw.ID != 1 {
		t.Fatalf("factory received forward %d, want 1", run.fw.ID)
	}
	if len(run.hosts) != 2 || run.hosts[0].ID != 1 || run.hosts[1].ID != 2 {
		t.Fatalf("chain hosts = %+v, want [h1 h2] in chain order", run.hosts)
	}

	st, ok := b.Status(1)
	if !ok || st.Status != RuntimeStateRunning {
		t.Fatalf("Status = %+v, %v; want running", st, ok)
	}
	if st.LatencyMs != 120 {
		t.Fatalf("LatencyMs = %d, want 120", st.LatencyMs)
	}

	snapshot := b.Snapshot()
	if len(snapshot) != 3 {
		t.Fatalf("Snapshot len = %d, want 3", len(snapshot))
	}
	for _, item := range snapshot {
		want := RuntimeStateStopped
		if item.ForwardID == 1 {
			want = RuntimeStateRunning
		}
		if item.Status != want {
			t.Fatalf("Snapshot[%d].Status = %s, want %s", item.ForwardID, item.Status, want)
		}
	}
}

func TestRuntimeStartIdempotent(t *testing.T) {
	factory := &runFactory{}
	b := newTestRuntime(seedRuntimeVault(), factory)

	if err := b.Start(1); err != nil {
		t.Fatalf("first Start failed: %v", err)
	}
	if err := b.Start(1); err != nil {
		t.Fatalf("second Start failed: %v", err)
	}
	if factory.count() != 1 {
		t.Fatalf("factory count = %d, want 1 (idempotent)", factory.count())
	}
}

func TestRuntimeStartUnknownForward(t *testing.T) {
	b := newTestRuntime(seedRuntimeVault(), &runFactory{})
	if err := b.Start(42); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("Start(42) err = %v, want not found error", err)
	}
}

func TestRuntimeStartFailureRecordsError(t *testing.T) {
	factory := &runFactory{startErr: errors.New("dial tcp: connection refused")}
	b := newTestRuntime(seedRuntimeVault(), factory)

	if err := b.Start(1); err == nil {
		t.Fatalf("Start should return dial error")
	}
	st, ok := b.Status(1)
	if !ok || st.Status != RuntimeStateError {
		t.Fatalf("Status = %+v, %v; want error", st, ok)
	}
	if !strings.Contains(st.LastError, "connection refused") {
		t.Fatalf("LastError = %q, want dial error text", st.LastError)
	}

	// 失败不存 handle：修复后可重试成功。
	factory.mu.Lock()
	factory.startErr = nil
	factory.mu.Unlock()
	if err := b.Start(1); err != nil {
		t.Fatalf("retry Start failed: %v", err)
	}
	if factory.count() != 2 {
		t.Fatalf("factory count = %d, want 2 (retry creates new run)", factory.count())
	}
	if st, _ := b.Status(1); st.Status != RuntimeStateRunning {
		t.Fatalf("Status after retry = %s, want running", st.Status)
	}
}

func TestRuntimeStop(t *testing.T) {
	factory := &runFactory{}
	b := newTestRuntime(seedRuntimeVault(), factory)

	if err := b.Start(1); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	if err := b.Stop(1); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
	if !factory.last().wasStopped() {
		t.Fatalf("run.Stop should be called")
	}
	if st, _ := b.Status(1); st.Status != RuntimeStateStopped {
		t.Fatalf("Status = %s, want stopped", st.Status)
	}
	// 未运行幂等。
	if err := b.Stop(1); err != nil {
		t.Fatalf("second Stop failed: %v", err)
	}
	if err := b.Stop(99); err != nil {
		t.Fatalf("Stop(99) failed: %v", err)
	}
}

func TestRuntimeEventDrivenStates(t *testing.T) {
	factory := &runFactory{}
	b := newTestRuntime(seedRuntimeVault(), factory)

	if err := b.Start(1); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	run := factory.last()

	run.events <- forward.RuntimeEvent{Type: forward.RuntimeEventDisconnected, Err: errors.New("ssh connection closed")}
	eventually(t, func() bool {
		st, _ := b.Status(1)
		return st.Status == RuntimeStateReconnecting && strings.Contains(st.LastError, "ssh connection closed")
	}, "disconnected should drive reconnecting state with last error")

	run.setLatency(88 * time.Millisecond)
	run.events <- forward.RuntimeEvent{Type: forward.RuntimeEventReconnected}
	eventually(t, func() bool {
		st, _ := b.Status(1)
		return st.Status == RuntimeStateRunning && st.LatencyMs == 88 && st.LastError == ""
	}, "reconnected should drive running state with latency and cleared error")
}

func TestRuntimeDoneWithErrFinalizesError(t *testing.T) {
	factory := &runFactory{}
	b := newTestRuntime(seedRuntimeVault(), factory)

	if err := b.Start(1); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	factory.last().kill(errors.New("forward: host key rejected: fingerprint changed"))

	eventually(t, func() bool {
		st, _ := b.Status(1)
		return st.Status == RuntimeStateError && strings.Contains(st.LastError, "host key rejected")
	}, "done with err should finalize error state")

	b.mu.Lock()
	remaining := len(b.runs)
	b.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("runs table should be cleaned, got %d entries", remaining)
	}

	found := false
	for _, item := range b.Snapshot() {
		if item.ForwardID == 1 {
			found = true
			if item.Status != RuntimeStateError {
				t.Fatalf("Snapshot[1].Status = %s, want error", item.Status)
			}
		}
	}
	if !found {
		t.Fatalf("Snapshot should contain forward 1")
	}
}

func TestRuntimeDoneCleanFinalizesStopped(t *testing.T) {
	factory := &runFactory{}
	b := newTestRuntime(seedRuntimeVault(), factory)

	if err := b.Start(1); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	factory.last().kill(nil)

	eventually(t, func() bool {
		st, _ := b.Status(1)
		return st.Status == RuntimeStateStopped && st.LastError == ""
	}, "done without err should finalize stopped state")
}

func TestRuntimeSnapshotDefaultsStopped(t *testing.T) {
	b := newTestRuntime(seedRuntimeVault(), &runFactory{})

	snapshot := b.Snapshot()
	if len(snapshot) != 3 {
		t.Fatalf("Snapshot len = %d, want 3", len(snapshot))
	}
	for _, item := range snapshot {
		if item.Status != RuntimeStateStopped {
			t.Fatalf("Snapshot[%d].Status = %s, want stopped", item.ForwardID, item.Status)
		}
	}
	if _, ok := b.Status(1); ok {
		t.Fatalf("Status(1) should be absent before any Start")
	}
}

func TestRuntimeStartAutoStart(t *testing.T) {
	factory := &runFactory{}
	b := newTestRuntime(seedRuntimeVault(), factory)

	errs, err := b.StartAutoStart()
	if err != nil {
		t.Fatalf("StartAutoStart failed: %v", err)
	}
	if len(errs) != 0 {
		t.Fatalf("StartAutoStart errs = %v, want empty", errs)
	}
	if factory.count() != 2 {
		t.Fatalf("factory count = %d, want 2 (only AutoStart forwards)", factory.count())
	}
	for _, run := range factory.runs {
		if run.fw.ID != 2 && run.fw.ID != 3 {
			t.Fatalf("started forward %d, want only 2 and 3", run.fw.ID)
		}
	}
	if _, ok := b.Status(1); ok {
		t.Fatalf("forward 1 (AutoStart=false) should not be started")
	}
}

func TestRuntimeStartAutoStartPartialFailure(t *testing.T) {
	factory := &runFactory{startErr: errors.New("dial refused")}
	b := newTestRuntime(seedRuntimeVault(), factory)

	errs, err := b.StartAutoStart()
	if err != nil {
		t.Fatalf("StartAutoStart failed: %v", err)
	}
	// 单项失败不中断其他项：两项都尝试过且都记录了错误状态。
	if factory.count() != 2 {
		t.Fatalf("factory count = %d, want 2 (all attempted despite failures)", factory.count())
	}
	if len(errs) != 2 {
		t.Fatalf("errs = %v, want both ids", errs)
	}
	for _, id := range []int{2, 3} {
		if st, _ := b.Status(id); st.Status != RuntimeStateError {
			t.Fatalf("Status(%d) = %s, want error", id, st.Status)
		}
	}
}

func TestRuntimeHostKeyVerifierBranches(t *testing.T) {
	store := seedRuntimeVault()
	trustedKey, trustedFP := testPublicKey(t)
	otherKey, _ := testPublicKey(t)
	store.mu.Lock()
	store.data.HostKeys = []model.HostKey{
		{ID: 1, Host: "10.0.0.1", Port: 22, KeyType: trustedKey.Type(), FingerprintSHA256: trustedFP},
	}
	store.mu.Unlock()

	b := NewRuntimeBiz(store)
	verifier := b.hostKeyVerifier()

	if err := verifier("10.0.0.1", 22, trustedKey); err != nil {
		t.Fatalf("trusted fingerprint should pass, got %v", err)
	}

	mismatchErr := verifier("10.0.0.1", 22, otherKey)
	if !errors.Is(mismatchErr, ErrHostKeyMismatch) {
		t.Fatalf("mismatch err = %v, want errors.Is ErrHostKeyMismatch", mismatchErr)
	}
	if !strings.Contains(mismatchErr.Error(), trustedFP) {
		t.Fatalf("mismatch err should include stored fingerprint, got %v", mismatchErr)
	}

	unknownErr := verifier("10.0.0.9", 22, trustedKey)
	if !errors.Is(unknownErr, ErrHostKeyUnknown) {
		t.Fatalf("unknown err = %v, want errors.Is ErrHostKeyUnknown", unknownErr)
	}
}

func TestRuntimeShutdownStopsAll(t *testing.T) {
	factory := &runFactory{}
	b := newTestRuntime(seedRuntimeVault(), factory)

	if err := b.Start(1); err != nil {
		t.Fatalf("Start(1) failed: %v", err)
	}
	if err := b.Start(2); err != nil {
		t.Fatalf("Start(2) failed: %v", err)
	}

	b.Shutdown()

	for _, run := range factory.runs {
		if !run.wasStopped() {
			t.Fatalf("run for forward %d should be stopped", run.fw.ID)
		}
	}
	b.mu.Lock()
	remaining := len(b.runs)
	b.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("runs table should be empty after Shutdown, got %d", remaining)
	}
	for _, id := range []int{1, 2} {
		if st, _ := b.Status(id); st.Status != RuntimeStateStopped {
			t.Fatalf("Status(%d) = %s, want stopped", id, st.Status)
		}
	}
}
