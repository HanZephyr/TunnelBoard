package biz_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/HanZephyr/TunnelBoard/internal/biz"
	caddycore "github.com/HanZephyr/TunnelBoard/internal/caddy"
	"github.com/HanZephyr/TunnelBoard/internal/helper"
	"github.com/HanZephyr/TunnelBoard/internal/model"
	"github.com/HanZephyr/TunnelBoard/internal/route"
)

type fakeHelperClient struct {
	calls       []helper.Request
	failOnOp    map[string]string
	ensureErr   error
	ensureCalls int
	pingErr     error
}

func (f *fakeHelperClient) Call(req helper.Request) (helper.Response, error) {
	f.calls = append(f.calls, req)
	if msg, ok := f.failOnOp[req.Op]; ok {
		return helper.Response{OK: false, Error: msg}, nil
	}
	return helper.Response{OK: true}, nil
}

func (f *fakeHelperClient) Ping() (string, error) {
	if f.pingErr != nil {
		return "", f.pingErr
	}
	return "fake", nil
}

func (f *fakeHelperClient) EnsureInstalled() error {
	f.ensureCalls++
	return f.ensureErr
}

type fakeCaddyAdapter struct {
	mu          sync.Mutex
	running     bool
	diagnoseErr error
	reloadErr   error
	rootCA      []byte
	rootCAErr   error
	reloads     [][]byte
	lastConfig  []byte
	stopCalls   int
	applyDelay  time.Duration
	activeApply int
	maxApply    int
}

func (f *fakeCaddyAdapter) DiagnosePort() error {
	if f.diagnoseErr != nil {
		return f.diagnoseErr
	}
	// 模拟生产 Caddy 自身占着 443：已运行时端口预检必然失败，
	// 用以验证业务层不会在已运行分支里被这次预检误伤。
	if f.running {
		return errors.New("caddy: 127.0.0.1:443 unavailable: bind: address in use")
	}
	return nil
}
func (f *fakeCaddyAdapter) Status(context.Context) caddycore.Status {
	lastError := ""
	if f.diagnoseErr != nil {
		lastError = f.diagnoseErr.Error()
	}
	return caddycore.Status{Owned: f.running, Ready: f.running, LastError: lastError}
}
func (f *fakeCaddyAdapter) Apply(_ context.Context, _ string, config []byte) (caddycore.ApplyResult, error) {
	f.mu.Lock()
	f.activeApply++
	if f.activeApply > f.maxApply {
		f.maxApply = f.activeApply
	}
	delay := f.applyDelay
	f.mu.Unlock()
	if delay > 0 {
		time.Sleep(delay)
	}
	defer func() { f.mu.Lock(); f.activeApply--; f.mu.Unlock() }()
	if f.reloadErr != nil {
		return caddycore.ApplyResult{}, f.reloadErr
	}
	if !f.running && f.diagnoseErr != nil {
		return caddycore.ApplyResult{Outcome: caddycore.OutcomePortConflict, Detail: f.diagnoseErr.Error()}, nil
	}
	if f.running && string(f.lastConfig) == string(config) {
		return caddycore.ApplyResult{Outcome: caddycore.OutcomeUnchanged}, nil
	}
	f.reloads = append(f.reloads, config)
	f.lastConfig = append([]byte(nil), config...)
	f.running = true
	return caddycore.ApplyResult{Outcome: caddycore.OutcomeApplied}, nil
}
func (f *fakeCaddyAdapter) Stop(context.Context) error { f.stopCalls++; f.running = false; return nil }
func (f *fakeCaddyAdapter) RootCACert(time.Duration) ([]byte, error) {
	return f.rootCA, f.rootCAErr
}

type fakeCATrust struct {
	state       helper.CATrustState
	identity    helper.CAIdentity
	ensureCalls int
	removeCalls int
	ensureErr   error
}

func newFakeCATrust() *fakeCATrust {
	return &fakeCATrust{state: helper.CAConfirmationRequired, identity: helper.CAIdentity{SHA256: strings.Repeat("a", 64)}}
}

