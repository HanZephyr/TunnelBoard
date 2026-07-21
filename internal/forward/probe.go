package forward

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/HanZephyr/TunnelBoard/internal/model"

	"golang.org/x/crypto/ssh"
)

var ErrKeepAliveTimeout = errors.New("ssh keepalive timeout")

type LocalListenerState string

const (
	LocalListenerAvailable LocalListenerState = "available"
	LocalListenerOccupied  LocalListenerState = "occupied"
	LocalListenerInvalid   LocalListenerState = "invalid"
	LocalListenerUnknown   LocalListenerState = "unknown"
)

type LocalListenerProbe struct {
	State             LocalListenerState
	NormalizedAddress string
	Err               error
}

type keepAliveResult struct {
	latency time.Duration
	err     error
}

// probeSSH 对一次 SSH global request 设置独立 deadline。调用者在 timeout 后
// 关闭捕获的精确连接代，使可能仍阻塞的 SendRequest goroutine 退出。
func probeSSH(ctx context.Context, client sshClient, timeout time.Duration) (time.Duration, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	result := make(chan keepAliveResult, 1)
	go func() {
		latency, err := sendKeepAliveRequest(client)
		result <- keepAliveResult{latency: latency, err: err}
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-timer.C:
		return 0, ErrKeepAliveTimeout
	case r := <-result:
		return r.latency, r.err
	}
}

// sendKeepAliveRequest 发送一次 keepalive@openssh.com 请求并测量往返时延；
// LocalForward 探测循环（经 TestSSHHostLatency）与 SSHConnPool 池级 keepalive
// 共用它，避免两份探测实现。
func sendKeepAliveRequest(client sshClient) (time.Duration, error) {
	start := time.Now()
	_, _, err := client.SendRequest("keepalive@openssh.com", true, nil)
	if err != nil {
		return 0, err
	}
	return time.Since(start), nil
}

// TestSSHHostLatency measures pure SSH channel round-trip latency via keepalive.
func TestSSHHostLatency(client *ssh.Client) (time.Duration, error) {
	if client == nil {
		return 0, fmt.Errorf("ssh client is nil")
	}
	return sendKeepAliveRequest(client)
}

// TestSSHHostConnection verifies SSH handshake/auth against the SSH host.
func TestSSHHostConnection(host model.SSHHost, verifier HostKeyVerifier) error {
	client, err := dialSSH(host, verifier)
	if err != nil {
		return err
	}
	return client.Close()
}

// TestForwardConnection verifies forward prerequisites and target reachability.
// Currently it supports "local", "remote" and "dynamic" modes only.
func TestForwardConnection(fw model.Forward, hosts []model.SSHHost, verifier HostKeyVerifier) (time.Duration, error) {
	mode := strings.TrimSpace(fw.Mode)
	if mode == "" {
		mode = "local"
	}
	if mode != "local" && mode != "remote" && mode != "dynamic" {
		return 0, fmt.Errorf("mode %s test is not supported yet", mode)
	}

	if mode == "local" || mode == "dynamic" {
		localHost := strings.TrimSpace(fw.LocalHost)
		if localHost == "" {
			localHost = "127.0.0.1"
		}
		localAddr := net.JoinHostPort(localHost, strconv.Itoa(fw.LocalPort))
		ln, err := net.Listen("tcp", localAddr)
		if err != nil {
			return 0, fmt.Errorf("local listen %s failed: %w", localAddr, err)
		}
		_ = ln.Close()
	}

	client, closeChain, err := dialSSHChain(hosts, verifier)
	if err != nil {
		return 0, err
	}
	defer closeChain()

	latency, err := TestSSHHostLatency(client)
	if err != nil {
		return 0, fmt.Errorf("measure ssh latency failed: %w", err)
	}

	if mode == "dynamic" {
		if err := probeDynamicForwardCapability(client); err != nil {
			return 0, err
		}
		return latency, nil
	}
	if mode == "remote" {
		if err := probeRemoteListen(client, fw.RemoteHost, fw.RemotePort); err != nil {
			return 0, err
		}
		return latency, nil
	}

	if err := probeRemoteDial(client, fw.RemoteHost, fw.RemotePort); err != nil {
		return 0, err
	}
	return latency, nil
}

func probeRemoteDial(client *ssh.Client, remoteHost string, remotePort int) error {
	addr := net.JoinHostPort(strings.TrimSpace(remoteHost), strconv.Itoa(remotePort))
	remoteConn, err := client.Dial("tcp", addr)
	if err != nil {
		return fmt.Errorf("remote dial %s failed: %w", addr, err)
	}
	return remoteConn.Close()
}

func probeRemoteListen(client *ssh.Client, remoteHost string, remotePort int) error {
	host := strings.TrimSpace(remoteHost)
	if host == "" {
		host = "127.0.0.1"
	}
	addr := net.JoinHostPort(host, strconv.Itoa(remotePort))
	ln, err := client.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("remote listen %s failed: %w", addr, err)
	}
	return ln.Close()
}

func probeDynamicForwardCapability(client *ssh.Client) error {
	// Use a closed local target to detect "forwarding prohibited" without requiring a real endpoint.
	probeAddr := "127.0.0.1:1"
	remoteConn, err := client.Dial("tcp", probeAddr)
	if err == nil {
		return remoteConn.Close()
	}
	if isPortForwardDenied(err) {
		return fmt.Errorf("dynamic forward is not allowed by ssh server: %w", err)
	}
	return nil
}

func isPortForwardDenied(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "administratively prohibited") ||
		strings.Contains(msg, "forwarding disabled") ||
		strings.Contains(msg, "port forwarding disabled")
}

// CheckLocalPortAvailable 通过实际绑定预检本地监听端口；可绑定则立即释放并返回 nil。
// 空 host 按 127.0.0.1 处理（与 LocalForward 启动时的回退一致）。
func CheckLocalPortAvailable(host string, port int) error {
	preview := PreviewLocalListener(host, port)
	if preview.State == LocalListenerAvailable {
		return nil
	}
	return preview.Err
}

// PreviewLocalListener 只观察当前 bind 结果，不创建 reservation，也不修改 Runtime。
// owned_by_self 由 biz.RuntimeBiz 的只读 ownership Interface 在此结果之前判定。
func PreviewLocalListener(host string, port int) LocalListenerProbe {
	host = strings.TrimSpace(host)
	if host == "" {
		host = "127.0.0.1"
	}
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	if port < 1 || port > 65535 {
		return LocalListenerProbe{State: LocalListenerInvalid, NormalizedAddress: addr, Err: fmt.Errorf("invalid local port %d", port)}
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		wrapped := fmt.Errorf("listen %s failed: %w", addr, err)
		if errors.Is(err, syscall.EADDRINUSE) || errors.Is(err, syscall.Errno(10048)) {
			return LocalListenerProbe{State: LocalListenerOccupied, NormalizedAddress: addr, Err: wrapped}
		}
		return LocalListenerProbe{State: LocalListenerUnknown, NormalizedAddress: addr, Err: wrapped}
	}
	if err := ln.Close(); err != nil {
		return LocalListenerProbe{State: LocalListenerUnknown, NormalizedAddress: addr, Err: fmt.Errorf("close listener %s failed: %w", addr, err)}
	}
	return LocalListenerProbe{State: LocalListenerAvailable, NormalizedAddress: addr}
}
