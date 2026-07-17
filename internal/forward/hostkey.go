package forward

import (
	"errors"
	"fmt"
	"net"

	"golang.org/x/crypto/ssh"
)

// ErrHostKeyRejected 表示主机密钥核验被 verifier 拒绝（未知指纹或指纹变化）。
// 重连路径以此判定终态错误：指纹问题重试不会改变结果，必须停止重连。
var ErrHostKeyRejected = errors.New("forward: host key rejected")

// HostKeyVerifier 在 SSH 握手时核验服务器主机密钥；返回 nil 放行，非 nil 阻断握手。
// 实现由上层以 Vault 指纹库提供：未知指纹与指纹变化都必须返回阻断错误。
type HostKeyVerifier func(host string, port int, key ssh.PublicKey) error

// makeHostKeyCallback 把 HostKeyVerifier 适配为 ssh.HostKeyCallback。闭包捕获配置侧的
// host/port（多跳链逐跳不同），只向 verifier 透传 ssh.PublicKey；指纹计算
// （ssh.FingerprintSHA256）由上层实现负责。
// verifier 为 nil 时返回错误：失败封闭，不允许无校验拨号。
// verifier 拒绝时以 ErrHostKeyRejected 包装原错误（多 %w 保留两条 errors.Is 链）。
func makeHostKeyCallback(host string, port int, verifier HostKeyVerifier) (ssh.HostKeyCallback, error) {
	if verifier == nil {
		return nil, fmt.Errorf("host key verifier is required")
	}
	return func(_ string, _ net.Addr, key ssh.PublicKey) error {
		if err := verifier(host, port, key); err != nil {
			return fmt.Errorf("%w: %w", ErrHostKeyRejected, err)
		}
		return nil
	}, nil
}
