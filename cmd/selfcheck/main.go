//go:build windows

// selfcheck 是 TunnelBoard 的冒烟验收驱动器：以真实组件（helper 管道客户端、
// Caddy Adapter、路由编译器）对真实系统执行最小闭环，供 scripts/smoke-windows.py 编排。
// 每阶段输出 SMOKE-OK <stage> 或 SMOKE-FAIL: <原因> 并以退出码报告。
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/pem"
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
	cafile := flag.String("cafile", "", "root CA PEM path (ca stages)")
	_ = flag.CommandLine.Parse(args)

	var err error
	switch stageName {
	case "ping":
		err = stagePing()
	case "hosts-apply":
		err = stageHosts(*domain, true)
	case "hosts-remove":
		err = stageHosts(*domain, false)
	case "trust-ca":
		err = stageCA(*cafile, true)
	case "untrust-ca":
		err = stageCA(*cafile, false)
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

// callOK 发送一个特权请求并把非 OK 应答转为错误。
func callOK(req helper.Request) error {
	resp, err := helper.NewClient().Call(req)
	if err != nil {
		return err
	}
	if !resp.OK {
		return errors.New(resp.Error)
	}
	return nil
}

func stagePing() error {
	version, err := helper.NewClient().Ping()
	if err != nil {
		return err
	}
	fmt.Println("helper version:", version)
	return nil
}

// stageHosts 在保留现有受托管条目的前提下加入/移除冒烟域名，并回读验证。
func stageHosts(domain string, add bool) error {
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
	if err := callOK(helper.Request{Op: helper.OpApplyManagedHosts, Hosts: entries}); err != nil {
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

func stageCA(cafile string, trust bool) error {
	der, fp, err := readCAFingerprint(cafile)
	if err != nil {
		return err
	}
	if trust {
		return callOK(helper.Request{Op: helper.OpTrustLocalCA, CertDER: der, CertSHA256: fp})
	}
	return callOK(helper.Request{Op: helper.OpUntrustLocalCA, CertSHA256: fp})
}

func readCAFingerprint(path string) (der []byte, fp string, err error) {
	data, err := os.ReadFile(strings.TrimSpace(path))
	if err != nil {
		return nil, "", fmt.Errorf("read ca file: %w", err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, "", fmt.Errorf("ca file %s is not valid PEM", path)
	}
	sum := sha256.Sum256(block.Bytes)
	return block.Bytes, hex.EncodeToString(sum[:]), nil
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
	return caddy.New(datadir).Stop()
}
