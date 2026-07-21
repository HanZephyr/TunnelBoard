package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/HanZephyr/TunnelBoard/internal/biz"
	"github.com/HanZephyr/TunnelBoard/internal/model"
	"github.com/HanZephyr/TunnelBoard/internal/vault"
)

const (
	restoreStateFile   = "restore-state.json"
	restoreJournalFile = "restore-journal.json"
)

type restoreState struct {
	Quarantined bool `json:"quarantined"`
}

type RecoveryStore struct {
	dir string
	mu  sync.Mutex
}

func NewRecoveryStore(dataDir string) *RecoveryStore {
	return &RecoveryStore{dir: filepath.Join(dataDir, "state")}
}

func (s *RecoveryStore) State() (bool, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var state restoreState
	err := readApplicationJSON(filepath.Join(s.dir, restoreStateFile), &state)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, false, err
	}
	_, journalErr := os.Stat(filepath.Join(s.dir, restoreJournalFile))
	pending := journalErr == nil
	if journalErr != nil && !errors.Is(journalErr, os.ErrNotExist) {
		return false, false, journalErr
	}
	return state.Quarantined, pending, nil
}

func (s *RecoveryStore) setQuarantine(value bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return writeApplicationJSON(filepath.Join(s.dir, restoreStateFile), restoreState{Quarantined: value})
}

func (s *RecoveryStore) ClearQuarantine() error { return s.setQuarantine(false) }

type RestoreEffectsAdapter struct {
	store    *vault.Store
	runtime  RuntimePort
	routes   RoutePort
	recovery *RecoveryStore

	mu         sync.Mutex
	activeTx   string
	plans      map[string]biz.RuntimeSuspendPlan
	candidates map[string]vault.RestoreCandidate
}

func NewRestoreEffects(store *vault.Store, runtime RuntimePort, routes RoutePort, recovery *RecoveryStore) *RestoreEffectsAdapter {
	return &RestoreEffectsAdapter{store: store, runtime: runtime, routes: routes, recovery: recovery, plans: map[string]biz.RuntimeSuspendPlan{}, candidates: map[string]vault.RestoreCandidate{}}
}

func (e *RestoreEffectsAdapter) Snapshot(context.Context) (biz.RestoreFacts, error) {
	data, err := e.store.Load()
	if err != nil {
		return biz.RestoreFacts{}, err
	}
	runtimeState, err := e.runtime.Snapshot()
	if err != nil {
		return biz.RestoreFacts{}, err
	}
	routeState, err := e.routes.AppliedState()
	if err != nil {
		return biz.RestoreFacts{}, err
	}
	return biz.RestoreFacts{VaultRevision: revisionOfCatalog(data), RuntimeRevision: revisionOf(runtimeState), RouteRevision: revisionOf(routeState)}, nil
}

func (e *RestoreEffectsAdapter) VaultStorageRevision(context.Context) (string, error) {
	return e.store.StorageRevision()
}

func (e *RestoreEffectsAdapter) CaptureRunningForwards(context.Context) ([]int, error) {
	runtimeState, err := e.runtime.Snapshot()
	if err != nil {
		return nil, err
	}
	ids := make([]int, 0, len(runtimeState))
	for _, state := range runtimeState {
		if state.Status == biz.RuntimeStateRunning || state.Status == biz.RuntimeStateReconnecting {
			ids = append(ids, state.ForwardID)
		}
	}
	sort.Ints(ids)
	return ids, nil
}

func (e *RestoreEffectsAdapter) PrepareCandidate(_ context.Context, data model.VaultData) (biz.RestoreVaultCandidate, error) {
	candidate, err := e.store.PrepareRestoreCandidate(data)
	if err != nil {
		return biz.RestoreVaultCandidate{}, err
	}
	e.mu.Lock()
	e.candidates[candidate.ID] = candidate
	e.mu.Unlock()
	return biz.RestoreVaultCandidate{ID: candidate.ID}, nil
}

func (e *RestoreEffectsAdapter) WriteJournal(_ context.Context, journal biz.RestoreJournal) error {
	if err := writeApplicationJSON(filepath.Join(e.recovery.dir, restoreJournalFile), journal); err != nil {
		return err
	}
	e.mu.Lock()
	e.activeTx = journal.TransactionID
	e.mu.Unlock()
	return nil
}

func (e *RestoreEffectsAdapter) SuspendAll(ctx context.Context) (biz.RestoreSuspendPlan, error) {
	plan, err := e.runtime.SuspendAll(ctx)
	e.mu.Lock()
	tx := e.activeTx
	if tx != "" {
		e.plans[tx] = plan
	}
	e.mu.Unlock()
	ids := make([]int, 0, len(plan.Entries))
	for _, entry := range plan.Entries {
		ids = append(ids, entry.ForwardID)
	}
	return biz.RestoreSuspendPlan{RunningForwardIDs: ids}, err
}

