package biz

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	caddycore "github.com/HanZephyr/TunnelBoard/internal/caddy"
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
	Apply(ctx context.Context, revision string, config []byte) (caddycore.ApplyResult, error)
	Stop(ctx context.Context) error
	Status(ctx context.Context) caddycore.Status
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
	mu             sync.Mutex
	store          VaultStore
	catalog        *CatalogBiz
	helper         HelperClient
	caTrust        helper.LocalCATrust
	caddy          CaddyAdapter
	hostsPath      string // 只读快照用；写入只经 helper
	caddyConfigPth string // 回滚时恢复上一份配置
	caWaitTimeout  time.Duration
	state          routeStateStore
}

// NewRouterBiz 组装本地路由 Module；所有系统副作用经注入接缝完成，可在测试中全内存验证。
func NewRouterBiz(store VaultStore, catalog *CatalogBiz, helperClient HelperClient, caTrust helper.LocalCATrust, caddyAdapter CaddyAdapter, hostsPath, caddyConfigPath string) *RouterBiz {
	return &RouterBiz{
		store:          store,
		catalog:        catalog,
		helper:         helperClient,
		caTrust:        caTrust,
		caddy:          caddyAdapter,
		hostsPath:      hostsPath,
		caddyConfigPth: caddyConfigPath,
		caWaitTimeout:  10 * time.Second,
		state:          newRouteStateStore(filepath.Dir(caddyConfigPath)),
	}
}

// ApplyRoute 把单条 Route（及其所在的全局 Route 状态）应用到系统；
// 该 Route 的域名需要确认而调用方未确认时返回 ErrDomainConfirmationRequired。
func (b *RouterBiz) ApplyRoute(routeID int, confirmedDomains []string) (RouteApplyResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
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
	return b.applySystemLocked()
}

// ReconcileRoutes 按 Vault 当前状态全量重推系统（删除 Route/Forward 后的清理不需要域名确认）。
func (b *RouterBiz) ReconcileRoutes() (RouteApplyResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.applySystemLocked()
}

// RemoveRoute 删除 Route 并重推系统（hosts 记录随之撤销；最后一个 Caddy Route 移除后停止 Caddy 并撤 CA）。
func (b *RouterBiz) RemoveRoute(routeID int) (RouteApplyResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.catalog.DeleteWebRoute(routeID); err != nil {
		return RouteApplyResult{}, err
	}
	return b.applySystemLocked()
}

