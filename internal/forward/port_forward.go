package forward

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/HanZephyr/TunnelBoard/internal/model"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

var ErrUnsupportedMode = errors.New("only local, remote and dynamic modes are supported")

const (
	initReconnectWait = 500 * time.Millisecond
	maxReconnectWait  = 1 * time.Minute
	reconnectTimeout  = 15 * time.Minute
)

var (
	sshAgentMu   sync.Mutex
	sshAgentInst agent.ExtendedAgent
	sshAgentSock string
	sshAgentConn net.Conn
)

// dialChain 是 dialSSHChain 的可替换接缝：测试注入 fake 以驱动重连路径。
var dialChain = dialSSHChain

type RuntimeEventType string

const (
	RuntimeEventDisconnected RuntimeEventType = "disconnected"
	RuntimeEventReconnected  RuntimeEventType = "reconnected"
)

type RuntimeEvent struct {
	Type RuntimeEventType
	Err  error
}

type ChainDialer func(hosts []model.SSHHost, verifier HostKeyVerifier) (ChainLease, error)

// defaultChainDialer 保持历史行为：每条 Forward 独占拨号（shared=false）。
// 经 dialChain 变量转发，保留测试注入 fake 驱动重连路径的接缝。
func defaultChainDialer(hosts []model.SSHHost, verifier HostKeyVerifier) (ChainLease, error) {
	client, closeChain, err := dialChain(hosts, verifier)
	if err != nil {
		return nil, err
	}
	lease := &chainLease{
		terminal: client, probe: keepAliveInterval(hosts[len(hosts)-1]) > 0,
		interval: keepAliveInterval(hosts[len(hosts)-1]),
		timeout:  keepAliveRequestTimeout(keepAliveInterval(hosts[len(hosts)-1])),
		release:  closeChain, abort: closeChain,
	}
	lease.rebuild = func(ctx context.Context) (ChainLease, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
			return defaultChainDialer(hosts, verifier)
		}
	}
	return lease, nil
}

type LocalForward struct {
	forward  model.Forward
	hosts    []model.SSHHost
	verifier HostKeyVerifier
	dialer   ChainDialer

	mu          sync.Mutex
	started     bool
	stopping    bool
	runErr      error
	client      *ssh.Client
	lease       ChainLease
	listener    net.Listener
	done        chan struct{}
	events      chan RuntimeEvent
	keepStop    chan struct{}
	lastLatency time.Duration
	stopOnce    sync.Once
	stopDone    chan struct{}
	wg          sync.WaitGroup
	activeConns map[net.Conn]struct{}
	runCtx      context.Context
	runCancel   context.CancelFunc
}

func NewLocalForward(fw model.Forward, hosts []model.SSHHost, verifier HostKeyVerifier, dialer ChainDialer) *LocalForward {
	if dialer == nil {
		dialer = defaultChainDialer
	}
	return &LocalForward{
		forward:  fw,
		hosts:    append([]model.SSHHost{}, hosts...),
		verifier: verifier,
		dialer:   dialer,
	}
}

