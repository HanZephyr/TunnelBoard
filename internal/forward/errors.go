package forward

import (
	"errors"
	"strings"

	"golang.org/x/crypto/ssh"
)

// IsTerminalError 判定重连路径上的终态错误：主机密钥核验被拒（ErrHostKeyRejected）
// 或 SSH 认证失败。这两类错误重试不会改变结果，reconnectWithBackoff 命中后立即
// 返回，不进入指数退避。
//
// x/crypto/ssh 未为认证失败提供哨兵错误：握手阶段的认证失败只能匹配其固定错误
// 文本（client_auth.go 中 "unable to authenticate" 前缀），字符串匹配是通行做法；
// *ssh.PartialSuccessError 表示服务端要求追加认证方式，同样视为认证类终态。
func IsTerminalError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrHostKeyRejected) {
		return true
	}
	var partial *ssh.PartialSuccessError
	if errors.As(err, &partial) {
		return true
	}
	return strings.Contains(err.Error(), "unable to authenticate")
}
