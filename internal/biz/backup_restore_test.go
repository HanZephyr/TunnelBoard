package biz_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/HanZephyr/TunnelBoard/internal/biz"
	"github.com/HanZephyr/TunnelBoard/internal/model"
)

type fakeRestoreEffects struct {
	facts              biz.RestoreFacts
	calls              []string
	journalPhases      []string
	prepared           model.VaultData
	failAt             string
	compensated        *biz.RestoreCompensation
	suspendDeadline    time.Duration
	compensateDeadline time.Duration
}

func (f *fakeRestoreEffects) Snapshot(context.Context) (biz.RestoreFacts, error) {
	f.calls = append(f.calls, "snapshot")
	return f.facts, nil
}
func (f *fakeRestoreEffects) VaultStorageRevision(context.Context) (string, error) {
	f.calls = append(f.calls, "storage")
	return "storage-v1", nil
}
func (f *fakeRestoreEffects) CaptureRunningForwards(context.Context) ([]int, error) {
	f.calls = append(f.calls, "capture")
	return []int{1, 2}, nil
}
func (f *fakeRestoreEffects) PrepareCandidate(_ context.Context, data model.VaultData) (biz.RestoreVaultCandidate, error) {
	f.calls = append(f.calls, "prepare")
	f.prepared = data
	if f.failAt == "prepare" {
		return biz.RestoreVaultCandidate{}, errors.New("prepare failed")
	}
	return biz.RestoreVaultCandidate{ID: "candidate-1"}, nil
}
func (f *fakeRestoreEffects) WriteJournal(_ context.Context, journal biz.RestoreJournal) error {
	f.calls = append(f.calls, "journal")
	f.journalPhases = append(f.journalPhases, journal.Phase)
	if f.failAt == "journal" {
		return errors.New("journal failed")
	}
	return nil
}
func (f *fakeRestoreEffects) SuspendAll(ctx context.Context) (biz.RestoreSuspendPlan, error) {
	f.calls = append(f.calls, "suspend")
	if deadline, ok := ctx.Deadline(); ok {
		f.suspendDeadline = time.Until(deadline)
	}
	plan := biz.RestoreSuspendPlan{RunningForwardIDs: []int{1, 2}}
	if f.failAt == "suspend" {
		return plan, errors.New("suspend failed")
	}
	return plan, nil
}
func (f *fakeRestoreEffects) NeutralizeRoutes(context.Context) error {
	f.calls = append(f.calls, "neutralize")
	if f.failAt == "neutralize" {
		return errors.New("neutralize failed")
	}
	return nil
}
func (f *fakeRestoreEffects) ReplaceVault(context.Context, biz.RestoreVaultCandidate) error {
	f.calls = append(f.calls, "replace")
	if f.failAt == "replace" {
		return errors.New("replace failed")
	}
	return nil
}
func (f *fakeRestoreEffects) EnterQuarantine(context.Context) error {
	f.calls = append(f.calls, "quarantine")
	if f.failAt == "quarantine" {
		return errors.New("quarantine failed")
	}
	return nil
}
func (f *fakeRestoreEffects) CommitJournal(context.Context, string) error {
	f.calls = append(f.calls, "complete")
	if f.failAt == "complete" {
		return errors.New("complete failed")
	}
	return nil
}
func (f *fakeRestoreEffects) Compensate(ctx context.Context, c biz.RestoreCompensation) error {
	f.calls = append(f.calls, "compensate")
	if deadline, ok := ctx.Deadline(); ok {
		f.compensateDeadline = time.Until(deadline)
	}
	f.compensated = &c
	return nil
}