func (f *LocalForward) Start() error {
	mode := normalizeForwardMode(f.forward.Mode)
	if mode != "local" && mode != "remote" && mode != "dynamic" {
		return ErrUnsupportedMode
	}

	f.mu.Lock()
	if f.started {
		f.mu.Unlock()
		return nil
	}
	f.started = true
	f.runErr = nil
	f.done = make(chan struct{})
	f.events = make(chan RuntimeEvent, 8)
	f.keepStop = make(chan struct{})
	f.activeConns = make(map[net.Conn]struct{})
	f.runCtx, f.runCancel = context.WithCancel(context.Background())
	f.stopDone = make(chan struct{})
	f.mu.Unlock()

	slog.Info(
		"forward start",
		"forward_id", f.forward.ID,
		"name", f.forward.Name,
		"host_hops", len(f.hosts),
		"keepalive_interval_ms", f.lastHost().KeepAliveIntervalMs,
		"timeout_ms", f.lastHost().TimeoutMs,
	)

	lease, err := f.dialer(f.hosts, f.verifier)
	if err != nil {
		f.setRunErr(err)
		slog.Error("forward initial dial failed", "forward_id", f.forward.ID, "name", f.forward.Name, "err", err)
		return err
	}
	client := lease.Terminal()
	if client == nil {
		lease.Release()
		err = errors.New("forward: chain lease has no terminal ssh client")
		f.setRunErr(err)
		return err
	}
	if mode == "local" {
		if err := probeRemoteDial(client, f.forward.RemoteHost, f.forward.RemotePort); err != nil {
			lease.Release()
			f.setRunErr(err)
			slog.Error(
				"forward initial remote probe failed",
				"forward_id",
				f.forward.ID, "name", f.forward.Name, "err", err)
			return err
		}
	} else if mode == "dynamic" {
		if err := probeDynamicForwardCapability(client); err != nil {
			lease.Release()
			f.setRunErr(err)
			slog.Error("forward dynamic probe failed", "forward_id", f.forward.ID, "name", f.forward.Name, "err", err)
			return err
		}
	}

	var ln net.Listener
	if mode == "remote" {
		ln, err = f.bindRemoteListener(client)
		if err != nil {
			lease.Release()
			f.setRunErr(err)
			slog.Error("forward remote listen failed", "forward_id", f.forward.ID, "name", f.forward.Name, "err", err)
			return err
		}
	} else {
		localHost := strings.TrimSpace(f.forward.LocalHost)
		if localHost == "" {
			localHost = "127.0.0.1"
		}
		localAddr := net.JoinHostPort(localHost, strconv.Itoa(f.forward.LocalPort))
		ln, err = net.Listen("tcp", localAddr)
		if err != nil {
			lease.Release()
			runErr := fmt.Errorf("listen %s failed: %w", localAddr, err)
			f.setRunErr(runErr)
			slog.Error("forward listen failed", "forward_id", f.forward.ID, "name", f.forward.Name, "addr", localAddr, "err", runErr)
			return runErr
		}
	}

	f.mu.Lock()
	f.client = client
	f.lease = lease
	f.listener = ln
	f.lastLatency = 0
	done := f.done
	f.mu.Unlock()

	if latency, latencyErr := TestSSHHostLatency(client); latencyErr == nil {
		f.setLastLatency(latency)
	}

	if mode == "remote" {
		go f.serveRemote(done)
	} else {
		go f.serveLocal(done)
	}
	go f.monitorClientLifecycle(lease)
	return nil
}

func (f *LocalForward) Stop(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	f.stopOnce.Do(func() {
		slog.Info("forward stop requested", "forward_id", f.forward.ID, "name", f.forward.Name)
		f.mu.Lock()
		f.stopping = true
		ln := f.listener
		c := f.client
		lease := f.lease
		done := f.done
		keepStop := f.keepStop
		runCancel := f.runCancel
		active := make([]net.Conn, 0, len(f.activeConns))
		for conn := range f.activeConns {
			active = append(active, conn)
		}
		f.listener = nil
		f.client = nil
		f.lease = nil
		if f.stopDone == nil {
			f.stopDone = make(chan struct{})
		}
		stopDone := f.stopDone
		f.mu.Unlock()

		if keepStop != nil {
			close(keepStop)
		}
		if runCancel != nil {
			runCancel()
		}
		if lease != nil {
			lease.Release()
		} else if c != nil {
			_ = c.Close()
		}
		if ln != nil {
			_ = ln.Close()
		}
		for _, conn := range active {
			_ = conn.Close()
		}
		go func() {
			if done != nil {
				<-done
			}
			f.wg.Wait()
			close(stopDone)
		}()
	})
	f.mu.Lock()
	stopDone := f.stopDone
	f.mu.Unlock()
	select {
	case <-stopDone:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("forward stop timeout: %w", ctx.Err())
	}
}

func (f *LocalForward) Done() <-chan struct{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.done
}

func (f *LocalForward) Events() <-chan RuntimeEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.events
}

func (f *LocalForward) Err() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.runErr
}

func (f *LocalForward) LastLatency() (time.Duration, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.lastLatency <= 0 {
		return 0, false
	}
	return f.lastLatency, true
}

func (f *LocalForward) serveLocal(done chan struct{}) {
	defer close(done)

	for {
		f.mu.Lock()
		ln := f.listener
		f.mu.Unlock()
		if ln == nil {
			return
		}

		conn, err := ln.Accept()
		if err != nil {
			if f.isStopping() {
				return
			}
			if ne, ok := err.(net.Error); ok && ne.Temporary() {
				continue
			}
			if f.Err() == nil {
				f.setRunErr(fmt.Errorf("accept failed: %w", err))
			}
			return
		}

		f.wg.Add(1)
		go func(localConn net.Conn) {
			defer f.wg.Done()
			f.handleConn(localConn)
		}(conn)
	}
}