func (f *fakeCATrust) EnsureCurrentCaddyCATrusted(context.Context) (helper.CAIdentity, error) {
	f.ensureCalls++
	if f.ensureErr != nil {
		return helper.CAIdentity{}, f.ensureErr
	}
	f.state = helper.CATrusted
	return f.identity, nil
}
func (f *fakeCATrust) RemoveCurrentCaddyCA(context.Context) error {
	f.removeCalls++
	f.state = helper.CAConfirmationRequired
	return nil
}
func (f *fakeCATrust) Status(context.Context) (helper.CATrustStatus, error) {
	return helper.CATrustStatus{State: f.state, Identity: f.identity}, nil
}

// routerFixture 组装共享同一 fakeStore 的 catalog 与 router。
type routerFixture struct {
	store     *fakeStore
	catalog   *biz.CatalogBiz
	router    *biz.RouterBiz
	helper    *fakeHelperClient
	caddy     *fakeCaddyAdapter
	caTrust   *fakeCATrust
	hosts     string
	caddyJSON string
}

func newRouterFixture(t *testing.T) *routerFixture {
	t.Helper()
	fs := &fakeStore{data: model.VaultData{Version: 1}}
	catalog := biz.NewCatalogBiz(fs)
	fh := &fakeHelperClient{}
	fc := &fakeCaddyAdapter{}
	caTrust := newFakeCATrust()
	dir := t.TempDir()
	hostsPath := filepath.Join(dir, "hosts")
	caddyConfigPath := filepath.Join(dir, "caddy.json")
	r := biz.NewRouterBiz(fs, catalog, fh, caTrust, fc, hostsPath, caddyConfigPath)
	return &routerFixture{store: fs, catalog: catalog, router: r, helper: fh, caddy: fc, caTrust: caTrust, hosts: hostsPath, caddyJSON: caddyConfigPath}
}

func (f *routerFixture) seedForward(t *testing.T, mode string, port int) model.Forward {
	t.Helper()
	folder, err := f.catalog.CreateFolder("工作", 0)
	if err != nil {
		t.Fatal(err)
	}
	host, err := f.catalog.SaveSSHHost(model.SSHHost{Name: "h", Host: "10.0.0.1", AuthType: "password", Password: "x"})
	if err != nil {
		t.Fatal(err)
	}
	fw, err := f.catalog.SaveForward(model.Forward{FolderID: folder.ID, Name: "fw", Mode: mode, ChainHostIDs: []int{host.ID},
		LocalHost: "127.0.0.1", LocalPort: port, RemoteHost: "x", RemotePort: 80})
	if err != nil {
		t.Fatal(err)
	}
	return fw
}

// hosts-only Route 应用后 hosts 区块经 helper 写入，Caddy 不被触碰。
func TestApplyRouteHostsOnly(t *testing.T) {
	fx := newRouterFixture(t)
	fw := fx.seedForward(t, "local", 8080)
	rt, err := fx.catalog.SaveWebRoute(model.WebRoute{ForwardID: fw.ID, Domain: "db.test", HostsEnabled: true})
	if err != nil {
		t.Fatal(err)
	}

	result, err := fx.router.ApplyRoute(rt.ID, nil)
	if err != nil {
		t.Fatalf("ApplyRoute: %v", err)
	}
	if !result.HostsApplied || result.CaddyApplied || result.PortConflict != "" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if len(fx.helper.calls) != 1 || fx.helper.calls[0].Op != helper.OpApplyManagedHosts {
		t.Fatalf("helper calls = %+v", fx.helper.calls)
	}
	if len(fx.helper.calls[0].Hosts) != 1 || fx.helper.calls[0].Hosts[0].Domain != "db.test" {
		t.Fatalf("hosts entries = %+v", fx.helper.calls[0].Hosts)
	}
	if len(fx.caddy.reloads) != 0 {
		t.Fatalf("caddy must not be touched, reloads = %d", len(fx.caddy.reloads))
	}
}

// 非 .test/.localhost 域名必须确认：未确认拒绝且零副作用，确认后放行。
func TestApplyRouteDomainConfirmation(t *testing.T) {
	fx := newRouterFixture(t)
	fw := fx.seedForward(t, "local", 8080)
	rt, err := fx.catalog.SaveWebRoute(model.WebRoute{ForwardID: fw.ID, Domain: "grafana.example.com", HostsEnabled: true})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := fx.router.ApplyRoute(rt.ID, nil); !errors.Is(err, biz.ErrDomainConfirmationRequired) {
		t.Fatalf("err = %v, want ErrDomainConfirmationRequired", err)
	}
	if len(fx.helper.calls) != 0 {
		t.Fatalf("rejected apply must have zero side effects, calls = %+v", fx.helper.calls)
	}

	if _, err := fx.router.ApplyRoute(rt.ID, []string{"grafana.example.com"}); err != nil {
		t.Fatalf("confirmed apply: %v", err)
	}
	if len(fx.helper.calls) != 1 {
		t.Fatalf("confirmed apply should write hosts once, calls = %+v", fx.helper.calls)
	}
}

