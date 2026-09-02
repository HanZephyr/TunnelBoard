// Package loopbackhttps 是本机 HTTPS 入口地址的单一来源：浏览器始终访问 443，
// Caddy 实际 bind 的端口按平台选择。macOS 上特权端口不能由普通用户进程占用，
// 因此 Caddy 听在未特权回环端口，443 由一次性管理员授权安装的 pf 转发到达。
package loopbackhttps

import (
	"fmt"
	"runtime"
)

const (
	PublicPort = 443
	// DarwinUnprivilegedPort 是 macOS 上 Caddy 实际绑定的回环端口。
	// 避开 8443 等常见备用 HTTPS 端口，减少与用户 Forward 冲突。
	DarwinUnprivilegedPort = 17443
)

// BindPort 返回 Caddy HTTP 服务器应 Listen 的 TCP 端口。
func BindPort() int {
	if runtime.GOOS == "darwin" {
		return DarwinUnprivilegedPort
	}
	return PublicPort
}

// BindAddr 返回 Caddy 配置里的 listen 地址（仅回环）。
func BindAddr() string {
	return fmt.Sprintf("127.0.0.1:%d", BindPort())
}
