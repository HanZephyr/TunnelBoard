package helper

import "context"

// NewPlatformIntegration 组装一个平台的 hosts 特权调用与 CA 信任模块。
// Linux 必须共享同一个短生命周期 polkit 会话，防止 hosts 与 CA 分别获得可复用授权。
func NewPlatformIntegration(dataDir string) (Operator, LocalCATrust, func(context.Context) error, error) {
	return newPlatformIntegration(dataDir)
}
