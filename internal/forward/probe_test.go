package forward

import (
	"net"
	"strconv"
	"strings"
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
