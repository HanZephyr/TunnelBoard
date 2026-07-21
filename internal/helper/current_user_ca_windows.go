//go:build windows

package helper

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

type windowsCurrentUserRootStore struct{}

// NewCurrentUserCATrust 返回固定使用当前 Windows 用户 LocalAppData 与
// CurrentUser\Root 的 CA Module；不会访问 LocalMachine\Root，也不会触发 UAC。
func NewCurrentUserCATrust() (LocalCATrust, error) {
	root, err := CurrentUserDataDir()
	if err != nil {
		return nil, err
	}
	authority := filepath.Join(root, "caddy", "pki", "authorities", "local", "root.crt")
	record := filepath.Join(root, "state", "ca-trust.json")
	return NewLocalCATrust(authority, record, windowsCurrentUserRootStore{}), nil
}

// CurrentUserDataDir 是设备本地、不可随 Vault 重定向的当前用户运行目录。
func CurrentUserDataDir() (string, error) {
	base := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
	if base == "" {
		return "", errors.New("helper: LOCALAPPDATA is unavailable")
	}
	return filepath.Join(base, "TunnelBoard"), nil
}

func (windowsCurrentUserRootStore) ContainsSHA256(ctx context.Context, fingerprint string) (bool, error) {
	return visitCurrentUserRoot(ctx, fingerprint, false)
}

func (windowsCurrentUserRootStore) AddDER(ctx context.Context, certDER []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	store, err := openCurrentUserRoot()
	if err != nil {
		return err
	}
	defer windows.CertCloseStore(store, 0)
	if len(certDER) == 0 {
		return errors.New("helper: empty certificate DER")
	}
	cert, err := windows.CertCreateCertificateContext(
		windows.X509_ASN_ENCODING|windows.PKCS_7_ASN_ENCODING,
		&certDER[0], uint32(len(certDER)),
	)
	if err != nil {
		return fmt.Errorf("helper: create certificate context: %w", err)
	}
	defer windows.CertFreeCertificateContext(cert)
	if err := windows.CertAddCertificateContextToStore(store, cert, windows.CERT_STORE_ADD_REPLACE_EXISTING, nil); err != nil {
		return fmt.Errorf("helper: add certificate to CurrentUser Root: %w", err)
	}
	return nil
}

func (windowsCurrentUserRootStore) RemoveSHA256(ctx context.Context, fingerprint string) error {
	_, err := visitCurrentUserRoot(ctx, fingerprint, true)
	return err
}

func openCurrentUserRoot() (windows.Handle, error) {
	name, err := windows.UTF16PtrFromString("ROOT")
	if err != nil {
		return 0, err
	}
	store, err := windows.CertOpenSystemStore(0, name)
	if err != nil {
		return 0, fmt.Errorf("helper: open CurrentUser Root: %w", err)
	}
	return store, nil
}

// visitCurrentUserRoot 逐张比较 DER 的 SHA-256；delete=true 时只删除精确匹配证书。
func visitCurrentUserRoot(ctx context.Context, fingerprint string, deleteMatch bool) (bool, error) {
	if len(fingerprint) != sha256.Size*2 {
		return false, errors.New("helper: invalid certificate fingerprint")
	}
	store, err := openCurrentUserRoot()
	if err != nil {
		return false, err
	}
	defer windows.CertCloseStore(store, 0)

	var previous *windows.CertContext
	for {
		if err := ctx.Err(); err != nil {
			if previous != nil {
				_ = windows.CertFreeCertificateContext(previous)
			}
			return false, err
		}
		cert, enumErr := windows.CertEnumCertificatesInStore(store, previous)
		previous = nil // CertEnumCertificatesInStore 总会释放传入的 previous。
		if enumErr != nil {
			if errors.Is(enumErr, syscall.Errno(windows.CRYPT_E_NOT_FOUND)) {
				return false, nil
			}
			return false, fmt.Errorf("helper: enumerate CurrentUser Root: %w", enumErr)
		}
		der := unsafe.Slice(cert.EncodedCert, cert.Length)
		sum := sha256.Sum256(der)
		if hex.EncodeToString(sum[:]) == fingerprint {
			if deleteMatch {
				// CertDeleteCertificateFromStore 同时释放 cert context。
				if err := windows.CertDeleteCertificateFromStore(cert); err != nil {
					return false, fmt.Errorf("helper: delete certificate from CurrentUser Root: %w", err)
				}
			} else {
				_ = windows.CertFreeCertificateContext(cert)
			}
			return true, nil
		}
		previous = cert
	}
}
