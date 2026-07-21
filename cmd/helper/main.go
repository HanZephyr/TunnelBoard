//go:build windows

// tunnelboard-helper 是 TunnelBoard 的会话级受限特权辅助进程：首次 hosts 操作
// 经 UAC 启动，在父应用退出、IPC 断开或收到 shutdown 后退出，绝不注册 Windows 服务。
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/HanZephyr/TunnelBoard/internal/helper"
)

func main() {
	if handled, err := runSelfCheck(os.Args[1:], os.Stdout); handled {
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if handled, err := runLegacyCleanup(os.Args[1:], helper.RemoveLegacyInstallation); handled {
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if handled, err := runCurrentUserCleanup(os.Args[1:], func() error {
		return helper.RemoveCurrentUserCAAndPrivateKeys(context.Background())
	}); handled {
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	sessionHelper := flag.Bool("session-helper", false, "run as an elevated application-session helper")
	pipePath := flag.String("pipe", "", "parent-created random named pipe")
	parentPID := flag.Uint("parent-pid", 0, "parent TunnelBoard process id")
	protocol := flag.String("protocol", "", "exact session protocol version")
	flag.Parse()

	if !*sessionHelper {
		fmt.Fprintln(os.Stderr, "tunnelboard-helper must be started by TunnelBoard with --session-helper")
		os.Exit(2)
	}
	helper.SetupEventLogging()
	hostsPath := filepath.Join(os.Getenv("SystemRoot"), `System32\drivers\etc\hosts`)
	err := helper.RunSessionHelper(helper.SessionHelperOptions{
		PipePath:    *pipePath,
		ParentPID:   uint32(*parentPID),
		Protocol:    *protocol,
		Environment: helper.ProductionEnvironment(hostsPath, helper.SessionProtocolVersion),
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// runLegacyCleanup 只接受安装器使用的无参数固定迁移命令。它不会安装、启动
// 或重配任何服务，也不接受服务名、路径等调用方输入。
func runLegacyCleanup(args []string, cleanup func() error) (bool, error) {
	if len(args) != 1 || args[0] != "--cleanup-legacy-service" {
		return false, nil
	}
	return true, cleanup()
}

func runCurrentUserCleanup(args []string, cleanup func() error) (bool, error) {
	if len(args) != 1 || args[0] != "--cleanup-current-user-ca" {
		return false, nil
	}
	return true, cleanup()
}
