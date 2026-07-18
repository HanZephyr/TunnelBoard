// Package helper 实现受限特权辅助服务（tunnelboard-helper）的核心可测逻辑：
// 换行分隔 JSON 协议的类型与白名单请求分发、受托管 hosts 区块读写/回滚、本地 CA 校验。
// 命名管道服务与 Windows 服务安装属于进程外壳（cmd/helper），不在本包。
package helper

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/HanZephyr/TunnelBoard/internal/route"
)

// 操作白名单：helper 只接受这些 op，其余一律拒绝（红线：不执行白名单外请求）。
const (
	OpPing               = "ping"
	OpApplyManagedHosts  = "apply_managed_hosts"
	OpRemoveManagedHosts = "remove_managed_hosts"
	OpTrustLocalCA       = "trust_local_ca"
	OpUntrustLocalCA     = "untrust_local_ca"
)

// 请求校验上限，与设计文档 §2 的 schema 校验一致；IP 仅允许回环。
const (
	maxHostEntries  = 256
	maxDomainLen    = 253
	maxCertDERBytes = 16 << 10 // 16 KiB
	loopbackIP      = "127.0.0.1"
)

// Request 是主程序经命名管道发给 helper 的结构化请求。
type Request struct {
	Op         string            `json:"op"`
	Hosts      []route.HostEntry `json:"hosts,omitempty"`
	CertDER    []byte            `json:"certDer,omitempty"`
	CertSHA256 string            `json:"certSha256,omitempty"`
}

// Response 是 helper 对每个 Request 的应答；Error 非空即失败。
type Response struct {
	OK      bool   `json:"ok"`
	Version string `json:"version,omitempty"`
	Error   string `json:"error,omitempty"`
}

// Environment 注入 helper 的全部外部副作用：hosts 文件路径与 CA 系统操作。
// handler 只依赖该注入面，因而可在测试中以 fake 纯内存验证。
type Environment struct {
	HostsPath string
	TrustCA   func(certDER []byte, sha256 string) error
	UntrustCA func(sha256 string) error
	Version   string
}

// HandleRequest 校验并分发一个请求。校验失败一律返回 OK=false 且不产生任何副作用：
// 所有参数校验先于 Environment 注入函数与文件写入执行。
func HandleRequest(req Request, env Environment) Response {
	if err := ValidateRequest(req); err != nil {
		return fail(err)
	}
	switch req.Op {
	case OpPing:
		return Response{OK: true, Version: env.Version}
	case OpApplyManagedHosts:
		return done(WriteManagedHosts(env.HostsPath, req.Hosts))
	case OpRemoveManagedHosts:
		return done(WriteManagedHosts(env.HostsPath, nil))
	case OpTrustLocalCA:
		return done(env.TrustCA(req.CertDER, req.CertSHA256))
	case OpUntrustLocalCA:
		return done(env.UntrustCA(req.CertSHA256))
	default:
		return fail(fmt.Errorf("helper: unknown op %q", req.Op))
	}
}

// ValidateRequest 校验请求的操作与全部参数；零副作用，供服务端分发与本地直连客户端共用。
func ValidateRequest(req Request) error {
	switch req.Op {
	case OpPing, OpRemoveManagedHosts:
		return nil
	case OpApplyManagedHosts:
		return validateHostEntries(req.Hosts)
	case OpTrustLocalCA:
		if err := validateSHA256Hex(req.CertSHA256); err != nil {
			return err
		}
		if len(req.CertDER) == 0 {
			return errors.New("helper: empty certificate DER")
		}
		if len(req.CertDER) > maxCertDERBytes {
			return fmt.Errorf("helper: certificate DER too large: %d > %d bytes", len(req.CertDER), maxCertDERBytes)
		}
		return ValidateLocalCA(req.CertDER, req.CertSHA256)
	case OpUntrustLocalCA:
		return validateSHA256Hex(req.CertSHA256)
	default:
		return fmt.Errorf("helper: unknown op %q", req.Op)
	}
}

func fail(err error) Response { return Response{OK: false, Error: err.Error()} }

func done(err error) Response {
	if err != nil {
		return fail(err)
	}
	return Response{OK: true}
}

// validateHostEntries 校验 hosts 条目集：数量上限、域名合法且去重（大小写不敏感，DNS 语义）、
// IP 必须恰为回环地址。
func validateHostEntries(entries []route.HostEntry) error {
	if len(entries) > maxHostEntries {
		return fmt.Errorf("helper: too many host entries: %d > %d", len(entries), maxHostEntries)
	}
	seen := make(map[string]struct{}, len(entries))
	for _, e := range entries {
		if err := validateDomain(e.Domain); err != nil {
			return err
		}
		if e.IP != loopbackIP {
			return fmt.Errorf("helper: hosts entry %q has non-loopback IP %q", e.Domain, e.IP)
		}
		key := strings.ToLower(e.Domain)
		if _, dup := seen[key]; dup {
			return fmt.Errorf("helper: duplicate hosts domain %q", e.Domain)
		}
		seen[key] = struct{}{}
	}
	return nil
}

// validateDomain 校验单个域名：非空、不含空白/控制字符、长度不超过 253 字节、必须含 "."。
func validateDomain(domain string) error {
	if domain == "" {
		return errors.New("helper: empty domain")
	}
	if len(domain) > maxDomainLen {
		return fmt.Errorf("helper: domain %q exceeds %d bytes", domain, maxDomainLen)
	}
	if !strings.Contains(domain, ".") {
		return fmt.Errorf("helper: domain %q must contain a dot", domain)
	}
	for _, r := range domain {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return fmt.Errorf("helper: domain %q contains whitespace or control characters", domain)
		}
	}
	return nil
}

// validateSHA256Hex 校验指纹格式：恰好 64 位小写十六进制（SHA-256）。
func validateSHA256Hex(s string) error {
	if len(s) != sha256.Size*2 {
		return fmt.Errorf("helper: sha256 must be %d lowercase hex chars, got %d", sha256.Size*2, len(s))
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return fmt.Errorf("helper: sha256 %q must be lowercase hex", s)
		}
	}
	return nil
}
