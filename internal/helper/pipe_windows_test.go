//go:build windows

package helper_test

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/HanZephyr/TunnelBoard/internal/helper"
	"github.com/HanZephyr/TunnelBoard/internal/route"
)

// 在真实命名管道上验证 客户端↔服务 回环：ping、白名单操作落盘、未知 op 拒绝。
func TestPipeRoundTrip(t *testing.T) {
	hostsPath := filepath.Join(t.TempDir(), "hosts")
	trustCalls, untrustCalls := 0, 0
	env := helper.Environment{
		HostsPath: hostsPath,
		TrustCA:   func(certDER []byte, sha256 string) error { trustCalls++; return nil },
		UntrustCA: func(sha256 string) error { untrustCalls++; return nil },
		Version:   "test-1",
	}
	pipePath := `\\.\pipe\tunnelboard-helper-test-` + strconv.Itoa(os.Getpid())
	owner, ownerErr := helper.CurrentUserSID()
	if ownerErr != nil {
		t.Fatalf("CurrentUserSID: %v", ownerErr)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = helper.ServePipe(ctx, env, pipePath, owner) }()

	client := &helper.Client{PipePath: pipePath, Timeout: 3 * time.Second}
	var version string
	var err error
	for i := 0; i < 100; i++ {
		version, err = client.Ping()
		if err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("ping after server start: %v", err)
	}
	if version != "test-1" {
		t.Fatalf("version = %q, want test-1", version)
	}

	resp, err := client.Call(helper.Request{
		Op:    helper.OpApplyManagedHosts,
		Hosts: []route.HostEntry{{Domain: "db.test", IP: "127.0.0.1"}},
	})
	if err != nil {
		t.Fatalf("apply call: %v", err)
	}
	if !resp.OK {
		t.Fatalf("apply response not ok: %s", resp.Error)
	}
	content, err := os.ReadFile(hostsPath)
	if err != nil {
		t.Fatalf("read hosts: %v", err)
	}
	if !strings.Contains(string(content), helper.BlockBegin) || !strings.Contains(string(content), "127.0.0.1 db.test") {
		t.Fatalf("managed block not written: %q", content)
	}

	resp, err = client.Call(helper.Request{Op: "drop_table"})
	if err != nil {
		t.Fatalf("unknown op call: %v", err)
	}
	if resp.OK || !strings.Contains(resp.Error, "unknown op") {
		t.Fatalf("unknown op must be rejected: %+v", resp)
	}
	if trustCalls != 0 || untrustCalls != 0 {
		t.Fatalf("CA ops must not run in this test, got %d/%d calls", trustCalls, untrustCalls)
	}
}
