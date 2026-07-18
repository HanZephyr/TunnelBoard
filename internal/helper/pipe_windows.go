//go:build windows

package helper

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"strings"

	winio "github.com/Microsoft/go-winio"
)

// PipePath 是 helper 服务监听的命名管道；主程序经它发送白名单请求。
const PipePath = `\\.\pipe\tunnelboard-helper`

// pipeSDDL 返回管道 DACL：仅 SYSTEM（服务账户）与指定的安装者 SID 可访问。
// 安装者 SID 在提权安装时由主程序（普通用户身份）显式传入并落盘，
// 服务运行时读取——绝不使用服务上下文中的账户（那是 SYSTEM 自身，见 issue #1）。
func pipeSDDL(ownerSID string) (string, error) {
	ownerSID = strings.TrimSpace(ownerSID)
	if ownerSID == "" || strings.ContainsAny(ownerSID, " \t\r\n()") {
		return "", fmt.Errorf("helper: invalid owner SID %q", ownerSID)
	}
	return "D:P(A;;GA;;;SY)(A;;GA;;;" + ownerSID + ")", nil
}

// ServePipe 监听命名管道并逐连接处理换行分隔 JSON 请求，直到 ctx 取消。
// 单连接单请求：客户端短连接模型，生命周期最简单。
func ServePipe(ctx context.Context, env Environment, pipePath, ownerSID string) error {
	sddl, err := pipeSDDL(ownerSID)
	if err != nil {
		return err
	}
	ln, err := winio.ListenPipe(pipePath, &winio.PipeConfig{SecurityDescriptor: sddl})
	if err != nil {
		return fmt.Errorf("helper: listen pipe: %w", err)
	}
	defer ln.Close()

	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
			}
			if strings.Contains(err.Error(), "closed") {
				return nil
			}
			return fmt.Errorf("helper: accept pipe: %w", err)
		}
		go handlePipeConn(conn, env)
	}
}

// handlePipeConn 解码一行 JSON Request，经白名单分发后写回一行 JSON Response。
func handlePipeConn(conn net.Conn, env Environment) {
	defer conn.Close()
	var req Request
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&req); err != nil {
		writePipeJSON(conn, Response{OK: false, Error: "helper: bad request framing: " + err.Error()})
		return
	}
	resp := HandleRequest(req, env)
	if resp.OK {
		slog.Info("privileged op handled", "op", req.Op)
	} else {
		slog.Error("privileged op rejected", "op", req.Op, "err", resp.Error)
	}
	writePipeJSON(conn, resp)
}

func writePipeJSON(conn net.Conn, resp Response) {
	data, err := json.Marshal(resp)
	if err != nil {
		return
	}
	_, _ = conn.Write(append(data, '\n'))
}
