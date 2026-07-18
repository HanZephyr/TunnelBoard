//go:build windows

package helper

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
)

// CertutilTrustCA 把 TunnelBoard 本地 CA 写入本地计算机的 Root 存储（需管理员/SYSTEM，经 certutil）。
// 安装前再次执行 ValidateTunnelBoardCA 作为防御纵深；certutil 非零退出时错误附带其输出。
func CertutilTrustCA(certDER []byte, sha256 string) error {
	if err := ValidateTunnelBoardCA(certDER, sha256); err != nil {
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
	slog.Info("local ca trusted", "sha256_prefix", sha256[:12])
	return nil
}

// CertutilUntrustCA 按 SHA-256 指纹从 Root 存储删除对应 CA（需管理员/SYSTEM，经 certutil）。
// 红线：只删 TunnelBoard 本地 CA——先查存储并确认 Subject 含 "TunnelBoard"，否则拒绝，
// 防止经管道任意请求删除系统其他根证书。
func CertutilUntrustCA(sha256 string) error {
	lookup, err := exec.Command("certutil", "-store", "Root", sha256).CombinedOutput()
	if err != nil {
		return fmt.Errorf("helper: certutil -store Root lookup: %w: %s", err, strings.TrimSpace(string(lookup)))
	}
	if !strings.Contains(string(lookup), "TunnelBoard") {
		return fmt.Errorf("helper: refusing to delete non-TunnelBoard CA %s", sha256)
	}
	out, err := exec.Command("certutil", "-delstore", "Root", sha256).CombinedOutput()
	if err != nil {
		return fmt.Errorf("helper: certutil -delstore Root: %w: %s", err, strings.TrimSpace(string(out)))
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
