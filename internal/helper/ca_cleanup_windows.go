//go:build windows

package helper

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func legacyAuthorityPath() (string, error) {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		return "", errors.New("helper: APPDATA is unavailable for legacy CA cleanup")
	}
	return filepath.Join(appData, "TunnelBoard", "caddy", "pki", "authorities", "local", "root.crt"), nil
}

// RemoveLegacyMachineCA 只读取旧版默认每用户 Caddy authority，并从
// LocalMachine Root 删除精确 DER。它不接受路径、DER 或指纹参数。
func RemoveLegacyMachineCA(ctx context.Context) error {
	authority, err := legacyAuthorityPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(authority); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("helper: inspect legacy Caddy CA: %w", err)
	}
	return removeCAAuthorityAndKeys(ctx, authority, windowsLocalMachineRootStore{})
}

// RemoveCurrentUserCAAndPrivateKeys 是卸载器的固定无参数清理入口。
func RemoveCurrentUserCAAndPrivateKeys(ctx context.Context) error {
	root, err := CurrentUserDataDir()
	if err != nil {
		return err
	}
	trust := NewCurrentUserCATrustAt(root)
	if err := trust.RemoveCurrentCaddyCA(ctx); err != nil {
		return err
	}
	authority := filepath.Join(root, "caddy", "pki", "authorities", "local", "root.crt")
	if _, err := os.Stat(authority); errors.Is(err, os.ErrNotExist) {
		return os.RemoveAll(filepath.Join(root, "caddy", "pki"))
	} else if err != nil {
		return err
	}
	return removeCAAuthorityAndKeys(ctx, authority, windowsCurrentUserRootStore{})
}