func (f *LocalForward) serveRemote(done chan struct{}) {
	defer close(done)

	for {
		if f.isStopping() {
			return
		}

		f.mu.Lock()
		ln := f.listener
		f.mu.Unlock()
		if ln == nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}

		conn, err := ln.Accept()
		if err != nil {
			if f.isStopping() {
				return
			}
			if ne, ok := err.(net.Error); ok && ne.Temporary() {
				continue
			}
			time.Sleep(200 * time.Millisecond)
			continue
		}

		f.wg.Add(1)
		go func(remoteConn net.Conn) {
			defer f.wg.Done()
			f.handleConn(remoteConn)
		}(conn)
	}
}

func (f *LocalForward) handleConn(localConn net.Conn) {
	if !f.trackConn(localConn) {
		_ = localConn.Close()
		return
	}
	defer f.untrackConn(localConn)
	f.mu.Lock()
	client := f.client
	f.mu.Unlock()

	if client == nil {
		_ = localConn.Close()
		return
	}

	if normalizeForwardMode(f.forward.Mode) == "dynamic" {
		f.handleDynamicConn(localConn, client)
		return
	}
	if normalizeForwardMode(f.forward.Mode) == "remote" {
		f.handleRemoteConn(localConn)
		return
	}
	f.handleLocalConn(localConn, client)
}

func (f *LocalForward) handleLocalConn(localConn net.Conn, client *ssh.Client) {
	remoteAddr := net.JoinHostPort(strings.TrimSpace(f.forward.RemoteHost), strconv.Itoa(f.forward.RemotePort))
	remoteConn, err := client.Dial("tcp", remoteAddr)
	if err != nil {
		_ = localConn.Close()
		return
	}
	if !f.trackConn(remoteConn) {
		_ = localConn.Close()
		_ = remoteConn.Close()
		return
	}
	defer f.untrackConn(remoteConn)

	bridge(localConn, remoteConn)
}

func (f *LocalForward) handleDynamicConn(localConn net.Conn, client *ssh.Client) {
	targetAddr, err := readSOCKS5ConnectTarget(localConn)
	if err != nil {
		_ = localConn.Close()
		return
	}

	remoteConn, err := client.Dial("tcp", targetAddr)
	if err != nil {
		_ = writeSOCKS5Reply(localConn, socksReplyGeneralFailure)
		_ = localConn.Close()
		return
	}
	if !f.trackConn(remoteConn) {
		_ = localConn.Close()
		_ = remoteConn.Close()
		return
	}
	defer f.untrackConn(remoteConn)

	if err := writeSOCKS5Reply(localConn, socksReplySucceeded); err != nil {
		_ = remoteConn.Close()
		_ = localConn.Close()
		return
	}

	bridge(localConn, remoteConn)
}

func (f *LocalForward) handleRemoteConn(remoteConn net.Conn) {
	localHost := strings.TrimSpace(f.forward.LocalHost)
	if localHost == "" {
		localHost = "127.0.0.1"
	}
	localAddr := net.JoinHostPort(localHost, strconv.Itoa(f.forward.LocalPort))
	localConn, err := net.Dial("tcp", localAddr)
	if err != nil {
		_ = remoteConn.Close()
		return
	}
	if !f.trackConn(localConn) {
		_ = remoteConn.Close()
		_ = localConn.Close()
		return
	}
	defer f.untrackConn(localConn)
	bridge(remoteConn, localConn)
}

func (f *LocalForward) trackConn(conn net.Conn) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.stopping {
		return false
	}
	if f.activeConns == nil {
		f.activeConns = make(map[net.Conn]struct{})
	}
	f.activeConns[conn] = struct{}{}
	return true
}

func (f *LocalForward) untrackConn(conn net.Conn) {
	f.mu.Lock()
	delete(f.activeConns, conn)
	f.mu.Unlock()
}

