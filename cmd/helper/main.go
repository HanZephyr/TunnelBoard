//go:build windows

// tunnelboard-helper 是 TunnelBoard 的受限特权辅助进程：以 Windows 服务运行（SYSTEM），
// 只执行受托管 hosts 写入与本地 CA 信任操作（白名单见 internal/helper/protocol.go）。
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/HanZephyr/TunnelBoard/internal/helper"
)

// version 是 helper 协议/行为版本，主程序 EnsureInstalled 经 ping 校验。
const version = "0.1.0"

func main() {
	install := flag.Bool("install", false, "install and start the Windows service (requires elevation)")
	uninstall := flag.Bool("uninstall", false, "stop and remove the Windows service (requires elevation)")
	serve := flag.Bool("serve", false, "run the helper service (used by SCM)")
	owner := flag.String("owner", "", "SID of the installing (unprivileged) user, used for the pipe ACL")
	flag.Parse()

	var err error
	switch {
	case *install:
		ownerSID := strings.TrimSpace(*owner)
		if ownerSID == "" {
			// 直接以管理员身份运行 -install 时退化为当前用户（管理员）SID。
			if ownerSID, err = helper.CurrentUserSID(); err != nil {
				break
			}
		}
		err = helper.InstallService(ownerSID)
	case *uninstall:
		err = helper.UninstallService()
	default:
		_ = serve // 服务上下文由 RunServiceMain 自行检测；-serve 仅为语义标记
		hostsPath := filepath.Join(os.Getenv("SystemRoot"), `System32\drivers\etc\hosts`)
		err = helper.RunServiceMain(helper.ProductionEnvironment(hostsPath, version))
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
