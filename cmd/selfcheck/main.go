//go:build windows

// selfcheck 是 TunnelBoard 的冒烟验收驱动器：以真实组件（helper 管道客户端、
// Caddy Adapter、路由编译器）对真实系统执行最小闭环，供 scripts/smoke-windows.py 编排。
// 每阶段输出 SMOKE-OK <stage> 或 SMOKE-FAIL: <原因> 并以退出码报告。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/HanZephyr/TunnelBoard/internal/caddy"
	"github.com/HanZephyr/TunnelBoard/internal/helper"
	"github.com/HanZephyr/TunnelBoard/internal/model"
	"github.com/HanZephyr/TunnelBoard/internal/route"
)

func main() {
	// 首个位置参数（非 - 开头）是阶段名；Go flag 包遇位置参数即停止解析，需先剥离。
	stageName := "ping"
	args := os.Args[1:]
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		stageName = args[0]
		args = args[1:]
	}

	domain := flag.String("domain", "tunnelboard-smoke.test", "smoke test domain")
	port := flag.Int("port", 8099, "local upstream port for caddy stage")
	datadir := flag.String("datadir", "", "caddy adapter data dir (caddy stages)")
	_ = flag.CommandLine.Parse(args)

	var err error
	switch stageName {
	case "ping":
		err = stagePing()
	case "helper-session-apply":
		err = stageHelperSessionApply(*domain)
	case "hosts-apply":
		err = stageHosts(*domain, true)
	case "hosts-remove":
		err = stageHosts(*domain, false)
	case "trust-ca":
		err = stageCA(*datadir, true)
	case "untrust-ca":
		err = stageCA(*datadir, false)
	case "caddy-start":
		err = stageCaddyStart(*datadir, *domain, *port)
	case "caddy-stop":
		err = stageCaddyStop(*datadir)
	default:
		err = fmt.Errorf("unknown stage %q", stageName)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "SMOKE-FAIL:", err)
		os.Exit(1)
	}
	fmt.Println("SMOKE-OK", stageName)
}

func callOKWith(client *helper.Client, req helper.Request) error {
	resp, err := client.Call(req)
	if err != nil {
		return err
	}
	if !resp.OK {
		return errors.New(resp.Error)
	}
	return nil
}

func stageHelperSessionApply(domain string) error {
	client := helper.NewClient()
	defer client.Close(context.Background())
	version, err := client.Ping()
	if err != nil {
		return err
	}
	fmt.Println("helper version:", version)
	if err := stageHostsWithClient(client, domain, true); err != nil {
		return err
	}
	// 第二次执行验证同一 selfcheck 应用生命周期内复用现有 Helper，不再 UAC。
	return stageHostsWithClient(client, domain, true)
}

func stagePing() error {
	client := helper.NewClient()
	defer client.Close(context.Background())
	version, err := client.Ping()
	if err != nil {
		return err
	}
	fmt.Println("helper version:", version)
	return nil
}

// stageHosts 在保留现有受托管条目的前提下加入/移除冒烟域名，并回读验证。
func stageHosts(domain string, add bool) error {
	client := helper.NewClient()
	defer client.Close(context.Background())
	return stageHostsWithClient(client, domain, add)
}

func stageHostsWithClient(client *helper.Client, domain string, add bool) error {
	content, err := os.ReadFile(helper.SystemHostsPath())
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read hosts: %w", err)
	}
	var entries []route.HostEntry
	for _, e := range helper.ParseManagedHosts(string(content)) {
		if e.Domain != domain {
			entries = append(entries, e)
		}
	}
	if add {
		entries = append(entries, route.HostEntry{Domain: domain, IP: "127.0.0.1"})
	}
	if err := callOKWith(client, helper.Request{Op: helper.OpApplyManagedHosts, Hosts: entries}); err != nil {
		return err
	}

	after, err := os.ReadFile(helper.SystemHostsPath())
	if err != nil {
		return fmt.Errorf("reread hosts: %w", err)
	}
	found := false
	for _, e := range helper.ParseManagedHosts(string(after)) {
		if e.Domain == domain {
			found = true
		}
	}
	if add && !found {
		return fmt.Errorf("smoke domain %s not present after apply", domain)
	}
	if !add && found {
		return fmt.Errorf("smoke domain %s still present after remove", domain)
	}
	return nil
}

func stageCA(datadir string, trust bool) error {
	if strings.TrimSpace(datadir) == "" {
		return errors.New("-datadir is required")
	}
	trustStore := helper.NewCurrentUserCATrustAt(datadir)
	if trust {
		identity, err := trustStore.EnsureCurrentCaddyCATrusted(context.Background())
		if err == nil {
			fmt.Println("current-user CA:", identity.SHA256)
		}
		return err
	}
	return trustStore.RemoveCurrentCaddyCA(context.Background())
}

func stageCaddyStart(datadir, domain string, port int) error {
	if strings.TrimSpace(datadir) == "" {
		return fmt.Errorf("-datadir is required")
	}
	data := model.VaultData{
		Version:  1,
		Folders:  []model.Folder{{ID: 1, Name: "smoke"}},
		SSHHosts: []model.SSHHost{{ID: 1, Name: "dummy"}},
		Forwards: []model.Forward{{ID: 1, FolderID: 1, Name: "smoke", Mode: "local",
			ChainHostIDs: []int{1}, LocalHost: "127.0.0.1", LocalPort: port}},
		WebRoutes: []model.WebRoute{{ID: 1, ForwardID: 1, Domain: domain, CaddyEnabled: true, UpstreamScheme: "http"}},
	}
	config, err := route.CompileCaddy(data)
	if err != nil {
		return fmt.Errorf("compile caddy config: %w", err)
	}
	adapter := caddy.New(datadir) // 测试二进制经 TUNNELBOARD_CADDY_PATH 定位（跳过完整性校验）
	if err := adapter.Reload(config); err != nil {
		return fmt.Errorf("start caddy: %w", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for !adapter.Running() {
		if time.Now().After(deadline) {
			return fmt.Errorf("caddy did not come up within 10s")
		}
		time.Sleep(200 * time.Millisecond)
	}
	fmt.Println("caddy root ca:", filepath.Join(datadir, "caddy", "pki", "authorities", "local", "root.crt"))
	return nil
}

func stageCaddyStop(datadir string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return caddy.New(datadir).Stop(ctx)
}
