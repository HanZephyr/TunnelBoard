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
var (
	ErrDomainConfirmationRequired = errors.New("biz: domain override requires explicit confirmation")
	ErrCAConfirmationRequired     = errors.New("biz: current-user CA trust requires explicit fingerprint confirmation")
	ErrRouteRecoveryPending       = errors.New("biz: interrupted route transaction requires explicit recovery")
)

// HelperClient 是特权辅助服务的调用接缝（生产 = *helper.Client，测试 = fake）。
type HelperClient interface {
	Call(req helper.Request) (helper.Response, error)
	Ping() (string, error)
	EnsureInstalled() error
}

// CaddyAdapter 是 Caddy 进程管理接缝（生产 = *caddy.Adapter，测试 = fake）。
type CaddyAdapter interface {
	Apply(ctx context.Context, revision string, config []byte) (caddycore.ApplyResult, error)
	PrepareRootCA(ctx context.Context, config []byte) ([]byte, error)
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
	RouteID         int              `json:"routeId"`
	Domain          string           `json:"domain"`
	HostsEnabled    bool             `json:"hostsEnabled"`
	HostsApplied    bool             `json:"hostsApplied"`
	CaddyEnabled    bool             `json:"caddyEnabled"`
	CaddyRunning    bool             `json:"caddyRunning"`
	PortConflict    bool             `json:"portConflict"`
	CATrusted       bool             `json:"caTrusted"`
	State           RouteApplyStatus `json:"state"`
	DesiredRevision string           `json:"desiredRevision"`
	AppliedRevision string           `json:"appliedRevision,omitempty"`
	Error           string           `json:"error,omitempty"`
}

// RoutePreview 是 ApplyRoute 前的预览：将写入的 hosts 记录、需要的确认项与风险点。
type RoutePreview struct {
	HostsRecords         []route.HostEntry `json:"hostsRecords"`
	RequiresConfirmation []string          `json:"requiresConfirmation"`
	PortConflict         bool              `json:"portConflict"`
	CATrustNeeded        bool              `json:"caTrustNeeded"`
	CAFingerprint        string            `json:"caFingerprint,omitempty"`
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
	return b.applySystemLocked("")
}

// ReconcileRoutes 按 Vault 当前状态全量重推系统（删除 Route/Forward 后的清理不需要域名确认）。
func (b *RouterBiz) ReconcileRoutes() (RouteApplyResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.applySystemLocked("")
}

// ReconcileRoutesWithCATrust 仅在调用方已经向用户展示并确认了当前 CA 的
// 完整 SHA-256 指纹后使用。指纹在任何副作用发生前重新计算并精确匹配。
func (b *RouterBiz) ReconcileRoutesWithCATrust(confirmedFingerprint string) (RouteApplyResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.applySystemLocked(confirmedFingerprint)
}

// RemoveRoute 删除 Route 并重推系统（hosts 记录随之撤销；最后一个 Caddy Route 移除后停止 Caddy 并撤 CA）。
func (b *RouterBiz) RemoveRoute(routeID int) (RouteApplyResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.catalog.DeleteWebRoute(routeID); err != nil {
		return RouteApplyResult{}, err
	}
	return b.applySystemLocked("")
}

