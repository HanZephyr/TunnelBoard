package helper_test

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/HanZephyr/TunnelBoard/internal/helper"
)

func newKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return key
}

func makeSelfSignedCA(t *testing.T, commonName string) []byte {
	t.Helper()
	key := newKey(t)
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	return der
}

// sha256Hex 返回小写十六进制 SHA-256 指纹，与协议声明格式一致。
func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// 自签 CA（Caddy 本地 CA 的真实 CN 形态）→ 通过。
func TestValidateLocalCAAcceptsSelfSignedCA(t *testing.T) {
	der := makeSelfSignedCA(t, "Caddy Local Authority - 2026 ECC Root")
	if err := helper.ValidateLocalCA(der, sha256Hex(der)); err != nil {
		t.Fatalf("self-signed CA should pass: %v", err)
	}
}

// 指纹不匹配 → 拒绝。
func TestValidateLocalCARejectsFingerprintMismatch(t *testing.T) {
	der := makeSelfSignedCA(t, "Any CA")
	err := helper.ValidateLocalCA(der, strings.Repeat("0", 64))
	if err == nil || !strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("err = %v, want fingerprint mismatch", err)
	}
}

// 非 CA 证书 → 拒绝。
func TestValidateLocalCARejectsNonCA(t *testing.T) {
	key := newKey(t)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "leaf"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IsCA:         false,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	if err := helper.ValidateLocalCA(der, sha256Hex(der)); err == nil {
		t.Fatal("non-CA certificate must be rejected")
	}
}

// 由其他密钥签发（非自签）→ 拒绝。
func TestValidateLocalCARejectsNonSelfSigned(t *testing.T) {
	parent := newKey(t)
	child := newKey(t)
	parentTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(3),
		Subject:      pkix.Name{CommonName: "parent"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IsCA:         true, KeyUsage: x509.KeyUsageCertSign, BasicConstraintsValid: true,
	}
	childTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(4),
		Subject:      pkix.Name{CommonName: "child"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IsCA:         true, KeyUsage: x509.KeyUsageCertSign, BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, childTmpl, parentTmpl, &child.PublicKey, parent)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	if err := helper.ValidateLocalCA(der, sha256Hex(der)); err == nil {
		t.Fatal("non-self-signed certificate must be rejected")
	}
}

// DER 损坏 → 拒绝。
func TestValidateLocalCARejectsCorruptDER(t *testing.T) {
	if err := helper.ValidateLocalCA([]byte("not-a-cert"), strings.Repeat("0", 64)); err == nil {
		t.Fatal("corrupt DER must be rejected")
	}
}
