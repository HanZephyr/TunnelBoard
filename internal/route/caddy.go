package route

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/HanZephyr/TunnelBoard/internal/model"
)

// 以下类型按 Caddy v2 JSON 配置结构声明，键名与其一一对应；用结构体保证字段顺序与输出稳定。

type caddyConfig struct {
	Admin caddyAdmin `json:"admin"`
	Apps  caddyApps  `json:"apps"`
}

type caddyAdmin struct {
	Listen string `json:"listen"`
}

type caddyApps struct {
	HTTP caddyHTTPApp `json:"http"`
	TLS  caddyTLSApp  `json:"tls"`
	PKI  caddyPKIApp  `json:"pki"`
}

// caddyPKIApp 配置本地 CA：禁止 Caddy 自动安装根证书——
// 安装/撤销本地 CA 信任只能由受限特权辅助服务完成（CONTEXT.md 红线），
// 且该步骤在无控制台/无交互环境下会挂起启动流程。
type caddyPKIApp struct {
	CertificateAuthorities caddyPKIAuthorities `json:"certificate_authorities"`
}

type caddyPKIAuthorities struct {
	Local caddyPKILocal `json:"local"`
}

type caddyPKILocal struct {
	InstallTrust bool `json:"install_trust"`
}

type caddyHTTPApp struct {
	Servers map[string]caddyServer `json:"servers"`
}

type caddyServer struct {
	Listen []string     `json:"listen"`
	Routes []caddyRoute `json:"routes"`
}

type caddyRoute struct {
	Match    []caddyMatcher `json:"match"`
	Handle   []caddyHandler `json:"handle"`
	Terminal bool           `json:"terminal"`
}

type caddyMatcher struct {
	Host []string `json:"host"`
}

type caddyHandler struct {
	Handler   string          `json:"handler"`
	Transport *caddyTransport `json:"transport,omitempty"`
	Headers   *caddyHeaders   `json:"headers,omitempty"`
	Upstreams []caddyUpstream `json:"upstreams"`
}

type caddyTransport struct {
	Protocol string            `json:"protocol"`
	TLS      caddyTransportTLS `json:"tls"`
}

type caddyTransportTLS struct {
	ServerName string `json:"server_name"`
}

type caddyHeaders struct {
	Request caddyHeadersRequest `json:"request"`
}

type caddyHeadersRequest struct {
	Set map[string][]string `json:"set"`
}

type caddyUpstream struct {
	Dial string `json:"dial"`
}

type caddyTLSApp struct {
	Automation caddyAutomation `json:"automation"`
}

type caddyAutomation struct {
	Policies []caddyPolicy `json:"policies"`
}

type caddyPolicy struct {
	Subjects []string      `json:"subjects"`
	Issuers  []caddyIssuer `json:"issuers"`
}

type caddyIssuer struct {
	Module string `json:"module"`
}

// CompileCaddy 编译全局 Caddy 配置（JSON）。没有 CaddyEnabled=true 的 Route 时返回 (nil, nil)，
// 调用方据此不启动 Caddy 进程。Route 的 Forward 缺失或非 local（数据不一致）时返回错误。
// 输出对同一输入字节稳定：路由与 subjects 均按域名排序，map 键由 encoding/json 排序输出。
func CompileCaddy(data model.VaultData) ([]byte, error) {
	forwards := make(map[int]model.Forward, len(data.Forwards))
	for _, f := range data.Forwards {
		forwards[f.ID] = f
	}

	var enabled []model.WebRoute
	for _, r := range data.WebRoutes {
		if r.CaddyEnabled {
			enabled = append(enabled, r)
		}
	}
	if len(enabled) == 0 {
		return nil, nil
	}
	sort.Slice(enabled, func(i, j int) bool { return enabled[i].Domain < enabled[j].Domain })

	routes := make([]caddyRoute, 0, len(enabled))
	subjects := make([]string, 0, len(enabled))
	seen := make(map[string]struct{})
	for _, r := range enabled {
		f, ok := forwards[r.ForwardID]
		if !ok {
			return nil, fmt.Errorf("route: web route %d (%s) references missing forward %d", r.ID, r.Domain, r.ForwardID)
		}
		if f.Mode != "local" {
			return nil, fmt.Errorf("route: web route %d (%s) references non-local forward %d (mode %q)", r.ID, r.Domain, f.ID, f.Mode)
		}
		routes = append(routes, caddyRoute{
			Match:    []caddyMatcher{{Host: []string{r.Domain}}},
			Handle:   []caddyHandler{proxyHandler(r, f)},
			Terminal: true,
		})
		if _, dup := seen[r.Domain]; !dup {
			seen[r.Domain] = struct{}{}
			subjects = append(subjects, r.Domain)
		}
	}
	sort.Strings(subjects)

	cfg := caddyConfig{
		Admin: caddyAdmin{Listen: "127.0.0.1:2019"},
		Apps: caddyApps{
			HTTP: caddyHTTPApp{
				Servers: map[string]caddyServer{
					"tunnelboard": {
						Listen: []string{"127.0.0.1:443"},
						Routes: routes,
					},
				},
			},
			TLS: caddyTLSApp{
				Automation: caddyAutomation{
					Policies: []caddyPolicy{{
						Subjects: subjects,
						Issuers:  []caddyIssuer{{Module: "internal"}},
					}},
				},
			},
			PKI: caddyPKIApp{
				CertificateAuthorities: caddyPKIAuthorities{
					Local: caddyPKILocal{InstallTrust: false},
				},
			},
		},
	}
	return json.Marshal(cfg)
}

// proxyHandler 生成单个 Route 的 reverse_proxy handler。HTTPS 上游按 ADR 0002 设置显式
// SNI 与上游 Host 请求头，并保持默认严格校验（绝不写 insecure_skip_verify）。
func proxyHandler(r model.WebRoute, f model.Forward) caddyHandler {
	h := caddyHandler{
		Handler:   "reverse_proxy",
		Upstreams: []caddyUpstream{{Dial: fmt.Sprintf("127.0.0.1:%d", f.LocalPort)}},
	}
	if r.UpstreamScheme == "https" {
		h.Transport = &caddyTransport{
			Protocol: "http",
			TLS:      caddyTransportTLS{ServerName: r.TLSSNI},
		}
		h.Headers = &caddyHeaders{
			Request: caddyHeadersRequest{Set: map[string][]string{"Host": {r.TLSSNI}}},
		}
	}
	return h
}
