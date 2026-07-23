//go:build linux

package helper

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
)

// NewLinuxPrivilegedEffects 仅供安装包内的 root helper 进程使用。
// GUI 端只持有受限协议客户端，无法取得该系统副作用实现。
func NewLinuxPrivilegedEffects(parentPID int) (LinuxPrivilegedEffects, error) {
	uidText := strings.TrimSpace(os.Getenv("PKEXEC_UID"))
	if _, err := strconv.ParseUint(uidText, 10, 32); err != nil {
		return nil, fmt.Errorf("helper: pkexec caller identity is unavailable")
	}
	authority, err := linuxCaddyAuthorityForParent(parentPID, uidText)
	if err != nil {
		return nil, err
	}
	return newLinuxSystemEffects(authority)
}

func linuxCaddyAuthorityForParent(parentPID int, uidText string) (string, error) {
	parent, err := user.LookupId(uidText)
	if err != nil || parent.HomeDir == "" {
		return "", fmt.Errorf("helper: resolve Linux pkexec caller home: %w", err)
	}
	configHome := filepath.Join(parent.HomeDir, ".config")
	environment, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(parentPID), "environ"))
	if err != nil {
		return "", fmt.Errorf("helper: read Linux parent environment: %w", err)
	}
	for _, item := range strings.Split(string(environment), "\x00") {
		if value, ok := strings.CutPrefix(item, "XDG_CONFIG_HOME="); ok && filepath.IsAbs(value) {
			configHome = filepath.Clean(value)
		}
	}
	return filepath.Join(configHome, "TunnelBoard", "caddy", "pki", "authorities", "local", "root.crt"), nil
}
