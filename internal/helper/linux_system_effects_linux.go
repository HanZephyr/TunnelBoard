//go:build linux

package helper

import (
	"bytes"
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

// linuxSystemEffects 是 root 会话唯一的系统副作用实现。所有路径、刷新程序和
// 参数均由 os-release 解析结果固定，绝不从 GUI 协议读取。
type linuxSystemEffects struct {
	trustStore    linuxTrustStore
	authorityPath string
}

func newLinuxSystemEffects(authorityPath string) (*linuxSystemEffects, error) {
	content, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return nil, fmt.Errorf("helper: read /etc/os-release: %w", err)
	}
	store, err := linuxTrustStoreFromOSRelease(content)
	if err != nil {
		return nil, err
	}
	if !filepath.IsAbs(authorityPath) {
		return nil, errors.New("helper: Linux Caddy authority path must be absolute")
	}
	return &linuxSystemEffects{trustStore: store, authorityPath: filepath.Clean(authorityPath)}, nil
}

func newLinuxSystemMaintenanceEffects() (*linuxSystemEffects, error) {
	content, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return nil, fmt.Errorf("helper: read /etc/os-release: %w", err)
	}
	store, err := linuxTrustStoreFromOSRelease(content)
	if err != nil {
		return nil, err
	}
	return &linuxSystemEffects{trustStore: store}, nil
}

func (e *linuxSystemEffects) ApplyManagedHosts(_ context.Context, request Request) (Response, error) {
	switch request.Op {
	case OpApplyManagedHosts:
		if err := ValidateRequest(request); err != nil {
			return Response{}, err
		}
		digest, err := WriteManagedHostsTransaction(SystemHostsPath(), request.Hosts, request.TransactionID, request.ExpectedManagedDigest)
		if err != nil {
			return Response{OK: false, ManagedDigest: digest, Error: err.Error()}, nil
		}
		return Response{OK: true, ManagedDigest: digest}, nil
	case OpRemoveManagedHosts:
		if err := WriteManagedHosts(SystemHostsPath(), nil); err != nil {
			return Response{OK: false, Error: err.Error()}, nil
		}
		return Response{OK: true, ManagedDigest: ManagedEntriesDigest(nil)}, nil
	default:
		return Response{}, fmt.Errorf("helper: unsupported Linux hosts operation %q", request.Op)
	}
}

func (e *linuxSystemEffects) TrustCurrentCaddyCA(_ context.Context, certDER []byte) error {
	currentDER, err := e.currentCaddyAuthority()
	if err != nil {
		return err
	}
	if !bytes.Equal(certDER, currentDER) {
		return errors.New("helper: refusing to trust a CA other than the current TunnelBoard Caddy authority")
	}
	sum := sha256.Sum256(certDER)
	if err := ValidateLocalCA(certDER, hex.EncodeToString(sum[:])); err != nil {
		return err
	}
	content := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	if err := writeLinuxCAFile(e.trustStore.caPath, content); err != nil {
		return err
	}
	if err := e.refreshTrustStore(); err != nil {
		removeErr := e.removeExactCA(hex.EncodeToString(sum[:]))
		refreshErr := e.refreshTrustStore()
		return errors.Join(fmt.Errorf("helper: refresh Linux CA trust after install: %w", err), compensationError(removeErr, refreshErr))
	}
	return nil
}

func (e *linuxSystemEffects) currentCaddyAuthority() ([]byte, error) {
	content, err := os.ReadFile(e.authorityPath)
	if err != nil {
		return nil, fmt.Errorf("helper: read current Linux Caddy authority: %w", err)
	}
	block, rest := pem.Decode(content)
	if block == nil || block.Type != "CERTIFICATE" || len(strings.TrimSpace(string(rest))) != 0 {
		return nil, errors.New("helper: current Linux Caddy authority must contain exactly one PEM certificate")
	}
	sum := sha256.Sum256(block.Bytes)
	if err := ValidateLocalCA(block.Bytes, hex.EncodeToString(sum[:])); err != nil {
		return nil, err
	}
	if err := verifyAuthorityPrivateKey(e.authorityPath, block.Bytes); err != nil {
		return nil, err
	}
	return block.Bytes, nil
}

