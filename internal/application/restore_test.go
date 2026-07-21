package application_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/HanZephyr/TunnelBoard/internal/application"
	"github.com/HanZephyr/TunnelBoard/internal/biz"
	"github.com/HanZephyr/TunnelBoard/internal/model"
	"github.com/HanZephyr/TunnelBoard/internal/vault"
)

type restoreRuntime struct {
	fakeRuntime
	suspendCalls int
}

func (r *restoreRuntime) SuspendAll(context.Context) (biz.RuntimeSuspendPlan, error) {
	r.suspendCalls++
	return biz.RuntimeSuspendPlan{}, nil
}

type restoreRoutes struct {
	fakeRoutes
	neutralizeCalls int
}

func (r *restoreRoutes) NeutralizeRoutes(context.Context) error { r.neutralizeCalls++; return nil }

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
