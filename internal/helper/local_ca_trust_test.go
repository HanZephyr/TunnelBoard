package helper_test

import (
	"context"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"github.com/HanZephyr/TunnelBoard/internal/helper"
)

type memoryCertificateStore struct {
	certs map[string]bool
}

func (s *memoryCertificateStore) ContainsSHA256(_ context.Context, fingerprint string) (bool, error) {
	return s.certs[fingerprint], nil
}

func (s *memoryCertificateStore) AddDER(_ context.Context, certDER []byte) error {
	s.certs[sha256Hex(certDER)] = true
	return nil
}

func (s *memoryCertificateStore) RemoveSHA256(_ context.Context, fingerprint string) error {
	delete(s.certs, fingerprint)
	return nil
}

func writeAuthority(t *testing.T, dir string, der []byte) string {
	t.Helper()
	path := filepath.Join(dir, "root.crt")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// LocalCATrust 的调用者不传 DER 或待删除指纹；模块只信任固定 authority 文件中的当前 CA。
func TestLocalCATrustUsesCurrentAuthorityAndExactStoreIdentity(t *testing.T) {
	dir := t.TempDir()
	der := makeSelfSignedCA(t, "TunnelBoard Local CA")
	store := &memoryCertificateStore{certs: map[string]bool{}}
	trust := helper.NewLocalCATrust(writeAuthority(t, dir, der), filepath.Join(dir, "ca-trust.json"), store)

	identity, err := trust.EnsureCurrentCaddyCATrusted(context.Background())
	if err != nil {
		t.Fatalf("ensure trust: %v", err)
	}
	if identity.SHA256 != sha256Hex(der) || !store.certs[identity.SHA256] {
		t.Fatalf("identity/store = %+v/%v, want exact authority fingerprint", identity, store.certs)
	}

	status, err := trust.Status(context.Background())
	if err != nil || status.State != helper.CATrusted || status.Identity.SHA256 != identity.SHA256 {
		t.Fatalf("status = %+v, err = %v, want trusted current authority", status, err)
	}

	if err := trust.RemoveCurrentCaddyCA(context.Background()); err != nil {
		t.Fatalf("remove trust: %v", err)
	}
	if store.certs[identity.SHA256] {
		t.Fatal("remove must delete the exact recorded certificate")
	}
}

// Caddy 重新生成 CA 后，旧确认记录不能静默授权新证书。
func TestLocalCATrustRequiresNewEnsureWhenAuthorityChanges(t *testing.T) {
	dir := t.TempDir()
	oldDER := makeSelfSignedCA(t, "TunnelBoard Old CA")
	store := &memoryCertificateStore{certs: map[string]bool{}}
	authorityPath := writeAuthority(t, dir, oldDER)
	trust := helper.NewLocalCATrust(authorityPath, filepath.Join(dir, "ca-trust.json"), store)
	if _, err := trust.EnsureCurrentCaddyCATrusted(context.Background()); err != nil {
		t.Fatal(err)
	}

	newDER := makeSelfSignedCA(t, "TunnelBoard New CA")
	writeAuthority(t, dir, newDER)
	status, err := trust.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.State != helper.CAConfirmationRequired || status.Identity.SHA256 != sha256Hex(newDER) {
		t.Fatalf("status = %+v, want confirmation required for new authority", status)
	}
}
