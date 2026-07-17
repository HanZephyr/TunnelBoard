//go:build darwin || linux

package helper

import (
	"fmt"
	"os"

	"github.com/HanZephyr/TunnelBoard/internal/route"
)

// localElevatedClient 是 macOS/Linux 的特权直连客户端：没有常驻服务，
// 每次操作单独请求系统管理员授权（交接文档的退化路径；一次授权的常驻 Helper 列为后续 POC）。
// 代码按平台就绪，未经真机验证（迭代 5 的 macOS 验证项）。
type localElevatedClient struct{}

// NewLocalClient 返回本地直连客户端；EnsureInstalled 为空操作（无服务可装）。
func NewLocalClient() Operator {
	return &localElevatedClient{}
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
		err = c.applyManagedHosts(req.Hosts)
	case OpRemoveManagedHosts:
		err = c.applyManagedHosts(nil)
	case OpTrustLocalCA:
		err = c.trustLocalCA(req.CertDER)
	case OpUntrustLocalCA:
		err = c.untrustLocalCA(req.CertSHA256)
	}
	if err != nil {
		return Response{OK: false, Error: err.Error()}, nil
	}
	return Response{OK: true}, nil
}

// applyManagedHosts 读取当前 hosts，渲染受托管区块到临时文件，再经系统授权替换。
func (c *localElevatedClient) applyManagedHosts(entries []route.HostEntry) error {
	content, err := os.ReadFile(SystemHostsPath())
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("helper: read hosts: %w", err)
	}
	rendered := RenderManagedHosts(string(content), entries)
	tmp, err := os.CreateTemp("", "tunnelboard-hosts-*")
	if err != nil {
		return fmt.Errorf("helper: create temp hosts: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(rendered); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("helper: write temp hosts: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return elevatedCopy(tmp.Name(), SystemHostsPath())
}

// trustLocalCA 把 CA 写到临时文件后交给平台信任函数（证书内容已经 ValidateRequest 校验）。
func (c *localElevatedClient) trustLocalCA(certDER []byte) error {
	tmp, err := os.CreateTemp("", "tunnelboard-ca-*.cer")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(certDER); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return platformTrustCA(tmp.Name())
}
