package caddy

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPurgeStaleLocalCertificateCacheAfterAuthorityRotation(t *testing.T) {
	a := New(t.TempDir())
	currentRoot, currentIntermediate, currentLeaf := testCertificateChain(t, "current.localhost")
	_, staleIntermediate, staleLeaf := testCertificateChain(t, "stale.localhost")
	writeRootCertificate(t, a.DataDir, currentRoot)
	writeCachedCertificateChain(t, a.DataDir, "current.localhost", currentLeaf, currentIntermediate)
	writeCachedCertificateChain(t, a.DataDir, "stale.localhost", staleLeaf, staleIntermediate)

	purged, err := a.purgeStaleLocalCertificateCache()
	if err != nil {
		t.Fatalf("purge stale certificate cache: %v", err)
	}
	if !purged {
		t.Fatal("expected stale cache to be purged after the authority rotated")
	}
	if _, err := os.Stat(filepath.Join(a.DataDir, "caddy", "certificates", "local", "stale.localhost")); !os.IsNotExist(err) {
		t.Fatalf("stale cache directory still exists: %v", err)
	}
	if _, err := os.Stat(filepath.Join(a.DataDir, "caddy", "certificates", "local", "current.localhost")); err != nil {
		t.Fatalf("current cache directory must be retained: %v", err)
	}
}

func writeRootCertificate(t *testing.T, dataDir string, root []byte) {
	t.Helper()
	path := filepath.Join(dataDir, "caddy", "pki", "authorities", "local", "root.crt")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: root}), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeCachedCertificateChain(t *testing.T, dataDir, host string, leaf, intermediate []byte) {
	t.Helper()
	path := filepath.Join(dataDir, "caddy", "certificates", "local", host, host+".crt")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	contents := append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leaf}), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: intermediate})...)
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
}

func testCertificateChain(t *testing.T, host string) (root, intermediate, leaf []byte) {
	t.Helper()
	now := time.Now()
	rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	rootTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "TunnelBoard Test Root"},
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	root, err = x509.CreateCertificate(rand.Reader, rootTemplate, rootTemplate, &rootKey.PublicKey, rootKey)
	if err != nil {
		t.Fatal(err)
	}
	rootCertificate, err := x509.ParseCertificate(root)
	if err != nil {
		t.Fatal(err)
	}
	intermediateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	intermediateTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(2),
		Subject:               pkix.Name{CommonName: "TunnelBoard Test Intermediate"},
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	intermediate, err = x509.CreateCertificate(rand.Reader, intermediateTemplate, rootCertificate, &intermediateKey.PublicKey, rootKey)
	if err != nil {
		t.Fatal(err)
	}
	intermediateCertificate, err := x509.ParseCertificate(intermediate)
	if err != nil {
		t.Fatal(err)
	}
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(3),
		Subject:      pkix.Name{CommonName: host},
		DNSNames:     []string{host},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leaf, err = x509.CreateCertificate(rand.Reader, leafTemplate, intermediateCertificate, &leafKey.PublicKey, intermediateKey)
	if err != nil {
		t.Fatal(err)
	}
	return root, intermediate, leaf
}
