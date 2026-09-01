package forward

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

// 被占用的端口预检必须报错，释放后必须通过。
func TestCheckLocalPortAvailable(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port, err := strconv.Atoi(strings.Split(ln.Addr().String(), ":")[1])
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}

	if err := CheckLocalPortAvailable("127.0.0.1", port); err == nil {
		t.Fatal("occupied port must fail preflight")
	}
	if err := ln.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := CheckLocalPortAvailable("127.0.0.1", port); err != nil {
		t.Fatalf("freed port should pass preflight: %v", err)
	}
	// 空 host 按 127.0.0.1 处理，不得 panic。
	if err := CheckLocalPortAvailable("", port); err != nil {
		t.Fatalf("empty host should default to loopback: %v", err)
	}
}

func TestPreviewLocalListenerClassifiesBindResult(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port

	occupied := PreviewLocalListener("", port)
	if occupied.State != LocalListenerOccupied || occupied.NormalizedAddress != net.JoinHostPort("127.0.0.1", strconv.Itoa(port)) {
		t.Fatalf("occupied preview = %+v", occupied)
	}
	_ = ln.Close()
	available := PreviewLocalListener("127.0.0.1", port)
	if available.State != LocalListenerAvailable || available.Err != nil {
		t.Fatalf("available preview = %+v", available)
	}
	invalid := PreviewLocalListener("127.0.0.1", 0)
	if invalid.State != LocalListenerInvalid || invalid.Err == nil {
		t.Fatalf("invalid preview = %+v", invalid)
	}
}

// bind 错误分类必须区分"占用"与"特权端口/权限拒绝"（macOS/Linux <1024 端口）。
func TestClassifyLocalListenError(t *testing.T) {
	cases := []struct {
		name  string
		err   error
		state LocalListenerState
	}{
		{"eacces", syscall.EACCES, LocalListenerPrivileged},
		{"eperm", syscall.EPERM, LocalListenerPrivileged},
		{"wrapped-eacces", fmt.Errorf("bind: %w", syscall.EACCES), LocalListenerPrivileged},
		{"wrapped-eperm", fmt.Errorf("listen: %w", syscall.EPERM), LocalListenerPrivileged},
		{"eaddrinuse", syscall.EADDRINUSE, LocalListenerOccupied},
		{"wrapped-eaddrinuse", fmt.Errorf("bind: %w", syscall.EADDRINUSE), LocalListenerOccupied},
		{"other", errors.New("boom"), LocalListenerUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			probe := classifyLocalListenError("127.0.0.1:443", tc.err)
			if probe.State != tc.state {
				t.Fatalf("classify(%v) = %s, want %s (err=%v)", tc.err, probe.State, tc.state, probe.Err)
			}
		})
	}
}
