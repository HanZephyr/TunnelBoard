//go:build windows

package helper

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os/user"
	"strings"

	winio "github.com/Microsoft/go-winio"
)

// PipePath 是 helper 服务监听的命名管道；主程序经它发送白名单请求。
const PipePath = `\\.\pipe\tunnelboard-helper`

// pipeSDDL 返回管道 DACL：仅 SYSTEM（服务账户）与当前交互用户可访问。
// 其他本地用户无法连接该管道发送特权请求。
func pipeSDDL() (string, error) {
	u, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("helper: resolve current user: %w", err)
	}
	return "D:P(A;;GA;;;SY)(A;;GA;;;" + u.Uid + ")", nil
}

// ServePipe 监听命名管道并逐连接处理换行分隔 JSON 请求，直到 ctx 取消。
// 单连接单请求：客户端短连接模型，生命周期最简单。
func ServePipe(ctx context.Context, env Environment, pipePath string) error {
	sddl, err := pipeSDDL()
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
	writePipeJSON(conn, HandleRequest(req, env))
}

func writePipeJSON(conn net.Conn, resp Response) {
	data, err := json.Marshal(resp)
	if err != nil {
		return
	}
	_, _ = conn.Write(append(data, '\n'))
}