func TestCommitRestoreBoundsSuspendAndCompensationIndependently(t *testing.T) {
	effects := &fakeRestoreEffects{
		facts:  biz.RestoreFacts{VaultRevision: "v1", RuntimeRevision: "run1", RouteRevision: "route1"},
		failAt: "suspend",
	}
	coordinator := newRestoreCoordinator(t, effects)
	preview, err := coordinator.StageRestore(context.Background(), biz.RestoreStageRequest{
		Path: writeBackupFile(t, makeBackup(t)), Password: "pw",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.CommitRestore(context.Background(), biz.RestoreCommitRequest{Token: preview.Token, Confirmed: true}); err == nil {
		t.Fatal("suspend failure must be returned")
	}
	for name, budget := range map[string]time.Duration{"suspend": effects.suspendDeadline, "compensate": effects.compensateDeadline} {
		if budget <= 0 || budget > 5*time.Second {
			t.Fatalf("%s budget = %s, want independent deadline within 5s", name, budget)
		}
	}
}

func newRestoreCoordinator(t *testing.T, effects *fakeRestoreEffects) *biz.RestoreCoordinator {
	t.Helper()
	pkg := biz.NewBackupPackage("app-generation-1")
	return biz.NewRestoreCoordinator(pkg, effects)
}

func TestStageRestoreHasNoMutatingEffects(t *testing.T) {
	effects := &fakeRestoreEffects{facts: biz.RestoreFacts{VaultRevision: "v1", RuntimeRevision: "run1", RouteRevision: "route1"}}
	coordinator := newRestoreCoordinator(t, effects)
	preview, err := coordinator.StageRestore(context.Background(), biz.RestoreStageRequest{
		Path: writeBackupFile(t, makeBackup(t)), Password: "pw",
	})
	if err != nil {
		t.Fatalf("StageRestore: %v", err)
	}
	if preview.Token == "" || preview.Counts.Forwards != 2 || preview.Facts != effects.facts {
		t.Fatalf("preview = %+v", preview)
	}
	if !reflect.DeepEqual(effects.calls, []string{"snapshot"}) {
		t.Fatalf("StageRestore side effects = %v", effects.calls)
	}

	effects.calls = nil
	if _, err := coordinator.StageRestore(context.Background(), biz.RestoreStageRequest{
		Path: writeBackupFile(t, makeBackup(t)), Password: "wrong",
	}); err == nil {
		t.Fatal("wrong password must fail")
	}
	if !reflect.DeepEqual(effects.calls, []string{"snapshot"}) {
		t.Fatalf("failed StageRestore side effects = %v", effects.calls)
	}
}

func TestCommitRestoreRequiresConfirmationAndRejectsStalePreviewBeforeEffects(t *testing.T) {
	effects := &fakeRestoreEffects{facts: biz.RestoreFacts{VaultRevision: "v1", RuntimeRevision: "run1", RouteRevision: "route1"}}
	coordinator := newRestoreCoordinator(t, effects)
	preview, err := coordinator.StageRestore(context.Background(), biz.RestoreStageRequest{
		Path: writeBackupFile(t, makeBackup(t)), Password: "pw",
	})
	if err != nil {
		t.Fatal(err)
	}
	effects.calls = nil
	if _, err := coordinator.CommitRestore(context.Background(), biz.RestoreCommitRequest{Token: preview.Token}); !errors.Is(err, biz.ErrRestoreNotConfirmed) {
		t.Fatalf("unconfirmed error = %v", err)
	}
	if len(effects.calls) != 0 {
		t.Fatalf("unconfirmed calls = %v", effects.calls)
	}

	effects.facts.RuntimeRevision = "run2"
	if _, err := coordinator.CommitRestore(context.Background(), biz.RestoreCommitRequest{Token: preview.Token, Confirmed: true}); !errors.Is(err, biz.ErrRestorePreviewStale) {
		t.Fatalf("stale error = %v", err)
	}
	if !reflect.DeepEqual(effects.calls, []string{"snapshot"}) {
		t.Fatalf("stale commit effects = %v", effects.calls)
	}
}

func TestCommitRestoreRunsTransactionInOrderAndClearsMachineLocalCAState(t *testing.T) {
	effects := &fakeRestoreEffects{facts: biz.RestoreFacts{VaultRevision: "v1", RuntimeRevision: "run1", RouteRevision: "route1"}}
	coordinator := newRestoreCoordinator(t, effects)
	preview, err := coordinator.StageRestore(context.Background(), biz.RestoreStageRequest{
		Path: writeBackupFile(t, makeBackup(t)), Password: "pw",
	})
	if err != nil {
		t.Fatal(err)
	}
	effects.calls = nil
	result, err := coordinator.CommitRestore(context.Background(), biz.RestoreCommitRequest{Token: preview.Token, Confirmed: true})
	if err != nil {
		t.Fatalf("CommitRestore: %v", err)
	}
	want := []string{"snapshot", "storage", "capture", "journal", "prepare", "journal", "suspend", "journal", "neutralize", "journal", "journal", "replace", "journal", "quarantine", "journal", "complete"}
	if !reflect.DeepEqual(effects.calls, want) {
		t.Fatalf("calls = %v, want %v", effects.calls, want)
	}
	wantPhases := []string{
		biz.RestorePhasePlanned, biz.RestorePhaseCandidatePrepared, biz.RestorePhaseRuntimeSuspended,
		biz.RestorePhaseRoutesNeutralized, biz.RestorePhaseReplacingVault, biz.RestorePhaseVaultReplaced,
		biz.RestorePhaseQuarantined,
	}
	if !reflect.DeepEqual(effects.journalPhases, wantPhases) {
		t.Fatalf("journal phases = %v, want %v", effects.journalPhases, wantPhases)
	}
	if !result.Quarantined || result.TransactionID == "" {
		t.Fatalf("result = %+v", result)
	}
	if effects.prepared.Prefs.CATrustedSHA256 != "" {
		t.Fatal("machine-local CA state must not be restored")
	}
}

func TestCommitRestoreCompensatesPreReplacementFailure(t *testing.T) {
	effects := &fakeRestoreEffects{
		facts:  biz.RestoreFacts{VaultRevision: "v1", RuntimeRevision: "run1", RouteRevision: "route1"},
		failAt: "neutralize",
	}
	coordinator := newRestoreCoordinator(t, effects)
	preview, err := coordinator.StageRestore(context.Background(), biz.RestoreStageRequest{
		Path: writeBackupFile(t, makeBackup(t)), Password: "pw",
	})
	if err != nil {
		t.Fatal(err)
	}
	effects.calls = nil
	if _, err := coordinator.CommitRestore(context.Background(), biz.RestoreCommitRequest{Token: preview.Token, Confirmed: true}); err == nil {
		t.Fatal("neutralize failure must be returned")
	}
	want := []string{"snapshot", "storage", "capture", "journal", "prepare", "journal", "suspend", "journal", "neutralize", "compensate"}
	if !reflect.DeepEqual(effects.calls, want) {
		t.Fatalf("calls = %v, want %v", effects.calls, want)
	}
	if effects.compensated == nil || effects.compensated.ReplacedVault {
		t.Fatalf("compensation = %+v", effects.compensated)
	}
}

func TestCommitRestoreKeepsJournalPendingAfterVaultReplacement(t *testing.T) {
	effects := &fakeRestoreEffects{
		facts:  biz.RestoreFacts{VaultRevision: "v1", RuntimeRevision: "run1", RouteRevision: "route1"},
		failAt: "quarantine",
	}
	coordinator := newRestoreCoordinator(t, effects)
	preview, err := coordinator.StageRestore(context.Background(), biz.RestoreStageRequest{
		Path: writeBackupFile(t, makeBackup(t)), Password: "pw",
	})
	if err != nil {
		t.Fatal(err)
	}
	effects.calls = nil
	result, err := coordinator.CommitRestore(context.Background(), biz.RestoreCommitRequest{Token: preview.Token, Confirmed: true})
	if err == nil || !result.JournalPending || result.TransactionID == "" {
		t.Fatalf("post-replacement result = %+v, err = %v", result, err)
	}
	if effects.compensated != nil {
		t.Fatalf("post-replacement failure must not resurrect old state: %+v", effects.compensated)
	}
	want := []string{"snapshot", "storage", "capture", "journal", "prepare", "journal", "suspend", "journal", "neutralize", "journal", "journal", "replace", "journal", "quarantine"}
	if !reflect.DeepEqual(effects.calls, want) {
		t.Fatalf("calls = %v, want %v", effects.calls, want)
	}
}
