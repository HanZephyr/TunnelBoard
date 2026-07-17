//go:build windows

package caddy

import (
	"fmt"
	"os/exec"
	"syscall"
)

// startDetached 以脱离父进程的方式启动 Caddy：主程序退出后 Caddy 由 Stop 显式管理，
// 不随主程序窗口句柄组被终止。
func startDetached(bin string, args []string, dir string, env []string) error {
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | 0x00000008, // DETACHED_PROCESS
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("caddy: start process: %w", err)
	}
	_ = cmd.Process.Release()
	return nil
}
