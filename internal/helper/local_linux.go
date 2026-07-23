//go:build linux

package helper

// NewLocalClient/NewOperator 保留既有调用入口，但 Linux 现在始终使用受限的
// 短生命周期 polkit session，不能再回退到通用 pkexec 命令适配器。
func NewLocalClient() Operator {
	return &linuxSessionOperator{session: newLinuxPrivilegedSessionWithAuthorizer(newLinuxProcessSessionStarter(), newLinuxPolkitAuthorizer())}
}

func NewOperator() Operator { return NewLocalClient() }