func (f *LocalForward) monitorClientLifecycle(lease ChainLease) {
	defer f.closeEvents()

	for {
		disconnectErr := lease.WaitLoss(f.runContext())
		if disconnectErr == nil || f.isStopping() {
			return
		}

		slog.Warn("forward connection lost", "forward_id", f.forward.ID, "name", f.forward.Name, "err", disconnectErr)
		f.emitEvent(RuntimeEvent{
			Type: RuntimeEventDisconnected,
			Err:  disconnectErr,
		})
		f.clearTerminal(lease)

		reconnectedLease, reconnectErr := f.reconnectWithBackoff(lease)
		if reconnectErr != nil {
			f.setRunErr(fmt.Errorf("%v: %w", disconnectErr, reconnectErr))
			slog.Error("forward reconnect failed", "forward_id", f.forward.ID, "name", f.forward.Name, "err", reconnectErr)
			f.closeListener()
			return
		}
		if reconnectedLease == nil {
			return
		}
		f.emitEvent(RuntimeEvent{
			Type: RuntimeEventReconnected,
		})
		slog.Info("forward reconnected", "forward_id", f.forward.ID, "name", f.forward.Name)
		f.setLease(reconnectedLease)
		lease = reconnectedLease
	}
}

func (f *LocalForward) reconnectWithBackoff(current ChainLease) (ChainLease, error) {
	stop := f.stopSignal()
	if stop == nil {
		return nil, nil
	}

	deadline := time.Now().Add(reconnectTimeout)
	wait := initReconnectWait
	var lastErr error
	attempt := 0

	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}

		if !waitOrStop(minDuration(wait, remaining), stop) {
			return nil, nil
		}
		attempt++
		slog.Info("forward reconnect attempt", "forward_id", f.forward.ID, "name", f.forward.Name, "attempt", attempt, "wait", wait.String())

		ctx := f.runContext()
		lease, err := current.Rebuild(ctx)
		if err == nil {
			client := lease.Terminal()
			if client == nil {
				lease.Release()
				err = errors.New("forward: rebuilt chain lease has no terminal ssh client")
			} else {
				if normalizeForwardMode(f.forward.Mode) == "remote" {
					ln, listenErr := f.bindRemoteListener(client)
					if listenErr != nil {
						lease.Release()
						lastErr = listenErr
						slog.Warn("forward remote listen rebind failed", "forward_id", f.forward.ID, "name", f.forward.Name, "attempt", attempt, "err", listenErr)
						wait = nextReconnectWait(wait)
						continue
					}
					f.replaceListener(ln)
				}
				slog.Info("forward reconnect succeeded", "forward_id", f.forward.ID, "name", f.forward.Name, "attempt", attempt)
				return lease, nil
			}
		}

		lastErr = err
		// 终态错误（指纹被拒、认证失败）重试无意义，立即上抛；bindRemoteListener
		// 失败（端口冲突）不算终态，走上面的 continue 继续退避。
		if IsTerminalError(err) {
			slog.Error("forward reconnect hit terminal error", "forward_id", f.forward.ID, "name", f.forward.Name, "attempt", attempt, "err", err)
			return nil, err
		}
		slog.Warn("forward reconnect failed", "forward_id", f.forward.ID, "name", f.forward.Name, "attempt", attempt, "err", err)
		wait = nextReconnectWait(wait)
	}

	if lastErr == nil {
		lastErr = errors.New("reconnect timeout")
	}
	return nil, fmt.Errorf("reconnect timeout after %s: %w", reconnectTimeout, lastErr)
}

func nextReconnectWait(current time.Duration) time.Duration {
	if current <= 0 {
		return initReconnectWait
	}
	next := current * 2
	if next > maxReconnectWait {
		next = maxReconnectWait
	}
	return next
}

