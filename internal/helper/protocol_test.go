package helper_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HanZephyr/TunnelBoard/internal/helper"
	"github.com/HanZephyr/TunnelBoard/internal/route"
)

// countingEnvironment 记录注入函数的调用次数，用于断言校验失败时零副作用。
type countingEnvironment struct {
	hostsPath    string
	version      string
	trustErr     error
	untrustErr   error
	trustCalls   int
	untrustCalls int
}

func newCountingEnvironment(t *testing.T) *countingEnvironment {
	t.Helper()
	return &countingEnvironment{hostsPath: filepath.Join(t.TempDir(), "hosts")}
}

func (e *countingEnvironment) environment() helper.Environment {
	return helper.Environment{
		HostsPath: e.hostsPath,
		Version:   e.version,
		TrustCA: func(certDER []byte, sha256 string) error {
			e.trustCalls++
			return e.trustErr
		},
		UntrustCA: func(sha256 string) error {
			e.untrustCalls++
			return e.untrustErr
		},
	}
}

// assertNoSideEffects 断言 CA 函数零调用，且 hosts 文件及其备份/临时文件均未产生。
func (e *countingEnvironment) assertNoSideEffects(t *testing.T) {
	t.Helper()
	if e.trustCalls != 0 || e.untrustCalls != 0 {
		t.Fatalf("trustCalls=%d, untrustCalls=%d, want both 0", e.trustCalls, e.untrustCalls)
	}
	for _, p := range []string{e.hostsPath, e.hostsPath + ".tunnelboard.bak", e.hostsPath + ".tunnelboard.tmp"} {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s should not exist after rejected request (stat err = %v)", p, err)
		}
	}
}

// 未知 op 一律拒绝（红线：不执行白名单外请求），包括空 op。
func TestHandleRequestRejectsUnknownOp(t *testing.T) {
	for _, op := range []string{"restart_service", "EnsureInstalled", ""} {
		env := newCountingEnvironment(t)
		resp := helper.HandleRequest(helper.Request{Op: op}, env.environment())
		if resp.OK || resp.Error == "" {
			t.Fatalf("op %q: resp = %+v, want OK=false with error", op, resp)
		}
		env.assertNoSideEffects(t)
	}
}

// 旧版机器级 CA 操作不再属于提权 Helper 白名单；即使请求形态仍能被旧调用方构造，
// Helper 也必须拒绝且保持零副作用。
func TestHandleRequestRejectsLegacyCAOperations(t *testing.T) {
	for _, req := range []helper.Request{
		{Op: "trust_local_ca"},
		{Op: "untrust_local_ca"},
	} {
		env := newCountingEnvironment(t)
		resp := helper.HandleRequest(req, env.environment())
		if resp.OK || !strings.Contains(resp.Error, "unknown op") {
			t.Fatalf("op %q: resp = %+v, want unknown-op rejection", req.Op, resp)
		}
		env.assertNoSideEffects(t)
	}
}

// ping 返回注入的版本号，无其他副作用。
func TestHandleRequestPingReturnsVersion(t *testing.T) {
	env := newCountingEnvironment(t)
	env.version = "1.2.3"
	resp := helper.HandleRequest(helper.Request{Op: "ping"}, env.environment())
	if !resp.OK || resp.Version != "1.2.3" || resp.Error != "" {
		t.Fatalf("resp = %+v, want OK with version 1.2.3", resp)
	}
	env.assertNoSideEffects(t)
}

func validHosts() []route.HostEntry {
	return []route.HostEntry{
		{Domain: "app.test", IP: "127.0.0.1"},
		{Domain: "db.test", IP: "127.0.0.1"},
	}
}

// 合法 apply 写入受托管区块，文件内容为渲染结果，不触碰 CA 函数。
func TestHandleRequestApplyManagedHosts(t *testing.T) {
	env := newCountingEnvironment(t)
	resp := helper.HandleRequest(helper.Request{Op: "apply_managed_hosts", Hosts: validHosts()}, env.environment())
	if !resp.OK {
		t.Fatalf("resp = %+v, want OK", resp)
	}
	data, err := os.ReadFile(env.hostsPath)
	if err != nil {
		t.Fatalf("read hosts: %v", err)
	}
	want := helper.BlockBegin + "\r\n127.0.0.1 app.test\r\n127.0.0.1 db.test\r\n" + helper.BlockEnd + "\r\n"
	if string(data) != want {
		t.Fatalf("hosts content = %q, want %q", data, want)
	}
	if env.trustCalls != 0 || env.untrustCalls != 0 {
		t.Fatalf("CA functions called for hosts op: trust=%d untrust=%d", env.trustCalls, env.untrustCalls)
	}
}

