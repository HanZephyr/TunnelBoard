package application

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/HanZephyr/TunnelBoard/internal/biz"
	"github.com/HanZephyr/TunnelBoard/internal/forward"
	"github.com/HanZephyr/TunnelBoard/internal/model"
	"github.com/HanZephyr/TunnelBoard/internal/updater"
)

var (
	ErrMaintenance        = errors.New("application: maintenance in progress")
	ErrRevisionConflict   = errors.New("application: revision conflict")
	ErrCommandIDConflict  = errors.New("application: command id conflict")
	ErrSSHHostChangeToken = errors.New("application: invalid or expired ssh host change token")
	ErrSSHHostChangeStale = errors.New("application: ssh host change preview is stale")
)

const sshHostChangeTTL = 2 * time.Minute

type CommandIDConflictError struct {
	CommandID          string
	ExistingOperation  string
	RequestedOperation string
}

func (e *CommandIDConflictError) Error() string {
	return fmt.Sprintf("%v: commandId=%q existing=%s requested=%s", ErrCommandIDConflict, e.CommandID, e.ExistingOperation, e.RequestedOperation)
}

func (e *CommandIDConflictError) Unwrap() error { return ErrCommandIDConflict }

type RuntimePort interface {
	Snapshot() ([]biz.RuntimeStatus, error)
	Start(int) error
	Stop(int) error
	StartAutoStart() (map[int]error, error)
	Suspend(context.Context, []int) (biz.RuntimeSuspendPlan, error)
	SuspendAll(context.Context) (biz.RuntimeSuspendPlan, error)
	Resume(context.Context, biz.RuntimeSuspendPlan) biz.RuntimeResumeResult
	AffectedForHost(int) []biz.AffectedForward
	PreflightHostChange(context.Context, model.SSHHost, []biz.AffectedForward) map[int]string
	LocalListenerOwner(string, int) (int, bool)
	RetireHost(int)
}

type RoutePort interface {
	RouteStatus() ([]biz.RouteStatusItem, error)
	AppliedState() (biz.RouteAppliedState, error)
	PreviewDesired(model.VaultData, int) (biz.RoutePreview, error)
	NeutralizeRoutes(context.Context) error
	ReconcileRoutes() (biz.RouteApplyResult, error)
}

type RestorePort interface {
	StageRestore(context.Context, biz.RestoreStageRequest) (biz.RestorePreview, error)
	CommitRestore(context.Context, biz.RestoreCommitRequest) (biz.RestoreCommitResult, error)
}

type RecoveryPort interface {
	State() (quarantined bool, journalPending bool, err error)
	ClearQuarantine() error
}

type UpdatePort interface {
	Check(context.Context, string) (updater.Result, error)
}

type Dependencies struct {
	Store        biz.VaultStore
	Catalog      *biz.CatalogBiz
	Runtime      RuntimePort
	Routes       RoutePort
	Restore      RestorePort
	Recovery     RecoveryPort
	Backup       *biz.BackupBiz
	Packages     biz.BackupPackage
	CommandCache CommandCacheOptions
	Updates      UpdatePort
	AppVersion   string
}

type Service struct {
	store        biz.VaultStore
	catalog      *biz.CatalogBiz
	runtime      RuntimePort
	routes       RoutePort
	restore      RestorePort
	recovery     RecoveryPort
	backup       *biz.BackupBiz
	packages     biz.BackupPackage
	mutation     sync.Mutex
	importMu     sync.Mutex
	importStage  *stagedImport
	hostChangeMu sync.Mutex
	hostChanges  map[string]stagedSSHHostChange
	routeStageMu sync.Mutex
	routeStages  map[string]stagedRouteChange
	maintenance  atomic.Bool
	sequence     atomic.Uint64
	commands     *recentCommandCache
	updates      UpdatePort
	appVersion   string
	updateMu     sync.Mutex
	updateFlight *updateCheckFlight
}

type updateCheckFlight struct {
	done   chan struct{}
	result CheckForUpdatesResult
	err    error
}

func NewService(deps Dependencies) *Service {
	return &Service{
		store: deps.Store, catalog: deps.Catalog, runtime: deps.Runtime, routes: deps.Routes,
		restore: deps.Restore, recovery: deps.Recovery, backup: deps.Backup, packages: deps.Packages,
		hostChanges: make(map[string]stagedSSHHostChange), routeStages: make(map[string]stagedRouteChange),
		commands: newRecentCommandCache(deps.CommandCache),
		updates:  deps.Updates, appVersion: deps.AppVersion,
	}
}

func (s *Service) CheckForUpdates(ctx context.Context, command CheckForUpdatesCommand) (CheckForUpdatesResult, error) {
	if err := ctx.Err(); err != nil {
		return CheckForUpdatesResult{}, err
	}
	if command.Trigger != UpdateCheckStartup && command.Trigger != UpdateCheckManual {
		return CheckForUpdatesResult{}, fmt.Errorf("application: unsupported update trigger %q", command.Trigger)
	}
	if s.updates == nil {
		return CheckForUpdatesResult{}, errors.New("application: updater is unavailable")
	}
	quarantined, pending, err := s.recovery.State()
	if err != nil {
		return CheckForUpdatesResult{}, err
	}
	if quarantined || pending || s.maintenance.Load() {
		return CheckForUpdatesResult{Skipped: true, SkipReason: "recovery_isolation"}, nil
	}
	if command.Trigger == UpdateCheckStartup {
		data, err := s.store.Load()
		if err != nil {
			return CheckForUpdatesResult{}, err
		}
		if !data.Prefs.UpdateCheckEnabled {
			return CheckForUpdatesResult{Skipped: true, SkipReason: "preference_disabled"}, nil
		}
	}

	s.updateMu.Lock()
	if flight := s.updateFlight; flight != nil {
		s.updateMu.Unlock()
		select {
		case <-ctx.Done():
			return CheckForUpdatesResult{}, ctx.Err()
		case <-flight.done:
			return flight.result, flight.err
		}
	}
	flight := &updateCheckFlight{done: make(chan struct{})}
	s.updateFlight = flight
	s.updateMu.Unlock()

	result, checkErr := s.updates.Check(ctx, s.appVersion)
	flight.result = CheckForUpdatesResult{Result: result}
	flight.err = checkErr
	close(flight.done)
	s.updateMu.Lock()
	if s.updateFlight == flight {
		s.updateFlight = nil
	}
	s.updateMu.Unlock()
	return flight.result, flight.err
}

