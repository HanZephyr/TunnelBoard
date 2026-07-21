//go:build windows

package helper

import (
	"context"
	"time"
)

// Client 为现有业务调用面提供兼容 Adapter；内部不再按调用短连接或安装服务，
// 而是在本应用生命周期内复用同一个 PrivilegedSession。
type Client struct {
	Timeout time.Duration
	session PrivilegedSession
}

func NewClient() *Client {
	return &Client{Timeout: 15 * time.Second, session: NewPrivilegedSession(newWindowsSessionBackend())}
}

func (c *Client) EnsureInstalled() error {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout())
	defer cancel()
	return c.session.Ensure(ctx)
}

func (c *Client) Call(request Request) (Response, error) {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout())
	defer cancel()
	return c.session.Call(ctx, request)
}

func (c *Client) Ping() (string, error) {
	response, err := c.Call(Request{Op: OpPing})
	if err != nil {
		return "", err
	}
	return response.Version, nil
}

func (c *Client) Close(ctx context.Context) error {
	return c.session.Close(ctx)
}

func (c *Client) timeout() time.Duration {
	if c.Timeout <= 0 {
		return 15 * time.Second
	}
	return c.Timeout
}
