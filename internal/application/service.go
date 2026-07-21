package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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
	ErrMaintenance      = errors.New("application: maintenance in progress")
	ErrRevisionConflict = errors.New("application: revision conflict")
)

type RuntimePort interface {
	Snapshot() ([]biz.RuntimeStatus, error)
	Start(int) error
	Stop(int) error
	StartAutoStart() (map[int]error, error)
	Suspend(context.Context, []int) (biz.RuntimeSuspendPlan, error)
	SuspendAll(context.Context) (biz.RuntimeSuspendPlan, error)
	Resume(context.Context, biz.RuntimeSuspendPlan) biz.RuntimeResumeResult
	AffectedForHost(int) []biz.AffectedForward
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
	Store    biz.VaultStore
	Catalog  *biz.CatalogBiz
	Runtime  RuntimePort
	Routes   RoutePort
	Restore  RestorePort
	Recovery RecoveryPort
	Backup   *biz.BackupBiz
	Packages biz.BackupPackage
}

type Service struct {
	store       biz.VaultStore
	catalog     *biz.CatalogBiz
	runtime     RuntimePort
	routes      RoutePort
	restore     RestorePort
	recovery    RecoveryPort
	backup      *biz.BackupBiz
	packages    biz.BackupPackage
	mutation    sync.Mutex
	importMu    sync.Mutex
	importStage *stagedImport
	maintenance atomic.Bool
	sequence    atomic.Uint64
}

func NewService(deps Dependencies) *Service {
	return &Service{store: deps.Store, catalog: deps.Catalog, runtime: deps.Runtime, routes: deps.Routes, restore: deps.Restore, recovery: deps.Recovery, backup: deps.Backup, packages: deps.Packages}
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

func (s *Service) SaveSSHHost(ctx context.Context, command SaveSSHHostCommand) (SaveSSHHostResult, error) {
	if s.maintenance.Load() {
		return SaveSSHHostResult{}, ErrMaintenance
	}
	s.mutation.Lock()
	defer s.mutation.Unlock()
	if s.maintenance.Load() {
		return SaveSSHHostResult{}, ErrMaintenance
	}
	data, err := s.store.Load()
	if err != nil {
		return SaveSSHHostResult{}, err
	}
	currentRevision := revisionOfCatalog(data)
	if command.Meta.ExpectedRevision != "" && command.Meta.ExpectedRevision != currentRevision {
		return SaveSSHHostResult{}, fmt.Errorf("%w: current=%s", ErrRevisionConflict, currentRevision)
	}
	request := biz.SaveSSHHostRequest{Host: command.Host.model(), SecretAction: command.SecretAction, SecretInput: command.SecretInput}
	preview, changed, err := s.catalog.PreviewSSHHostSecure(request)
	if err != nil {
		return SaveSSHHostResult{}, err
	}
	affected := s.runtime.AffectedForHost(preview.ID)
	result := SaveSSHHostResult{Host: sshHostView(preview), ConnectionChanged: changed}
	for _, item := range affected {
		result.AffectedForwardIDs = append(result.AffectedForwardIDs, item.ForwardID)
		if item.RunningGeneration != 0 {
			result.RunningForwardIDs = append(result.RunningForwardIDs, item.ForwardID)
		}
	}
	if changed && len(result.RunningForwardIDs) != 0 && !command.ConfirmRestart {
		result.RequiresRestart = true
		result.AcceptedRevision = currentRevision
		return result, nil
	}

	var plan biz.RuntimeSuspendPlan
	if changed && len(result.RunningForwardIDs) != 0 {
		stopCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		plan, err = s.runtime.Suspend(stopCtx, result.RunningForwardIDs)
		cancel()
		if err != nil {
			_ = s.runtime.Resume(context.Background(), plan)
			return SaveSSHHostResult{}, err
		}
	}
	saved, changedAtCommit, err := s.catalog.SaveSSHHostSecure(request)
	if err != nil {
		if len(plan.Entries) != 0 {
			_ = s.runtime.Resume(context.Background(), plan)
		}
		return SaveSSHHostResult{}, err
	}
	if changedAtCommit {
		s.runtime.RetireHost(saved.ID)
	}
	if len(plan.Entries) != 0 {
		resume := s.runtime.Resume(ctx, plan)
		result.RestartErrors = resume.Errors
	}
	data, err = s.store.Load()
	if err != nil {
		return SaveSSHHostResult{}, err
	}
	result.Host = sshHostView(saved)
	result.ConnectionChanged = changedAtCommit
	result.AcceptedRevision = revisionOfCatalog(data)
	result.EventSequence = s.sequence.Add(1)
	return result, nil
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
	data, err := s.store.Load()
	if err != nil {
		return MoveForwardsResult{}, err
	}
	current := revisionOfCatalog(data)
	if command.Meta.ExpectedRevision != "" && command.Meta.ExpectedRevision != current {
		return MoveForwardsResult{}, fmt.Errorf("%w: current=%s", ErrRevisionConflict, current)
	}
	if err := s.catalog.MoveForwards(command.ForwardIDs, command.TargetFolderID); err != nil {
		return MoveForwardsResult{}, err
	}
	data, err = s.store.Load()
	if err != nil {
		return MoveForwardsResult{}, err
	}
	ids := append([]int(nil), command.ForwardIDs...)
	sort.Ints(ids)
	return MoveForwardsResult{MovedIDs: ids, AcceptedRevision: revisionOfCatalog(data), EventSequence: s.sequence.Add(1)}, nil
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
	s.importMu.Lock()
	if s.importStage != nil {
		s.importStage.backup.Destroy()
	}
	s.importStage = &stagedImport{token: packagePreview.Token, expiresAt: packagePreview.ExpiresAt, vaultRevision: revision, backup: staged}
	s.importMu.Unlock()
	return ImportStagePreview{Token: packagePreview.Token, ExpiresAt: packagePreview.ExpiresAt, Preview: preview}, nil
}

func (s *Service) CommitImport(ctx context.Context, command CommitImportCommand) (CommitImportResult, error) {
	if s.maintenance.Load() {
		return CommitImportResult{}, ErrMaintenance
	}
	s.mutation.Lock()
	defer s.mutation.Unlock()
	s.importMu.Lock()
	stage := s.importStage
	if stage == nil || command.Token == "" || command.Token != stage.token || time.Now().After(stage.expiresAt) {
		s.importMu.Unlock()
		return CommitImportResult{}, biz.ErrBackupStageToken
	}
	s.importStage = nil
	s.importMu.Unlock()
	defer stage.backup.Destroy()
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
	return CommitImportResult{Summary: summary, AcceptedRevision: revisionOfCatalog(data), EventSequence: s.sequence.Add(1)}, nil
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