func waitOrStop(wait time.Duration, stop <-chan struct{}) bool {
	if wait <= 0 {
		return true
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()

	select {
	case <-stop:
		return false
	case <-timer.C:
		return true
	}
}

func minDuration(a, b time.Duration) time.Duration {
	if a <= b {
		return a
	}
	return b
}

func keepAliveInterval(host model.SSHHost) time.Duration {
	interval := time.Duration(host.KeepAliveIntervalMs) * time.Millisecond
	if interval > 0 {
		return interval
	}
	return 0
}

func (f *LocalForward) lastHost() model.SSHHost {
	if len(f.hosts) == 0 {
		return model.SSHHost{}
	}
	return f.hosts[len(f.hosts)-1]
}

func keepAliveRequestTimeout(interval time.Duration) time.Duration {
	if interval <= 0 {
		return 5 * time.Second
	}
	timeout := interval / 2
	if timeout < 5*time.Second {
		timeout = 5 * time.Second
	}
	if timeout > 10*time.Second {
		timeout = 10 * time.Second
	}
	return timeout
}

func (f *LocalForward) stopSignal() <-chan struct{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.keepStop
}

func (f *LocalForward) setLease(next ChainLease) {
	f.mu.Lock()
	old := f.lease
	f.client = next.Terminal()
	f.lease = next
	f.lastLatency = 0
	f.mu.Unlock()
	if old != nil && old != next {
		go old.Release()
	}
}

func (f *LocalForward) clearTerminal(current ChainLease) {
	f.mu.Lock()
	if f.lease == current {
		f.client = nil
	}
	f.mu.Unlock()
}

func (f *LocalForward) runContext() context.Context {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.runCtx == nil {
		return context.Background()
	}
	return f.runCtx
}

func (f *LocalForward) setLastLatency(latency time.Duration) {
	if latency <= 0 {
		return
	}
	f.mu.Lock()
	f.lastLatency = latency
	f.mu.Unlock()
}

func (f *LocalForward) replaceListener(listener net.Listener) {
	f.mu.Lock()
	old := f.listener
	f.listener = listener
	f.mu.Unlock()
	if old != nil && old != listener {
		_ = old.Close()
	}
}

func (f *LocalForward) closeListener() {
	f.mu.Lock()
	ln := f.listener
	f.listener = nil
	f.mu.Unlock()
	if ln != nil {
		_ = ln.Close()
	}
}

func (f *LocalForward) emitEvent(event RuntimeEvent) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.events == nil {
		return
	}
	select {
	case f.events <- event:
	default:
	}
}

func (f *LocalForward) closeEvents() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.events == nil {
		return
	}
	close(f.events)
	f.events = nil
}

func (f *LocalForward) isStopping() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.stopping
}

func (f *LocalForward) setRunErr(err error) {
	if err == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.runErr == nil {
		f.runErr = err
	}
}

func normalizeForwardMode(raw string) string {
	mode := strings.TrimSpace(raw)
	if mode == "" {
		return "local"
	}
	return mode
}

func (f *LocalForward) bindRemoteListener(client *ssh.Client) (net.Listener, error) {
	host := strings.TrimSpace(f.forward.RemoteHost)
	if host == "" {
		host = "127.0.0.1"
	}
	addr := net.JoinHostPort(host, strconv.Itoa(f.forward.RemotePort))
	ln, err := client.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("remote listen %s failed: %w", addr, err)
	}
	return ln, nil
}

func bridge(c1, c2 net.Conn) {
	defer c1.Close()
	defer c2.Close()

	done := make(chan struct{}, 2)

	go func() {
		_, _ = io.Copy(c1, c2)
		done <- struct{}{}
	}()

	go func() {
		_, _ = io.Copy(c2, c1)
		done <- struct{}{}
	}()

	<-done
}

func dialSSH(host model.SSHHost, verifier HostKeyVerifier) (*ssh.Client, error) {
	conf, err := makeSSHClientConfig(host, verifier)
	if err != nil {
		return nil, err
	}

	hostName, port, err := normalizeHostAddress(host)
	if err != nil {
		return nil, err
	}
	addr := net.JoinHostPort(hostName, strconv.Itoa(port))

	client, err := ssh.Dial("tcp", addr, conf)
	if err != nil {
		return nil, fmt.Errorf("ssh dial %s failed: %w", addr, err)
	}
	return client, nil
}

func dialSSHChain(hosts []model.SSHHost, verifier HostKeyVerifier) (*ssh.Client, func(), error) {
	if len(hosts) == 0 {
		return nil, nil, fmt.Errorf("at least one ssh host is required")
	}

	first, err := dialSSH(hosts[0], verifier)
	if err != nil {
		return nil, nil, err
	}
	var closeOnce sync.Once
	closeFirst := func() {
		closeOnce.Do(func() { _ = first.Close() })
	}
	return dialChainFrom(first, closeFirst, hosts, verifier)
}

