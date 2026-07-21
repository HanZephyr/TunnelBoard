package forward

import (
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/HanZephyr/TunnelBoard/internal/model"

	"golang.org/x/crypto/ssh"
)

// fakeSSHClient 实现 sshClient 接缝：Wait 阻塞直到 Close 或 kill；
// Dial/SendRequest 记录调用并按需返回错误。
type fakeSSHClient struct {
	mu        sync.Mutex
	waitCh    chan struct{}
	closeOnce sync.Once
	closed    bool
	waitErr   error
	sendErr   error
	sendCalls int
	blockSend bool
	dialErr   error
	dialCalls []string
}

func newFakeSSHClient() *fakeSSHClient {
	return &fakeSSHClient{waitCh: make(chan struct{})}
}

func (c *fakeSSHClient) Wait() error {
	<-c.waitCh
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.waitErr
}

func (c *fakeSSHClient) Close() error {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	c.closeOnce.Do(func() { close(c.waitCh) })
	return nil
}

func (c *fakeSSHClient) Dial(network, addr string) (net.Conn, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.dialCalls = append(c.dialCalls, network+" "+addr)
	if c.dialErr != nil {
		return nil, c.dialErr
	}
	return nil, errors.New("fake dial not implemented")
}

func (c *fakeSSHClient) SendRequest(name string, wantReply bool, payload []byte) (bool, []byte, error) {
	c.mu.Lock()
	c.sendCalls++
	block := c.blockSend
	err := c.sendErr
	c.mu.Unlock()
	if block {
		<-c.waitCh
		return false, nil, errors.New("client closed")
	}
	if err != nil {
		return false, nil, err
	}
	return true, nil, nil
}

func TestSSHConnPool_KeepAliveTimeoutClosesBlockedGeneration(t *testing.T) {
	dialer := &fakePoolDialer{onClient: func(c *fakeSSHClient) { c.blockSend = true }}
	pool := newSSHConnPoolWithDial(dialer.dial)
	pool.probeTimeout = func(time.Duration) time.Duration { return 20 * time.Millisecond }
	hosts := poolTestHosts()
	hosts[0].KeepAliveIntervalMs = 5

	_, release, _, err := pool.dialChain(hosts, nil)
	if err != nil {
		t.Fatalf("dialChain: %v", err)
	}
	defer release()
	waitForPoolCond(t, dialer.client(0).isClosed, "blocked keepalive must close its connection generation")
	if dead, _, _ := pool.entryState(7); !dead {
		t.Fatal("timed-out keepalive must mark generation dead")
	}
}

func TestSSHConnPool_ConnectionIdentityChangeCreatesNewGeneration(t *testing.T) {
	dialer := &fakePoolDialer{}
	pool := newSSHConnPoolWithDial(dialer.dial)
	hostA := poolTestHosts()[0]
	_, releaseA, _, err := pool.dialChain([]model.SSHHost{hostA}, nil)
	if err != nil {
		t.Fatalf("dial A: %v", err)
	}

	hostB := hostA
	hostB.Host = "10.0.0.99"
	_, releaseB, _, err := pool.dialChain([]model.SSHHost{hostB}, nil)
	if err != nil {
		t.Fatalf("dial B: %v", err)
	}
	if dialer.callCount() != 2 {
		t.Fatalf("identity change dial calls = %d, want 2", dialer.callCount())
	}
	if dialer.client(0).isClosed() {
		t.Fatal("old generation must remain alive while its lease is held")
	}
	releaseA()
	if !dialer.client(0).isClosed() {
		t.Fatal("old generation must close after its final lease release")
	}
	if dialer.client(1).isClosed() {
		t.Fatal("releasing old identity must not close current identity")
	}
	releaseB()
}

func TestSSHConnPool_OldGenerationAbortDoesNotCloseNewGeneration(t *testing.T) {
	dialer := &fakePoolDialer{}
	pool := newSSHConnPoolWithDial(dialer.dial)
	host := poolTestHosts()[0]
	_, releaseA, _, err := pool.dialChain([]model.SSHHost{host}, nil)
	if err != nil {
		t.Fatalf("dial A: %v", err)
	}
	abortA := pool.captureGenerationAbort(host)
	releaseA()
	_, releaseB, _, err := pool.dialChain([]model.SSHHost{host}, nil)
	if err != nil {
		t.Fatalf("dial B: %v", err)
	}
	defer releaseB()
	abortA()
	if dialer.client(1).isClosed() {
		t.Fatal("old generation abort must not close the replacement client")
	}
}

