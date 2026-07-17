package biz

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/HanZephyr/TunnelBoard/internal/helper"
	"github.com/HanZephyr/TunnelBoard/internal/model"
	"github.com/HanZephyr/TunnelBoard/internal/route"
)

// ErrDomainConfirmationRequired 表示将覆盖非 .test/.localhost 域名的 DNS 解析，
// 调用方必须让用户明确确认后把该域名放入 confirmedDomains 重试（CONTEXT.md:63）。
var ErrDomainConfirmationRequired = errors.New("biz: domain override requires explicit confirmation")

// HelperClient 是特权辅助服务的调用接缝（生产 = *helper.Client，测试 = fake）。
type HelperClient interface {
	Call(req helper.Request) (helper.Response, error)
	Ping() (string, error)
	EnsureInstalled() error
}

// CaddyAdapter 是 Caddy 进程管理接缝（生产 = *caddy.Adapter，测试 = fake）。
type CaddyAdapter interface {
	DiagnosePort() error
	Running() bool
	Reload(config []byte) error
	Stop() error
	RootCACert(timeout time.Duration) ([]byte, error)
}

// RouteApplyResult 报告一次系统应用的副作用面；443 冲突不算错误，
// 按 CONTEXT.md 保留“域名 + Forward 端口”访问方式（hosts 照常生效）。
type RouteApplyResult struct {
	HostsApplied bool   `json:"hostsApplied"`
	CaddyApplied bool   `json:"caddyApplied"`
	PortConflict string `json:"portConflict,omitempty"`
}

// RouteStatusItem 是单条 Route 的系统生效状态。
type RouteStatusItem struct {
	RouteID      int    `json:"routeId"`
	Domain       string `json:"domain"`
	HostsEnabled bool   `json:"hostsEnabled"`
	HostsApplied bool   `json:"hostsApplied"`
	CaddyEnabled bool   `json:"caddyEnabled"`
	CaddyRunning bool   `json:"caddyRunning"`
	PortConflict bool   `json:"portConflict"`
	CATrusted    bool   `json:"caTrusted"`
}

// RoutePreview 是 ApplyRoute 前的预览：将写入的 hosts 记录、需要的确认项与风险点。
type RoutePreview struct {
	HostsRecords         []route.HostEntry `json:"hostsRecords"`
	RequiresConfirmation []string          `json:"requiresConfirmation"`
	PortConflict         bool              `json:"portConflict"`
	CATrustNeeded        bool              `json:"caTrustNeeded"`
}

// RouterBiz 是本地路由 Module：把 Vault 中的 Web Route 状态推到系统
// （受托管 hosts 区块、Caddy、本地 CA 信任），失败按逆序回滚并报告失败点。
type RouterBiz struct {
	store          VaultStore
	catalog        *CatalogBiz
	helper         HelperClient
	caddy          CaddyAdapter
	hostsPath      string // 只读快照用；写入只经 helper
	caddyConfigPth string // 回滚时恢复上一份配置
	caWaitTimeout  time.Duration
}

// NewRouterBiz 组装本地路由 Module；所有系统副作用经注入接缝完成，可在测试中全内存验证。
func NewRouterBiz(store VaultStore, catalog *CatalogBiz, helperClient HelperClient, caddyAdapter CaddyAdapter, hostsPath, caddyConfigPath string) *RouterBiz {
	return &RouterBiz{
		store:          store,
		catalog:        catalog,
		helper:         helperClient,
		caddy:          caddyAdapter,
		hostsPath:      hostsPath,
		caddyConfigPth: caddyConfigPath,
		caWaitTimeout:  10 * time.Second,
	}
}

// ApplyRoute 把单条 Route（及其所在的全局 Route 状态）应用到系统；
// 该 Route 的域名需要确认而调用方未确认时返回 ErrDomainConfirmationRequired。
func (b *RouterBiz) ApplyRoute(routeID int, confirmedDomains []string) (RouteApplyResult, error) {
	data, err := b.store.Load()
	if err != nil {
		return RouteApplyResult{}, err
	}
	var target *model.WebRoute
	for i := range data.WebRoutes {
		if data.WebRoutes[i].ID == routeID {
			target = &data.WebRoutes[i]
			break
		}
	}
	if target == nil {
		return RouteApplyResult{}, fmt.Errorf("web route %d not found", routeID)
	}
	if target.HostsEnabled && route.NeedsConfirmation(target.Domain) && !containsFold(confirmedDomains, target.Domain) {
		return RouteApplyResult{}, fmt.Errorf("%w: %s", ErrDomainConfirmationRequired, target.Domain)
	}
	return b.applySystem()
}