func (e *linuxSystemEffects) UntrustCurrentCaddyCA(_ context.Context, fingerprint string) error {
	if err := validateFingerprint(fingerprint); err != nil {
		return err
	}
	if err := e.removeExactCA(fingerprint); err != nil {
		return err
	}
	return e.refreshTrustStore()
}

// RemoveLinuxPackageSystemEffects 是 deb/rpm maintainer script 的固定清理入口。
// 它不读取或删除用户 Vault、密钥、备份和日志；只清理由包名钉死的系统 hosts 区块
// 与 CA 文件。包管理器已经以 root 执行，故不经过用户会话和 polkit。
func RemoveLinuxPackageSystemEffects(ctx context.Context) error {
	if os.Geteuid() != 0 {
		return errors.New("helper: Linux package cleanup requires root")
	}
	effects, err := newLinuxSystemMaintenanceEffects()
	if err != nil {
		return err
	}
	if _, err := effects.ApplyManagedHosts(ctx, Request{Op: OpRemoveManagedHosts}); err != nil {
		return err
	}
	return effects.removeManagedCAForUninstall()
}

func (e *linuxSystemEffects) removeManagedCAForUninstall() error {
	content, err := os.ReadFile(e.trustStore.caPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("helper: read package-managed Linux CA: %w", err)
	}
	block, rest := pem.Decode(content)
	if block == nil || block.Type != "CERTIFICATE" || len(strings.TrimSpace(string(rest))) != 0 {
		return errors.New("helper: refuse to remove malformed package-managed Linux CA")
	}
	sum := sha256.Sum256(block.Bytes)
	if err := ValidateLocalCA(block.Bytes, hex.EncodeToString(sum[:])); err != nil {
		return fmt.Errorf("helper: refuse to remove invalid package-managed Linux CA: %w", err)
	}
	if err := os.Remove(e.trustStore.caPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("helper: remove package-managed Linux CA: %w", err)
	}
	return e.refreshTrustStore()
}

func (e *linuxSystemEffects) removeExactCA(fingerprint string) error {
	content, err := os.ReadFile(e.trustStore.caPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("helper: read managed Linux CA: %w", err)
	}
	block, rest := pem.Decode(content)
	if block == nil || block.Type != "CERTIFICATE" || len(strings.TrimSpace(string(rest))) != 0 {
		return errors.New("helper: managed Linux CA file is not exactly one PEM certificate")
	}
	sum := sha256.Sum256(block.Bytes)
	if hex.EncodeToString(sum[:]) != fingerprint {
		return errors.New("helper: refusing to remove Linux CA with a different fingerprint")
	}
	if err := os.Remove(e.trustStore.caPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("helper: remove managed Linux CA: %w", err)
	}
	return nil
}

func (e *linuxSystemEffects) refreshTrustStore() error {
	if _, err := os.Stat(e.trustStore.refreshExecutable); err != nil {
		return fmt.Errorf("helper: Linux trust refresh program unavailable: %w", err)
	}
	_, err := (execCommandRunner{}).Run(context.Background(), e.trustStore.refreshExecutable, e.trustStore.refreshArgs...)
	if err != nil {
		return fmt.Errorf("helper: refresh Linux system trust: %w", err)
	}
	return nil
}

func writeLinuxCAFile(path string, content []byte) error {
	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, ".tunnelboard-ca-*.tmp")
	if err != nil {
		return fmt.Errorf("helper: create managed Linux CA temp: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o644); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(content); err != nil {
		_ = temp.Close()
		return fmt.Errorf("helper: write managed Linux CA: %w", err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("helper: replace managed Linux CA: %w", err)
	}
	return nil
}