// LegacyMutation 仅供 app.go 迁移期兼容绑定使用，使旧调用也服从 maintenance gate。
// 新用例必须新增有类型的 Service 方法，不向 WebView 暴露此函数接缝。
func (s *Service) LegacyMutation(ctx context.Context, mutate func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.maintenance.Load() {
		return ErrMaintenance
	}
	s.mutation.Lock()
	defer s.mutation.Unlock()
	if s.maintenance.Load() {
		return ErrMaintenance
	}
	if err := mutate(); err != nil {
		return err
	}
	s.sequence.Add(1)
	return nil
}

func (s *Service) StartForwards(ctx context.Context, ids []int) map[int]string {
	errorsByID := map[int]string{}
	err := s.LegacyMutation(ctx, func() error {
		for _, id := range ids {
			if err := s.runtime.Start(id); err != nil {
				errorsByID[id] = err.Error()
			}
		}
		return nil
	})
	if err != nil {
		for _, id := range ids {
			errorsByID[id] = err.Error()
		}
	}
	return errorsByID
}

// StopForward 是 SafeStop，不受 maintenance 阻塞。
func (s *Service) StopForward(id int) error { return s.runtime.Stop(id) }

type stagedImport struct {
	token         string
	expiresAt     time.Time
	vaultRevision string
	backup        biz.StagedBackup
	keyPaths      map[string]string
	keyViews      []KeyFileView
	exporting     map[string]bool
	committing    bool
	committed     bool
}

type stagedSSHHostChange struct {
	expiresAt     time.Time
	vaultRevision string
	oldIdentity   forward.ConnectionIdentity
	affected      []biz.AffectedForward
	request       biz.SaveSSHHostRequest
	proposed      model.SSHHost
}

const routeChangeStageTTL = 5 * time.Minute

type stagedRouteChange struct {
	token           string
	expiresAt       time.Time
	desiredRevision string
	appliedRevision string
	intent          RouteChangeIntent
	candidate       model.VaultData
	route           *model.WebRoute
	requiredDomains []string
	caTrustNeeded   bool
}

func (s *Service) GetSnapshot(ctx context.Context) (AppSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return AppSnapshot{}, err
	}
	data, err := s.store.Load()
	if err != nil {
		return AppSnapshot{}, err
	}
	runtimeView, err := s.runtime.Snapshot()
	if err != nil {
		return AppSnapshot{}, err
	}
	routes, err := s.routes.RouteStatus()
	if err != nil {
		return AppSnapshot{}, err
	}
	applied, err := s.routes.AppliedState()
	if err != nil {
		return AppSnapshot{}, err
	}
	quarantined, pending, err := s.recovery.State()
	if err != nil {
		return AppSnapshot{}, err
	}
	vaultRevision := revisionOfCatalog(data)
	return AppSnapshot{
		SchemaVersion: 1, EventSequence: s.sequence.Load(), ObservedAt: time.Now().UTC(),
		Revisions: DomainRevisions{Vault: vaultRevision, Runtime: revisionOf(runtimeView), Route: applied.AppliedDesiredRevision, Preferences: revisionOf(data.Prefs)},
		Catalog:   catalogView(data), Runtime: runtimeView, Routes: routes, RouteApplied: applied,
		Preferences:     PreferencesView{AutoRun: data.Prefs.AutoRun, UpdateCheckEnabled: data.Prefs.UpdateCheckEnabled, UILocale: data.Prefs.UILocale},
		Recovery:        RecoveryView{Quarantined: quarantined, JournalPending: pending, Maintenance: s.maintenance.Load()},
		Capabilities:    CapabilityView{MutationAllowed: !s.maintenance.Load()},
		SSHHostDefaults: SSHHostView{Port: 22, AuthType: "ssh_key", KeepAliveIntervalMs: 5000, TimeoutMs: 5000},
	}, nil
}

func (s *Service) PreviewSSHHostChange(ctx context.Context, command SaveSSHHostCommand) (SSHHostChangePreview, error) {
	if err := ctx.Err(); err != nil {
		return SSHHostChangePreview{}, err
	}
	if s.maintenance.Load() {
		return SSHHostChangePreview{}, ErrMaintenance
	}
	s.mutation.Lock()
	defer s.mutation.Unlock()
	if s.maintenance.Load() {
		return SSHHostChangePreview{}, ErrMaintenance
	}
	data, err := s.store.Load()
	if err != nil {
		return SSHHostChangePreview{}, err
	}
	currentRevision := revisionOfCatalog(data)
	if command.Meta.ExpectedRevision != "" && command.Meta.ExpectedRevision != currentRevision {
		return SSHHostChangePreview{}, fmt.Errorf("%w: current=%s", ErrRevisionConflict, currentRevision)
	}
	request := biz.SaveSSHHostRequest{Host: command.Host.model(), SecretAction: command.SecretAction, SecretInput: command.SecretInput}
	preview, changed, err := s.catalog.PreviewSSHHostSecure(request)
	if err != nil {
		return SSHHostChangePreview{}, err
	}
	affected := s.runtime.AffectedForHost(preview.ID)
	result := SSHHostChangePreview{Host: sshHostView(preview), ConnectionChanged: changed, RequiresCommit: changed, AffectedForwards: cloneAffectedForwards(affected), AcceptedRevision: currentRevision}
	if !changed {
		return result, nil
	}
	old, ok := findSSHHost(data.SSHHosts, preview.ID)
	if !ok {
		return SSHHostChangePreview{}, fmt.Errorf("%w: ssh host %d disappeared", ErrSSHHostChangeStale, preview.ID)
	}
	token, err := randomSSHHostChangeToken()
	if err != nil {
		return SSHHostChangePreview{}, err
	}
	result.Token = token
	result.ExpiresAt = time.Now().UTC().Add(sshHostChangeTTL)
	s.hostChangeMu.Lock()
	for stagedToken, stage := range s.hostChanges {
		if !stage.expiresAt.After(time.Now().UTC()) {
			delete(s.hostChanges, stagedToken)
		}
	}
	s.hostChanges[token] = stagedSSHHostChange{expiresAt: result.ExpiresAt, vaultRevision: currentRevision, oldIdentity: forward.SSHConnectionIdentity(old), affected: cloneAffectedForwards(affected), request: request, proposed: preview}
	s.hostChangeMu.Unlock()
	return result, nil
}