func TestSSHConnectionIdentityUsesCredentialRevisionWithoutSecret(t *testing.T) {
	host := poolTestHosts()[0]
	identityA := SSHConnectionIdentity(host)
	host.Password = "different secret"
	if identityB := SSHConnectionIdentity(host); identityB != identityA {
		t.Fatal("secret plaintext must not be part of ConnectionIdentity")
	}
	host.CredentialRevision++
	if identityB := SSHConnectionIdentity(host); identityB == identityA {
		t.Fatal("CredentialRevision change must rotate ConnectionIdentity")
	}
}

// kill 模拟连接死亡：Wait 以给定错误返回（不经过 Close）。
func (c *fakeSSHClient) kill(err error) {
	c.mu.Lock()
	c.waitErr = err
	c.mu.Unlock()
	c.closeOnce.Do(func() { close(c.waitCh) })
}

func (c *fakeSSHClient) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func (c *fakeSSHClient) dialLog() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string{}, c.dialCalls...)
}

func (c *fakeSSHClient) sendCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sendCalls
}

// fakePoolDialer 记录首跳拨号次数并按序产出 fake 客户端。
type fakePoolDialer struct {
	mu       sync.Mutex
	calls    int
	clients  []*fakeSSHClient
	err      error
	onClient func(*fakeSSHClient)
}

func (d *fakePoolDialer) dial(host model.SSHHost, verifier HostKeyVerifier) (sshClient, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls++
	if d.err != nil {
		return nil, d.err
	}
	c := newFakeSSHClient()
	if d.onClient != nil {
		d.onClient(c)
	}
	d.clients = append(d.clients, c)
	return c, nil
}

func (d *fakePoolDialer) callCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.calls
}

func (d *fakePoolDialer) client(i int) *fakeSSHClient {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.clients[i]
}

