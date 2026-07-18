package helper

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"fmt"
)

// ValidateLocalCA 校验待信任的证书是本工具可用的本地根 CA，四个条件缺一不可：
// DER 可解析为 X.509 证书；DER 的 SHA-256 与声明值（小写十六进制）一致；
// IsCA 且 BasicConstraintsValid；自签名（根 CA 形态）。
//
// 刻意不做 CN 匹配：本地 CA 由内置 Caddy 生成，CN 为 "Caddy Local Authority"——
// 不可配置；且任何 CN 字符串都可伪造，不构成安全边界。
// 真正的边界是：管道访问控制（仅安装者）、指纹一致、自签 CA 形态、以及 helper
// 只删除自己信任过的那张 CA（见 trusted-ca 记录）。
func ValidateLocalCA(certDER []byte, declaredSHA256 string) error {
	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		return fmt.Errorf("helper: cannot parse certificate DER: %w", err)
	}
	sum := sha256.Sum256(certDER)
	if got := hex.EncodeToString(sum[:]); got != declaredSHA256 {
		return fmt.Errorf("helper: certificate SHA-256 mismatch: got %s, declared %s", got, declaredSHA256)
	}
	if !cert.IsCA || !cert.BasicConstraintsValid {
		return fmt.Errorf("helper: certificate is not a CA (IsCA=%v)", cert.IsCA)
	}
	if err := cert.CheckSignatureFrom(cert); err != nil {
		return fmt.Errorf("helper: certificate is not self-signed: %w", err)
	}
	return nil
}