// ReconcileRoutes 按 Vault 当前状态全量重推系统（删除 Route/Forward 后的清理不需要域名确认）。
func (b *RouterBiz) ReconcileRoutes() (RouteApplyResult, error) {
	return b.applySystem()
}

// RemoveRoute 删除 Route 并重推系统（hosts 记录随之撤销；最后一个 Caddy Route 移除后停止 Caddy 并撤 CA）。
func (b *RouterBiz) RemoveRoute(routeID int) (RouteApplyResult, error) {
	if err := b.catalog.DeleteWebRoute(routeID); err != nil {
		return RouteApplyResult{}, err
	}
	return b.applySystem()
}

// applySystem 是统一的系统重推流程：快照 → hosts → Caddy → CA；失败逆序回滚。
func (b *RouterBiz) applySystem() (RouteApplyResult, error) {
	data, err := b.store.Load()
	if err != nil {
		return RouteApplyResult{}, err
	}
	entries, _ := route.PlanHosts(data)
	caddyConfig, err := route.CompileCaddy(data)
	if err != nil {
		return RouteApplyResult{}, err
	}

	prevEntries := b.currentManagedEntries()
	prevRunning := b.caddy.Running()
	result := RouteApplyResult{}

	// 需要特权操作的场景：写 hosts、信任/撤销 CA；否则完全无需 helper。
	needHelper := len(entries) > 0 || len(prevEntries) > 0 || len(caddyConfig) > 0 || data.Prefs.CATrustedSHA256 != ""
	if needHelper {
		if err := b.helper.EnsureInstalled(); err != nil {
			return result, fmt.Errorf("privileged helper unavailable: %w", err)
		}
	}

	hostsChanged := !entriesEqual(entries, prevEntries)
	if hostsChanged {
		if err := b.callHelper(helper.Request{Op: helper.OpApplyManagedHosts, Hosts: entries}); err != nil {
			return result, fmt.Errorf("apply managed hosts: %w", err)
		}
		result.HostsApplied = true
	} else {
		result.HostsApplied = true
	}

	// Caddy：无启用 Route 时确保停止并撤销 CA；有则诊断端口→重载→信任 CA。
	if len(caddyConfig) == 0 {
		if prevRunning {
			if err := b.caddy.Stop(); err != nil {
				return result, fmt.Errorf("stop caddy: %w", err)
			}
		}
		if data.Prefs.CATrustedSHA256 != "" {
			if err := b.callHelper(helper.Request{Op: helper.OpUntrustLocalCA, CertSHA256: data.Prefs.CATrustedSHA256}); err != nil {
				return result, fmt.Errorf("untrust local ca: %w", err)
			}
			if err := b.setCATrusted(""); err != nil {
				return result, err
			}
		}
		return result, nil
	}

	if err := b.caddy.DiagnosePort(); err != nil {
		// 443 冲突：不启动 Caddy，保留 hosts-only 访问；非致命。
		result.PortConflict = err.Error()
		return result, nil
	}
	prevConfig, _ := os.ReadFile(b.caddyConfigPth)
	if err := b.caddy.Reload(caddyConfig); err != nil {
		b.rollbackHosts(prevEntries, hostsChanged)
		return result, fmt.Errorf("reload caddy: %w", err)
	}
	result.CaddyApplied = true

	if data.Prefs.CATrustedSHA256 == "" {
		der, err := b.caddy.RootCACert(b.caWaitTimeout)
		if err != nil {
			b.rollbackCaddy(prevRunning, prevConfig)
			b.rollbackHosts(prevEntries, hostsChanged)
			return result, fmt.Errorf("read local ca: %w", err)
		}
		sum := sha256.Sum256(der)
		fp := hex.EncodeToString(sum[:])
		if err := b.callHelper(helper.Request{Op: helper.OpTrustLocalCA, CertDER: der, CertSHA256: fp}); err != nil {
			b.rollbackCaddy(prevRunning, prevConfig)
			b.rollbackHosts(prevEntries, hostsChanged)
			return result, fmt.Errorf("trust local ca: %w", err)
		}
		if err := b.setCATrusted(fp); err != nil {
			return result, err
		}
	}
	return result, nil
}

