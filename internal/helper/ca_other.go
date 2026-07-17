//go:build !windows

package helper

import "errors"

// ProductionEnvironment 在非 Windows 平台返回"不支持"的 CA 实现：
// 首发平台仅 Windows，macOS/Linux Adapter 后置（设计文档 §1）。
func ProductionEnvironment(hostsPath, version string) Environment {
	unsupported := func(op string) error {
		return errors.New("helper: CA operation " + op + " is only supported on Windows")
	}
	return Environment{
		HostsPath: hostsPath,
		TrustCA:   func(certDER []byte, sha256 string) error { return unsupported("trust") },
		UntrustCA: func(sha256 string) error { return unsupported("untrust") },
		Version:   version,
	}
}
