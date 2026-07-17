//go:build windows

package helper

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
)

// ownerFilePath 是安装者 SID 的落盘位置（安装时由提权进程写入，服务运行时读取）。
// 置于 ProgramData：服务（SYSTEM）与提权安装进程都可写读，普通用户进程只读无妨（SID 非秘密）。
func ownerFilePath() string {
	base := os.Getenv("ProgramData")
	if strings.TrimSpace(base) == "" {
		base = `C:\ProgramData`
	}
	return filepath.Join(base, "TunnelBoard", "helper-owner")
}

// writeOwnerSID 持久化安装者 SID。
func writeOwnerSID(sid string) error {
	sid = strings.TrimSpace(sid)
	if sid == "" {
		return fmt.Errorf("helper: empty owner sid")
	}
	if err := os.MkdirAll(filepath.Dir(ownerFilePath()), 0o755); err != nil {
		return err
	}
	return os.WriteFile(ownerFilePath(), []byte(sid+"\n"), 0o644)
}

// readOwnerSID 读取安装者 SID。
func readOwnerSID() (string, error) {
	data, err := os.ReadFile(ownerFilePath())
	if err != nil {
		return "", err
	}
	sid := strings.TrimSpace(string(data))
	if sid == "" {
		return "", fmt.Errorf("helper: owner sid file is empty")
	}
	return sid, nil
}

// CurrentUserSID 返回当前进程用户的 SID（交互式调试用）。
func CurrentUserSID() (string, error) {
	u, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("helper: resolve current user: %w", err)
	}
	return u.Uid, nil
}
