package helper

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type cleanupCertificateStore struct{ removed []string }

func (*cleanupCertificateStore) ContainsSHA256(context.Context, string) (bool, error) {
	return true, nil
}
func (*cleanupCertificateStore) AddDER(context.Context, []byte) error { return nil }
func (s *cleanupCertificateStore) RemoveSHA256(_ context.Context, fingerprint string) error {
	s.removed = append(s.removed, fingerprint)
	return nil
}

func TestRemoveCAAuthorityAndKeysDeletesExactTrustAndPrivateMaterial(t *testing.T) {
	root := t.TempDir()
	authority := filepath.Join(root, "pki", "authorities", "local", "root.crt")
	if err := os.MkdirAll(filepath.Dir(authority), 0o700); err != nil {
		t.Fatal(err)
	}
	der, keyPEM := testCleanupCA(t)
	if err := os.WriteFile(authority, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(filepath.Dir(authority), "root.key")
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	store := &cleanupCertificateStore{}
	if err := removeCAAuthorityAndKeys(context.Background(), authority, store); err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256(der)
	if len(store.removed) != 1 || store.removed[0] != fmt.Sprintf("%x", want[:]) {
		t.Fatalf("removed=%v", store.removed)
	}
	if _, err := os.Stat(filepath.Join(root, "pki")); !os.IsNotExist(err) {
		t.Fatalf("PKI directory still exists: %v", err)
	}
}

func TestRemoveCAAuthorityAndKeysRejectsMismatchedPrivateKey(t *testing.T) {
	root := t.TempDir()
	authority := filepath.Join(root, "pki", "authorities", "local", "root.crt")
	if err := os.MkdirAll(filepath.Dir(authority), 0o700); err != nil {
		t.Fatal(err)
	}
	der, _ := testCleanupCA(t)
	_, unrelatedKey := testCleanupCA(t)
	if err := os.WriteFile(authority, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(authority), "root.key"), unrelatedKey, 0o600); err != nil {
		t.Fatal(err)
	}
	store := &cleanupCertificateStore{}
	if err := removeCAAuthorityAndKeys(context.Background(), authority, store); err == nil {
		t.Fatal("mismatched private key must prevent privileged root-store deletion")
	}
	if len(store.removed) != 0 {
		t.Fatalf("root store changed despite mismatched key: %v", store.removed)
	}
}

func TestLegacyAuthorityPathsIncludeRedirectedConfigRoot(t *testing.T) {
	appData := t.TempDir()
	implicit := filepath.Join(appData, "TunnelBoard")
	redirected := filepath.Join(t.TempDir(), "portable-vault")
	if err := os.MkdirAll(implicit, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(implicit, "config.root"), []byte(redirected+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	paths, err := legacyAuthorityPaths(appData)
	if err != nil {
		t.Fatal(err)
	}
	wantRedirected := filepath.Join(redirected, "caddy", "pki", "authorities", "local", "root.crt")
	if len(paths) != 2 || paths[1] != wantRedirected {
		t.Fatalf("legacy authority paths = %v, want redirected %s", paths, wantRedirected)
	}
}

func testCleanupCA(t *testing.T) ([]byte, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "TunnelBoard Local CA"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return der, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
}