func (s *Service) CommitSSHHostChange(ctx context.Context, command CommitSSHHostChangeCommand) (CommitSSHHostChangeResult, error) {
	if err := ctx.Err(); err != nil {
		return CommitSSHHostChangeResult{}, err
	}
	if s.maintenance.Load() {
		return CommitSSHHostChangeResult{}, ErrMaintenance
	}
	s.mutation.Lock()
	defer s.mutation.Unlock()
	if s.maintenance.Load() {
		return CommitSSHHostChangeResult{}, ErrMaintenance
	}
	cached, digest, ok, err := lookupCommandResult[CommitSSHHostChangeResult](s.commands, command.Meta.CommandID, "commit_ssh_host_change", command)
	if err != nil {
		return CommitSSHHostChangeResult{}, err
	}
	if ok {
		return cached, nil
	}
	result, err := s.commitSSHHostChangeOnceLocked(ctx, command.Token)
	if err == nil {
		if cacheErr := storeCommandResult(s.commands, command.Meta.CommandID, "commit_ssh_host_change", digest, result); cacheErr != nil {
			return CommitSSHHostChangeResult{}, cacheErr
		}
	}
	return result, err
}

func (s *Service) commitSSHHostChangeOnceLocked(ctx context.Context, token string) (CommitSSHHostChangeResult, error) {
	if err := ctx.Err(); err != nil {
		return CommitSSHHostChangeResult{}, err
	}
	s.hostChangeMu.Lock()
	stage, ok := s.hostChanges[token]
	delete(s.hostChanges, token)
	s.hostChangeMu.Unlock()
	if !ok || token == "" || !stage.expiresAt.After(time.Now().UTC()) {
		return CommitSSHHostChangeResult{}, ErrSSHHostChangeToken
	}
	data, err := s.store.Load()
	if err != nil {
		return CommitSSHHostChangeResult{}, err
	}
	currentRevision := revisionOfCatalog(data)
	currentHost, exists := findSSHHost(data.SSHHosts, stage.request.Host.ID)
	if currentRevision != stage.vaultRevision || !exists || forward.SSHConnectionIdentity(currentHost) != stage.oldIdentity {
		return CommitSSHHostChangeResult{}, fmt.Errorf("%w: current=%s", ErrSSHHostChangeStale, currentRevision)
	}
	currentAffected := s.runtime.AffectedForHost(stage.request.Host.ID)
	if !sameAffectedForwards(currentAffected, stage.affected) {
		return CommitSSHHostChangeResult{}, ErrSSHHostChangeStale
	}

	result := CommitSSHHostChangeResult{Host: sshHostView(stage.proposed), ForwardResults: initialHostChangeForwardResults(stage.affected)}
	preflightCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	result.PreflightErrors = s.runtime.PreflightHostChange(preflightCtx, stage.proposed, stage.affected)
	cancel()
	if len(result.PreflightErrors) != 0 {
		result.FailureStage = "preflight"
		sanitizeSSHHostChangeResult(&result, stage.proposed.Password)
		return result, nil
	}

	runningIDs := runningAffectedIDs(stage.affected)
	var plan biz.RuntimeSuspendPlan
	if len(runningIDs) != 0 {
		stopCtx, stopCancel := context.WithTimeout(ctx, 5*time.Second)
		plan, err = s.runtime.Suspend(stopCtx, runningIDs)
		stopCancel()
		if err != nil {
			result.FailureStage, result.OperationError = "stop", err.Error()
			compensateHostChange(s.runtime, plan, result.ForwardResults)
			sanitizeSSHHostChangeResult(&result, stage.proposed.Password)
			return result, nil
		}
	}
	saved, changed, err := s.catalog.SaveSSHHostSecure(stage.request)
	if err != nil {
		result.FailureStage, result.OperationError = "save", err.Error()
		compensateHostChange(s.runtime, plan, result.ForwardResults)
		sanitizeSSHHostChangeResult(&result, stage.proposed.Password)
		return result, nil
	}
	if !changed {
		result.FailureStage = "save"
		result.OperationError = ErrSSHHostChangeStale.Error()
		compensateHostChange(s.runtime, plan, result.ForwardResults)
		sanitizeSSHHostChangeResult(&result, stage.proposed.Password)
		return result, nil
	}
	s.runtime.RetireHost(saved.ID)
	if len(plan.Entries) != 0 {
		restartCtx, restartCancel := context.WithTimeout(ctx, 5*time.Second)
		resume := s.runtime.Resume(restartCtx, plan)
		restartCancel()
		applyResumeResult(result.ForwardResults, resume, "restarted", "restart_failed", false)
		if len(resume.Errors) != 0 {
			result.FailureStage = "restart"
		}
	}
	data, err = s.store.Load()
	if err != nil {
		return CommitSSHHostChangeResult{}, err
	}
	result.Committed = true
	result.Host = sshHostView(saved)
	result.AcceptedRevision = revisionOfCatalog(data)
	result.EventSequence = s.sequence.Add(1)
	sanitizeSSHHostChangeResult(&result, stage.proposed.Password)
	return result, nil
}

// SaveSSHHost 保留旧绑定的兼容形状；连接身份变化必须携带 Preview 返回的 token，
// 单独的 ConfirmRestart 布尔值不再被视为事实绑定的授权。
func (s *Service) SaveSSHHost(ctx context.Context, command SaveSSHHostCommand) (SaveSSHHostResult, error) {
	if command.ConfirmRestart && command.PreviewToken != "" {
		committed, err := s.CommitSSHHostChange(ctx, CommitSSHHostChangeCommand{Meta: command.Meta, Token: command.PreviewToken})
		legacy := saveSSHHostResultFromCommit(committed)
		if err != nil {
			return legacy, err
		}
		if !committed.Committed {
			return legacy, fmt.Errorf("application: ssh host change failed during %s: %s", committed.FailureStage, committed.OperationError)
		}
		return legacy, nil
	}
	preview, err := s.PreviewSSHHostChange(ctx, command)
	if err != nil {
		return SaveSSHHostResult{}, err
	}
	if preview.RequiresCommit {
		result := SaveSSHHostResult{Host: preview.Host, ConnectionChanged: true, RequiresRestart: true, PreviewToken: preview.Token, PreviewExpiresAt: preview.ExpiresAt, AcceptedRevision: preview.AcceptedRevision}
		for _, item := range preview.AffectedForwards {
			result.AffectedForwardIDs = append(result.AffectedForwardIDs, item.ForwardID)
			if item.RunningGeneration != 0 {
				result.RunningForwardIDs = append(result.RunningForwardIDs, item.ForwardID)
			}
		}
		return result, nil
	}
	return s.saveSSHHostWithoutIdentityChange(ctx, command, preview.AcceptedRevision)
}

