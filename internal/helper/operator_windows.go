//go:build windows

package helper

// NewOperator 返回 Windows 会话级 Helper 客户端；首次特权调用触发 UAC，
// 同一应用生命周期内复用，绝不注册或安装持久服务。
func NewOperator() Operator {
	return NewClient()
}