// 首个 Caddy Route 启用：配置重载后取 CA 并信任，指纹记入 Vault 偏好。
func TestApplyRouteCaddyTrustFlow(t *testing.T) {
	fx := newRouterFixture(t)
	fw := fx.seedForward(t, "local", 8080)
	fx.caddy.rootCA = []byte("fake-der")
	rt, err := fx.catalog.SaveWebRoute(model.WebRoute{ForwardID: fw.ID, Domain: "db.test", HostsEnabled: true, CaddyEnabled: true})
	if err != nil {
		t.Fatal(err)
	}

	result, err := fx.router.ApplyRoute(rt.ID, nil)
	if err != nil {
		t.Fatalf("ApplyRoute: %v", err)
	}
	if !result.CaddyApplied {
		t.Fatalf("caddy should be applied: %+v", result)
	}
	if len(fx.caddy.reloads) != 1 {
		t.Fatalf("reloads = %d, want 1", len(fx.caddy.reloads))
	}
	if fx.caTrust.ensureCalls != 1 {
		t.Fatalf("CurrentUser CA ensure calls = %d, want 1", fx.caTrust.ensureCalls)
	}
	data, _ := fx.store.Load()
	if data.Prefs.CATrustedSHA256 != "" {
		t.Fatalf("machine-local CA fact must not enter Vault: %+v", data.Prefs)
	}
}

// 443 冲突：Caddy 不启动但 hosts 照常生效，结果携带冲突说明且无错误。
func TestApplyRoutePortConflictKeepsHosts(t *testing.T) {
	fx := newRouterFixture(t)
	fw := fx.seedForward(t, "local", 8080)
	fx.caddy.diagnoseErr = errors.New("caddy: 127.0.0.1:443 unavailable: bind: address in use")
	rt, err := fx.catalog.SaveWebRoute(model.WebRoute{ForwardID: fw.ID, Domain: "db.test", HostsEnabled: true, CaddyEnabled: true})
	if err != nil {
		t.Fatal(err)
	}

	result, err := fx.router.ApplyRoute(rt.ID, nil)
	if err != nil {
		t.Fatalf("port conflict must not fail apply: %v", err)
	}
	if result.PortConflict == "" || !result.HostsApplied || result.CaddyApplied {
		t.Fatalf("unexpected result: %+v", result)
	}
	if len(fx.caddy.reloads) != 0 {
		t.Fatalf("conflicted caddy must not reload, reloads = %d", len(fx.caddy.reloads))
	}
}

// Caddy 重载失败时 hosts 回滚到应用前的区块内容。
func TestApplyRouteRollbackOnCaddyFailure(t *testing.T) {
	fx := newRouterFixture(t)
	fw := fx.seedForward(t, "local", 8080)
	fx.caddy.reloadErr = errors.New("bad config")
	prev := helper.BlockBegin + "\r\n127.0.0.1 old.test\r\n" + helper.BlockEnd + "\r\n"
	if err := os.WriteFile(fx.hosts, []byte(prev), 0o644); err != nil {
		t.Fatal(err)
	}
	rt, err := fx.catalog.SaveWebRoute(model.WebRoute{ForwardID: fw.ID, Domain: "db.test", HostsEnabled: true, CaddyEnabled: true})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := fx.router.ApplyRoute(rt.ID, nil); err == nil {
		t.Fatal("reload failure must fail apply")
	}
	if len(fx.helper.calls) != 2 {
		t.Fatalf("expected apply + rollback calls, got %+v", fx.helper.calls)
	}
	if fx.helper.calls[0].Hosts[0].Domain != "db.test" {
		t.Fatalf("first call should apply new entries: %+v", fx.helper.calls[0].Hosts)
	}
	rollback := fx.helper.calls[1].Hosts
	if len(rollback) != 1 || rollback[0].Domain != "old.test" {
		t.Fatalf("rollback should restore previous entries, got %+v", rollback)
	}
}

