package helper

import (
	"bytes"
	"context"
	"crypto"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const maxCAKeyFileBytes = 1 << 20

func legacyAuthorityPaths(appData string) ([]string, error) {
	implicit := filepath.Join(appData, "TunnelBoard")
	authority := func(root string) string {
		return filepath.Join(root, "caddy", "pki", "authorities", "local", "root.crt")
	}
	paths := []string{authority(implicit)}
	raw, err := os.ReadFile(filepath.Join(implicit, "config.root"))
	if errors.Is(err, os.ErrNotExist) {
		return paths, nil
	}
	if err != nil {
		return nil, fmt.Errorf("helper: read legacy config.root: %w", err)
	}
	line := strings.TrimSpace(strings.Split(string(raw), "\n")[0])
	if line == "" || !filepath.IsAbs(line) {
		return nil, errors.New("helper: legacy config.root must contain an absolute path")
	}
	redirected := authority(filepath.Clean(line))
	if !strings.EqualFold(filepath.Clean(redirected), filepath.Clean(paths[0])) {
		paths = append(paths, redirected)
	}
	return paths, nil
}

// removeCAAuthorityAndKeys 撤销 authority 文件中精确 DER 对应的根证书，
// 只有证书存储操作成功后才删除同一 Caddy PKI 目录中的私钥材料。
func removeCAAuthorityAndKeys(ctx context.Context, authorityPath string, store CertificateStore) error {
	trust := NewLocalCATrust(authorityPath, filepath.Join(filepath.Dir(authorityPath), ".migration-unused.json"), store).(*localCATrust)
	identity, der, err := trust.currentAuthority(ctx)
	if err != nil {
		return err
	}
	if err := verifyAuthorityPrivateKey(authorityPath, der); err != nil {
		return err
	}
	if err := store.RemoveSHA256(ctx, identity.SHA256); err != nil {
		return fmt.Errorf("helper: remove exact legacy CA: %w", err)
	}
	pkiDir := filepath.Dir(filepath.Dir(filepath.Dir(authorityPath)))
	if filepath.Base(pkiDir) != "pki" {
		return errors.New("helper: refuse to remove CA keys outside a Caddy pki directory")
	}
	if err := os.RemoveAll(pkiDir); err != nil {
		return fmt.Errorf("helper: remove Caddy CA private material: %w", err)
	}
	// 叶子证书及其私钥同样与刚删除的 authority 绑定；若保留它们，Caddy
	// 可能继续为相同域名提供一条由旧中间证书签发的缓存链。
	certificateCache := filepath.Join(filepath.Dir(pkiDir), "certificates")
	if err := os.RemoveAll(certificateCache); err != nil {
		return fmt.Errorf("helper: remove Caddy certificate cache: %w", err)
	}
	return nil
}

// verifyAuthorityPrivateKey prevents an unprivileged user from replacing the
// legacy root.crt with an arbitrary machine-trusted certificate before the
// elevated migration runs. Only an authority whose adjacent private key
// proves ownership may be removed from LocalMachine Root.
func verifyAuthorityPrivateKey(authorityPath string, certDER []byte) error {
	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		return fmt.Errorf("helper: parse CA for private-key proof: %w", err)
	}
	keyPath := filepath.Join(filepath.Dir(authorityPath), "root.key")
	file, err := os.Open(keyPath)
	if err != nil {
		return fmt.Errorf("helper: open Caddy CA private key proof: %w", err)
	}
	keyPEM, readErr := io.ReadAll(io.LimitReader(file, maxCAKeyFileBytes+1))
	closeErr := file.Close()
	if readErr != nil {
		return fmt.Errorf("helper: read Caddy CA private key proof: %w", readErr)
	}
	if closeErr != nil {
		return fmt.Errorf("helper: close Caddy CA private key proof: %w", closeErr)
	}
	if len(keyPEM) > maxCAKeyFileBytes {
		return errors.New("helper: Caddy CA private key proof exceeds size limit")
	}
	block, rest := pem.Decode(keyPEM)
	if block == nil || len(bytes.TrimSpace(rest)) != 0 {
		return errors.New("helper: Caddy CA private key proof must contain exactly one PEM key")
	}
	key, err := parseAuthorityPrivateKey(block)
	if err != nil {
		return err
	}
	signer, ok := key.(crypto.Signer)
	if !ok {
		return errors.New("helper: Caddy CA private key proof is not a signing key")
	}
	certPublic, err := x509.MarshalPKIXPublicKey(cert.PublicKey)
	if err != nil {
		return fmt.Errorf("helper: marshal Caddy CA public key: %w", err)
	}
	keyPublic, err := x509.MarshalPKIXPublicKey(signer.Public())
	if err != nil {
		return fmt.Errorf("helper: marshal Caddy CA private-key public half: %w", err)
	}
	if !bytes.Equal(certPublic, keyPublic) {
		return errors.New("helper: Caddy CA certificate does not match its private key")
	}
	return nil
}

func parseAuthorityPrivateKey(block *pem.Block) (any, error) {
	var key any
	var err error
	switch block.Type {
	case "PRIVATE KEY":
		key, err = x509.ParsePKCS8PrivateKey(block.Bytes)
	case "EC PRIVATE KEY":
		key, err = x509.ParseECPrivateKey(block.Bytes)
	case "RSA PRIVATE KEY":
		key, err = x509.ParsePKCS1PrivateKey(block.Bytes)
	default:
		return nil, fmt.Errorf("helper: unsupported Caddy CA private key PEM type %q", block.Type)
	}
	if err != nil {
		return nil, fmt.Errorf("helper: parse Caddy CA private key proof: %w", err)
	}
	return key, nil
}
