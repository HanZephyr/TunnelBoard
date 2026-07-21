package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	if err := os.Remove(filepath.Join(e.recovery.dir, restoreJournalFile)); err != nil && !errors.Is(err, os.ErrNotExist) {
		failures = append(failures, err)
	}
	return errors.Join(failures...)
}

// RecoverPending 在启动时保守处理未知中断点：保持/进入隔离并再次收敛网络副作用。
func (e *RestoreEffectsAdapter) RecoverPending(ctx context.Context) error {
	_, pending, err := e.recovery.State()
	if err != nil || !pending {
		return err
	}
	if err := e.routes.NeutralizeRoutes(ctx); err != nil {
		return err
	}
	return e.recovery.setQuarantine(true)
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