// applySystem 是统一的系统重推流程：快照 → hosts → Caddy → CA；失败逆序回滚。
func (b *RouterBiz) applySystemLocked() (result RouteApplyResult, retErr error) {
	data, err := b.store.Load()
	if err != nil {
		return RouteApplyResult{}, err
	}
	if data.Prefs.CATrustedSHA256 != "" {
		if _, err := b.store.Update(func(current *model.VaultData) error {
			current.Prefs.CATrustedSHA256 = ""
			return nil
		}); err != nil {
			return RouteApplyResult{}, fmt.Errorf("migrate legacy CA trust state: %w", err)
		}
		data.Prefs.CATrustedSHA256 = ""
	}
	entries, _ := route.PlanHosts(data)
	caddyConfig, err := route.CompileCaddy(data)
	if err != nil {
		return RouteApplyResult{}, err
	}
	desiredRevision := desiredRouteRevision(data)
	beforeApplied, err := b.state.loadState()
	if err != nil {
		return RouteApplyResult{}, err
	}
	journal := routeJournal{
		TxID:            newRouteTxID(),
		DesiredRevision: desiredRevision,
		BeforeApplied:   beforeApplied,
		TargetHosts:     append([]route.HostEntry(nil), entries...),
		TargetCaddyHash: digestBytes(caddyConfig),
		Phase:           "planned",
		CreatedAt:       time.Now().UTC(),
	}
	if err := b.state.saveJournal(journal); err != nil {
		return RouteApplyResult{}, err
	}
	caFingerprint := ""
	if caStatus, statusErr := b.caTrust.Status(context.Background()); statusErr == nil && caStatus.State == helper.CATrusted {
		caFingerprint = caStatus.Identity.SHA256
	}
	defer func() {
		status := RouteStatusApplied
		if result.PortConflict != "" {
			status = RouteStatusHostsOnly
		}
		state := RouteAppliedState{
			AppliedDesiredRevision: desiredRevision,
			HostsDigest:            digestHosts(entries),
			AppliedHosts:           append([]route.HostEntry(nil), entries...),
			CaddyConfigDigest:      digestBytes(caddyConfig),
			CATrustedSHA256:        caFingerprint,
			Status:                 status,
			PortConflict:           result.PortConflict,
		}
		caddyStatus := b.caddy.Status(context.Background())
		state.CaddyGeneration = caddyStatus.Generation
		if retErr != nil {
			state = beforeApplied
			state.Status = RouteStatusError
			state.LastError = sanitizeRouteError(retErr)
			state.PendingTxID = journal.TxID
			_ = b.state.saveState(state)
			return
		}
		if err := b.state.saveState(state); err != nil {
			retErr = err
			return
		}
		if err := b.state.clearJournal(); err != nil {
			retErr = err
		}
	}()
	advanceJournal := func(phase string) error {
		journal.Phase = phase
		return b.state.saveJournal(journal)
	}

	prevEntries := b.currentManagedEntries()
	prevRunning := b.caddy.Status(context.Background()).Owned

	// 需要特权操作的场景：写 hosts、信任/撤销 CA；否则完全无需 helper。
	needHelper := len(entries) > 0 || len(prevEntries) > 0
	slog.Info("route apply planned", "hosts_entries", len(entries), "caddy_enabled", len(caddyConfig) > 0, "ca_trusted", caFingerprint != "")
	if needHelper {
		if err := b.helper.EnsureInstalled(); err != nil {
			slog.Error("privileged helper unavailable", "err", err)
			return result, fmt.Errorf("privileged helper unavailable: %w", err)
		}
	}

	hostsChanged := !entriesEqual(entries, prevEntries)
	if hostsChanged {
		if err := b.callHelper(helper.Request{Op: helper.OpApplyManagedHosts, Hosts: entries}); err != nil {
			slog.Error("apply managed hosts failed", "err", err)
			return result, fmt.Errorf("apply managed hosts: %w", err)
		}
		slog.Info("managed hosts applied", "entries", len(entries))
		result.HostsApplied = true
	} else {
		result.HostsApplied = true
	}
	if err := advanceJournal("hosts_applied"); err != nil {
		return result, err
	}

	// Caddy：无启用 Route 时确保停止并撤销 CA；有则诊断端口→重载→信任 CA。
	if len(caddyConfig) == 0 {
		if prevRunning {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			err := b.caddy.Stop(ctx)
			cancel()
			if err != nil {
				slog.Error("stop caddy failed", "err", err)
				return result, fmt.Errorf("stop caddy: %w", err)
			}
			slog.Info("caddy stopped (no enabled routes)")
		}
		if caFingerprint != "" {
			if err := b.caTrust.RemoveCurrentCaddyCA(context.Background()); err != nil {
				slog.Error("untrust local ca failed", "err", err)
				return result, fmt.Errorf("untrust local ca: %w", err)
			}
			slog.Info("local ca untrusted")
			caFingerprint = ""
		}
		if err := advanceJournal("neutralized"); err != nil {
			return result, err
		}
		return result, nil
	}

	prevConfig, _ := os.ReadFile(b.caddyConfigPth)
	// 短路：Caddy 已运行且新编译配置与磁盘 caddy.json 字节相同时，跳过 admin API 热重载。
	// CompileCaddy 对同一输入字节稳定（路由/subjects 排序、json map 键排序），bytes.Equal 安全。
	// 既避免无谓的 /load 请求，也避免 Caddy 落盘 caddy.json 时重写相同文件。
	if prevRunning && bytes.Equal(prevConfig, caddyConfig) {
		slog.Info("caddy config unchanged, supervisor will verify digest")
	}
	revision := configRevision(caddyConfig)
	applyResult, err := b.caddy.Apply(context.Background(), revision, caddyConfig)
	if err != nil {
		b.rollbackHosts(prevEntries, hostsChanged)
		slog.Error("reload caddy failed", "err", err)
		return result, fmt.Errorf("reload caddy: %w", err)
	}
	if applyResult.Outcome == caddycore.OutcomePortConflict {
		result.PortConflict = applyResult.Detail
		return result, nil
	}
	{
		slog.Info("caddy config reloaded")
		result.CaddyApplied = true
	}
	if err := advanceJournal("caddy_applied"); err != nil {
		return result, err
	}

	caStatus, err := b.caTrust.Status(context.Background())
	if err != nil {
		return result, fmt.Errorf("query current-user ca: %w", err)
	}
	if caStatus.State != helper.CATrusted {
		identity, err := b.caTrust.EnsureCurrentCaddyCATrusted(context.Background())
		if err != nil {
			b.rollbackCaddy(prevRunning, prevConfig)
			b.rollbackHosts(prevEntries, hostsChanged)
			slog.Error("trust local ca failed", "err", err)
			return result, fmt.Errorf("trust local ca: %w", err)
		}
		caFingerprint = identity.SHA256
		slog.Info("local ca trusted", "sha256_prefix", identity.SHA256[:12])
	} else {
		caFingerprint = caStatus.Identity.SHA256
	}
	if err := advanceJournal("ca_applied"); err != nil {
		return result, err
	}
	return result, nil
}