// remove 清空区块但保留区块外内容。
func TestHandleRequestRemoveManagedHosts(t *testing.T) {
	env := newCountingEnvironment(t)
	original := "# my own comment\r\n"
	if err := os.WriteFile(env.hostsPath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	if resp := helper.HandleRequest(helper.Request{Op: "apply_managed_hosts", Hosts: validHosts()}, env.environment()); !resp.OK {
		t.Fatalf("apply: %+v", resp)
	}
	resp := helper.HandleRequest(helper.Request{Op: "remove_managed_hosts"}, env.environment())
	if !resp.OK {
		t.Fatalf("resp = %+v, want OK", resp)
	}
	data, err := os.ReadFile(env.hostsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != original {
		t.Fatalf("hosts content = %q, want original %q", data, original)
	}
}

// 各类非法 hosts 条目均被拒绝且零副作用；253 字节域名是合法边界。
func TestHandleRequestRejectsInvalidHosts(t *testing.T) {
	cases := map[string][]route.HostEntry{
		"empty domain":        {{Domain: "", IP: "127.0.0.1"}},
		"domain with space":   {{Domain: "bad name.test", IP: "127.0.0.1"}},
		"domain with tab":     {{Domain: "bad\tname.test", IP: "127.0.0.1"}},
		"domain with newline": {{Domain: "bad\nname.test", IP: "127.0.0.1"}},
		"domain without dot":  {{Domain: "localhost", IP: "127.0.0.1"}},
		"domain too long":     {{Domain: strings.Repeat("a", 252) + ".t", IP: "127.0.0.1"}},
		"duplicate domain": {
			{Domain: "db.test", IP: "127.0.0.1"},
			{Domain: "db.test", IP: "127.0.0.1"},
		},
		"duplicate domain different case": {
			{Domain: "DB.test", IP: "127.0.0.1"},
			{Domain: "db.test", IP: "127.0.0.1"},
		},
		"non-loopback IP":  {{Domain: "db.test", IP: "10.0.0.1"}},
		"loopback-ish IP":  {{Domain: "db.test", IP: "127.0.0.2"}},
		"IPv6 loopback IP": {{Domain: "db.test", IP: "::1"}},
		"empty IP":         {{Domain: "db.test", IP: ""}},
	}
	for name, hosts := range cases {
		t.Run(name, func(t *testing.T) {
			env := newCountingEnvironment(t)
			resp := helper.HandleRequest(helper.Request{Op: "apply_managed_hosts", Hosts: hosts}, env.environment())
			if resp.OK || resp.Error == "" {
				t.Fatalf("resp = %+v, want OK=false with error", resp)
			}
			env.assertNoSideEffects(t)
		})
	}

	// 边界：恰好 253 字节的域名应通过校验并成功写入。
	long253 := strings.Repeat("a", 251) + ".t"
	env := newCountingEnvironment(t)
	resp := helper.HandleRequest(helper.Request{Op: "apply_managed_hosts", Hosts: []route.HostEntry{{Domain: long253, IP: "127.0.0.1"}}}, env.environment())
	if !resp.OK {
		t.Fatalf("253-byte domain: resp = %+v, want OK", resp)
	}
}

// 条目数上限 256：257 拒绝且零副作用，256 通过。
func TestHandleRequestHostsEntryLimit(t *testing.T) {
	entries := func(n int) []route.HostEntry {
		out := make([]route.HostEntry, 0, n)
		for i := 0; i < n; i++ {
			out = append(out, route.HostEntry{Domain: fmt.Sprintf("app%03d.test", i), IP: "127.0.0.1"})
		}
		return out
	}

	env := newCountingEnvironment(t)
	resp := helper.HandleRequest(helper.Request{Op: "apply_managed_hosts", Hosts: entries(257)}, env.environment())
	if resp.OK {
		t.Fatalf("257 entries: resp = %+v, want rejected", resp)
	}
	env.assertNoSideEffects(t)

	env2 := newCountingEnvironment(t)
	resp = helper.HandleRequest(helper.Request{Op: "apply_managed_hosts", Hosts: entries(256)}, env2.environment())
	if !resp.OK {
		t.Fatalf("256 entries: resp = %+v, want OK", resp)
	}
}

// hosts 写入失败（路径为目录）透传为失败响应。
func TestHandleRequestApplyManagedHostsPropagatesWriteError(t *testing.T) {
	env := newCountingEnvironment(t)
	env.hostsPath = t.TempDir() // 目录：读取即失败
	resp := helper.HandleRequest(helper.Request{Op: "apply_managed_hosts", Hosts: validHosts()}, env.environment())
	if resp.OK || resp.Error == "" {
		t.Fatalf("resp = %+v, want OK=false with error", resp)
	}
}
