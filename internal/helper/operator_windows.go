//go:build windows

package helper

// NewOperator 返回 Windows 的命名管道客户端（helper 服务经一次 UAC 安装）。
func NewOperator() Operator {
	return NewClient()
}
