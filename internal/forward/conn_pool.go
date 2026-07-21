package forward

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/HanZephyr/TunnelBoard/internal/model"

	"golang.org/x/crypto/ssh"
)

// sshClient 是 *ssh.Client 的最小可测试接缝：连接池只依赖这四个方法，
// 真实 *ssh.Client 天然满足；测试以 fake 驱动 watcher / keepalive / 重拨路径。
type sshClient interface {
	Wait() error
	Close() error
	Dial(network, addr string) (net.Conn, error)
	SendRequest(name string, wantReply bool, payload []byte) (bool, []byte, error)
}

// poolDialer 是首跳拨号接缝：生产实现为 dialSSH 的接口适配，测试注入 fake。
type poolDialer func(host model.SSHHost, verifier HostKeyVerifier) (sshClient, error)

// defaultPoolKeepAliveInterval 是池级 keepalive 的缺省周期：首跳未配置
// KeepAliveIntervalMs（0 或缺省）时按 5s 探测。
const defaultPoolKeepAliveInterval = 5 * time.Second

// poolKeepAliveInterval 池级 keepalive 周期：显式配置优先，否则回落默认值。
func poolKeepAliveInterval(host model.SSHHost) time.Duration {
	if interval := keepAliveInterval(host); interval > 0 {
		return interval
	}
	return defaultPoolKeepAliveInterval
}

// poolEntry 是单台首跳主机的池条目。mu 保护全部字段；拨号期间持锁
// （可接受的串行化），并据此实现并发 acquire 的单飞重拨。
// 一代连接 = client + closeAll + 其 watcher / keepalive goroutine；重拨换代后
// 旧代的 release 只减引用，不会误关新一代连接（代际以 client 同一性判定）。
type poolEntry struct {
	mu         sync.Mutex
	client     sshClient
	closeAll   func() // 幂等关闭当前一代：停 keepalive + Close
	dead       bool
	refs       int
	generation uint64
}

// watch 等待连接层终结：Wait 返回后仅当仍是同一代连接时标记 dead 并触发
// closeAll（Close 对已死连接幂等），让 keepalive goroutine 及时退出。
func (e *poolEntry) watch(client sshClient, generation uint64, closeAll func()) {
	_ = client.Wait()
	e.mu.Lock()
	same := e.client == client && e.generation == generation
	if same {
		e.dead = true
	}
	e.mu.Unlock()
	if same {
		closeAll()
	}
}

// keepAliveLoop 池级 keepalive：周期探测首跳，失败即标记 dead 并关闭；
// 重拨由后续 acquire 单飞完成。与 LocalForward 的探测循环共用 sendKeepAliveRequest。
func (e *poolEntry) keepAliveLoop(client sshClient, generation uint64, interval, timeout time.Duration, stop <-chan struct{}, closeAll func()) {
	for {
		timer := time.NewTimer(interval)
		select {
		case <-stop:
			timer.Stop()
			return
		case <-timer.C:
			if _, err := probeSSH(context.Background(), client, timeout); err == nil {
				continue
			}
			e.mu.Lock()
			same := e.client == client && e.generation == generation
			if same {
				e.dead = true
			}
			e.mu.Unlock()
			if same {
				closeAll()
			}
			return
		}
	}
}

// ConnectionIdentity 是不含秘密材料的真实 SSH transport 身份。
// Password/口令变化由 CredentialRevision 表达，避免把可猜测摘要变成离线 oracle。
type ConnectionIdentity struct {
	Host               string
	Port               int
	User               string
	AuthType           string
	KeyPath            string
	AgentSocketPath    string
	KeepAliveInterval  int
	Timeout            int
	HostKeyAlgorithms  string
	CredentialRevision uint64
}

func SSHConnectionIdentity(host model.SSHHost) ConnectionIdentity {
	port := host.Port
	if port == 0 {
		port = 22
	}
	return ConnectionIdentity{
		Host: strings.ToLower(strings.TrimSpace(host.Host)), Port: port,
		User: strings.TrimSpace(host.User), AuthType: strings.ToLower(strings.TrimSpace(host.AuthType)),
		KeyPath: strings.TrimSpace(host.KeyPath), AgentSocketPath: strings.TrimSpace(host.AgentSocketPath),
		KeepAliveInterval: host.KeepAliveIntervalMs, Timeout: host.TimeoutMs,
		HostKeyAlgorithms: strings.TrimSpace(host.HostKeyAlgorithms), CredentialRevision: host.CredentialRevision,
	}
}

