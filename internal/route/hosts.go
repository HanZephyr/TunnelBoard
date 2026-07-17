// Package route 是本地路由的纯函数层：hosts 条目规划与 Caddy 配置编译。
// 只消费 Vault 快照并产出确定性结果，不感知 SSH、特权操作与进程管理。
package route

import (
	"sort"
	"strings"

	"github.com/HanZephyr/TunnelBoard/internal/model"
)

// HostEntry 是一条受托管 hosts 记录，把域名映射到回环地址。
type HostEntry struct {
	Domain string `json:"domain"`
	IP     string `json:"ip"`
}

// PlanHosts 从 Vault 快照计算受托管 hosts 条目与需要用户确认的域名。
// HostsEnabled=true 且所属 Forward 存在且为 local 模式的 Route 产生一条 127.0.0.1 映射；
// 域名不以 ".test"/".localhost" 结尾（按小写归一判断）时进入 requiresConfirmation，
// 由调用方在 Apply 前取得用户确认。两个输出都按域名排序去重，保证确定性，便于 diff 与测试。
func PlanHosts(data model.VaultData) (entries []HostEntry, requiresConfirmation []string) {
	forwards := make(map[int]model.Forward, len(data.Forwards))
	for _, f := range data.Forwards {
		forwards[f.ID] = f
	}

	seen := make(map[string]struct{})
	for _, r := range data.WebRoutes {
		if !r.HostsEnabled {
			continue
		}
		f, ok := forwards[r.ForwardID]
		if !ok || f.Mode != "local" {
			continue
		}
		if _, dup := seen[r.Domain]; dup {
			continue
		}
		seen[r.Domain] = struct{}{}
		entries = append(entries, HostEntry{Domain: r.Domain, IP: "127.0.0.1"})
		if !isLocalDomain(r.Domain) {
			requiresConfirmation = append(requiresConfirmation, r.Domain)
		}
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].Domain < entries[j].Domain })
	sort.Strings(requiresConfirmation)
	return entries, requiresConfirmation
}

// NeedsConfirmation 报告写入该域名的 hosts 覆盖是否需要用户明确确认（非本机约定后缀）。
func NeedsConfirmation(domain string) bool {
	return !isLocalDomain(domain)
}

// isLocalDomain 报告域名是否带有本机约定后缀；后缀含前导点（裸 "test"/"localhost" 不算），
// 比较前按小写归一（DNS 大小写不敏感）。
func isLocalDomain(domain string) bool {
	d := strings.ToLower(domain)
	return strings.HasSuffix(d, ".test") || strings.HasSuffix(d, ".localhost")
}
