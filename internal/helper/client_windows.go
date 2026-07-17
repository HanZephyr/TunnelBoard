//go:build windows

package helper

import (
	"bufio"
	"encoding/json"
	"fmt"
	"time"

	winio "github.com/Microsoft/go-winio"
)

// Client 是主程序侧的 helper 客户端：每次调用建连、发送单请求、读应答后关闭。
type Client struct {
	PipePath string
	Timeout  time.Duration
}

// NewClient 返回指向标准管道的客户端。
func NewClient() *Client {
	return &Client{PipePath: PipePath, Timeout: 10 * time.Second}
}

// Call 发送一个请求并等待应答；resp.OK=false 时以 error 返回 Response.Error。
func (c *Client) Call(req Request) (Response, error) {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	conn, err := winio.DialPipe(c.PipePath, &timeout)
	if err != nil {
		return Response{}, fmt.Errorf("helper: dial pipe: %w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))

	data, err := json.Marshal(req)
	if err != nil {
		return Response{}, err
	}
	if _, err := conn.Write(append(data, '\n')); err != nil {
		return Response{}, fmt.Errorf("helper: write request: %w", err)
	}
	var resp Response
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&resp); err != nil {
		return Response{}, fmt.Errorf("helper: read response: %w", err)
	}
	return resp, nil
}

// Ping 探测 helper 服务可用性并返回其版本。
func (c *Client) Ping() (string, error) {
	resp, err := c.Call(Request{Op: OpPing})
	if err != nil {
		return "", err
	}
	if !resp.OK {
		return "", fmt.Errorf("helper: ping failed: %s", resp.Error)
	}
	return resp.Version, nil
}