func (s *Service) saveSSHHostWithoutIdentityChange(ctx context.Context, command SaveSSHHostCommand, expectedRevision string) (SaveSSHHostResult, error) {
	if err := ctx.Err(); err != nil {
		return SaveSSHHostResult{}, err
	}
	if s.maintenance.Load() {
		return SaveSSHHostResult{}, ErrMaintenance
	}
	s.mutation.Lock()
	defer s.mutation.Unlock()
	if s.maintenance.Load() {
		return SaveSSHHostResult{}, ErrMaintenance
	}
	cached, digest, ok, err := lookupCommandResult[SaveSSHHostResult](s.commands, command.Meta.CommandID, "save_ssh_host", command)
	if err != nil {
		return SaveSSHHostResult{}, err
	}
	if ok {
		return cached, nil
	}
	data, err := s.store.Load()
	if err != nil {
		return SaveSSHHostResult{}, err
	}
	current := revisionOfCatalog(data)
	if current != expectedRevision {
		return SaveSSHHostResult{}, fmt.Errorf("%w: current=%s", ErrRevisionConflict, current)
	}
	request := biz.SaveSSHHostRequest{Host: command.Host.model(), SecretAction: command.SecretAction, SecretInput: command.SecretInput}
	saved, changed, err := s.catalog.SaveSSHHostSecure(request)
	if err != nil {
		return SaveSSHHostResult{}, err
	}
	if changed {
		return SaveSSHHostResult{}, ErrSSHHostChangeStale
	}
	data, err = s.store.Load()
	if err != nil {
		return SaveSSHHostResult{}, err
	}
	result := SaveSSHHostResult{Host: sshHostView(saved), AcceptedRevision: revisionOfCatalog(data), EventSequence: s.sequence.Add(1)}
	if err := storeCommandResult(s.commands, command.Meta.CommandID, "save_ssh_host", digest, result); err != nil {
		return SaveSSHHostResult{}, err
	}
	return result, nil
}

func randomSSHHostChangeToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("application: create ssh host change token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func findSSHHost(hosts []model.SSHHost, id int) (model.SSHHost, bool) {
	for _, host := range hosts {
		if host.ID == id {
			return host, true
		}
	}
	return model.SSHHost{}, false
}

func cloneAffectedForwards(items []biz.AffectedForward) []biz.AffectedForward {
	cloned := append([]biz.AffectedForward(nil), items...)
	sort.Slice(cloned, func(i, j int) bool { return cloned[i].ForwardID < cloned[j].ForwardID })
	return cloned
}

