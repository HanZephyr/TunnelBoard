package biz_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/HanZephyr/TunnelBoard/internal/biz"
	"github.com/HanZephyr/TunnelBoard/internal/model"
)

type fakeRestoreEffects struct {
	facts       biz.RestoreFacts
	calls       []string
	prepared    model.VaultData
	failAt      string
	compensated *biz.RestoreCompensation
}

func (f *fakeRestoreEffects) Snapshot(context.Context) (biz.RestoreFacts, error) {
	f.calls = append(f.calls, "snapshot")
	return f.facts, nil
}
func (f *fakeRestoreEffects) PrepareCandidate(_ context.Context, data model.VaultData) (biz.RestoreVaultCandidate, error) {
	f.calls = append(f.calls, "prepare")
	f.prepared = data
	if f.failAt == "prepare" {
		return biz.RestoreVaultCandidate{}, errors.New("prepare failed")
	}
	return biz.RestoreVaultCandidate{ID: "candidate-1"}, nil
}
func (f *fakeRestoreEffects) WriteJournal(context.Context, biz.RestoreJournal) error {
	f.calls = append(f.calls, "journal")
	if f.failAt == "journal" {
		return errors.New("journal failed")
	}
	return nil
}
func (f *fakeRestoreEffects) SuspendAll(context.Context) (biz.RestoreSuspendPlan, error) {
	f.calls = append(f.calls, "suspend")
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
func (f *fakeRestoreEffects) Compensate(_ context.Context, c biz.RestoreCompensation) error {
	f.calls = append(f.calls, "compensate")
	f.compensated = &c
	return nil
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
	want := []string{"snapshot", "journal", "prepare", "suspend", "neutralize", "replace", "quarantine", "complete"}
	if !reflect.DeepEqual(effects.calls, want) {
		t.Fatalf("calls = %v, want %v", effects.calls, want)
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
	want := []string{"snapshot", "journal", "prepare", "suspend", "neutralize", "compensate"}
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
	want := []string{"snapshot", "journal", "prepare", "suspend", "neutralize", "replace", "quarantine"}
	if !reflect.DeepEqual(effects.calls, want) {
		t.Fatalf("calls = %v, want %v", effects.calls, want)
	}
}