// release 归还一份引用：归零且仍是同一代连接时关闭并置 dead；
// 跨代 release（重拨后归还旧代）只减引用，不动新一代连接。
func (e *poolEntry) release(client sshClient) {
	e.mu.Lock()
	e.refs--
	if e.refs != 0 || e.client != client {
		e.mu.Unlock()
		return
	}
	e.dead = true
	closeAll := e.closeAll
	e.mu.Unlock()
	if closeAll != nil {
		closeAll()
	}
}

// SSHConnPool 按首跳复用 SSH 连接：同一 model.SSHHost.ID 的多条 Forward
// 共享一条首跳连接并以引用计数管理生命周期；连接死亡（watcher / 池级
// keepalive 发现）后由下一个 acquire 单飞重拨。
//
// 并发设计：pool.mu 只保护 entries 表，entry.mu 保护条目状态；二者从不同时
// 持有（先持 pool.mu 取条目，放锁后再持 entry.mu 操作），无锁序反转。
// 拨号、标记 dead、release 归零关闭都在 entry.mu 下串行，goroutine
// （watcher / keepalive）只在放锁后触发 closeAll。
type SSHConnPool struct {
	mu             sync.Mutex
	entries        map[int]map[ConnectionIdentity]*poolEntry
	dial           poolDialer
	nextGeneration uint64
	probeTimeout   func(time.Duration) time.Duration
}

// NewSSHConnPool 创建以 dialSSH 为首跳拨号实现的连接池。
func NewSSHConnPool() *SSHConnPool {
	return &SSHConnPool{
		entries: make(map[int]map[ConnectionIdentity]*poolEntry),
		dial: func(host model.SSHHost, verifier HostKeyVerifier) (sshClient, error) {
			return dialSSH(host, verifier)
		},
		probeTimeout: keepAliveRequestTimeout,
	}
}

// newSSHConnPoolWithDial 测试接缝：注入 fake 首跳拨号器。
func newSSHConnPoolWithDial(dial poolDialer) *SSHConnPool {
	pool := NewSSHConnPool()
	pool.dial = dial
	return pool
}

// acquire 取一份首跳连接引用：无连接或已死亡时持 entry 锁单飞拨号
// （并发 acquire 只有第一个真正拨号，其余直接复用结果）；拨号失败把错误
// 传给当前调用者，重试节奏由 LocalForward 的退避循环决定。
func (p *SSHConnPool) acquire(host model.SSHHost, verifier HostKeyVerifier) (*poolEntry, sshClient, error) {
	p.mu.Lock()
	identity := SSHConnectionIdentity(host)
	byIdentity := p.entries[host.ID]
	if byIdentity == nil {
		byIdentity = make(map[ConnectionIdentity]*poolEntry)
		p.entries[host.ID] = byIdentity
	}
	entry, ok := byIdentity[identity]
	if !ok {
		entry = &poolEntry{}
		byIdentity[identity] = entry
	}
	p.mu.Unlock()

	entry.mu.Lock()
	defer entry.mu.Unlock()

	if entry.client == nil || entry.dead {
		oldClose := entry.closeAll
		client, err := p.dial(host, verifier)
		if err != nil {
			return nil, nil, err
		}
		stop := make(chan struct{})
		var once sync.Once
		closeAll := func() {
			once.Do(func() {
				close(stop)
				_ = client.Close()
			})
		}
		entry.client = client
		entry.closeAll = closeAll
		entry.dead = false
		p.mu.Lock()
		p.nextGeneration++
		generation := p.nextGeneration
		p.mu.Unlock()
		entry.generation = generation
		interval := poolKeepAliveInterval(host)
		go entry.watch(client, generation, closeAll)
		go entry.keepAliveLoop(client, generation, interval, p.probeTimeout(interval), stop, closeAll)
		if oldClose != nil {
			// 上一代连接已死：停掉其 keepalive goroutine，Close 为幂等兜底。
			oldClose()
		}
	}
	entry.refs++
	return entry, entry.client, nil
}