// ResumeCaddy 在应用启动时恢复 Caddy 运行：存在启用 Caddy 的 Route 时按 Vault 状态重启进程。
// 与 ApplyRoute 的区别：不重写 hosts（已持久化）、绝不触发特权安装（不弹 UAC）；
// CA 未信任时仅在 helper 可达时补信任，否则如实记录，待用户手动应用。
func (b *RouterBiz) ResumeCaddy() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	data, err := b.store.Load()
	if err != nil {
		return err
	}
	if !hasCaddyEnabledRoute(data) {
		return nil
	}
	caddyConfig, err := route.CompileCaddy(data)
	if err != nil {
		return err
	}
	if b.caddy.Status(context.Background()).Owned {
		slog.Info("caddy already running at resume")
	} else {
		applyResult, err := b.caddy.Apply(context.Background(), configRevision(caddyConfig), caddyConfig)
		if err != nil {
			return fmt.Errorf("resume caddy reload: %w", err)
		}
		if applyResult.Outcome == caddycore.OutcomePortConflict {
			slog.Warn("caddy port conflict at resume, routes stay hosts-only", "detail", applyResult.Detail)
			return nil
		}
		slog.Info("caddy resumed at startup")
	}

	caStatus, err := b.caTrust.Status(context.Background())
	if err != nil {
		return fmt.Errorf("query current-user ca at resume: %w", err)
	}
	if caStatus.State == helper.CATrusted {
		return nil
	}
	// 启动恢复不替用户作出新的根证书信任决定；下一次显式 Apply 再登记当前用户 CA。
	slog.Info("current-user ca confirmation required; defer until explicit route apply")
	return nil
}

// RouteStatus 汇总每条 Route 的系统生效状态。
func (b *RouterBiz) RouteStatus() ([]RouteStatusItem, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	data, err := b.store.Load()
	if err != nil {
		return nil, err
	}
	applied := map[string]bool{}
	for _, e := range b.currentManagedEntries() {
		applied[e.Domain] = true
	}
	caddyStatus := b.caddy.Status(context.Background())
	running := caddyStatus.Owned
	portConflict := !running && strings.Contains(strings.ToLower(caddyStatus.LastError), "443")
	caStatus, caErr := b.caTrust.Status(context.Background())
	caTrusted := caErr == nil && caStatus.State == helper.CATrusted

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
	b.mu.Lock()
	defer b.mu.Unlock()
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
	// Preview 保持纯读：端口冲突由 Supervisor 在 Commit/Apply 时按真实启动结果分类。
	caStatus, caErr := b.caTrust.Status(context.Background())
	preview.CATrustNeeded = hasCaddyEnabledRoute(data) && (caErr != nil || caStatus.State != helper.CATrusted)
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

func (b *RouterBiz) rollbackHosts(prevEntries []route.HostEntry, hostsChanged bool) {
	if hostsChanged {
		slog.Warn("rollback managed hosts to previous entries", "entries", len(prevEntries))
		_ = b.callHelper(helper.Request{Op: helper.OpApplyManagedHosts, Hosts: prevEntries})
	}
}

func (b *RouterBiz) rollbackCaddy(prevRunning bool, prevConfig []byte) {
	if prevRunning && len(prevConfig) > 0 {
		slog.Warn("rollback caddy to previous config")
		_, _ = b.caddy.Apply(context.Background(), configRevision(prevConfig), prevConfig)
	} else if !prevRunning {
		slog.Warn("rollback caddy: stop process")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = b.caddy.Stop(ctx)
		cancel()
	}
}

func configRevision(config []byte) string {
	sum := sha256.Sum256(config)
	return hex.EncodeToString(sum[:])
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
