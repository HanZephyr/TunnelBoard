//go:build !windows

package helper

import (
	"errors"
	"time"
)

// Client 的非 Windows 桩：首发仅 Windows 支持特权操作（设计文档 §1）。
type Client struct {
	PipePath string
	Timeout  time.Duration
}

// NewClient 在非 Windows 平台返回永远报错的客户端。
func NewClient() *Client {
	return &Client{}
}

// Call 在非 Windows 平台不可用。
func (c *Client) Call(req Request) (Response, error) {
	return Response{}, errors.New("helper: privileged operations are only supported on Windows")
}

// Ping 在非 Windows 平台不可用。
func (c *Client) Ping() (string, error) {
	return "", errors.New("helper: privileged operations are only supported on Windows")
}
