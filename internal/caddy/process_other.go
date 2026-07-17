//go:build !windows

package caddy

import (
	"fmt"
	"os/exec"
	"syscall"
)

// startDetached 以独立会话启动 Caddy（Setsid），主程序退出不影响 Caddy 生命周期。
func startDetached(bin string, args []string, dir string, env []string) error {
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("caddy: start process: %w", err)
	}
	_ = cmd.Process.Release()
	return nil
}