// dialChain 是 DialChain 的接口级实现：首跳入池共享（ID==0 临时主机除外），
// 多跳经共享首跳穿链。拆出接口层是为了让测试以 fake sshClient 驱动全部路径。
func (p *SSHConnPool) dialChain(hosts []model.SSHHost, verifier HostKeyVerifier) (sshClient, func(), bool, error) {
	if len(hosts) == 0 {
		return nil, nil, false, fmt.Errorf("at least one ssh host is required")
	}

	first := hosts[0]
	if first.ID == 0 {
		// 临时主机（连接测试等）永不入池，走独占链路；经 dialChain 变量转发，
		// 与 LocalForward 默认拨号器保持同一可替换接缝。
		client, closeChain, err := dialChain(hosts, verifier)
		if err != nil {
			return nil, nil, false, err
		}
		return client, closeChain, false, nil
	}

	entry, shared, err := p.acquire(first, verifier)
	if err != nil {
		return nil, nil, false, err
	}
	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() { entry.release(shared) })
	}

	if len(hosts) == 1 {
		return shared, release, true, nil
	}
	// 多跳：经共享首跳继续穿链；closeChain 的失败路径会连带 release 首跳。
	last, closeChain, err := dialChainFrom(shared, release, hosts, verifier)
	if err != nil {
		return nil, nil, false, err
	}
	return last, closeChain, true, nil
}

// DialChain 返回末跳客户端、关闭函数、是否共享。单跳：直接返回共享首跳；
// 多跳：经共享首跳继续拨剩余链（复用 dialChainFrom 穿链逻辑）。
// 关闭函数幂等：多跳关闭整条自建链并归还首跳引用，引用归零才真正关闭首跳。
func (p *SSHConnPool) DialChain(hosts []model.SSHHost, verifier HostKeyVerifier) (client *ssh.Client, closeChain func(), shared bool, err error) {
	sc, closeChain, shared, err := p.dialChain(hosts, verifier)
	if err != nil {
		return nil, nil, false, err
	}
	client, ok := sc.(*ssh.Client)
	if !ok {
		closeChain()
		return nil, nil, false, fmt.Errorf("forward: pooled ssh client has unexpected type %T", sc)
	}
	return client, closeChain, shared, nil
}

// CloseAll 关闭全部池化连接（应用 Shutdown 用）；条目标记 dead，
// 之后的 acquire 会按单飞逻辑重拨。
func (p *SSHConnPool) CloseAll() {
	p.mu.Lock()
	var entries []*poolEntry
	for _, byIdentity := range p.entries {
		for _, entry := range byIdentity {
			entries = append(entries, entry)
		}
	}
	p.mu.Unlock()

	for _, entry := range entries {
		entry.mu.Lock()
		entry.dead = true
		closeAll := entry.closeAll
		entry.mu.Unlock()
		if closeAll != nil {
			closeAll()
		}
	}
}

// PoolStat 是单个共享连接条目的状态快照（界面展示连接复用情况用）。
type PoolStat struct {
	HostID int  `json:"hostId"`
	Refs   int  `json:"refs"`
	Alive  bool `json:"alive"`
}

// Stats 返回全部共享连接条目的快照（按 HostID 升序）；无并发副作用。
func (p *SSHConnPool) Stats() []PoolStat {
	p.mu.Lock()
	byID := make(map[int][]*poolEntry, len(p.entries))
	ids := make([]int, 0, len(p.entries))
	for id, entries := range p.entries {
		for _, e := range entries {
			byID[id] = append(byID[id], e)
		}
		ids = append(ids, id)
	}
	p.mu.Unlock()

	sort.Ints(ids)
	out := make([]PoolStat, 0, len(ids))
	for _, id := range ids {
		stat := PoolStat{HostID: id}
		for _, e := range byID[id] {
			e.mu.Lock()
			stat.Refs += e.refs
			stat.Alive = stat.Alive || (e.client != nil && !e.dead)
			e.mu.Unlock()
		}
		out = append(out, stat)
	}
	return out
}
