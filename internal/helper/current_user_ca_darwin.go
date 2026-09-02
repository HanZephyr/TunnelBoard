//go:build darwin

package helper

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// loginKeychain 是当前用户的 login keychain 相对名。旧版本曾把 CA 写到这里，
// Chrome 不把 login keychain 当作 HTTPS 信任锚；现在只在安装到 System
// keychain 成功后尽力清掉这份副本，避免钥匙串里留下重复项。
const loginKeychain = "login.keychain-db"

func CurrentUserDataDir() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "TunnelBoard"), nil
}

func NewCurrentUserCATrust() (LocalCATrust, error) {
	root, err := CurrentUserDataDir()
	if err != nil {
		return nil, err
	}
	return NewCurrentUserCATrustAt(root), nil
}

// NewCurrentUserCATrustAt 将本地 Caddy 的 CA 经管理员授权写入 System keychain
//（-r trustRoot）。这与 Windows CurrentUser\Root 不同：Chrome 在 macOS 上只
// 稳定信任系统钥匙串里的根证书，因此必须提权。
func NewCurrentUserCATrustAt(root string) LocalCATrust {
	privilege, err := newNativePlatformPrivilege()
	if err != nil {
		privilege = unavailablePrivilege{err: err}
	}
	return newDarwinSystemCATrust(root, privilege, execCommandRunner{})
}

func newDarwinSystemCATrust(root string, privilege PlatformPrivilege, runner CommandRunner) LocalCATrust {
	if runner == nil {
		runner = execCommandRunner{}
	}
	authority := filepath.Join(root, "caddy", "pki", "authorities", "local", "root.crt")
	record := filepath.Join(root, "state", "ca-trust.json")
	return NewLocalCATrust(authority, record, darwinSystemKeychainStore{privilege: privilege, runner: runner})
}

// darwinSystemKeychainStore 只读枚举不提权；写入/删除 System keychain 必须
// 穿过 PlatformPrivilege（osascript 管理员授权）。公开 API 仍不暴露路径或命令。
type darwinSystemKeychainStore struct {
	privilege PlatformPrivilege
	runner    CommandRunner
}

func (s darwinSystemKeychainStore) ContainsSHA256(ctx context.Context, fingerprint string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	entries, err := listKeychainCertificates(ctx, s.runner, darwinSystemKeychain)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if entry.sha256 == fingerprint {
			return true, nil
		}
	}
	return false, nil
}

func (s darwinSystemKeychainStore) AddDER(ctx context.Context, certDER []byte) error {
	if s.privilege == nil {
		return fmt.Errorf("helper: macOS CA trust requires administrator authorization")
	}
	if err := s.privilege.TrustLocalCA(ctx, certDER); err != nil {
		return err
	}
	sum := sha256.Sum256(certDER)
	_ = removeUnprivilegedKeychainSHA256(ctx, s.runner, loginKeychain, hex.EncodeToString(sum[:]))
	return nil
}

func (s darwinSystemKeychainStore) RemoveSHA256(ctx context.Context, fingerprint string) error {
	if s.privilege == nil {
		return fmt.Errorf("helper: macOS CA trust requires administrator authorization")
	}
	if err := s.privilege.UntrustLocalCA(ctx, fingerprint); err != nil {
		return err
	}
	_ = removeUnprivilegedKeychainSHA256(ctx, s.runner, loginKeychain, fingerprint)
	return nil
}

func removeUnprivilegedKeychainSHA256(ctx context.Context, runner CommandRunner, keychain, fingerprint string) error {
	entries, err := listKeychainCertificates(ctx, runner, keychain)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.sha256 != fingerprint {
			continue
		}
		if err := validateSHA1Fingerprint(entry.sha1); err != nil {
			return err
		}
		out, err := runner.Run(ctx, "/usr/bin/security", "delete-certificate", "-Z", entry.sha1, keychain)
		if err != nil {
			return fmt.Errorf("helper: delete CA from %s: %w: %s", keychain, err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	return nil
}
