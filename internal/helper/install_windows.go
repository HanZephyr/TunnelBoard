//go:build windows

package helper

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"time"
)

// HelperBinaryName 是与主程序同目录交付的 helper 可执行文件名。
const HelperBinaryName = "tunnelboard-helper.exe"

// HelperBinaryEnvVar 允许在开发/测试中覆盖 helper 二进制路径。
const HelperBinaryEnvVar = "TUNNELBOARD_HELPER_PATH"

// EnsureInstalled 确认 helper 服务可用：先 Ping；不可达则经 UAC 提升安装并轮询等待上线。
// 主程序本身绝不以管理员运行，提升只发生在 helper 的 -install 子进程。
func (c *Client) EnsureInstalled() error {
	if _, err := c.Ping(); err == nil {
		return nil
	}
	if err := elevateInstall(); err != nil {
		return err
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		if _, err := c.Ping(); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("helper: service did not come up within 30s after install")
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// elevateInstall 以 runas 提升执行 helper 的 -install（触发一次 UAC 并等待结束）。
// 当前（普通权限）用户的 SID 显式传给提权进程：管道 DACL 按它授权，
// 覆盖标准用户借管理员凭据安装的场景（提权进程自身账户并非安装者）。
func elevateInstall() error {
	exe, err := helperBinaryPath()
	if err != nil {
		return err
	}
	u, err := user.Current()
	if err != nil {
		return fmt.Errorf("helper: resolve current user: %w", err)
	}
	cmd := exec.Command(
		"powershell", "-NoProfile", "-WindowStyle", "Hidden", "-Command",
		"Start-Process", "-Verb", "RunAs", "-Wait", "-FilePath", exe, "-ArgumentList", "-install", "-owner", u.Uid,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("helper: elevated install failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// helperBinaryPath 定位 helper 二进制：环境变量覆盖优先，否则与主程序同目录。
func helperBinaryPath() (string, error) {
	if p := strings.TrimSpace(os.Getenv(HelperBinaryEnvVar)); p != "" {
		if _, err := os.Stat(p); err != nil {
			return "", fmt.Errorf("helper: %s=%s not usable: %w", HelperBinaryEnvVar, p, err)
		}
		return p, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("helper: resolve executable: %w", err)
	}
	p := filepath.Join(filepath.Dir(exe), HelperBinaryName)
	if _, err := os.Stat(p); err != nil {
		return "", fmt.Errorf("helper: %s not found beside app: %w", p, err)
	}
	return p, nil
}