// dialChainFrom 从首跳 first 出发逐跳穿透 hosts[1:]，返回末跳客户端与整链
// 释放函数（幂等，含 closeFirst）。closeFirst 定义首跳的释放动作：独占链路
// 为 Close，池共享链路为归还引用。是 dialSSHChain 与 SSHConnPool 的公共穿链逻辑。
func dialChainFrom(first sshClient, closeFirst func(), hosts []model.SSHHost, verifier HostKeyVerifier) (*ssh.Client, func(), error) {
	if len(hosts) == 1 {
		client, ok := first.(*ssh.Client)
		if !ok {
			closeFirst()
			return nil, nil, fmt.Errorf("forward: first hop ssh client has unexpected type %T", first)
		}
		return client, closeFirst, nil
	}

	closers := make([]func(), 0, len(hosts))
	closers = append(closers, closeFirst)
	closeOnce := sync.Once{}
	closeAll := func() {
		closeOnce.Do(func() {
			for i := len(closers) - 1; i >= 0; i-- {
				closers[i]()
			}
		})
	}

	current := first
	for i := 1; i < len(hosts); i++ {
		next := hosts[i]
		conf, err := makeSSHClientConfig(next, verifier)
		if err != nil {
			closeAll()
			return nil, nil, err
		}

		hostName, port, err := normalizeHostAddress(next)
		if err != nil {
			closeAll()
			return nil, nil, fmt.Errorf("hop %d: %w", i, err)
		}
		addr := net.JoinHostPort(hostName, strconv.Itoa(port))

		conn, err := current.Dial("tcp", addr)
		if err != nil {
			closeAll()
			return nil, nil, fmt.Errorf("ssh dial %s via hop %d failed: %w", addr, i, err)
		}

		cconn, chans, reqs, err := ssh.NewClientConn(conn, addr, conf)
		if err != nil {
			_ = conn.Close()
			closeAll()
			return nil, nil, fmt.Errorf("ssh handshake %s via hop %d failed: %w", addr, i, err)
		}

		client := ssh.NewClient(cconn, chans, reqs)
		closers = append(closers, func() { _ = client.Close() })
		current = client
	}

	// len(hosts) > 1 时 current 必为循环内 ssh.NewClient 的产物。
	return current.(*ssh.Client), closeAll, nil
}

// normalizeHostAddress 统一地址归一化：去空白、默认端口 22。拨号地址与主机密钥
// verifier 的 (host, port) 身份必须来自同一归一化结果。
func normalizeHostAddress(host model.SSHHost) (string, int, error) {
	hostName := strings.TrimSpace(host.Host)
	if hostName == "" {
		return "", 0, fmt.Errorf("ssh host address is required")
	}
	port := host.Port
	if port <= 0 {
		port = 22
	}
	return hostName, port, nil
}

func makeSSHClientConfig(host model.SSHHost, verifier HostKeyVerifier) (*ssh.ClientConfig, error) {
	user := strings.TrimSpace(host.User)
	if user == "" {
		return nil, fmt.Errorf("ssh host user is required")
	}

	auth, err := makeAuthMethod(host)
	if err != nil {
		return nil, err
	}

	hostName, port, err := normalizeHostAddress(host)
	if err != nil {
		return nil, err
	}
	cb, err := makeHostKeyCallback(hostName, port, verifier)
	if err != nil {
		return nil, err
	}

	timeout := time.Duration(host.TimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	config := &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{auth},
		HostKeyCallback: cb,
		Timeout:         timeout,
	}

	// 添加HostKeyAlgorithms支持
	if host.HostKeyAlgorithms != "" {
		algorithms := parseHostKeyAlgorithms(host.HostKeyAlgorithms)
		config.HostKeyAlgorithms = algorithms
	}

	return config, nil
}

func makeAuthMethod(host model.SSHHost) (ssh.AuthMethod, error) {
	switch strings.TrimSpace(host.AuthType) {
	case "password":
		if host.Password == "" {
			return nil, fmt.Errorf("password auth requires password")
		}
		return ssh.Password(host.Password), nil
	case "ssh_key":
		signer, err := loadPrivateSigner(host.KeyPath, host.Password)
		if err != nil {
			return nil, err
		}
		return ssh.PublicKeys(ensureSignerSupportsLegacyRSA(signer)), nil
	case "ssh_agent":
		return ssh.PublicKeysCallback(func() ([]ssh.Signer, error) {
			return getSSHAgentSigners(host)
		}), nil
	default:
		return nil, fmt.Errorf("unsupported authType: %s", host.AuthType)
	}
}

