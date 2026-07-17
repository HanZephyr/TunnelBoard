//go:build darwin

package helper

import (
	"fmt"
	"os/exec"
	"strings"
)

// elevatedCopy 经 AppleScript 以管理员权限把 src 覆盖到 dst（每次操作触发一次系统授权）。
func elevatedCopy(src, dst string) error {
	script := fmt.Sprintf(`do shell script "cp %s %s" with administrator privileges`, shellQuote(src), shellQuote(dst))
	out, err := exec.Command("osascript", "-e", script).CombinedOutput()
	if err != nil {
		return fmt.Errorf("helper: elevated copy: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// platformTrustCA 把 TunnelBoard 本地 CA 加入系统钥匙串信任（需管理员）。
func platformTrustCA(certPath string) error {
	script := fmt.Sprintf(`do shell script "security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain %s" with administrator privileges`, shellQuote(certPath))
	out, err := exec.Command("osascript", "-e", script).CombinedOutput()
	if err != nil {
		return fmt.Errorf("helper: trust local ca: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// untrustLocalCA 从系统钥匙串删除该 CA（需管理员）。
func (c *localElevatedClient) untrustLocalCA(sha256 string) error {
	script := fmt.Sprintf(`do shell script "security delete-certificate -Z %s /Library/Keychains/System.keychain" with administrator privileges`, sha256)
	out, err := exec.Command("osascript", "-e", script).CombinedOutput()
	if err != nil {
		return fmt.Errorf("helper: untrust local ca: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// shellQuote 包装单引号并转义内嵌单引号。
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
