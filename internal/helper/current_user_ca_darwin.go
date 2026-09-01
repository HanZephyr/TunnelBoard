//go:build darwin

package helper

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// loginKeychain 是当前用户的 login keychain 相对名；/usr/bin/security
// 会按默认 keychain 搜索路径解析到 ~/Library/Keychains/login.keychain-db。
// 当前用户域的操作不需要 root，也不需要 osascript 提权弹窗。
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

// NewCurrentUserCATrustAt 将本地 Caddy 的 CA 信任写入当前用户 login keychain，
// 并信任为根证书（-r trustRoot），与 Windows CurrentUser\Root 语义一致。
func NewCurrentUserCATrustAt(root string) LocalCATrust {
	authority := filepath.Join(root, "caddy", "pki", "authorities", "local", "root.crt")
	record := filepath.Join(root, "state", "ca-trust.json")
	return NewLocalCATrust(authority, record, darwinLoginKeychainStore{})
}

type darwinLoginKeychainStore struct{}

func (darwinLoginKeychainStore) ContainsSHA256(ctx context.Context, fingerprint string) (bool, error) {
	entries, err := listLoginKeychainCertificates(ctx)
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

func (darwinLoginKeychainStore) AddDER(ctx context.Context, certDER []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	tmp, err := os.CreateTemp("", "tunnelboard-ca-*.pem")
	if err != nil {
		return fmt.Errorf("helper: create temp CA file: %w", err)
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("helper: chmod temp CA file: %w", err)
	}
	if err := pem.Encode(tmp, &pem.Block{Type: "CERTIFICATE", Bytes: certDER}); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("helper: write temp CA PEM: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("helper: close temp CA file: %w", err)
	}
	out, err := exec.CommandContext(ctx, "/usr/bin/security",
		"add-trusted-cert", "-d", "-r", "trustRoot", "-k", loginKeychain, name).CombinedOutput()
	if err != nil {
		return fmt.Errorf("helper: add CA to login keychain: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (darwinLoginKeychainStore) RemoveSHA256(ctx context.Context, fingerprint string) error {
	entries, err := listLoginKeychainCertificates(ctx)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.sha256 == fingerprint {
			out, err := exec.CommandContext(ctx, "/usr/bin/security",
				"delete-certificate", "-Z", entry.sha1, loginKeychain).CombinedOutput()
			if err != nil {
				return fmt.Errorf("helper: delete CA from login keychain: %w: %s", err, strings.TrimSpace(string(out)))
			}
			return nil
		}
	}
	return nil
}

type keychainCertificate struct {
	sha1   string
	sha256 string
}

// listLoginKeychainCertificates 枚举 login keychain 中全部证书。
// `security find-certificate -a -p -Z` 输出形如：
//
//	SHA-1 hash: 1234abcd...
//	-----BEGIN CERTIFICATE-----
//	...
//	-----END CERTIFICATE-----
//
// 我们保留每张证书的 SHA-1（delete-certificate -Z 需要）并现场重算 SHA-256
// （接口指纹按 SHA-256 匹配）。
func listLoginKeychainCertificates(ctx context.Context) ([]keychainCertificate, error) {
	out, err := exec.CommandContext(ctx, "/usr/bin/security",
		"find-certificate", "-a", "-p", "-Z", loginKeychain).Output()
	if err != nil {
		return nil, fmt.Errorf("helper: enumerate login keychain: %w: %s", err, strings.TrimSpace(string(out)))
	}
	var (
		entries []keychainCertificate
		sha1Hex string
		pemBuf  strings.Builder
	)
	flush := func() {
		if pemBuf.Len() == 0 {
			return
		}
		block, _ := pem.Decode([]byte(pemBuf.String()))
		if block != nil && block.Type == "CERTIFICATE" {
			sum := sha256.Sum256(block.Bytes)
			entries = append(entries, keychainCertificate{
				sha1:   sha1Hex,
				sha256: hex.EncodeToString(sum[:]),
			})
		}
		pemBuf.Reset()
		sha1Hex = ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if rest, found := strings.CutPrefix(line, "SHA-1 hash:"); found {
			flush()
			sha1Hex = strings.TrimSpace(rest)
			continue
		}
		pemBuf.WriteString(line)
		pemBuf.WriteString("\n")
	}
	flush()
	return entries, nil
}