func getSSHAgentSigners(host model.SSHHost) ([]ssh.Signer, error) {
	a, sock, err := getSSHAgent(host.AgentSocketPath)
	if err != nil {
		return nil, err
	}

	signers, err := a.Signers()
	if err != nil {
		// Agent may have restarted while the app keeps running.
		resetSSHAgent()
		var retryErr error
		a, sock, retryErr = getSSHAgent(host.AgentSocketPath)
		if retryErr != nil {
			return nil, retryErr
		}
		signers, err = a.Signers()
		if err != nil {
			return nil, fmt.Errorf("load identities from SSH agent failed (%s): %w", sock, err)
		}
	}
	if len(signers) == 0 {
		return nil, fmt.Errorf("ssh agent has no identities; selected socket=%s; run ssh-add first", sock)
	}
	for i := range signers {
		signers[i] = ensureSignerSupportsLegacyRSA(signers[i])
	}
	return signers, nil
}

func ensureSignerSupportsLegacyRSA(signer ssh.Signer) ssh.Signer {
	if signer == nil {
		return nil
	}
	pub := signer.PublicKey()
	if pub == nil || pub.Type() != ssh.KeyAlgoRSA {
		return signer
	}
	algorithmSigner, ok := signer.(ssh.AlgorithmSigner)
	if !ok {
		return signer
	}
	compatibleSigner, err := ssh.NewSignerWithAlgorithms(algorithmSigner, []string{
		ssh.KeyAlgoRSASHA512,
		ssh.KeyAlgoRSASHA256,
		ssh.KeyAlgoRSA,
	})
	if err != nil {
		return signer
	}
	return compatibleSigner
}

func loadPrivateSigner(keyPath, passphrase string) (ssh.Signer, error) {
	resolvedPath, err := resolveKeyPath(keyPath)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(resolvedPath)
	if err != nil {
		return nil, fmt.Errorf("read key file failed (%s): %w", resolvedPath, err)
	}

	if passphrase != "" {
		signer, passErr := ssh.ParsePrivateKeyWithPassphrase(raw, []byte(passphrase))
		if passErr == nil {
			return signer, nil
		}
		signer, keyErr := ssh.ParsePrivateKey(raw)
		if keyErr == nil {
			return signer, nil
		}
		return nil, fmt.Errorf("parse key failed: %v / %v", passErr, keyErr)
	}

	signer, err := ssh.ParsePrivateKey(raw)
	if err != nil {
		return nil, fmt.Errorf("parse key failed: %w", err)
	}
	return signer, nil
}

func resolveKeyPath(path string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", fmt.Errorf("ssh_key auth requires keyPath")
	}

	if strings.HasPrefix(trimmed, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home dir failed: %w", err)
		}
		trimmed = filepath.Join(home, strings.TrimPrefix(trimmed, "~/"))
	}

	if filepath.IsAbs(trimmed) {
		return filepath.Clean(trimmed), nil
	}

	if _, err := os.Stat(trimmed); err == nil {
		return filepath.Clean(trimmed), nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Clean(trimmed), nil
	}
	fromSSHDir := filepath.Join(home, ".ssh", trimmed)
	if _, err := os.Stat(fromSSHDir); err == nil {
		return filepath.Clean(fromSSHDir), nil
	}

	return filepath.Clean(trimmed), nil
}

func getSSHAgent(preferredSocketPath string) (agent.ExtendedAgent, string, error) {
	sshAgentMu.Lock()
	defer sshAgentMu.Unlock()

	candidates := agentSocketCandidates(preferredSocketPath)
	if len(candidates) == 0 {
		return nil, "", fmt.Errorf("no SSH agent socket found; set agent socket path, SSH_AUTH_SOCK, or TUNNELBOARD_SSH_AUTH_SOCK")
	}

	if sshAgentInst != nil {
		if signers, err := sshAgentInst.Signers(); err == nil && len(signers) > 0 && socketInCandidates(sshAgentSock, candidates) {
			return sshAgentInst, sshAgentSock, nil
		}
		resetSSHAgentLocked()
	}

	var failures []string
	for _, sock := range candidates {
		conn, err := dialSSHAgentSocket(sock)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", sock, err))
			continue
		}

		inst := agent.NewClient(conn)
		signers, err := inst.Signers()
		if err != nil {
			_ = conn.Close()
			failures = append(failures, fmt.Sprintf("%s: %v", sock, err))
			continue
		}
		if len(signers) == 0 {
			_ = conn.Close()
			failures = append(failures, fmt.Sprintf("%s: no identities", sock))
			continue
		}

		sshAgentInst = inst
		sshAgentSock = sock
		sshAgentConn = conn
		return sshAgentInst, sshAgentSock, nil
	}

	return nil, "", fmt.Errorf("ssh agent has no usable identities; tried: %s", strings.Join(failures, "; "))
}