func waitForPoolCond(t *testing.T, cond func() bool, msg string) {
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

func poolTestHosts() []model.SSHHost {
	return []model.SSHHost{{ID: 7, Host: "10.0.0.1", Port: 22, User: "u", AuthType: "password", Password: "x"}}
}

// entryState 池内省：持锁读取条目 dead/refs，供测试断言生命周期。
func (p *SSHConnPool) entryState(id int) (dead bool, refs int, ok bool) {
	p.mu.Lock()
	entries, exists := p.entries[id]
	p.mu.Unlock()
	if !exists || len(entries) == 0 {
		return false, 0, false
	}
	dead = true
	for _, entry := range entries {
		entry.mu.Lock()
		dead = dead && entry.dead
		refs += entry.refs
		entry.mu.Unlock()
	}
	return dead, refs, true
}

// (a) 同一首跳主机两次 DialChain：只拨一次号，两次返回同一客户端，shared=true。
func TestSSHConnPool_SharedFirstHop(t *testing.T) {
	dialer := &fakePoolDialer{}
	pool := newSSHConnPoolWithDial(dialer.dial)
	hosts := poolTestHosts()

	c1, close1, shared1, err := pool.dialChain(hosts, nil)
	if err != nil {
		t.Fatalf("first dialChain: %v", err)
	}
	if !shared1 {
		t.Fatalf("first dialChain shared = false, want true")
	}
	c2, close2, shared2, err := pool.dialChain(hosts, nil)
	if err != nil {
		t.Fatalf("second dialChain: %v", err)
	}
	if !shared2 {
		t.Fatalf("second dialChain shared = false, want true")
	}
	if c1 != c2 {
		t.Fatalf("same first-hop host should return the same client")
	}
	if dialer.callCount() != 1 {
		t.Fatalf("dial calls = %d, want 1 (first hop shared)", dialer.callCount())
	}
	if _, refs, _ := pool.entryState(7); refs != 2 {
		t.Fatalf("refs = %d, want 2", refs)
	}
	close1()
	close2()
}

// (b) ID==0 临时主机（连接测试等）永不入池：每次独立拨号，shared=false。
func TestSSHConnPool_ZeroIDNeverPooled(t *testing.T) {
	dialer := &fakePoolDialer{}
	pool := newSSHConnPoolWithDial(dialer.dial)

	directCalls := 0
	swapDialChain(t, func(hosts []model.SSHHost, verifier HostKeyVerifier) (*ssh.Client, func(), error) {
		directCalls++
		return &ssh.Client{}, func() {}, nil
	})

	hosts := []model.SSHHost{{ID: 0, Host: "10.0.0.9", Port: 22, User: "u"}}
	_, _, shared1, err := pool.DialChain(hosts, nil)
	if err != nil {
		t.Fatalf("first DialChain: %v", err)
	}
	_, _, shared2, err := pool.DialChain(hosts, nil)
	if err != nil {
		t.Fatalf("second DialChain: %v", err)
	}
	if shared1 || shared2 {
		t.Fatalf("ID==0 host must not be shared, got shared1=%v shared2=%v", shared1, shared2)
	}
	if directCalls != 2 {
		t.Fatalf("direct dial calls = %d, want 2 (no pooling for ID==0)", directCalls)
	}
	if dialer.callCount() != 0 {
		t.Fatalf("pooled dialer must not be used for ID==0, got %d calls", dialer.callCount())
	}
}

// (c) release 归零：连接被关闭并标记 dead；再次 acquire 重新拨号。
func TestSSHConnPool_ReleaseLastRefClosesAndRedials(t *testing.T) {
	dialer := &fakePoolDialer{}
	pool := newSSHConnPoolWithDial(dialer.dial)
	hosts := poolTestHosts()

	c1, close1, _, err := pool.dialChain(hosts, nil)
	if err != nil {
		t.Fatalf("dialChain: %v", err)
	}
	close1()
	if !dialer.client(0).isClosed() {
		t.Fatalf("last release should close the connection")
	}
	dead, refs, _ := pool.entryState(7)
	if !dead || refs != 0 {
		t.Fatalf("entry dead=%v refs=%d, want dead=true refs=0", dead, refs)
	}

	c2, close2, _, err := pool.dialChain(hosts, nil)
	if err != nil {
		t.Fatalf("re-acquire: %v", err)
	}
	defer close2()
	if dialer.callCount() != 2 {
		t.Fatalf("dial calls = %d, want 2 (redial after release)", dialer.callCount())
	}
	if c2 == c1 {
		t.Fatalf("redial must produce a new client generation")
	}
}

// (d) watcher：Wait 返回后标记 dead；并发 acquire 由 entry 锁单飞重拨，dialer 只多调一次。
func TestSSHConnPool_WatcherDeathSingleflightRedial(t *testing.T) {
	dialer := &fakePoolDialer{}
	pool := newSSHConnPoolWithDial(dialer.dial)
	hosts := poolTestHosts()

	c1, close1, _, err := pool.dialChain(hosts, nil)
	if err != nil {
		t.Fatalf("dialChain: %v", err)
	}
	defer close1()

	dialer.client(0).kill(errors.New("connection reset by peer"))
	waitForPoolCond(t, func() bool {
		dead, _, _ := pool.entryState(7)
		return dead
	}, "watcher should mark entry dead after Wait returns")

	const n = 10
	var wg sync.WaitGroup
	clients := make([]sshClient, n)
	closes := make([]func(), n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c, release, shared, err := pool.dialChain(hosts, nil)
			if err == nil && !shared {
				errs[i] = errors.New("shared = false, want true")
				return
			}
			clients[i], closes[i], errs[i] = c, release, err
		}(i)
	}
	wg.Wait()
	for i := range errs {
		if errs[i] != nil {
			t.Fatalf("concurrent acquire %d: %v", i, errs[i])
		}
	}
	defer func() {
		for _, release := range closes {
			release()
		}
	}()

	if dialer.callCount() != 2 {
		t.Fatalf("dial calls = %d, want 2 (single redial for %d concurrent acquires)", dialer.callCount(), n)
	}
	for i := 1; i < n; i++ {
		if clients[i] != clients[0] {
			t.Fatalf("acquire %d got a different client; concurrent acquires must share the redialed connection", i)
		}
	}
	if clients[0] == c1 {
		t.Fatalf("redialed client should be a new generation")
	}
	dead, refs, _ := pool.entryState(7)
	if dead {
		t.Fatalf("entry should be alive after redial")
	}
	if refs != n+1 {
		t.Fatalf("refs = %d, want %d (initial lease + %d acquires)", refs, n+1, n)
	}
}

// (e) 池级 keepalive 失败：标记 dead 并关闭连接；下一次 acquire 触发重拨。
func TestSSHConnPool_KeepAliveFailureMarksDead(t *testing.T) {
	dialer := &fakePoolDialer{onClient: func(c *fakeSSHClient) {
		c.sendErr = errors.New("keepalive refused")
	}}
	pool := newSSHConnPoolWithDial(dialer.dial)
	hosts := poolTestHosts()
	hosts[0].KeepAliveIntervalMs = 10

	_, close1, _, err := pool.dialChain(hosts, nil)
	if err != nil {
		t.Fatalf("dialChain: %v", err)
	}
	defer close1()

	waitForPoolCond(t, func() bool {
		dead, _, _ := pool.entryState(7)
		return dead
	}, "pool keepalive failure should mark entry dead")
	if !dialer.client(0).isClosed() {
		t.Fatalf("keepalive failure should close the connection")
	}
	if dialer.client(0).sendCount() == 0 {
		t.Fatalf("pool keepalive should have sent keepalive requests")
	}

	_, close2, _, err := pool.dialChain(hosts, nil)
	if err != nil {
		t.Fatalf("redial after keepalive failure: %v", err)
	}
	close2()
	if dialer.callCount() != 2 {
		t.Fatalf("dial calls = %d, want 2 (redial after keepalive failure)", dialer.callCount())
	}
}