// applySystem 是统一的系统重推流程：快照 → hosts → Caddy → CA；失败逆序回滚。
func (b *RouterBiz) applySystemLocked(confirmedCAFingerprint string) (result RouteApplyResult, retErr error) {
	if err := b.validatePendingJournalLocked(); err != nil {
		return RouteApplyResult{}, err
	}
	data, err := b.store.Load()
	if err != nil {
		return RouteApplyResult{}, err
	}
	if hasCaddyEnabledRoute(data) {
		preview, err := b.previewDesiredLocked(data, 0)
		if err != nil {
			return RouteApplyResult{}, err
		}
		if preview.CATrustNeeded && !strings.EqualFold(strings.TrimSpace(confirmedCAFingerprint), preview.CAFingerprint) {
			return RouteApplyResult{}, fmt.Errorf("%w: %s", ErrCAConfirmationRequired, preview.CAFingerprint)
		}
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
	prevEntries := b.currentManagedEntries()
	prevRunning := b.caddy.Status(context.Background()).Owned
	prevConfig, _ := os.ReadFile(b.caddyConfigPth)
	caFingerprint := ""
	prevCATrusted := false
	if caStatus, statusErr := b.caTrust.Status(context.Background()); statusErr == nil && caStatus.State == helper.CATrusted {
		caFingerprint = caStatus.Identity.SHA256
		prevCATrusted = true
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
	hostsMutated := false
	caddyMutated := false
	caMutated := false
	defer func() {
		if retErr == nil {
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
				CaddyGeneration:        b.caddy.Status(context.Background()).Generation,
			}
			if err := b.state.saveState(state); err != nil {
				retErr = fmt.Errorf("persist applied route state: %w", err)
			} else if err := b.state.clearJournal(); err != nil {
				retErr = fmt.Errorf("complete route journal: %w", err)
			}
		}
		if retErr == nil {
			return
		}
		compensationErr := b.compensateRoute(prevEntries, prevRunning, prevConfig, prevCATrusted, hostsMutated, caddyMutated, caMutated)
		state := beforeApplied
		state.Status = RouteStatusError
		state.LastError = sanitizeRouteError(errors.Join(retErr, compensationErr))
		if compensationErr != nil {
			state.PendingTxID = journal.TxID
		} else {
			state.PendingTxID = ""
			if clearErr := b.state.clearJournal(); clearErr != nil {
				compensationErr = errors.Join(compensationErr, fmt.Errorf("clear compensated route journal: %w", clearErr))
				state.PendingTxID = journal.TxID
			}
		}
		if stateErr := b.state.saveState(state); stateErr != nil {
			compensationErr = errors.Join(compensationErr, fmt.Errorf("persist route failure state: %w", stateErr))
		}
		retErr = errors.Join(retErr, compensationErr)
	}()
	advanceJournal := func(phase string) error {
		journal.Phase = phase
		return b.state.saveJournal(journal)
	}

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
		hostsMutated = true
		if err := b.callHelper(helper.Request{Op: helper.OpApplyManagedHosts, Hosts: entries, TransactionID: journal.TxID, ExpectedManagedDigest: digestHosts(prevEntries)}); err != nil {
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
			caddyMutated = true
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
			caMutated = true
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

	// 短路：Caddy 已运行且新编译配置与磁盘 caddy.json 字节相同时，跳过 admin API 热重载。
	// CompileCaddy 对同一输入字节稳定（路由/subjects 排序、json map 键排序），bytes.Equal 安全。
	// 既避免无谓的 /load 请求，也避免 Caddy 落盘 caddy.json 时重写相同文件。
	if prevRunning && bytes.Equal(prevConfig, caddyConfig) {
		slog.Info("caddy config unchanged, supervisor will verify digest")
	}
	revision := configRevision(caddyConfig)
	caddyMutated = true
	applyResult, err := b.caddy.Apply(context.Background(), revision, caddyConfig)
	if err != nil {
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
		caMutated = true
		identity, err := b.caTrust.EnsureCurrentCaddyCATrusted(context.Background())
		if err != nil {
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
	pending, err := b.recoveryPendingLocked()
	if err != nil {
		return err
	}
	if pending {
		return ErrRouteRecoveryPending
	}
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

// RecoveryPending reports whether a previous Route transaction did not reach a
// durable terminal state. It is read-only and never prompts for privilege.
func (b *RouterBiz) RecoveryPending() (bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.recoveryPendingLocked()
}

func (b *RouterBiz) recoveryPendingLocked() (bool, error) {
	_, err := b.state.loadJournal()
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

func (b *RouterBiz) validatePendingJournalLocked() error {
	journal, err := b.state.loadJournal()
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load pending route transaction: %w", err)
	}
	if journal.TxID == "" || !knownRouteJournalPhase(journal.Phase) {
		return errors.New("biz: invalid pending route transaction")
	}
	// The next journal save atomically supersedes this record. Re-applying the latest
	// Vault desired state is idempotent and safely converges any crash-interrupted phase.
	slog.Warn("recover interrupted route transaction by forward reconciliation", "tx_id", journal.TxID, "phase", journal.Phase)
	return nil
}

// AppliedState 返回每用户、每机器的 Route 实际状态；它不执行探测或系统副作用。
func (b *RouterBiz) AppliedState() (RouteAppliedState, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state.loadState()
}

// NeutralizeRoutes 将 TunnelBoard 管理的网络副作用收敛到安全中性态，并持久化
// quarantined。它不修改 Vault 中的期望配置，供完全还原事务和崩溃恢复复用。
func (b *RouterBiz) NeutralizeRoutes(ctx context.Context) (retErr error) {
	if ctx == nil {
		ctx = context.Background()
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	before, err := b.state.loadState()
	if err != nil {
		return err
	}
	state := RouteAppliedState{Status: RouteStatusQuarantined}
	defer func() {
		if retErr != nil {
			state = before
			state.Status = RouteStatusCleanupPending
			state.LastError = sanitizeRouteError(retErr)
		}
		if err := b.state.saveState(state); retErr == nil && err != nil {
			retErr = err
		}
	}()

	managed := b.currentManagedEntries()
	if len(managed) != 0 || len(before.AppliedHosts) != 0 {
		if err := b.helper.EnsureInstalled(); err != nil {
			return fmt.Errorf("privileged helper unavailable: %w", err)
		}
		if err := b.callHelper(helper.Request{Op: helper.OpApplyManagedHosts, Hosts: []route.HostEntry{}}); err != nil {
			return fmt.Errorf("remove managed hosts: %w", err)
		}
	}
	if b.caddy.Status(ctx).Owned {
		if err := b.caddy.Stop(ctx); err != nil {
			return fmt.Errorf("stop caddy: %w", err)
		}
	}
	caStatus, err := b.caTrust.Status(ctx)
	if err != nil {
		return fmt.Errorf("query current-user ca: %w", err)
	}
	if caStatus.State == helper.CATrusted {
		if err := b.caTrust.RemoveCurrentCaddyCA(ctx); err != nil {
			return fmt.Errorf("remove current-user ca: %w", err)
		}
	}
	if err := b.state.clearJournal(); err != nil {
		return err
	}
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
	desiredRevision := desiredRouteRevision(data)
	appliedState, stateErr := b.state.loadState()
	if stateErr != nil {
		appliedState = RouteAppliedState{Status: RouteStatusUnknown, LastError: sanitizeRouteError(stateErr)}
	}
	caddyStatus := b.caddy.Status(context.Background())
	running := caddyStatus.Owned
	portConflict := appliedState.AppliedDesiredRevision == desiredRevision && appliedState.PortConflict != ""
	caStatus, caErr := b.caTrust.Status(context.Background())
	caTrusted := caErr == nil && caStatus.State == helper.CATrusted

	items := make([]RouteStatusItem, 0, len(data.WebRoutes))
	for _, r := range data.WebRoutes {
		state := RouteStatusPending
		if appliedState.AppliedDesiredRevision == desiredRevision {
			state = appliedState.Status
		} else if appliedState.Status == RouteStatusError || appliedState.Status == RouteStatusCleanupPending || appliedState.Status == RouteStatusQuarantined {
			state = appliedState.Status
		}
		if state == "" {
			state = RouteStatusUnknown
		}
		items = append(items, RouteStatusItem{
			RouteID: r.ID, Domain: r.Domain, HostsEnabled: r.HostsEnabled,
			HostsApplied: r.HostsEnabled && applied[r.Domain], CaddyEnabled: r.CaddyEnabled,
			CaddyRunning: r.CaddyEnabled && running, PortConflict: r.CaddyEnabled && portConflict,
			CATrusted: caTrusted, State: state, DesiredRevision: desiredRevision,
			AppliedRevision: appliedState.AppliedDesiredRevision, Error: appliedState.LastError,
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
	return b.previewDesiredLocked(data, routeID)
}

// PreviewDesired 对尚未写入 Vault 的完整候选状态进行纯读预览；CA 需求以
// 当前用户证书库的实际查询结果为准，不相信历史 applied 指纹。
func (b *RouterBiz) PreviewDesired(data model.VaultData, routeID int) (RoutePreview, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.previewDesiredLocked(data, routeID)
}

func (b *RouterBiz) previewDesiredLocked(data model.VaultData, routeID int) (RoutePreview, error) {
	entries, _ := route.PlanHosts(data)
	preview := RoutePreview{HostsRecords: entries}
	for _, r := range data.WebRoutes {
		if r.ID == routeID && r.HostsEnabled && route.NeedsConfirmation(r.Domain) {
			preview.RequiresConfirmation = []string{r.Domain}
		}
	}
	// Preview 保持纯读：端口冲突由 Supervisor 在 Commit/Apply 时按真实启动结果分类。
	if !hasCaddyEnabledRoute(data) {
		return preview, nil
	}
	ctx := context.Background()
	caStatus, caErr := b.caTrust.Status(ctx)
	if caErr != nil {
		return RoutePreview{}, fmt.Errorf("query current-user ca for preview: %w", caErr)
	}
	preview.CATrustNeeded = caStatus.State != helper.CATrusted
	preview.CAFingerprint = caStatus.Identity.SHA256
	if preview.CAFingerprint == "" {
		config, err := route.CompileCaddy(data)
		if err != nil {
			return RoutePreview{}, err
		}
		der, err := b.caddy.PrepareRootCA(ctx, config)
		if err != nil {
			return RoutePreview{}, fmt.Errorf("prepare current-user ca for preview: %w", err)
		}
		sum := sha256.Sum256(der)
		preview.CAFingerprint = hex.EncodeToString(sum[:])
		if err := helper.ValidateLocalCA(der, preview.CAFingerprint); err != nil {
			return RoutePreview{}, fmt.Errorf("validate current-user ca for preview: %w", err)
		}
	}
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

func (b *RouterBiz) compensateRoute(prevEntries []route.HostEntry, prevRunning bool, prevConfig []byte, prevCATrusted, hostsMutated, caddyMutated, caMutated bool) error {
	var failures []error
	// Remove newly introduced trust before restoring the previous Caddy generation.
	if caMutated && !prevCATrusted {
		if err := b.caTrust.RemoveCurrentCaddyCA(context.Background()); err != nil {
			failures = append(failures, fmt.Errorf("rollback current-user ca: %w", err))
		}
	}
	if caddyMutated {
		if prevRunning && len(prevConfig) > 0 {
			slog.Warn("rollback caddy to previous config")
			if _, err := b.caddy.Apply(context.Background(), configRevision(prevConfig), prevConfig); err != nil {
				failures = append(failures, fmt.Errorf("rollback caddy config: %w", err))
			}
		} else if !prevRunning {
			slog.Warn("rollback caddy: stop process")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			err := b.caddy.Stop(ctx)
			cancel()
			if err != nil {
				failures = append(failures, fmt.Errorf("rollback caddy process: %w", err))
			}
		}
	}
	if hostsMutated {
		slog.Warn("rollback managed hosts to previous entries", "entries", len(prevEntries))
		if err := b.callHelper(helper.Request{
			Op: helper.OpApplyManagedHosts, Hosts: prevEntries, TransactionID: newRouteTxID(),
		}); err != nil {
			failures = append(failures, fmt.Errorf("rollback managed hosts: %w", err))
		}
	}
	// If neutralization removed an existing trust, restore it only after the previous
	// Caddy configuration is active again so the trust operation observes the old CA.
	if caMutated && prevCATrusted {
		if _, err := b.caTrust.EnsureCurrentCaddyCATrusted(context.Background()); err != nil {
			failures = append(failures, fmt.Errorf("restore current-user ca: %w", err))
		}
	}
	return errors.Join(failures...)
}

func knownRouteJournalPhase(phase string) bool {
	switch phase {
	case "planned", "hosts_applied", "caddy_applied", "ca_applied", "neutralized":
		return true
	default:
		return false
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
