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
	maintenance  atomic.Bool
	sequence     atomic.Uint64
	commands     *recentCommandCache
}

func NewService(deps Dependencies) *Service {
	return &Service{store: deps.Store, catalog: deps.Catalog, runtime: deps.Runtime, routes: deps.Routes, restore: deps.Restore, recovery: deps.Recovery, backup: deps.Backup, packages: deps.Packages, hostChanges: make(map[string]stagedSSHHostChange), commands: newRecentCommandCache(deps.CommandCache)}
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
	cached, digest, ok, err := lookupCommandResult[CommitSSHHostChangeResult](s.commands, command.Meta.CommandID, "commit_ssh_host_change", command)
	if err != nil {
		return CommitSSHHostChangeResult{}, err
	}
	if ok {
		return cached, nil
	}
	result, err := s.commitSSHHostChangeOnce(ctx, command.Token)
	if err == nil {
		if cacheErr := storeCommandResult(s.commands, command.Meta.CommandID, "commit_ssh_host_change", digest, result); cacheErr != nil {
			return CommitSSHHostChangeResult{}, cacheErr
		}
	}
	return result, err
}

func (s *Service) commitSSHHostChangeOnce(ctx context.Context, token string) (CommitSSHHostChangeResult, error) {
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
	if s.maintenance.Load() {
		return CommitSSHHostChangeResult{}, ErrMaintenance
	}
	s.mutation.Lock()
	defer s.mutation.Unlock()
	if s.maintenance.Load() {
		return CommitSSHHostChangeResult{}, ErrMaintenance
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
	summary, err := s.backup.ApplyStagedImport(stage.backup, command.Plan)
	if err != nil {
		return CommitImportResult{}, err
	}
	data, err = s.store.Load()
	if err != nil {
		return CommitImportResult{}, err
	}
	// Vault 数据只允许消费一次；私钥字节继续由短期 key-export lease 持有，
	// 不返回 WebView，也不要求再次提供备份密码。
	stage.backup.Vault = model.VaultData{}
	summary.KeyFiles = nil
	summary.KeyFilePaths = nil
	s.importMu.Lock()
	stage.committing = false
	stage.committed = true
	keyViews := append([]KeyFileView(nil), stage.keyViews...)
	if len(stage.keyPaths) == 0 {
		stage.backup.Destroy()
		s.importStage = nil
	}
	s.importMu.Unlock()
	commitFailed = false
	return CommitImportResult{Summary: summary, KeyFiles: keyViews, AcceptedRevision: revisionOfCatalog(data), EventSequence: s.sequence.Add(1)}, nil
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