// (f) 多跳：首跳共享入池，剩余链经共享 client 拨出（fake 记录 Dial 参数）。
// fake 无真实传输层，穿链必然在 Dial 处失败；断言失败路径会归还首跳引用。
func TestSSHConnPool_MultiHopExtendsFromSharedFirstHop(t *testing.T) {
	dialer := &fakePoolDialer{onClient: func(c *fakeSSHClient) {
		c.dialErr = errors.New("no transport")
	}}
	pool := newSSHConnPoolWithDial(dialer.dial)
	hosts := []model.SSHHost{
		{ID: 7, Host: "10.0.0.1", Port: 22, User: "u", AuthType: "password", Password: "x"},
		{ID: 8, Host: "10.0.0.2", Port: 2222, User: "u", AuthType: "password", Password: "x"},
	}
	verifier := func(host string, port int, key ssh.PublicKey) error { return nil }

	_, _, _, err := pool.dialChain(hosts, verifier)
	if err == nil || !strings.Contains(err.Error(), "via hop 1") {
		t.Fatalf("err = %v, want dial failure via hop 1", err)
	}
	if got := dialer.client(0).dialLog(); len(got) != 1 || got[0] != "tcp 10.0.0.2:2222" {
		t.Fatalf("first-hop Dial calls = %v, want [tcp 10.0.0.2:2222]", got)
	}
	if dialer.callCount() != 1 {
		t.Fatalf("dial calls = %d, want 1 (only first hop pooled)", dialer.callCount())
	}
	dead, refs, _ := pool.entryState(7)
	if refs != 0 {
		t.Fatalf("refs = %d, want 0 after failed chain dial", refs)
	}
	if !dead || !dialer.client(0).isClosed() {
		t.Fatalf("failed chain dial should release the shared first hop (dead=%v closed=%v)", dead, dialer.client(0).isClosed())
	}
}

// (g) CloseAll 关闭全部池化连接并标记 dead。
func TestSSHConnPool_CloseAll(t *testing.T) {
	dialer := &fakePoolDialer{}
	pool := newSSHConnPoolWithDial(dialer.dial)
	h1 := poolTestHosts()
	h2 := []model.SSHHost{{ID: 8, Host: "10.0.0.2", Port: 22, User: "u", AuthType: "password", Password: "x"}}

	_, close1, _, err := pool.dialChain(h1, nil)
	if err != nil {
		t.Fatalf("dialChain h1: %v", err)
	}
	_, close2, _, err := pool.dialChain(h2, nil)
	if err != nil {
		t.Fatalf("dialChain h2: %v", err)
	}
	defer close1()
	defer close2()

	pool.CloseAll()
	if !dialer.client(0).isClosed() || !dialer.client(1).isClosed() {
		t.Fatalf("CloseAll should close every pooled connection")
	}
	for _, id := range []int{7, 8} {
		if dead, _, _ := pool.entryState(id); !dead {
			t.Fatalf("entry %d should be dead after CloseAll", id)
		}
	}
}

func TestPoolKeepAliveInterval(t *testing.T) {
	if got := poolKeepAliveInterval(model.SSHHost{KeepAliveIntervalMs: 0}); got != 5*time.Second {
		t.Fatalf("default pool keepalive interval = %v, want 5s", got)
	}
	if got := poolKeepAliveInterval(model.SSHHost{KeepAliveIntervalMs: 7000}); got != 7*time.Second {
		t.Fatalf("pool keepalive interval = %v, want 7s", got)
	}
}

// Stats 快照：两次 acquire 后 refs=2 且 alive；release 归零后 alive=false。
func TestPoolStats(t *testing.T) {
	dialer := &fakePoolDialer{}
	pool := newSSHConnPoolWithDial(dialer.dial)
	host := model.SSHHost{ID: 7, Host: "10.0.0.1", Port: 22}

	_, release1, shared1, err := pool.dialChain([]model.SSHHost{host}, nil)
	if err != nil || !shared1 {
		t.Fatalf("dial1: %v shared=%v", err, shared1)
	}
	_, release2, _, err := pool.dialChain([]model.SSHHost{host}, nil)
	if err != nil {
		t.Fatalf("dial2: %v", err)
	}

	stats := pool.Stats()
	if len(stats) != 1 || stats[0].HostID != 7 || stats[0].Refs != 2 || !stats[0].Alive {
		t.Fatalf("stats = %+v, want [{7 refs:2 alive:true}]", stats)
	}

	release1()
	release2()
	stats = pool.Stats()
	if len(stats) != 1 || stats[0].Alive {
		t.Fatalf("after release all, stats = %+v, want alive=false", stats)
	}
}
