//go:build windows

package helper

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
)

// CertutilTrustCA 把本地 CA 写入本地计算机的 Root 存储（需管理员/SYSTEM，经 certutil）。
// 安装前再次执行 ValidateLocalCA 作为防御纵深；成功后记录指纹到 trusted-ca，
// 使 UntrustCA 只能撤销本工具自己信任过的那张 CA。certutil 非零退出时错误附带其输出。
func CertutilTrustCA(certDER []byte, sha256 string) error {
	if err := ValidateLocalCA(certDER, sha256); err != nil {
		return err
	}
	tmp, err := os.CreateTemp("", "tunnelboard-ca-*.cer")
	if err != nil {
		return fmt.Errorf("helper: create temp cert file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(certDER); err != nil {
		tmp.Close()
		return fmt.Errorf("helper: write temp cert file: %w", err)
	}
	// 先关闭再交给 certutil 读取（Windows 文件句柄语义）。
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("helper: close temp cert file: %w", err)
	}
	out, err := exec.Command("certutil", "-addstore", "-f", "Root", tmpPath).CombinedOutput()
	if err != nil {
		return fmt.Errorf("helper: certutil -addstore Root: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if err := writeCAFingerprint(sha256); err != nil {
		return fmt.Errorf("helper: record trusted ca fingerprint: %w", err)
	}
	slog.Info("local ca trusted", "sha256_prefix", sha256[:12])
	return nil
}

// CertutilUntrustCA 按 SHA-256 指纹从 Root 存储删除本地 CA（需管理员/SYSTEM，经 certutil）。
// 红线：只撤销 helper 自己信任过的那张 CA——指纹必须与 trusted-ca 记录一致，
// 防止借管道删除系统其他根证书。
func CertutilUntrustCA(sha256 string) error {
	trusted, err := readCAFingerprint()
	if err != nil {
		return fmt.Errorf("helper: no trusted ca fingerprint recorded: %w", err)
	}
	if trusted != sha256 {
		return fmt.Errorf("helper: refusing to delete ca %s (not installed by TunnelBoard)", sha256)
	}
	out, err := exec.Command("certutil", "-delstore", "Root", sha256).CombinedOutput()
	if err != nil {
		return fmt.Errorf("helper: certutil -delstore Root: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if err := clearCAFingerprint(); err != nil {
		return fmt.Errorf("helper: clear trusted ca fingerprint: %w", err)
	}
	slog.Info("local ca untrusted", "sha256_prefix", sha256[:12])
	return nil
}

// ProductionEnvironment 返回生产注入：hosts 路径由调用方钉死
// （%SystemRoot%\System32\drivers\etc\hosts，不接受请求内路径参数），CA 操作走 certutil。
func ProductionEnvironment(hostsPath, version string) Environment {
	return Environment{
		HostsPath: hostsPath,
		TrustCA:   CertutilTrustCA,
		UntrustCA: CertutilUntrustCA,
		Version:   version,
	}
}
