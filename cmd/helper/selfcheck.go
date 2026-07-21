package main

import (
	"encoding/json"
	"fmt"
	"io"
)

// protocolVersion 是主程序与本次应用会话 Helper 的精确握手版本。
const protocolVersion = "0.1.0"

type selfCheckResult struct {
	ProtocolVersion   string `json:"protocol_version"`
	PersistentService bool   `json:"persistent_service"`
}

// runSelfCheck 必须在任何日志、IPC、UAC 或 SCM 初始化之前调用。
// 只有精确参数组合才处理，避免未知参数被误当成安全自检。
func runSelfCheck(args []string, output io.Writer) (bool, error) {
	if len(args) != 2 || args[0] != "--self-check" || args[1] != "--json" {
		return false, nil
	}
	payload, err := json.Marshal(selfCheckResult{
		ProtocolVersion:   protocolVersion,
		PersistentService: false,
	})
	if err != nil {
		return true, err
	}
	_, err = fmt.Fprintln(output, string(payload))
	return true, err
}