func (e *RestoreEffectsAdapter) NeutralizeRoutes(ctx context.Context) error {
	return e.routes.NeutralizeRoutes(ctx)
}

func (e *RestoreEffectsAdapter) ReplaceVault(_ context.Context, candidate biz.RestoreVaultCandidate) error {
	e.mu.Lock()
	stored, ok := e.candidates[candidate.ID]
	e.mu.Unlock()
	if !ok {
		return errors.New("application: unknown restore candidate")
	}
	return e.store.CommitRestoreCandidate(stored)
}

func (e *RestoreEffectsAdapter) EnterQuarantine(context.Context) error {
	return e.recovery.setQuarantine(true)
}

func (e *RestoreEffectsAdapter) CommitJournal(_ context.Context, transactionID string) error {
	e.mu.Lock()
	delete(e.plans, transactionID)
	e.activeTx = ""
	e.mu.Unlock()
	err := os.Remove(filepath.Join(e.recovery.dir, restoreJournalFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (e *RestoreEffectsAdapter) Compensate(ctx context.Context, state biz.RestoreCompensation) error {
	var failures []error
	if state.RoutesNeutralized {
		if _, err := e.routes.ReconcileRoutes(); err != nil {
			failures = append(failures, fmt.Errorf("restore route effects: %w", err))
		}
	}
	e.mu.Lock()
	plan := e.plans[state.TransactionID]
	candidate, hasCandidate := e.candidates[state.Candidate.ID]
	delete(e.plans, state.TransactionID)
	delete(e.candidates, state.Candidate.ID)
	e.activeTx = ""
	e.mu.Unlock()
	if len(plan.Entries) != 0 {
		resume := e.runtime.Resume(ctx, plan)
		if len(resume.Errors) != 0 {
			failures = append(failures, fmt.Errorf("resume runtime: %v", resume.Errors))
		}
	}
	if hasCandidate {
		if err := e.store.DiscardRestoreCandidate(candidate); err != nil {
			failures = append(failures, err)
		}
	}
	if len(failures) != 0 {
		// The durable journal is the only restart-safe record of the original
		// Runtime set and Route state. Never consume it until compensation has
		// completely converged.
		return errors.Join(failures...)
	}
	if err := os.Remove(filepath.Join(e.recovery.dir, restoreJournalFile)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// RecoverPending 在启动网络功能前恢复未完成的 Restore 事务。
// Vault 尚未替换时恢复旧 Route 和原运行 Forward；Vault 已替换时则只能
// 收敛到新 Vault 的隔离态，绝不复活旧网络副作用。只有全部恢复动作成功后才消费 journal。
func (e *RestoreEffectsAdapter) RecoverPending(ctx context.Context) error {
	journalPath := filepath.Join(e.recovery.dir, restoreJournalFile)
	var journal biz.RestoreJournal
	if err := readApplicationJSON(journalPath, &journal); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return e.convergePersistedQuarantine(ctx)
		}
		return err
	}
	if journal.TransactionID == "" || !knownRestorePhase(journal.Phase) {
		return errors.New("application: invalid pending restore journal")
	}

	data, err := e.store.Load()
	if err != nil {
		return fmt.Errorf("application: inspect pending restore vault: %w", err)
	}
	currentRevision := revisionOfCatalog(data)
	currentStorageRevision, err := e.store.StorageRevision()
	if err != nil {
		return fmt.Errorf("application: inspect pending restore storage revision: %w", err)
	}
	vaultReplaced := journal.VaultReplaced || restorePhaseAtLeast(journal.Phase, biz.RestorePhaseVaultReplaced)
	if journal.Phase == biz.RestorePhaseReplacingVault {
		// replacing_vault is deliberately persisted before the atomic rename. Comparing the
		// live revision resolves which side of that rename the process reached before crashing.
		if journal.BeforeVaultStorageRevision != "" {
			vaultReplaced = currentStorageRevision != journal.BeforeVaultStorageRevision
		} else {
			// Legacy journals did not persist an encrypted storage digest. Fail safely when
			// their public revision cannot prove that the old Vault is still active.
			vaultReplaced = journal.Before.VaultRevision == "" || currentRevision != journal.Before.VaultRevision
		}
	}

	if vaultReplaced {
		if err := e.routes.NeutralizeRoutes(ctx); err != nil {
			return fmt.Errorf("application: neutralize replaced restore routes: %w", err)
		}
		if err := e.recovery.setQuarantine(true); err != nil {
			return fmt.Errorf("application: persist restore quarantine: %w", err)
		}
		if err := e.discardJournalCandidate(journal.CandidateID); err != nil {
			return err
		}
		return e.CommitJournal(ctx, journal.TransactionID)
	}

	if currentRevision != journal.Before.VaultRevision {
		return errors.New("application: pending restore vault revision is ambiguous")
	}
	if restorePhaseAtLeast(journal.Phase, biz.RestorePhaseRuntimeSuspending) {
		if _, err := e.routes.ReconcileRoutes(); err != nil {
			return fmt.Errorf("application: restore pre-restore routes: %w", err)
		}
		if err := e.resumeJournalForwards(ctx, &journal); err != nil {
			return err
		}
	}
	if err := e.discardJournalCandidate(journal.CandidateID); err != nil {
		return err
	}
	if err := e.recovery.setQuarantine(false); err != nil {
		return fmt.Errorf("application: clear stale restore quarantine: %w", err)
	}
	return e.CommitJournal(ctx, journal.TransactionID)
}

// convergePersistedQuarantine treats restore-state.json as a durable write-ahead
// intent. A completed restore already leaves Route state quarantined, so ordinary
// restarts do not repeat privileged cleanup. If activation crashed after starting a
// Route transaction or publishing applied state, startup idempotently neutralizes it.
func (e *RestoreEffectsAdapter) convergePersistedQuarantine(ctx context.Context) error {
	quarantined, _, err := e.recovery.State()
	if err != nil || !quarantined {
		return err
	}
	pending, err := e.routes.RecoveryPending()
	if err != nil {
		return fmt.Errorf("application: inspect quarantined route recovery: %w", err)
	}
	applied, err := e.routes.AppliedState()
	if err != nil {
		return fmt.Errorf("application: inspect quarantined route state: %w", err)
	}
	if !pending && applied.Status == biz.RouteStatusQuarantined {
		return nil
	}
	if err := e.routes.NeutralizeRoutes(ctx); err != nil {
		return fmt.Errorf("application: restore quarantined network state: %w", err)
	}
	return nil
}

func (e *RestoreEffectsAdapter) resumeJournalForwards(ctx context.Context, journal *biz.RestoreJournal) error {
	runtimeState, err := e.runtime.Snapshot()
	if err != nil {
		return fmt.Errorf("application: inspect runtime during restore recovery: %w", err)
	}
	running := make(map[int]struct{}, len(runtimeState))
	for _, state := range runtimeState {
		if state.Status == biz.RuntimeStateRunning || state.Status == biz.RuntimeStateReconnecting {
			running[state.ForwardID] = struct{}{}
		}
	}
	remaining := append([]int(nil), journal.RunningForwardIDs...)
	for len(remaining) != 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		id := remaining[0]
		if _, alreadyRunning := running[id]; !alreadyRunning {
			if err := e.runtime.Start(id); err != nil {
				return fmt.Errorf("application: resume forward %d after restore interruption: %w", id, err)
			}
		}
		remaining = remaining[1:]
		journal.RunningForwardIDs = append([]int(nil), remaining...)
		if err := e.WriteJournal(ctx, *journal); err != nil {
			return fmt.Errorf("application: checkpoint restored forward %d: %w", id, err)
		}
	}
	return nil
}

func (e *RestoreEffectsAdapter) discardJournalCandidate(candidateID string) error {
	if candidateID == "" {
		return nil
	}
	if err := e.store.DiscardRestoreCandidate(vault.RestoreCandidate{ID: candidateID}); err != nil {
		return fmt.Errorf("application: discard restore candidate: %w", err)
	}
	return nil
}

func knownRestorePhase(phase string) bool {
	_, ok := restorePhaseOrder[phase]
	return ok
}

func restorePhaseAtLeast(actual, expected string) bool {
	return restorePhaseOrder[actual] >= restorePhaseOrder[expected]
}

var restorePhaseOrder = map[string]int{
	biz.RestorePhasePlanned:           1,
	biz.RestorePhaseCandidatePrepared: 2,
	biz.RestorePhaseRuntimeSuspending: 3,
	biz.RestorePhaseRuntimeSuspended:  4,
	biz.RestorePhaseRoutesNeutralized: 5,
	biz.RestorePhaseReplacingVault:    6,
	biz.RestorePhaseVaultReplaced:     7,
	biz.RestorePhaseQuarantined:       8,
}

func readApplicationJSON(path string, target any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return fmt.Errorf("application: decode %s: %w", filepath.Base(path), err)
	}
	return nil
}

func writeApplicationJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".application-state-*.tmp")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err := f.Chmod(0o600); err == nil {
		_, err = f.Write(raw)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(name, path)
}