// 最后一个 Caddy Route 移除：停止 Caddy 并撤销 CA 信任，偏好清空。
func TestRemoveRouteStopsCaddyAndUntrusts(t *testing.T) {
	fx := newRouterFixture(t)
	fw := fx.seedForward(t, "local", 8080)
	fx.caddy.running = true
	fx.caTrust.state = helper.CATrusted
	rt, err := fx.catalog.SaveWebRoute(model.WebRoute{ForwardID: fw.ID, Domain: "db.test", HostsEnabled: true, CaddyEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fx.store.Update(func(d *model.VaultData) error {
		d.Prefs.CATrustedSHA256 = "deadbeef"
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := fx.router.RemoveRoute(rt.ID); err != nil {
		t.Fatalf("RemoveRoute: %v", err)
	}
	if fx.caddy.stopCalls != 1 {
		t.Fatalf("caddy stop calls = %d, want 1", fx.caddy.stopCalls)
	}
	if fx.caTrust.removeCalls != 1 {
		t.Fatalf("CurrentUser CA remove calls = %d, want 1", fx.caTrust.removeCalls)
	}
	data, _ := fx.store.Load()
	if data.Prefs.CATrustedSHA256 != "" {
		t.Fatalf("pref should be cleared: %+v", data.Prefs)
	}
	if len(data.WebRoutes) != 0 {
		t.Fatalf("route should be deleted: %+v", data.WebRoutes)
	}
}

// RouteStatus 反映区块内容、Caddy 运行态与 CA 信任状态。
func TestRouteStatusReflectsSystem(t *testing.T) {
	fx := newRouterFixture(t)
	fw := fx.seedForward(t, "local", 8080)
	if _, err := fx.catalog.SaveWebRoute(model.WebRoute{ForwardID: fw.ID, Domain: "db.test", HostsEnabled: true, CaddyEnabled: true}); err != nil {
		t.Fatal(err)
	}
	block := helper.BlockBegin + "\r\n127.0.0.1 db.test\r\n" + helper.BlockEnd + "\r\n"
	if err := os.WriteFile(fx.hosts, []byte(block), 0o644); err != nil {
		t.Fatal(err)
	}
	fx.caddy.running = true
	fx.caTrust.state = helper.CATrusted
	if _, err := fx.store.Update(func(d *model.VaultData) error {
		d.Prefs.CATrustedSHA256 = "deadbeef"
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	items, err := fx.router.RouteStatus()
	if err != nil {
		t.Fatalf("RouteStatus: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %+v", items)
	}
	got := items[0]
	if !got.HostsApplied || !got.CaddyRunning || !got.CATrusted || got.PortConflict {
		t.Fatalf("unexpected status: %+v", got)
	}
}

// PreviewRoute 保持纯读，只暴露确认项与 CA 信任需求；端口冲突由真实 Apply 分类。
func TestPreviewRoute(t *testing.T) {
	fx := newRouterFixture(t)
	fw := fx.seedForward(t, "local", 8080)
	fx.caddy.diagnoseErr = errors.New("conflict")
	rt, err := fx.catalog.SaveWebRoute(model.WebRoute{ForwardID: fw.ID, Domain: "grafana.example.com", HostsEnabled: true, CaddyEnabled: true})
	if err != nil {
		t.Fatal(err)
	}

	preview, err := fx.router.PreviewRoute(rt.ID)
	if err != nil {
		t.Fatalf("PreviewRoute: %v", err)
	}
	if len(preview.RequiresConfirmation) != 1 || preview.RequiresConfirmation[0] != "grafana.example.com" {
		t.Fatalf("RequiresConfirmation = %+v", preview.RequiresConfirmation)
	}
	if preview.PortConflict {
		t.Fatal("Preview must not bind-probe port 443")
	}
	if !preview.CATrustNeeded {
		t.Fatal("CATrustNeeded should be true")
	}
	if len(preview.HostsRecords) != 1 || preview.HostsRecords[0] != (route.HostEntry{Domain: "grafana.example.com", IP: "127.0.0.1"}) {
		t.Fatalf("HostsRecords = %+v", preview.HostsRecords)
	}
}

// Caddy 已运行时再应用一条 Caddy 路由：跳过 443 端口预检，直接走 admin API 热重载，
// 不报端口冲突、不重启 Caddy、不重复信任 CA。回归 issue：多路由只能有一条经 Caddy 接管。
func TestApplyRouteCaddyAlreadyRunningReloads(t *testing.T) {
	fx := newRouterFixture(t)
	fw := fx.seedForward(t, "local", 8080)
	// 模拟"第一条路由已让 Caddy 跑起来、CA 已信任"的稳态。
	fx.caddy.running = true
	fx.caddy.rootCA = []byte("fake-der")
	if _, err := fx.store.Update(func(d *model.VaultData) error {
		d.Prefs.CATrustedSHA256 = "deadbeef"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	rt, err := fx.catalog.SaveWebRoute(model.WebRoute{ForwardID: fw.ID, Domain: "db.test", HostsEnabled: true, CaddyEnabled: true})
	if err != nil {
		t.Fatal(err)
	}

	result, err := fx.router.ApplyRoute(rt.ID, nil)
	if err != nil {
		t.Fatalf("ApplyRoute: %v", err)
	}
	if result.PortConflict != "" {
		t.Fatalf("should not report port conflict when caddy already running: %+v", result)
	}
	if !result.CaddyApplied {
		t.Fatalf("caddy should be reloaded: %+v", result)
	}
	if len(fx.caddy.reloads) != 1 {
		t.Fatalf("reloads = %d, want 1", len(fx.caddy.reloads))
	}
	if fx.caddy.stopCalls != 0 {
		t.Fatalf("must not stop caddy, stopCalls = %d", fx.caddy.stopCalls)
	}
	for _, c := range fx.helper.calls {
		if c.Op == helper.OpTrustLocalCA {
			t.Fatalf("must not re-trust CA when already trusted: %+v", fx.helper.calls)
		}
	}
}

// 配置未变时短路：Caddy 已运行、磁盘 caddy.json 与新编译配置字节相同，
// 应跳过 admin API 热重载（fakeCaddyAdapter.reloads 不增加），但 CaddyApplied 仍为 true。
// 回归重复应用同一条路由、用户反复点"应用"等无效 reload 场景。
func TestApplyRouteSkipsReloadWhenConfigUnchanged(t *testing.T) {
	fx := newRouterFixture(t)
	fw := fx.seedForward(t, "local", 8080)
	rt, err := fx.catalog.SaveWebRoute(model.WebRoute{ForwardID: fw.ID, Domain: "db.test", HostsEnabled: true, CaddyEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	// 模拟"上一次应用已让 Caddy 跑起来、CA 已信任、caddy.json 已落盘"的稳态。
	data, _ := fx.store.Load()
	prevCfg, err := route.CompileCaddy(data)
	if err != nil {
		t.Fatalf("compile prev cfg: %v", err)
	}
	if err := os.WriteFile(fx.caddyJSON, prevCfg, 0o600); err != nil {
		t.Fatal(err)
	}
	fx.caddy.running = true
	fx.caddy.lastConfig = append([]byte(nil), prevCfg...)
	if _, err := fx.store.Update(func(d *model.VaultData) error {
		d.Prefs.CATrustedSHA256 = "deadbeef"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// 让 hosts 也已经是目标内容，避免 hosts 写入干扰判定。
	block := helper.BlockBegin + "\r\n127.0.0.1 db.test\r\n" + helper.BlockEnd + "\r\n"
	if err := os.WriteFile(fx.hosts, []byte(block), 0o644); err != nil {
		t.Fatal(err)
	}

	before := len(fx.caddy.reloads)
	result, err := fx.router.ApplyRoute(rt.ID, nil)
	if err != nil {
		t.Fatalf("ApplyRoute: %v", err)
	}
	if !result.CaddyApplied || result.PortConflict != "" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if len(fx.caddy.reloads) != before {
		t.Fatalf("reload must be skipped, reloads = %d (before %d)", len(fx.caddy.reloads), before)
	}
}

// 启动时恢复 Caddy：启用 Caddy 的 Route 存在时按 Vault 状态重启进程，不碰 hosts、不触发特权安装。
func TestResumeCaddyStartsWhenEnabled(t *testing.T) {
	fx := newRouterFixture(t)
	fw := fx.seedForward(t, "local", 8080)
	if _, err := fx.catalog.SaveWebRoute(model.WebRoute{ForwardID: fw.ID, Domain: "db.test", HostsEnabled: true, CaddyEnabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.store.Update(func(d *model.VaultData) error {
		d.Prefs.CATrustedSHA256 = "deadbeef"
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if err := fx.router.ResumeCaddy(); err != nil {
		t.Fatalf("ResumeCaddy: %v", err)
	}
	if len(fx.caddy.reloads) != 1 {
		t.Fatalf("reloads = %d, want 1", len(fx.caddy.reloads))
	}
	if fx.helper.ensureCalls != 0 {
		t.Fatalf("resume must never trigger privilege install, ensureCalls = %d", fx.helper.ensureCalls)
	}
	for _, c := range fx.helper.calls {
		if c.Op == helper.OpApplyManagedHosts {
			t.Fatal("resume must not rewrite hosts")
		}
	}
}

// 无启用 Caddy 的 Route：完全不动。
func TestResumeCaddyNoopWithoutEnabledRoutes(t *testing.T) {
	fx := newRouterFixture(t)
	fw := fx.seedForward(t, "local", 8080)
	if _, err := fx.catalog.SaveWebRoute(model.WebRoute{ForwardID: fw.ID, Domain: "db.test", HostsEnabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := fx.router.ResumeCaddy(); err != nil {
		t.Fatalf("ResumeCaddy: %v", err)
	}
	if len(fx.caddy.reloads) != 0 || len(fx.helper.calls) != 0 {
		t.Fatalf("must be noop, reloads = %d, helper calls = %+v", len(fx.caddy.reloads), fx.helper.calls)
	}
}

// 443 冲突时按 hosts-only 处理，不报错、不重载。
func TestResumeCaddyPortConflict(t *testing.T) {
	fx := newRouterFixture(t)
	fw := fx.seedForward(t, "local", 8080)
	fx.caddy.diagnoseErr = errors.New("caddy: 127.0.0.1:443 unavailable: bind: address in use")
	if _, err := fx.catalog.SaveWebRoute(model.WebRoute{ForwardID: fw.ID, Domain: "db.test", HostsEnabled: true, CaddyEnabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := fx.router.ResumeCaddy(); err != nil {
		t.Fatalf("port conflict must not fail resume: %v", err)
	}
	if len(fx.caddy.reloads) != 0 {
		t.Fatalf("conflicted caddy must not reload, reloads = %d", len(fx.caddy.reloads))
	}
}

// 启动恢复绝不替用户作出新的根 CA 信任决定。
func TestResumeCaddyDefersCurrentUserCAConfirmation(t *testing.T) {
	fx := newRouterFixture(t)
	fw := fx.seedForward(t, "local", 8080)
	if _, err := fx.catalog.SaveWebRoute(model.WebRoute{ForwardID: fw.ID, Domain: "db.test", HostsEnabled: true, CaddyEnabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := fx.router.ResumeCaddy(); err != nil {
		t.Fatalf("ResumeCaddy: %v", err)
	}
	if fx.caTrust.ensureCalls != 0 {
		t.Fatalf("startup must not trust a new CA, calls=%d", fx.caTrust.ensureCalls)
	}
}

func TestRouteCoordinatorSerializesAndPersistsAppliedState(t *testing.T) {
	fx := newRouterFixture(t)
	fw := fx.seedForward(t, "local", 8080)
	rt, err := fx.catalog.SaveWebRoute(model.WebRoute{ForwardID: fw.ID, Domain: "db.test", HostsEnabled: true, CaddyEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	fx.caddy.rootCA = []byte("fake-der")
	fx.caddy.applyDelay = 30 * time.Millisecond

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := fx.router.ApplyRoute(rt.ID, nil)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	fx.caddy.mu.Lock()
	maxApply := fx.caddy.maxApply
	fx.caddy.mu.Unlock()
	if maxApply != 1 {
		t.Fatalf("Caddy Apply concurrency = %d, want 1", maxApply)
	}

	raw, err := os.ReadFile(filepath.Join(filepath.Dir(fx.caddyJSON), "state", "route-applied.json"))
	if err != nil {
		t.Fatalf("read applied state: %v", err)
	}
	var state biz.RouteAppliedState
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatal(err)
	}
	if state.Status != biz.RouteStatusApplied || state.AppliedDesiredRevision == "" || len(state.AppliedHosts) != 1 {
		t.Fatalf("applied state = %+v", state)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(fx.caddyJSON), "state", "route-journal.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("completed transaction must clear journal, err=%v", err)
	}
}