// RouteStatus 汇总每条 Route 的系统生效状态。
func (b *RouterBiz) RouteStatus() ([]RouteStatusItem, error) {
	data, err := b.store.Load()
	if err != nil {
		return nil, err
	}
	applied := map[string]bool{}
	for _, e := range b.currentManagedEntries() {
		applied[e.Domain] = true
	}
	running := b.caddy.Running()
	portConflict := b.caddy.DiagnosePort() != nil && !running
	caTrusted := data.Prefs.CATrustedSHA256 != ""

	items := make([]RouteStatusItem, 0, len(data.WebRoutes))
	for _, r := range data.WebRoutes {
		items = append(items, RouteStatusItem{
			RouteID:      r.ID,
			Domain:       r.Domain,
			HostsEnabled: r.HostsEnabled,
			HostsApplied: r.HostsEnabled && applied[r.Domain],
			CaddyEnabled: r.CaddyEnabled,
			CaddyRunning: r.CaddyEnabled && running,
			PortConflict: r.CaddyEnabled && portConflict,
			CATrusted:    caTrusted,
		})
	}
	return items, nil
}

// PreviewRoute 返回 ApplyRoute 前需要用户知悉的记录与确认项。
func (b *RouterBiz) PreviewRoute(routeID int) (RoutePreview, error) {
	data, err := b.store.Load()
	if err != nil {
		return RoutePreview{}, err
	}
	entries, _ := route.PlanHosts(data)
	preview := RoutePreview{HostsRecords: entries}
	for _, r := range data.WebRoutes {
		if r.ID == routeID && r.HostsEnabled && route.NeedsConfirmation(r.Domain) {
			preview.RequiresConfirmation = []string{r.Domain}
		}
	}
	if !b.caddy.Running() && b.caddy.DiagnosePort() != nil {
		preview.PortConflict = true
	}
	preview.CATrustNeeded = data.Prefs.CATrustedSHA256 == "" && hasCaddyEnabledRoute(data)
	return preview, nil
}

// currentManagedEntries 只读快照当前受托管区块（hosts 文件本身可读，不需要特权）。
func (b *RouterBiz) currentManagedEntries() []route.HostEntry {
	content, err := os.ReadFile(b.hostsPath)
	if err != nil {
		return nil
	}
	return helper.ParseManagedHosts(string(content))
}

func (b *RouterBiz) callHelper(req helper.Request) error {
	resp, err := b.helper.Call(req)
	if err != nil {
		return err
	}
	if !resp.OK {
		return errors.New(resp.Error)
	}
	return nil
}

func (b *RouterBiz) setCATrusted(fp string) error {
	_, err := b.store.Update(func(d *model.VaultData) error {
		d.Prefs.CATrustedSHA256 = fp
		return nil
	})
	return err
}

func (b *RouterBiz) rollbackHosts(prevEntries []route.HostEntry, hostsChanged bool) {
	if hostsChanged {
		_ = b.callHelper(helper.Request{Op: helper.OpApplyManagedHosts, Hosts: prevEntries})
	}
}

func (b *RouterBiz) rollbackCaddy(prevRunning bool, prevConfig []byte) {
	if prevRunning && len(prevConfig) > 0 {
		_ = b.caddy.Reload(prevConfig)
	} else if !prevRunning {
		_ = b.caddy.Stop()
	}
}

func hasCaddyEnabledRoute(data model.VaultData) bool {
	for _, r := range data.WebRoutes {
		if r.CaddyEnabled {
			return true
		}
	}
	return false
}

func entriesEqual(a, b []route.HostEntry) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func containsFold(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
