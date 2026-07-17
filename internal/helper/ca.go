package helper

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"strings"
)

// ValidateTunnelBoardCA 校验一张证书确为 TunnelBoard 本地 CA，三个条件缺一不可：
// DER 可解析为 X.509 证书；DER 的 SHA-256 与声明值（小写十六进制）一致；
// Subject CommonName 含 "TunnelBoard"。错误信息区分失败原因。
func ValidateTunnelBoardCA(certDER []byte, declaredSHA256 string) error {
	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		return fmt.Errorf("helper: cannot parse certificate DER: %w", err)
	}
	sum := sha256.Sum256(certDER)
	if got := hex.EncodeToString(sum[:]); got != declaredSHA256 {
		return fmt.Errorf("helper: certificate SHA-256 mismatch: got %s, declared %s", got, declaredSHA256)
	}
	if !strings.Contains(cert.Subject.CommonName, "TunnelBoard") {
		return fmt.Errorf("helper: certificate CN %q is not a TunnelBoard CA", cert.Subject.CommonName)
	}
	return nil
}
