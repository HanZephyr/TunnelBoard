package biz_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/HanZephyr/TunnelBoard/internal/biz"
)

func writeBackupFile(t *testing.T, raw []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "backup.tbb")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestBackupPackageStagesOnceAndConsumesPurposeBoundToken(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	pkg := biz.NewBackupPackage("app-generation-1", biz.WithBackupClock(func() time.Time { return now }))
	path := writeBackupFile(t, makeBackup(t))

	preview, err := pkg.Stage(context.Background(), biz.StageRequest{
		Path: path, Password: "pw", Purpose: biz.StagePurposeRestore, VaultRevision: "vault-7",
	})
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if preview.Token == "" || preview.ExpiresAt != now.Add(10*time.Minute) {
		t.Fatalf("preview = %+v", preview)
	}
	if preview.Counts.Forwards != 2 || preview.Counts.SSHHosts != 1 {
		t.Fatalf("counts = %+v", preview.Counts)
	}

	// Stage 后删除源文件，Take 仍应使用内存中的已验证内容，不能重新读文件或重复 KDF。
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := pkg.Take(context.Background(), biz.TakeStageRequest{
		Token: preview.Token, Purpose: biz.StagePurposeImport, VaultRevision: "vault-7",
	}); !errors.Is(err, biz.ErrBackupStagePurpose) {
		t.Fatalf("wrong purpose error = %v", err)
	}
	staged, err := pkg.Take(context.Background(), biz.TakeStageRequest{
		Token: preview.Token, Purpose: biz.StagePurposeRestore, VaultRevision: "vault-7",
	})
	if err != nil {
		t.Fatalf("Take: %v", err)
	}
	if len(staged.Vault.Forwards) != 2 || staged.FileDigest == "" {
		t.Fatalf("staged = %+v", staged)
	}
	if _, err := pkg.Take(context.Background(), biz.TakeStageRequest{
		Token: preview.Token, Purpose: biz.StagePurposeRestore, VaultRevision: "vault-7",
	}); !errors.Is(err, biz.ErrBackupStageToken) {
		t.Fatalf("second Take error = %v", err)
	}
}

func TestBackupPackageInvalidatesExpiredStaleAndSupersededTokens(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	pkg := biz.NewBackupPackage("app-generation-1", biz.WithBackupClock(func() time.Time { return now }))

	first, err := pkg.Stage(context.Background(), biz.StageRequest{
		Path: writeBackupFile(t, makeBackup(t)), Password: "pw", Purpose: biz.StagePurposeRestore, VaultRevision: "vault-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := pkg.Stage(context.Background(), biz.StageRequest{
		Path: writeBackupFile(t, makeBackup(t)), Password: "pw", Purpose: biz.StagePurposeRestore, VaultRevision: "vault-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pkg.Take(context.Background(), biz.TakeStageRequest{
		Token: first.Token, Purpose: biz.StagePurposeRestore, VaultRevision: "vault-1",
	}); !errors.Is(err, biz.ErrBackupStageToken) {
		t.Fatalf("superseded token error = %v", err)
	}
	if _, err := pkg.Stage(context.Background(), biz.StageRequest{
		Path: "missing.tbb", Password: "pw", Purpose: biz.StagePurposeRestore, VaultRevision: "vault-1",
	}); err == nil {
		t.Fatal("missing replacement package must fail")
	}
	if _, err := pkg.Take(context.Background(), biz.TakeStageRequest{
		Token: second.Token, Purpose: biz.StagePurposeRestore, VaultRevision: "vault-1",
	}); !errors.Is(err, biz.ErrBackupStageToken) {
		t.Fatalf("failed replacement must still revoke old token: %v", err)
	}
	stale, err := pkg.Stage(context.Background(), biz.StageRequest{
		Path: writeBackupFile(t, makeBackup(t)), Password: "pw", Purpose: biz.StagePurposeRestore, VaultRevision: "vault-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pkg.Take(context.Background(), biz.TakeStageRequest{
		Token: stale.Token, Purpose: biz.StagePurposeRestore, VaultRevision: "vault-2",
	}); !errors.Is(err, biz.ErrBackupStageStale) {
		t.Fatalf("stale revision error = %v", err)
	}

	expiring, err := pkg.Stage(context.Background(), biz.StageRequest{
		Path: writeBackupFile(t, makeBackup(t)), Password: "pw", Purpose: biz.StagePurposeRestore, VaultRevision: "vault-2",
	})
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(10*time.Minute + time.Nanosecond)
	if _, err := pkg.Take(context.Background(), biz.TakeStageRequest{
		Token: expiring.Token, Purpose: biz.StagePurposeRestore, VaultRevision: "vault-2",
	}); !errors.Is(err, biz.ErrBackupStageExpired) {
		t.Fatalf("expired token error = %v", err)
	}
}

func TestBackupPackageRejectsOversizedFileBeforeReadingItAll(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oversized.tbb")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(64<<20 + 1); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	pkg := biz.NewBackupPackage("app-generation-1")
	_, err = pkg.Stage(context.Background(), biz.StageRequest{
		Path: path, Password: "pw", Purpose: biz.StagePurposeImport, VaultRevision: "vault-1",
	})
	if !errors.Is(err, biz.ErrBackupPackageTooLarge) {
		t.Fatalf("Stage error = %v, want ErrBackupPackageTooLarge", err)
	}
}
