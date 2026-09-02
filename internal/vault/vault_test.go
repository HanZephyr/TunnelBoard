package vault_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/HanZephyr/TunnelBoard/internal/model"
	"github.com/HanZephyr/TunnelBoard/internal/vault"
)

// vault.key 存在而 vault.dat 缺失时，视为全新数据：以现有密钥初始化空 Vault，密钥本身不变。
func TestOpenKeepsExistingKeyWhenDataMissing(t *testing.T) {
	dir := t.TempDir()
	if _, err := vault.Open(dir); err != nil {
		t.Fatalf("Open: %v", err)
	}
	keyPath := filepath.Join(dir, "vault.key")
	keyBefore, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "vault.dat")); err != nil {
		t.Fatal(err)
	}

	s, err := vault.Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	data, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if data.Version != 1 || len(data.SSHHosts) != 0 {
		t.Fatalf("expected fresh empty vault, got %+v", data)
	}
	keyAfter, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(keyBefore, keyAfter) {
		t.Fatal("existing key must be reused, not regenerated")
	}
}

// vault.dat 存在而密钥遗失时，Open 必须返回 ErrKeyUnavailable 且不触碰数据文件：
// 用户只能选择导入备份包或显式初始化空 Vault，应用不得静默覆盖。
func TestOpenReportsKeyUnavailableWithoutTouchingData(t *testing.T) {
	dir := t.TempDir()
	s, err := vault.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := s.Update(func(d *model.VaultData) error {
		d.SSHHosts = append(d.SSHHosts, model.SSHHost{ID: 1, Name: "h", Password: "x"})
		return nil
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	datPath := filepath.Join(dir, "vault.dat")
	before, err := os.ReadFile(datPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "vault.key")); err != nil {
		t.Fatal(err)
	}

	if _, err := vault.Open(dir); !errors.Is(err, vault.ErrKeyUnavailable) {
		t.Fatalf("Open err = %v, want ErrKeyUnavailable", err)
	}
	after, err := os.ReadFile(datPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("vault.dat must not be touched when key is unavailable")
	}
}

// 密文被篡改或密钥被替换时，Load 必须失败而不是返回脏数据。
func TestLoadFailsOnTamperingOrWrongKey(t *testing.T) {
	newStore := func(t *testing.T) (dir string) {
		t.Helper()
		dir = t.TempDir()
		s, err := vault.Open(dir)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		if _, err := s.Update(func(d *model.VaultData) error {
			d.SSHHosts = append(d.SSHHosts, model.SSHHost{ID: 1, Name: "h", Password: "x"})
			return nil
		}); err != nil {
			t.Fatalf("Update: %v", err)
		}
		return dir
	}

	t.Run("篡改密文字节", func(t *testing.T) {
		dir := newStore(t)
		datPath := filepath.Join(dir, "vault.dat")
		raw, err := os.ReadFile(datPath)
		if err != nil {
			t.Fatal(err)
		}
		raw[len(raw)-1] ^= 0xff
		if err := os.WriteFile(datPath, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		s, err := vault.Open(dir)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		if _, err := s.Load(); err == nil {
			t.Fatal("Load should fail on tampered ciphertext")
		}
	})

	t.Run("替换为其他实例的密钥", func(t *testing.T) {
		dir := newStore(t)
		other := newStore(t)
		otherKey, err := os.ReadFile(filepath.Join(other, "vault.key"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "vault.key"), otherKey, 0o600); err != nil {
			t.Fatal(err)
		}
		s, err := vault.Open(dir)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		if _, err := s.Load(); err == nil {
			t.Fatal("Load should fail with wrong key")
		}
	})
}

// 迭代 1 验收：磁盘上任何文件（vault.dat、vault.key 及临时残留）都不得出现秘密明文。
func TestSecretsNeverAppearOnDisk(t *testing.T) {
	dir := t.TempDir()
	s, err := vault.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	secret := "p@ssw0rd-never-on-disk"
	if _, err := s.Update(func(d *model.VaultData) error {
		d.SSHHosts = append(d.SSHHosts, model.SSHHost{ID: 1, Name: "h", AuthType: "ssh_key", Password: secret})
		return nil
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	needle := []byte(secret)
	err = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(content, needle) {
			t.Errorf("secret found in plaintext file: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
}

// Update 写入的数据应能跨会话读回：关闭后重新 Open，内容一致。
func TestStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := vault.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	want := model.SSHHost{ID: 1, Name: "跳板", Host: "10.0.0.1", Port: 22, User: "ops",
		AuthType: "password", Password: "s3cr3t-value"}
	if _, err := s.Update(func(d *model.VaultData) error {
		d.SSHHosts = append(d.SSHHosts, want)
		return nil
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	reopened, err := vault.Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got, err := reopened.Load()
	if err != nil {
		t.Fatalf("Load after reopen: %v", err)
	}
	if len(got.SSHHosts) != 1 || got.SSHHosts[0] != want {
		t.Fatalf("roundtrip mismatch: got %+v, want %+v", got.SSHHosts, want)
	}
}

func TestRestoreCandidateIsValidatedBeforeAtomicReplacement(t *testing.T) {
	dir := t.TempDir()
	s, err := vault.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := s.Update(func(d *model.VaultData) error {
		d.Folders = []model.Folder{{ID: 1, Name: "old"}}
		return nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	candidate, err := s.PrepareRestoreCandidate(model.VaultData{Version: 1, Folders: []model.Folder{{ID: 2, Name: "restored"}}})
	if err != nil {
		t.Fatalf("PrepareRestoreCandidate: %v", err)
	}
	before, _ := s.Load()
	if len(before.Folders) != 1 || before.Folders[0].Name != "old" {
		t.Fatalf("preparing candidate changed live vault: %+v", before)
	}
	if err := s.CommitRestoreCandidate(candidate); err != nil {
		t.Fatalf("CommitRestoreCandidate: %v", err)
	}
	after, _ := s.Load()
	if len(after.Folders) != 1 || after.Folders[0].Name != "restored" {
		t.Fatalf("restored vault = %+v", after)
	}
}

// 密钥与 Vault 均不存在时，Open 应完成首次初始化：生成密钥、创建空 Vault。
func TestOpenInitializesFreshVault(t *testing.T) {
	dir := t.TempDir()

	s, err := vault.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	data, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if data.Version != 1 {
		t.Fatalf("fresh vault version = %d, want 1", data.Version)
	}
	if !data.Prefs.UpdateCheckEnabled {
		t.Fatal("fresh vault must explicitly enable update checks")
	}
	if len(data.SSHHosts) != 0 || len(data.Forwards) != 0 {
		t.Fatalf("fresh vault should be empty, got %+v", data)
	}

	if runtime.GOOS == "windows" {
		return // Windows 下文件权限语义不同，跳过权限断言
	}
	for _, name := range []string{"vault.key", "vault.dat"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("stat %s: %v", name, err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("%s perm = %o, want 600", name, perm)
		}
	}
}

func TestOpenReportsUnreadableDataFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix file modes")
	}
	dir := t.TempDir()
	if _, err := vault.Open(dir); err != nil {
		t.Fatal(err)
	}
	datPath := filepath.Join(dir, "vault.dat")
	if err := os.Chmod(datPath, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(datPath, 0o600) })
	if _, err := vault.Open(dir); err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("Open err = %v, want permission denied", err)
	}
}
