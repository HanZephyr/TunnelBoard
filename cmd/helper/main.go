//go:build windows

// tunnelboard-helper 是 TunnelBoard 的会话级受限特权辅助进程：首次 hosts 操作
// 经 UAC 启动，在父应用退出、IPC 断开或收到 shutdown 后退出，绝不注册 Windows 服务。
package main

import (
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
