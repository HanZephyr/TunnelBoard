//go:build windows

package helper

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

func parentProcessAppData(parentPID uint32) (string, error) {
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, parentPID)
	if err != nil {
		return "", fmt.Errorf("helper: open parent for profile resolution: %w", err)
	}
	defer windows.CloseHandle(process)
	var token windows.Token
	if err := windows.OpenProcessToken(process, windows.TOKEN_QUERY, &token); err != nil {
		return "", fmt.Errorf("helper: open parent token: %w", err)
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return "", fmt.Errorf("helper: resolve parent SID: %w", err)
	}
	keyPath := `SOFTWARE\Microsoft\Windows NT\CurrentVersion\ProfileList\` + user.User.Sid.String()
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, keyPath, registry.QUERY_VALUE)
	if err != nil {
		return "", fmt.Errorf("helper: open parent profile registry: %w", err)
	}
	defer key.Close()
	profile, _, err := key.GetStringValue("ProfileImagePath")
	if err != nil {
		return "", fmt.Errorf("helper: read parent profile path: %w", err)
	}
	profile = os.ExpandEnv(strings.TrimSpace(profile))
	if profile == "" || !filepath.IsAbs(profile) {
		return "", errors.New("helper: parent profile path is invalid")
	}
	return filepath.Join(profile, "AppData", "Roaming"), nil
}

// RemoveLegacyMachineCA 从已解析的原始用户 APPDATA 读取旧版默认目录及其
// config.root 重定向，并从 LocalMachine Root 删除精确 DER。
func RemoveLegacyMachineCA(ctx context.Context) error {
	return RemoveLegacyMachineCAForAppData(ctx, os.Getenv("APPDATA"))
}

func RemoveLegacyMachineCAForAppData(ctx context.Context, appData string) error {
	if appData == "" {
		return errors.New("helper: APPDATA is unavailable for legacy CA cleanup")
	}
	authorities, err := legacyAuthorityPaths(appData)
	if err != nil {
		return err
	}
	for _, authority := range authorities {
		if _, err := os.Stat(authority); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return fmt.Errorf("helper: inspect legacy Caddy CA: %w", err)
		}
		if err := removeCAAuthorityAndKeys(ctx, authority, windowsLocalMachineRootStore{}); err != nil {
			return err
		}
	}
	return nil
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
