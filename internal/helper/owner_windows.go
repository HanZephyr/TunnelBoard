//go:build windows

package helper

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
)

// CurrentUserSID 返回创建本次随机命名管道的普通用户 SID。
// SID 只用于当前进程内构造 DACL，不再写入 ProgramData。
func CurrentUserSID() (string, error) {
	current, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("helper: resolve current user: %w", err)
	}
	return current.Uid, nil
}

func programDataDir() string {
	base := strings.TrimSpace(os.Getenv("ProgramData"))
	if base == "" {
		base = `C:\ProgramData`
	}
	return filepath.Join(base, "TunnelBoard")
}
