//go:build windows

package helper

import (
	"fmt"
	"strings"
)

// PipePath 仅作为旧版本固定管道名的迁移检测常量保留。新会话绝不监听它。
const PipePath = `\\.\pipe\tunnelboard-helper`

// pipeSDDL 允许创建管道的当前用户、提升后的 Administrators token 与 SYSTEM
// 访问；随机单实例名称、启动进程 PID 校验和协议握手共同构成完整边界。
func pipeSDDL(ownerSID string) (string, error) {
	ownerSID = strings.TrimSpace(ownerSID)
	if ownerSID == "" || strings.ContainsAny(ownerSID, " \t\r\n()") {
		return "", fmt.Errorf("helper: invalid owner SID %q", ownerSID)
	}
	return "D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;GA;;;" + ownerSID + ")", nil
}
