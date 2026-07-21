package application_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/HanZephyr/TunnelBoard/internal/application"
	"github.com/HanZephyr/TunnelBoard/internal/biz"
	"github.com/HanZephyr/TunnelBoard/internal/model"
	"github.com/HanZephyr/TunnelBoard/internal/vault"
)

func writeRestoreJournal(t *testing.T, dir string, journal biz.RestoreJournal) {
	t.Helper()
	stateDir := filepath.Join(dir, "state")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(journal)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "restore-journal.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

type restoreRuntime struct {
	fakeRuntime
	suspendCalls int
}

func (r *restoreRuntime) Snapshot() ([]biz.RuntimeStatus, error) { return nil, nil }

func (r *restoreRuntime) SuspendAll(context.Context) (biz.RuntimeSuspendPlan, error) {
	r.suspendCalls++
	return biz.RuntimeSuspendPlan{}, nil
}

type restoreRoutes struct {
	fakeRoutes
	neutralizeCalls int
	neutralizeErr   error
}

func (r *restoreRoutes) NeutralizeRoutes(context.Context) error {
	r.neutralizeCalls++
	return r.neutralizeErr
}

func TestStageRestoreWrongPasswordHasZeroNetworkSideEffects(t *testing.T) {
	dir := t.TempDir()
	store, err := vault.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	backup, err := vault.ExportBackup(model.VaultData{Version: 1}, nil, "right-password", vault.DefaultBackupKDF())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "backup.tbbak")
	if err := os.WriteFile(path, backup, 0o600); err != nil {
		t.Fatal(err)
	}
	runtime := &restoreRuntime{}
	routes := &restoreRoutes{}
	recovery := application.NewRecoveryStore(dir)
	effects := application.NewRestoreEffects(store, runtime, routes, recovery)
	coordinator := biz.NewRestoreCoordinator(biz.NewBackupPackage("test-generation"), effects)

	if _, err := coordinator.StageRestore(context.Background(), biz.RestoreStageRequest{Path: path, Password: "wrong-password"}); err == nil {
		t.Fatal("wrong password must fail")
	}
	if runtime.suspendCalls != 0 || routes.neutralizeCalls != 0 {
		t.Fatalf("Stage caused network effects: suspend=%d neutralize=%d", runtime.suspendCalls, routes.neutralizeCalls)
	}
}

