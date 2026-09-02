//go:build linux

package helper

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func newPlatformIntegration(dataDir string) (Operator, LocalCATrust, func(context.Context) error, error) {
	trustStore, err := currentLinuxTrustStore()
	if err != nil {
		return nil, nil, nil, err
	}
	session := newLinuxPrivilegedSessionWithAuthorizer(newLinuxProcessSessionStarter(), newLinuxPolkitAuthorizer())
	authority := filepath.Join(dataDir, "caddy", "pki", "authorities", "local", "root.crt")
	record := filepath.Join(dataDir, "state", "ca-trust.json")
	store := linuxSystemCertificateStore{session: session, trustStore: trustStore}
	operator := &linuxSessionOperator{session: session}
	return operator, NewLocalCATrust(authority, record, store), session.Close, nil
}

func currentLinuxTrustStore() (linuxTrustStore, error) {
	content, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return linuxTrustStore{}, fmt.Errorf("helper: read /etc/os-release: %w", err)
	}
	return linuxTrustStoreFromOSRelease(content)
}

type linuxSessionOperator struct{ session *linuxPrivilegedSession }

func (o *linuxSessionOperator) Ping() (string, error) { return "linux-polkit-session", nil }

func (o *linuxSessionOperator) EnsureInstalled() error { return ensureLinuxPrivilegedPayload() }

func (o *linuxSessionOperator) EnsureLoopbackHTTPSRedirect(context.Context) error { return nil }

func (o *linuxSessionOperator) Call(request Request) (Response, error) {
	if o.session == nil {
		return Response{}, errors.New("helper: Linux privileged session is unavailable")
	}
	return o.session.Call(context.Background(), request)
}

func (o *linuxSessionOperator) Close(ctx context.Context) error {
	if o.session == nil {
		return nil
	}
	return o.session.Close(ctx)
}

// linuxSystemCertificateStore 只读取发行版固定 CA 文件；写入和删除一律穿过共享
// 受限会话，确保 LocalCATrust 的公开 API 仍不暴露任意证书路径或 root 命令。
type linuxSystemCertificateStore struct {
	session    *linuxPrivilegedSession
	trustStore linuxTrustStore
}

func (s linuxSystemCertificateStore) ContainsSHA256(ctx context.Context, fingerprint string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := validateFingerprint(fingerprint); err != nil {
		return false, err
	}
	content, err := os.ReadFile(s.trustStore.caPath)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("helper: read managed Linux CA: %w", err)
	}
	block, rest := pem.Decode(content)
	if block == nil || block.Type != "CERTIFICATE" || len(strings.TrimSpace(string(rest))) != 0 {
		return false, errors.New("helper: managed Linux CA file is not exactly one PEM certificate")
	}
	sum := sha256.Sum256(block.Bytes)
	return hex.EncodeToString(sum[:]) == fingerprint, nil
}

func (s linuxSystemCertificateStore) AddDER(ctx context.Context, certDER []byte) error {
	if s.session == nil {
		return errors.New("helper: Linux privileged session is unavailable")
	}
	return s.session.TrustCurrentCaddyCA(ctx, certDER)
}

func (s linuxSystemCertificateStore) RemoveSHA256(ctx context.Context, fingerprint string) error {
	if s.session == nil {
		return errors.New("helper: Linux privileged session is unavailable")
	}
	return s.session.UntrustCurrentCaddyCA(ctx, fingerprint)
}