func agentSocketCandidates(preferredSocketPath string) []string {
	seen := map[string]struct{}{}
	var candidates []string
	add := func(sock string) {
		normalized := normalizeAgentSocketPath(sock)
		if normalized == "" {
			return
		}
		if _, exists := seen[normalized]; exists {
			return
		}
		seen[normalized] = struct{}{}
		candidates = append(candidates, normalized)
	}

	add(preferredSocketPath)
	add(os.Getenv("TUNNELBOARD_SSH_AUTH_SOCK"))
	add(os.Getenv("SSH_AUTH_SOCK"))

	for _, s := range defaultAgentSocketCandidates() {
		add(s)
	}

	if runtime.GOOS != "windows" {
		home, err := os.UserHomeDir()
		if err == nil && strings.TrimSpace(home) != "" {
			add(filepath.Join(home, ".ssh", "ssh-agent.sock"))
		}
	}

	return candidates
}

func socketInCandidates(sock string, candidates []string) bool {
	normalizedSock := normalizeAgentSocketPath(sock)
	for _, candidate := range candidates {
		if candidate == normalizedSock {
			return true
		}
	}
	return false
}

func normalizeAgentSocketPath(sock string) string {
	s := strings.TrimSpace(sock)
	if s == "" {
		return ""
	}
	if strings.HasPrefix(s, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return s
		}
		return filepath.Join(home, strings.TrimPrefix(s, "~/"))
	}
	return s
}

func resetSSHAgent() {
	sshAgentMu.Lock()
	defer sshAgentMu.Unlock()
	resetSSHAgentLocked()
}

func resetSSHAgentLocked() {
	if sshAgentConn != nil {
		_ = sshAgentConn.Close()
	}
	sshAgentInst = nil
	sshAgentSock = ""
	sshAgentConn = nil
}

func splitAlgorithms(s string) []string {
	return strings.Split(s, ",")
}

// parseHostKeyAlgorithms 解析HostKeyAlgorithms字符串，支持OpenSSH风格的语法：
// - "+ssh-rsa"：在默认算法基础上添加ssh-rsa
// - "ssh-rsa"：只使用ssh-rsa（替换默认算法）
// - "-ssh-rsa"：从默认算法中移除ssh-rsa
// - 逗号分隔的列表：直接使用该列表
func parseHostKeyAlgorithms(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}

	// 检查是否是OpenSSH风格的语法
	if strings.HasPrefix(s, "+") {
		// "+ssh-rsa" 语法：在默认算法基础上添加
		algorithm := strings.TrimPrefix(s, "+")
		return append(getDefaultHostKeyAlgorithms(), algorithm)
	} else if strings.HasPrefix(s, "-") {
		// "-ssh-rsa" 语法：从默认算法中移除
		algorithm := strings.TrimPrefix(s, "-")
		return removeFromDefaultHostKeyAlgorithms(algorithm)
	} else {
		// 普通逗号分隔的列表
		return splitAlgorithms(s)
	}
}

// getDefaultHostKeyAlgorithms 返回Go SSH默认支持的主机密钥算法
func getDefaultHostKeyAlgorithms() []string {
	// Go SSH默认支持的主机密钥算法
	return []string{
		"ssh-rsa",
		"rsa-sha2-256",
		"rsa-sha2-512",
		"ecdsa-sha2-nistp256",
		"ecdsa-sha2-nistp384",
		"ecdsa-sha2-nistp521",
		"ssh-ed25519",
	}
}

// removeFromDefaultHostKeyAlgorithms 从默认算法列表中移除指定的算法
func removeFromDefaultHostKeyAlgorithms(algorithm string) []string {
	defaultAlgos := getDefaultHostKeyAlgorithms()
	var result []string
	for _, algo := range defaultAlgos {
		if algo != algorithm {
			result = append(result, algo)
		}
	}
	return result
}
