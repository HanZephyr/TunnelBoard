//go:build darwin

package helper

import "context"

// macOS 的 hosts 与 CA 共用同一个 PlatformPrivilege：二者都要改系统级状态，
// 每次操作单独弹出管理员授权。CA 不能走 login keychain：Chrome 不把它当作
// HTTPS 信任锚；安装/撤销必须写入 /Library/Keychains/System.keychain。
func newPlatformIntegration(dataDir string) (Operator, LocalCATrust, func(context.Context) error, error) {
	privilege, err := newNativePlatformPrivilege()
	if err != nil {
		return nil, nil, nil, err
	}
	operator := &localElevatedClient{privilege: privilege}
	trust := newDarwinSystemCATrust(dataDir, privilege, execCommandRunner{})
	return operator, trust, nil, nil
}
