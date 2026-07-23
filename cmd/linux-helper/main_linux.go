//go:build linux

// tunnelboard-linux-helper 是由 pkexec 启动的短生命周期 root 子进程。
// 它不监听 socket、不注册 service，也没有任意命令执行模式。
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/HanZephyr/TunnelBoard/internal/helper"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "package-uninstall" {
		if err := helper.RemoveLinuxPackageSystemEffects(context.Background()); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) < 2 || os.Args[1] != "session" {
		fmt.Fprintln(os.Stderr, "usage: tunnelboard-linux-helper session --session-id ID --authorization-id ID --parent-pid PID --parent-start-time TICKS | package-uninstall")
		os.Exit(2)
	}
	flags := flag.NewFlagSet("session", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	sessionID := flags.String("session-id", "", "")
	authorizationID := flags.String("authorization-id", "", "")
	parentPID := flags.Int("parent-pid", 0, "")
	parentStartTime := flags.Uint64("parent-start-time", 0, "")
	if err := flags.Parse(os.Args[2:]); err != nil || flags.NArg() != 0 || *sessionID == "" || *authorizationID == "" || *parentPID <= 1 || *parentStartTime == 0 {
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(2)
	}
	if err := helper.BindLinuxPrivilegedParent(*parentPID); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	parentAlive := func() error {
		return helper.ValidateLinuxPrivilegedParent(*parentPID, *parentStartTime)
	}
	authorizationActive := func(ctx context.Context) error {
		return helper.ValidateLinuxTemporaryAuthorization(ctx, *authorizationID, *parentPID, *parentStartTime)
	}
	if err := parentAlive(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	effects, err := newEffects(*parentPID)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := helper.ServeLinuxPrivilegedSession(context.Background(), os.Stdin, os.Stdout, helper.LinuxPrivilegedSessionOptions{
		SessionID: *sessionID, Effects: effects, ParentAlive: parentAlive,
		AuthorizationActive: authorizationActive,
	}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newEffects(parentPID int) (helper.LinuxPrivilegedEffects, error) {
	// Root helper 的 concrete effect 构造器仅在 internal/helper 的 Linux build 中存在；
	// 通过这个固定入口避免将路径、刷新命令或发行版判断暴露为 CLI 参数。
	return helper.NewLinuxPrivilegedEffects(parentPID)
}
