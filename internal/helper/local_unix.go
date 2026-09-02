//go:build darwin

package helper

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/HanZephyr/TunnelBoard/internal/route"
)

// localElevatedClient 是 macOS 的特权直连客户端：没有常驻服务，
// 每次操作单独请求系统管理员授权（交接文档的退化路径；一次授权的常驻 Helper 列为后续 POC）。
// 代码按平台就绪，未经真机验证（迭代 5 的 macOS 验证项）。
type localElevatedClient struct {
	privilege PlatformPrivilege
}

// NewLocalClient 返回本地直连客户端；EnsureInstalled 为空操作（无服务可装）。
func NewLocalClient() Operator {
	privilege, err := newNativePlatformPrivilege()
	if err != nil {
		return &localElevatedClient{privilege: unavailablePrivilege{err: err}}
	}
	return &localElevatedClient{privilege: privilege}
}

// NewOperator 返回本地直连客户端。
func NewOperator() Operator {
	return NewLocalClient()
}

// Ping 报告本地模式版本（无服务握手）。
func (c *localElevatedClient) Ping() (string, error) {
	return "local-elevated", nil
}

// EnsureInstalled 本地模式无需安装。
func (c *localElevatedClient) EnsureInstalled() error { return nil }

func (c *localElevatedClient) EnsureLoopbackHTTPSRedirect(ctx context.Context) error {
	if c.privilege == nil {
		return errors.New("helper: platform privilege is unavailable")
	}
	return c.privilege.EnsureLoopbackHTTPSRedirect(ctx)
}

// Call 校验请求并按操作执行提权写入；全部操作复用服务端的同一套校验红线。
func (c *localElevatedClient) Call(req Request) (Response, error) {
	if err := ValidateRequest(req); err != nil {
		return Response{OK: false, Error: err.Error()}, nil
	}
	var err error
	switch req.Op {
	case OpPing:
		return Response{OK: true, Version: "local-elevated"}, nil
	case OpApplyManagedHosts:
		var digest string
		digest, err = c.applyManagedHosts(req.Hosts, req.ExpectedManagedDigest)
		if err == nil {
			return Response{OK: true, ManagedDigest: digest}, nil
		}
	case OpRemoveManagedHosts:
		_, err = c.applyManagedHosts(nil, "")
	}
	if err != nil {
		return Response{OK: false, Error: err.Error()}, nil
	}
	return Response{OK: true}, nil
}

// applyManagedHosts 读取当前 hosts，渲染受托管区块到临时文件，再经系统授权替换。
func (c *localElevatedClient) applyManagedHosts(entries []route.HostEntry, expectedDigest string) (string, error) {
	content, err := os.ReadFile(SystemHostsPath())
	if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("helper: read hosts: %w", err)
	}
	currentDigest := ManagedEntriesDigest(ParseManagedHosts(string(content)))
	if expectedDigest != "" && expectedDigest != currentDigest {
		return currentDigest, fmt.Errorf("%w: got %s, want %s", ErrManagedHostsConflict, currentDigest, expectedDigest)
	}
	rendered := RenderManagedHosts(string(content), entries)
	if err := c.privilege.ApplyManagedHosts(context.Background(), []byte(rendered)); err != nil {
		return "", err
	}
	return ManagedEntriesDigest(entries), nil
}

type unavailablePrivilege struct{ err error }

func (p unavailablePrivilege) ApplyManagedHosts(context.Context, []byte) error {
	return p.err
}
func (p unavailablePrivilege) TrustLocalCA(context.Context, []byte) error {
	return p.err
}
func (p unavailablePrivilege) UntrustLocalCA(context.Context, string) error {
	return p.err
}
func (p unavailablePrivilege) EnsureLoopbackHTTPSRedirect(context.Context) error {
	return p.err
}
func (p unavailablePrivilege) RepairDataDirOwner(context.Context, string, string) error {
	return p.err
}