func sameAffectedForwards(a, b []biz.AffectedForward) bool {
	a, b = cloneAffectedForwards(a), cloneAffectedForwards(b)
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

// PreviewRouteChange 构造完整候选 desired state，并用短期 token 绑定当前
// Vault/applied revisions。该方法纯读，不写 Vault、hosts、Caddy 或 CA。
func (s *Service) PreviewRouteChange(ctx context.Context, intent RouteChangeIntent) (RouteChangePreview, error) {
	if err := ctx.Err(); err != nil {
		return RouteChangePreview{}, err
	}
	if s.maintenance.Load() {
		return RouteChangePreview{}, ErrMaintenance
	}
	data, err := s.store.Load()
	if err != nil {
		return RouteChangePreview{}, err
	}
	desiredRevision := revisionOfCatalog(data)
	if intent.ExpectedRevision != "" && intent.ExpectedRevision != desiredRevision {
		return RouteChangePreview{}, fmt.Errorf("%w: current=%s", ErrRevisionConflict, desiredRevision)
	}
	applied, err := s.routes.AppliedState()
	if err != nil {
		return RouteChangePreview{}, err
	}
	// VaultData 的 slice 是引用语义；Preview 必须复制将要修改的 Route slice，
	// 否则内存 Store/缓存 Adapter 会在纯读预览期间被意外改写。
	candidate := data
	candidate.WebRoutes = append([]model.WebRoute(nil), data.WebRoutes...)
	changedRoute, previous, err := applyRouteIntent(&candidate, intent)
	if err != nil {
		return RouteChangePreview{}, err
	}
	previewRouteID := 0
	if changedRoute != nil {
		previewRouteID = changedRoute.ID
	}
	systemPreview, err := s.routes.PreviewDesired(candidate, previewRouteID)
	if err != nil {
		return RouteChangePreview{}, err
	}
	requiredDomains := append([]string(nil), systemPreview.RequiresConfirmation...)
	token, err := newRouteChangeToken()
	if err != nil {
		return RouteChangePreview{}, err
	}
	expiresAt := time.Now().Add(routeChangeStageTTL)
	caTrustNeeded := systemPreview.CATrustNeeded
	stage := stagedRouteChange{
		token: token, expiresAt: expiresAt, desiredRevision: desiredRevision,
		appliedRevision: applied.AppliedDesiredRevision, intent: intent, candidate: candidate,
		route: cloneWebRoute(changedRoute), requiredDomains: append([]string(nil), requiredDomains...),
		caTrustNeeded: caTrustNeeded,
	}
	s.routeStageMu.Lock()
	// UI 全局只允许一个 Route mutation；新 Preview 同时废止旧 token，既避免
	// 旧确认框迟到提交，也让恶意重复预览无法在会话内堆积 stage。
	s.routeStages = map[string]stagedRouteChange{token: stage}
	s.routeStageMu.Unlock()
	return RouteChangePreview{
		Token: token, ExpiresAt: expiresAt, DesiredRevision: desiredRevision,
		AppliedRevision: applied.AppliedDesiredRevision, Route: cloneWebRoute(changedRoute),
		LinkedChanges: linkedRouteFlagChanges(previous, changedRoute), HostsRecords: systemPreview.HostsRecords,
		RequiresConfirmation: requiredDomains,
		CATrustNeeded:        caTrustNeeded,
	}, nil
}

// CommitRouteChange 先在应用 mutation lock 内复核 Preview token，再以一次
// Vault Update 保存 desired state，最后串行 reconcile。保存成功后应用失败不会
// 回滚 desired，而是通过 saved_not_applied 把两种事实同时返回给 UI。
func (s *Service) CommitRouteChange(ctx context.Context, command CommitRouteChangeCommand) (RouteCommandResult, error) {
	if err := ctx.Err(); err != nil {
		return RouteCommandResult{}, err
	}
	if s.maintenance.Load() {
		return RouteCommandResult{}, ErrMaintenance
	}
	s.mutation.Lock()
	defer s.mutation.Unlock()
	if s.maintenance.Load() {
		return RouteCommandResult{}, ErrMaintenance
	}
	cached, digest, ok, err := lookupCommandResult[RouteCommandResult](s.commands, command.Meta.CommandID, "commit_route_change", command)
	if err != nil {
		return RouteCommandResult{}, err
	}
	if ok {
		return cached, nil
	}
	s.routeStageMu.Lock()
	stage, ok := s.routeStages[command.Token]
	s.routeStageMu.Unlock()
	if !ok || strings.TrimSpace(command.Token) == "" || time.Now().After(stage.expiresAt) {
		return s.cacheRouteCommandResult(command, digest, rejectedRouteResult("invalid_or_expired_token", "route preview token is invalid or expired"))
	}
	if !containsAllDomains(command.ConfirmedDomains, stage.requiredDomains) {
		return s.cacheRouteCommandResult(command, digest, rejectedRouteResult("confirmation_required", "domain override confirmation is required"))
	}
	if stage.caTrustNeeded && !command.ConfirmCATrust {
		return s.cacheRouteCommandResult(command, digest, rejectedRouteResult("ca_confirmation_required", "current-user CA trust confirmation is required"))
	}
	data, err := s.store.Load()
	if err != nil {
		return RouteCommandResult{}, err
	}
	if revisionOfCatalog(data) != stage.desiredRevision {
		s.deleteRouteStage(command.Token)
		return s.cacheRouteCommandResult(command, digest, rejectedRouteResult("stale_revision", "route preview is stale"))
	}
	applied, err := s.routes.AppliedState()
	if err != nil {
		return RouteCommandResult{}, err
	}
	if applied.AppliedDesiredRevision != stage.appliedRevision {
		s.deleteRouteStage(command.Token)
		return s.cacheRouteCommandResult(command, digest, rejectedRouteResult("stale_applied_revision", "applied route state changed after preview"))
	}

	var savedRoute *model.WebRoute
	updated, err := s.store.Update(func(current *model.VaultData) error {
		if revisionOfCatalog(*current) != stage.desiredRevision {
			return ErrRevisionConflict
		}
		changed, _, changeErr := applyRouteIntent(current, stage.intent)
		if changeErr != nil {
			return changeErr
		}
		savedRoute = cloneWebRoute(changed)
		return nil
	})
	if err != nil {
		s.deleteRouteStage(command.Token)
		code := "desired_save_failed"
		if errors.Is(err, ErrRevisionConflict) {
			code = "stale_revision"
		}
		return s.cacheRouteCommandResult(command, digest, RouteCommandResult{Outcome: RouteOutcomeRejected, Error: &AppErrorView{Code: code, Message: err.Error()}})
	}
	s.deleteRouteStage(command.Token)
	acceptedRevision := revisionOfCatalog(updated)
	applyResult, reconcileErr := s.routes.ReconcileRoutes()
	applied, appliedErr := s.routes.AppliedState()
	result := RouteCommandResult{
		DesiredSaved: true, AcceptedRevision: acceptedRevision, Route: savedRoute,
		StateMayHaveChanged: true, EventSequence: s.sequence.Add(1),
	}
	if appliedErr == nil {
		result.Applied = &applied
	}
	if reconcileErr != nil {
		result.Outcome = RouteOutcomeSavedNotApplied
		result.Error = &AppErrorView{Code: "route_apply_failed", Message: reconcileErr.Error()}
		if appliedErr != nil {
			result.Outcome = RouteOutcomeStateUnknown
			result.Error = &AppErrorView{Code: "route_state_unknown", Message: appliedErr.Error()}
		}
		return s.cacheRouteCommandResult(command, digest, result)
	}
	if applyResult.PortConflict != "" {
		result.Outcome = RouteOutcomeHostsOnly
		return s.cacheRouteCommandResult(command, digest, result)
	}
	result.Outcome = RouteOutcomeApplied
	return s.cacheRouteCommandResult(command, digest, result)
}

func (s *Service) cacheRouteCommandResult(command CommitRouteChangeCommand, digest [sha256.Size]byte, result RouteCommandResult) (RouteCommandResult, error) {
	if err := storeCommandResult(s.commands, command.Meta.CommandID, "commit_route_change", digest, result); err != nil {
		return RouteCommandResult{}, err
	}
	return result, nil
}

func (s *Service) deleteRouteStage(token string) {
	s.routeStageMu.Lock()
	delete(s.routeStages, token)
	s.routeStageMu.Unlock()
}

func rejectedRouteResult(code, message string) RouteCommandResult {
	return RouteCommandResult{Outcome: RouteOutcomeRejected, Error: &AppErrorView{Code: code, Message: message}}
}

func applyRouteIntent(data *model.VaultData, intent RouteChangeIntent) (*model.WebRoute, *model.WebRoute, error) {
	if data == nil {
		return nil, nil, errors.New("application: missing route candidate")
	}
	var changed *model.WebRoute
	var previous *model.WebRoute
	switch intent.Action {
	case RouteChangeUpsert:
		if intent.Route == nil {
			return nil, nil, errors.New("application: route payload is required")
		}
		route := normalizeRoute(*intent.Route)
		if route.ID == 0 {
			route.ID = nextRouteID(data.WebRoutes)
			data.WebRoutes = append(data.WebRoutes, route)
			changed = &data.WebRoutes[len(data.WebRoutes)-1]
		} else {
			idx := routeIndex(data.WebRoutes, route.ID)
			if idx < 0 {
				return nil, nil, fmt.Errorf("web route %d not found", route.ID)
			}
			copy := data.WebRoutes[idx]
			previous = &copy
			data.WebRoutes[idx] = route
			changed = &data.WebRoutes[idx]
		}
	case RouteChangeSetFlag:
		idx := routeIndex(data.WebRoutes, intent.RouteID)
		if idx < 0 {
			return nil, nil, fmt.Errorf("web route %d not found", intent.RouteID)
		}
		copy := data.WebRoutes[idx]
		previous = &copy
		switch intent.Flag {
		case RouteFlagHostsEnabled:
			data.WebRoutes[idx].HostsEnabled = intent.Enabled
			if !intent.Enabled {
				data.WebRoutes[idx].CaddyEnabled = false
			}
		case RouteFlagCaddyEnabled:
			data.WebRoutes[idx].CaddyEnabled = intent.Enabled
			if intent.Enabled {
				data.WebRoutes[idx].HostsEnabled = true
			}
		default:
			return nil, nil, fmt.Errorf("application: unsupported route flag %q", intent.Flag)
		}
		changed = &data.WebRoutes[idx]
	case RouteChangeDelete:
		idx := routeIndex(data.WebRoutes, intent.RouteID)
		if idx < 0 {
			return nil, nil, fmt.Errorf("web route %d not found", intent.RouteID)
		}
		copy := data.WebRoutes[idx]
		previous = &copy
		data.WebRoutes = append(data.WebRoutes[:idx], data.WebRoutes[idx+1:]...)
	default:
		return nil, nil, fmt.Errorf("application: unsupported route change action %q", intent.Action)
	}
	if err := data.Validate(); err != nil {
		return nil, nil, err
	}
	return changed, previous, nil
}

func normalizeRoute(route model.WebRoute) model.WebRoute {
	route.Domain = strings.TrimSpace(strings.ToLower(route.Domain))
	route.TLSSNI = strings.TrimSpace(route.TLSSNI)
	if route.UpstreamScheme == "" {
		route.UpstreamScheme = "http"
	}
	if route.CaddyEnabled {
		route.HostsEnabled = true
	} else if !route.HostsEnabled {
		route.CaddyEnabled = false
	}
	return route
}

func nextRouteID(routes []model.WebRoute) int {
	maxID := 0
	for _, route := range routes {
		if route.ID > maxID {
			maxID = route.ID
		}
	}
	return maxID + 1
}

func routeIndex(routes []model.WebRoute, id int) int {
	for i := range routes {
		if routes[i].ID == id {
			return i
		}
	}
	return -1
}

func containsAllDomains(confirmed, required []string) bool {
	for _, requiredDomain := range required {
		found := false
		for _, confirmedDomain := range confirmed {
			if strings.EqualFold(strings.TrimSpace(confirmedDomain), requiredDomain) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func runningAffectedIDs(items []biz.AffectedForward) []int {
	ids := make([]int, 0, len(items))
	for _, item := range items {
		if item.RunningGeneration != 0 {
			ids = append(ids, item.ForwardID)
		}
	}
	sort.Ints(ids)
	return ids
}

func initialHostChangeForwardResults(items []biz.AffectedForward) []SSHHostChangeForwardResult {
	result := make([]SSHHostChangeForwardResult, 0, len(items))
	for _, item := range cloneAffectedForwards(items) {
		status := "not_running"
		if item.RunningGeneration != 0 {
			status = "pending"
		}
		result = append(result, SSHHostChangeForwardResult{ForwardID: item.ForwardID, PreviousGeneration: item.RunningGeneration, Status: status})
	}
	return result
}

func applyResumeResult(results []SSHHostChangeForwardResult, resume biz.RuntimeResumeResult, successStatus, failureStatus string, compensation bool) {
	started := make(map[int]struct{}, len(resume.Started))
	for _, id := range resume.Started {
		started[id] = struct{}{}
	}
	for i := range results {
		if results[i].PreviousGeneration == 0 {
			continue
		}
		if message, failed := resume.Errors[results[i].ForwardID]; failed {
			results[i].Status = failureStatus
			if compensation {
				results[i].CompensationError = message
			} else {
				results[i].Error = message
			}
			continue
		}
		if _, ok := started[results[i].ForwardID]; ok {
			results[i].Status = successStatus
		}
	}
}

func compensateHostChange(runtime RuntimePort, plan biz.RuntimeSuspendPlan, results []SSHHostChangeForwardResult) {
	if len(plan.Entries) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	resume := runtime.Resume(ctx, plan)
	cancel()
	applyResumeResult(results, resume, "compensated", "compensation_failed", true)
}

func saveSSHHostResultFromCommit(committed CommitSSHHostChangeResult) SaveSSHHostResult {
	result := SaveSSHHostResult{Host: committed.Host, ConnectionChanged: true, AcceptedRevision: committed.AcceptedRevision, EventSequence: committed.EventSequence, RestartErrors: map[int]string{}}
	for _, item := range committed.ForwardResults {
		result.AffectedForwardIDs = append(result.AffectedForwardIDs, item.ForwardID)
		if item.PreviousGeneration != 0 {
			result.RunningForwardIDs = append(result.RunningForwardIDs, item.ForwardID)
		}
		if item.Error != "" {
			result.RestartErrors[item.ForwardID] = item.Error
		}
		if item.CompensationError != "" {
			result.RestartErrors[item.ForwardID] = item.CompensationError
		}
	}
	if len(result.RestartErrors) == 0 {
		result.RestartErrors = nil
	}
	return result
}

func sanitizeSSHHostChangeResult(result *CommitSSHHostChangeResult, secret string) {
	if result == nil || secret == "" {
		return
	}
	redact := func(message string) string { return strings.ReplaceAll(message, secret, "[REDACTED]") }
	result.OperationError = redact(result.OperationError)
	for id, message := range result.PreflightErrors {
		result.PreflightErrors[id] = redact(message)
	}
	for i := range result.ForwardResults {
		result.ForwardResults[i].Error = redact(result.ForwardResults[i].Error)
		result.ForwardResults[i].CompensationError = redact(result.ForwardResults[i].CompensationError)
	}
}

func cloneWebRoute(route *model.WebRoute) *model.WebRoute {
	if route == nil {
		return nil
	}
	copy := *route
	return &copy
}

func linkedRouteFlagChanges(before, after *model.WebRoute) []RouteFlagChange {
	if after == nil {
		return nil
	}
	var changes []RouteFlagChange
	if before == nil || before.HostsEnabled != after.HostsEnabled {
		changes = append(changes, RouteFlagChange{Flag: RouteFlagHostsEnabled, Enabled: after.HostsEnabled})
	}
	if before == nil || before.CaddyEnabled != after.CaddyEnabled {
		changes = append(changes, RouteFlagChange{Flag: RouteFlagCaddyEnabled, Enabled: after.CaddyEnabled})
	}
	return changes
}

func newRouteChangeToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("application: create route preview token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func (s *Service) MoveForwards(ctx context.Context, command MoveForwardsCommand) (MoveForwardsResult, error) {
	if err := ctx.Err(); err != nil {
		return MoveForwardsResult{}, err
	}
	if s.maintenance.Load() {
		return MoveForwardsResult{}, ErrMaintenance
	}
	s.mutation.Lock()
	defer s.mutation.Unlock()
	if s.maintenance.Load() {
		return MoveForwardsResult{}, ErrMaintenance
	}
	cached, digest, ok, err := lookupCommandResult[MoveForwardsResult](s.commands, command.Meta.CommandID, "move_forwards", command)
	if err != nil {
		return MoveForwardsResult{}, err
	}
	if ok {
		return cached, nil
	}
	data, err := s.store.Load()
	if err != nil {
		return MoveForwardsResult{}, err
	}
	current := revisionOfCatalog(data)
	if command.Meta.ExpectedRevision != "" && command.Meta.ExpectedRevision != current {
		return MoveForwardsResult{}, fmt.Errorf("%w: current=%s", ErrRevisionConflict, current)
	}
	report, err := s.catalog.MoveForwards(command.ForwardIDs, command.TargetFolderID)
	if err != nil {
		return MoveForwardsResult{}, err
	}
	if len(report.ChangedIDs) == 0 {
		result := MoveForwardsResult{ChangedIDs: report.ChangedIDs, UnchangedIDs: report.UnchangedIDs, AcceptedRevision: current, EventSequence: s.sequence.Load()}
		if err := storeCommandResult(s.commands, command.Meta.CommandID, "move_forwards", digest, result); err != nil {
			return MoveForwardsResult{}, err
		}
		return result, nil
	}
	data, err = s.store.Load()
	if err != nil {
		return MoveForwardsResult{}, err
	}
	result := MoveForwardsResult{ChangedIDs: report.ChangedIDs, UnchangedIDs: report.UnchangedIDs, AcceptedRevision: revisionOfCatalog(data), EventSequence: s.sequence.Add(1)}
	if err := storeCommandResult(s.commands, command.Meta.CommandID, "move_forwards", digest, result); err != nil {
		return MoveForwardsResult{}, err
	}
	return result, nil
}

func (s *Service) PreviewLocalListener(ctx context.Context, command PreviewLocalListenerCommand) LocalListenerPreview {
	if err := ctx.Err(); err != nil {
		return LocalListenerPreview{State: "unknown", ErrorCode: "cancelled"}
	}
	if strings.TrimSpace(command.Mode) == "remote" {
		return LocalListenerPreview{State: "not_applicable"}
	}
	if owner, ok := s.runtime.LocalListenerOwner(command.Host, command.Port); ok {
		state := "occupied"
		if command.ForwardID != 0 && owner == command.ForwardID {
			state = "owned_by_self"
		}
		return LocalListenerPreview{State: state, OwnerForwardID: owner}
	}
	probe := forward.PreviewLocalListener(command.Host, command.Port)
	result := LocalListenerPreview{NormalizedAddress: probe.NormalizedAddress}
	switch probe.State {
	case forward.LocalListenerAvailable:
		result.State = "available"
	case forward.LocalListenerOccupied:
		result.State = "occupied"
	case forward.LocalListenerInvalid:
		result.State, result.ErrorCode = "unknown", "invalid_listener"
	default:
		result.State, result.ErrorCode = "unknown", "listener_check_failed"
	}
	return result
}

func (s *Service) StageRestore(ctx context.Context, request biz.RestoreStageRequest) (biz.RestorePreview, error) {
	return s.restore.StageRestore(ctx, request)
}

func (s *Service) StageImport(ctx context.Context, request StageImportRequest) (ImportStagePreview, error) {
	if s.backup == nil || s.packages == nil {
		return ImportStagePreview{}, errors.New("application: import module is unavailable")
	}
	data, err := s.store.Load()
	if err != nil {
		return ImportStagePreview{}, err
	}
	revision := revisionOfCatalog(data)
	packagePreview, err := s.packages.Stage(ctx, biz.StageRequest{Path: request.Path, Password: request.Password, Purpose: biz.StagePurposeImport, VaultRevision: revision})
	if err != nil {
		return ImportStagePreview{}, err
	}
	staged, err := s.packages.Take(ctx, biz.TakeStageRequest{Token: packagePreview.Token, Purpose: biz.StagePurposeImport, VaultRevision: revision})
	if err != nil {
		return ImportStagePreview{}, err
	}
	preview, err := s.backup.PreviewStagedImport(staged)
	if err != nil {
		staged.Destroy()
		return ImportStagePreview{}, err
	}
	keyPaths, keyViews, err := makeKeyFileLease(preview.KeyFiles, staged.KeyFiles)
	if err != nil {
		staged.Destroy()
		return ImportStagePreview{}, err
	}
	publicPreview := ImportPreviewView{
		Counts: preview.Counts, FolderName: preview.FolderName,
		HostConflicts: preview.HostConflicts, KeyFiles: keyViews,
	}
	s.importMu.Lock()
	if s.importStage != nil {
		s.importStage.backup.Destroy()
	}
	s.importStage = &stagedImport{
		token: packagePreview.Token, expiresAt: packagePreview.ExpiresAt,
		vaultRevision: revision, backup: staged, keyPaths: keyPaths,
		keyViews: keyViews, exporting: map[string]bool{},
	}
	s.importMu.Unlock()
	return ImportStagePreview{Token: packagePreview.Token, ExpiresAt: packagePreview.ExpiresAt, Preview: publicPreview}, nil
}

func (s *Service) CommitImport(ctx context.Context, command CommitImportCommand) (CommitImportResult, error) {
	if s.maintenance.Load() {
		return CommitImportResult{}, ErrMaintenance
	}
	s.mutation.Lock()
	defer s.mutation.Unlock()
	cached, digest, ok, err := lookupCommandResult[CommitImportResult](s.commands, command.Meta.CommandID, "commit_import", command)
	if err != nil {
		return CommitImportResult{}, err
	}
	if ok {
		return cached, nil
	}
	s.importMu.Lock()
	stage := s.importStage
	if stage == nil || command.Token == "" || command.Token != stage.token || time.Now().After(stage.expiresAt) || stage.committed || stage.committing {
		s.importMu.Unlock()
		return CommitImportResult{}, biz.ErrBackupStageToken
	}
	stage.committing = true
	s.importMu.Unlock()
	commitFailed := true
	defer func() {
		if !commitFailed {
			return
		}
		s.importMu.Lock()
		if s.importStage == stage {
			stage.committing = false
		}
		s.importMu.Unlock()
	}()
	data, err := s.store.Load()
	if err != nil {
		return CommitImportResult{}, err
	}
	current := revisionOfCatalog(data)
	if current != stage.vaultRevision || (command.Meta.ExpectedRevision != "" && command.Meta.ExpectedRevision != current) {
		return CommitImportResult{}, fmt.Errorf("%w: current=%s", ErrRevisionConflict, current)
	}
	summary, data, err := s.backup.ApplyStagedImportWithData(stage.backup, command.Plan)
	if err != nil {
		return CommitImportResult{}, err
	}
	// Vault 数据只允许消费一次；私钥字节继续由短期 key-export lease 持有，
	// 不返回 WebView，也不要求再次提供备份密码。
	stage.backup.Vault = model.VaultData{}
	summary.KeyFiles = nil
	summary.KeyFilePaths = nil
	result := CommitImportResult{Summary: summary, KeyFiles: append([]KeyFileView(nil), stage.keyViews...), AcceptedRevision: revisionOfCatalog(data), EventSequence: s.sequence.Add(1)}
	if err := storeCommandResult(s.commands, command.Meta.CommandID, "commit_import", digest, result); err != nil {
		return CommitImportResult{}, err
	}
	s.importMu.Lock()
	stage.committing = false
	stage.committed = true
	if len(stage.keyPaths) == 0 {
		stage.backup.Destroy()
		s.importStage = nil
	}
	s.importMu.Unlock()
	commitFailed = false
	return result, nil
}

// SaveImportKeyFile 使用 StageImport 建立的短期 lease 把单个私钥直接写到用户选择的路径。
// 私钥字节不会进入 Wails DTO；每个 keyID 成功保存一次后立即从内存清除。
func (s *Service) SaveImportKeyFile(ctx context.Context, token, keyID, destination string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.importMu.Lock()
	stage := s.importStage
	if stage == nil || !stage.committed || token == "" || token != stage.token || time.Now().After(stage.expiresAt) {
		s.importMu.Unlock()
		return biz.ErrBackupStageToken
	}
	path, ok := stage.keyPaths[keyID]
	if !ok || stage.exporting[keyID] {
		s.importMu.Unlock()
		return errors.New("application: import key lease is invalid or already in use")
	}
	content, ok := stage.backup.KeyFiles[path]
	if !ok {
		s.importMu.Unlock()
		return errors.New("application: staged import key is unavailable")
	}
	copyOfContent := append([]byte(nil), content...)
	stage.exporting[keyID] = true
	s.importMu.Unlock()

	err := writePrivateKeyAtomic(ctx, destination, copyOfContent)
	for i := range copyOfContent {
		copyOfContent[i] = 0
	}

	s.importMu.Lock()
	defer s.importMu.Unlock()
	if s.importStage != stage {
		return biz.ErrBackupStageToken
	}
	delete(stage.exporting, keyID)
	if err != nil {
		return err
	}
	for i := range stage.backup.KeyFiles[path] {
		stage.backup.KeyFiles[path][i] = 0
	}
	delete(stage.backup.KeyFiles, path)
	delete(stage.keyPaths, keyID)
	for index := range stage.keyViews {
		if stage.keyViews[index].ID == keyID {
			stage.keyViews = append(stage.keyViews[:index], stage.keyViews[index+1:]...)
			break
		}
	}
	if len(stage.keyPaths) == 0 {
		stage.backup.Destroy()
		s.importStage = nil
	}
	return nil
}

func makeKeyFileLease(paths []string, keyFiles map[string][]byte) (map[string]string, []KeyFileView, error) {
	sorted := append([]string(nil), paths...)
	sort.Strings(sorted)
	byID := make(map[string]string, len(sorted))
	views := make([]KeyFileView, 0, len(sorted))
	for _, path := range sorted {
		content, ok := keyFiles[path]
		if !ok {
			return nil, nil, fmt.Errorf("application: staged key %q is unavailable", filepath.Base(path))
		}
		random := make([]byte, 18)
		if _, err := rand.Read(random); err != nil {
			return nil, nil, fmt.Errorf("application: create key lease id: %w", err)
		}
		id := base64.RawURLEncoding.EncodeToString(random)
		for i := range random {
			random[i] = 0
		}
		byID[id] = path
		views = append(views, KeyFileView{ID: id, Name: filepath.Base(path), Size: len(content)})
	}
	return byID, views, nil
}

func (s *Service) CommitRestore(ctx context.Context, request biz.RestoreCommitRequest) (biz.RestoreCommitResult, error) {
	if !s.maintenance.CompareAndSwap(false, true) {
		return biz.RestoreCommitResult{}, ErrMaintenance
	}
	defer s.maintenance.Store(false)
	s.mutation.Lock()
	defer s.mutation.Unlock()
	result, err := s.restore.CommitRestore(ctx, request)
	if err == nil || result.JournalPending {
		s.sequence.Add(1)
	}
	return result, err
}

// ActivateRestoredNetwork 是解除恢复隔离的唯一入口；任一步失败都重新收敛到中性态。
func (s *Service) ActivateRestoredNetwork(ctx context.Context) error {
	if s.maintenance.Load() {
		return ErrMaintenance
	}
	s.mutation.Lock()
	defer s.mutation.Unlock()
	quarantined, _, err := s.recovery.State()
	if err != nil || !quarantined {
		return err
	}
	if _, err := s.routes.ReconcileRoutes(); err != nil {
		_ = s.routes.NeutralizeRoutes(ctx)
		return err
	}
	startErrors, err := s.runtime.StartAutoStart()
	if err != nil || len(startErrors) != 0 {
		stopCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_, _ = s.runtime.SuspendAll(stopCtx)
		cancel()
		_ = s.routes.NeutralizeRoutes(ctx)
		if err != nil {
			return err
		}
		return fmt.Errorf("application: restored forwards failed to start: %v", startErrors)
	}
	if err := s.recovery.ClearQuarantine(); err != nil {
		_, _ = s.runtime.SuspendAll(ctx)
		_ = s.routes.NeutralizeRoutes(ctx)
		return err
	}
	s.sequence.Add(1)
	return nil
}

func catalogView(data model.VaultData) CatalogView {
	hosts := make([]SSHHostView, 0, len(data.SSHHosts))
	for _, host := range data.SSHHosts {
		hosts = append(hosts, sshHostView(host))
	}
	return CatalogView{Folders: data.Folders, SSHHosts: hosts, Forwards: data.Forwards, WebRoutes: data.WebRoutes, HostKeys: data.HostKeys}
}

func sshHostView(host model.SSHHost) SSHHostView {
	return SSHHostView{ID: host.ID, Name: host.Name, Host: host.Host, Port: host.Port, User: host.User, AuthType: host.AuthType,
		KeyPath: host.KeyPath, AgentSocketPath: host.AgentSocketPath, KeepAliveIntervalMs: host.KeepAliveIntervalMs,
		TimeoutMs: host.TimeoutMs, HostKeyAlgorithms: host.HostKeyAlgorithms, Notes: host.Notes, HasSecret: host.Password != ""}
}

func (input SSHHostInput) model() model.SSHHost {
	return model.SSHHost{ID: input.ID, Name: input.Name, Host: input.Host, Port: input.Port, User: input.User, AuthType: input.AuthType,
		KeyPath: input.KeyPath, AgentSocketPath: input.AgentSocketPath, KeepAliveIntervalMs: input.KeepAliveIntervalMs,
		TimeoutMs: input.TimeoutMs, HostKeyAlgorithms: input.HostKeyAlgorithms, Notes: input.Notes}
}

func revisionOfCatalog(data model.VaultData) string {
	public := struct {
		Version             int             `json:"version"`
		Catalog             CatalogView     `json:"catalog"`
		CredentialRevisions map[int]uint64  `json:"credentialRevisions"`
		Prefs               PreferencesView `json:"prefs"`
	}{Version: data.Version, Catalog: catalogView(data), CredentialRevisions: map[int]uint64{}, Prefs: PreferencesView{AutoRun: data.Prefs.AutoRun, UpdateCheckEnabled: data.Prefs.UpdateCheckEnabled, UILocale: data.Prefs.UILocale}}
	for _, host := range data.SSHHosts {
		public.CredentialRevisions[host.ID] = host.CredentialRevision
	}
	return revisionOf(public)
}

func revisionOf(value any) string {
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
