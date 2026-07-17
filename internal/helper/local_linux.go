//go:build linux

package helper

import (
	"fmt"
	"os/exec"
	"strings"
)

const linuxCAName = "tunnelboard-local-ca.crt"
const linuxCAPath = "/usr/local/share/ca-certificates/" + linuxCAName

// elevatedCopy 经 pkexec 把 src 覆盖到 dst（每次操作触发一次系统授权）。
func elevatedCopy(src, dst string) error {
	out, err := exec.Command("pkexec", "cp", src, dst).CombinedOutput()
	if err != nil {
		return fmt.Errorf("helper: elevated copy: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// platformTrustCA 安装 TunnelBoard 本地 CA 并刷新系统信任库（需管理员）。
func platformTrustCA(certPath string) error {
	cmd := fmt.Sprintf("cp %s %s && update-ca-certificates", certPath, linuxCAPath)
	out, err := exec.Command("pkexec", "sh", "-c", cmd).CombinedOutput()
	if err != nil {
		return fmt.Errorf("helper: trust local ca: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// untrustLocalCA 删除该 CA 并刷新系统信任库（需管理员）。
func (c *localElevatedClient) untrustLocalCA(sha256 string) error {
	_ = sha256 // 文件名固定，指纹仅用于请求校验
	out, err := exec.Command("pkexec", "sh", "-c", "rm -f "+linuxCAPath+" && update-ca-certificates").CombinedOutput()
	if err != nil {
		return fmt.Errorf("helper: untrust local ca: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