func TestCommitRestoreAtomicallyReplacesVaultAndEntersQuarantine(t *testing.T) {
	dir := t.TempDir()
	store, err := vault.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	restored := model.VaultData{Version: 1, Folders: []model.Folder{{ID: 1, Name: "restored"}}, Prefs: model.Prefs{CATrustedSHA256: "must-not-port"}}
	backup, err := vault.ExportBackup(restored, nil, "password", vault.DefaultBackupKDF())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "backup.tbbak")
	if err := os.WriteFile(path, backup, 0o600); err != nil {
		t.Fatal(err)
	}
	runtime := &restoreRuntime{}
	routes := &restoreRoutes{}
	recovery := application.NewRecoveryStore(dir)
	effects := application.NewRestoreEffects(store, runtime, routes, recovery)
	coordinator := biz.NewRestoreCoordinator(biz.NewBackupPackage("test-generation"), effects)
	preview, err := coordinator.StageRestore(context.Background(), biz.RestoreStageRequest{Path: path, Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := coordinator.CommitRestore(context.Background(), biz.RestoreCommitRequest{Token: preview.Token, Confirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Quarantined || result.JournalPending || runtime.suspendCalls != 1 || routes.neutralizeCalls != 1 {
		t.Fatalf("result=%+v suspend=%d neutralize=%d", result, runtime.suspendCalls, routes.neutralizeCalls)
	}
	data, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Folders) != 1 || data.Folders[0].Name != "restored" || data.Prefs.CATrustedSHA256 != "" {
		t.Fatalf("restored data = %+v", data)
	}
	quarantined, pending, err := recovery.State()
	if err != nil || !quarantined || pending {
		t.Fatalf("recovery state quarantined=%v pending=%v err=%v", quarantined, pending, err)
	}
}

func TestRecoverPendingBeforeVaultReplacementRestoresOldRuntimeAndConsumesJournal(t *testing.T) {
	dir := t.TempDir()
	store, err := vault.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Update(func(data *model.VaultData) error {
		data.Forwards = []model.Forward{{ID: 1, Name: "one"}, {ID: 2, Name: "two"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	runtime := &restoreRuntime{}
	routes := &restoreRoutes{}
	recovery := application.NewRecoveryStore(dir)
	effects := application.NewRestoreEffects(store, runtime, routes, recovery)
	service := application.NewService(application.Dependencies{
		Store: store, Catalog: biz.NewCatalogBiz(store), Runtime: runtime, Routes: routes,
		Restore: biz.NewRestoreCoordinator(biz.NewBackupPackage("test"), effects), Recovery: recovery,
	})
	snapshot, err := service.GetSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := store.PrepareRestoreCandidate(model.VaultData{Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	writeRestoreJournal(t, dir, biz.RestoreJournal{
		TransactionID: "tx-before", Before: biz.RestoreFacts{VaultRevision: snapshot.Revisions.Vault},
		Phase: biz.RestorePhaseRuntimeSuspended, CandidateID: candidate.ID, RunningForwardIDs: []int{1, 2},
	})
	if err := effects.RecoverPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	if runtime.starts != 2 || routes.reconciles != 1 {
		t.Fatalf("old state was not restored: starts=%d reconciles=%d", runtime.starts, routes.reconciles)
	}
	quarantined, pending, err := recovery.State()
	if err != nil || quarantined || pending {
		t.Fatalf("recovery state quarantined=%v pending=%v err=%v", quarantined, pending, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "restore-candidates", candidate.ID)); !os.IsNotExist(err) {
		t.Fatalf("candidate was not discarded: %v", err)
	}
}

func TestRecoverPendingAfterVaultReplacementQuarantinesOnceAndConsumesJournal(t *testing.T) {
	dir := t.TempDir()
	store, err := vault.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	runtime := &restoreRuntime{}
	routes := &restoreRoutes{}
	recovery := application.NewRecoveryStore(dir)
	effects := application.NewRestoreEffects(store, runtime, routes, recovery)
	service := application.NewService(application.Dependencies{
		Store: store, Catalog: biz.NewCatalogBiz(store), Runtime: runtime, Routes: routes,
		Restore: biz.NewRestoreCoordinator(biz.NewBackupPackage("test"), effects), Recovery: recovery,
	})
	before, err := service.GetSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := store.PrepareRestoreCandidate(model.VaultData{Version: 1, Folders: []model.Folder{{ID: 9, Name: "new"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CommitRestoreCandidate(candidate); err != nil {
		t.Fatal(err)
	}
	writeRestoreJournal(t, dir, biz.RestoreJournal{
		TransactionID: "tx-after", Before: biz.RestoreFacts{VaultRevision: before.Revisions.Vault},
		Phase: biz.RestorePhaseReplacingVault, CandidateID: candidate.ID, RunningForwardIDs: []int{1}, RoutesNeutralized: true,
	})
	if err := effects.RecoverPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	quarantined, pending, err := recovery.State()
	if err != nil || !quarantined || pending || routes.neutralizeCalls != 1 || runtime.starts != 0 {
		t.Fatalf("new state did not converge: quarantined=%v pending=%v neutralize=%d starts=%d err=%v", quarantined, pending, routes.neutralizeCalls, runtime.starts, err)
	}
	if err := effects.RecoverPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	if routes.neutralizeCalls != 1 {
		t.Fatalf("consumed journal repeated neutralization: %d", routes.neutralizeCalls)
	}
}

func TestRecoverPendingDetectsReplacementThatOnlyChangesSecrets(t *testing.T) {
	dir := t.TempDir()
	store, err := vault.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	host := model.SSHHost{ID: 1, Name: "host", Host: "example.test", Port: 22, User: "user", AuthType: "password", Password: "old", CredentialRevision: 1}
	if _, err := store.Update(func(data *model.VaultData) error {
		data.SSHHosts = []model.SSHHost{host}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	candidateData, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	runtime := &restoreRuntime{}
	routes := &restoreRoutes{}
	recovery := application.NewRecoveryStore(dir)
	effects := application.NewRestoreEffects(store, runtime, routes, recovery)
	service := application.NewService(application.Dependencies{
		Store: store, Catalog: biz.NewCatalogBiz(store), Runtime: runtime, Routes: routes,
		Restore: biz.NewRestoreCoordinator(biz.NewBackupPackage("test"), effects), Recovery: recovery,
	})
	before, err := service.GetSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	beforeStorage, err := store.StorageRevision()
	if err != nil {
		t.Fatal(err)
	}
	host.Password = "new"
	candidateData.SSHHosts = []model.SSHHost{host}
	candidate, err := store.PrepareRestoreCandidate(candidateData)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CommitRestoreCandidate(candidate); err != nil {
		t.Fatal(err)
	}
	after, err := service.GetSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if after.Revisions.Vault != before.Revisions.Vault {
		t.Fatalf("test requires identical public revisions: before=%s after=%s", before.Revisions.Vault, after.Revisions.Vault)
	}
	writeRestoreJournal(t, dir, biz.RestoreJournal{
		TransactionID: "tx-secret-only", Before: biz.RestoreFacts{VaultRevision: before.Revisions.Vault},
		BeforeVaultStorageRevision: beforeStorage, Phase: biz.RestorePhaseReplacingVault, CandidateID: candidate.ID,
	})
	if err := effects.RecoverPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	quarantined, pending, err := recovery.State()
	if err != nil || !quarantined || pending || routes.neutralizeCalls != 1 || runtime.starts != 0 {
		t.Fatalf("secret-only replacement recovery quarantined=%v pending=%v neutralize=%d starts=%d err=%v", quarantined, pending, routes.neutralizeCalls, runtime.starts, err)
	}
}

func TestRecoverPendingKeepsJournalWhenOldRuntimeCannotBeRestored(t *testing.T) {
	dir := t.TempDir()
	store, err := vault.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	runtime := &restoreRuntime{fakeRuntime: fakeRuntime{startErrors: map[int]error{2: context.DeadlineExceeded}}}
	routes := &restoreRoutes{}
	recovery := application.NewRecoveryStore(dir)
	effects := application.NewRestoreEffects(store, runtime, routes, recovery)
	service := application.NewService(application.Dependencies{
		Store: store, Catalog: biz.NewCatalogBiz(store), Runtime: runtime, Routes: routes,
		Restore: biz.NewRestoreCoordinator(biz.NewBackupPackage("test"), effects), Recovery: recovery,
	})
	before, err := service.GetSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	writeRestoreJournal(t, dir, biz.RestoreJournal{
		TransactionID: "tx-resume-failure", Before: biz.RestoreFacts{VaultRevision: before.Revisions.Vault},
		Phase: biz.RestorePhaseRuntimeSuspended, RunningForwardIDs: []int{1, 2},
	})
	if err := effects.RecoverPending(context.Background()); err == nil {
		t.Fatal("runtime recovery failure must be returned")
	}
	_, pending, err := recovery.State()
	if err != nil || !pending {
		t.Fatalf("failed recovery must preserve journal: pending=%v err=%v", pending, err)
	}
	var journal biz.RestoreJournal
	if err := readTestJSON(filepath.Join(dir, "state", "restore-journal.json"), &journal); err != nil {
		t.Fatal(err)
	}
	if len(journal.RunningForwardIDs) != 1 || journal.RunningForwardIDs[0] != 2 {
		t.Fatalf("checkpointed remaining forwards = %v", journal.RunningForwardIDs)
	}
}

func TestRecoverPendingKeepsJournalWhenNewVaultCannotBeNeutralized(t *testing.T) {
	dir := t.TempDir()
	store, err := vault.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	routes := &restoreRoutes{neutralizeErr: context.DeadlineExceeded}
	recovery := application.NewRecoveryStore(dir)
	effects := application.NewRestoreEffects(store, &restoreRuntime{}, routes, recovery)
	writeRestoreJournal(t, dir, biz.RestoreJournal{
		TransactionID: "tx-neutralize-failure", Phase: biz.RestorePhaseVaultReplaced, VaultReplaced: true,
	})
	if err := effects.RecoverPending(context.Background()); err == nil {
		t.Fatal("route neutralization failure must be returned")
	}
	quarantined, pending, err := recovery.State()
	if err != nil || quarantined || !pending {
		t.Fatalf("failed convergence state quarantined=%v pending=%v err=%v", quarantined, pending, err)
	}
}

func readTestJSON(path string, target any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, target)
}
