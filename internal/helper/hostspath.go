package helper

import (
	"os"
	"path/filepath"
	"runtime"
)

// SystemHostsPath 返回平台 hosts 文件路径；主程序只读它做快照，写入只经特权 helper。
func SystemHostsPath() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(os.Getenv("SystemRoot"), `System32\drivers\etc\hosts`)
	}
	return "/etc/hosts"
}
