package route_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/HanZephyr/TunnelBoard/internal/loopbackhttps"
	"github.com/HanZephyr/TunnelBoard/internal/model"
	"github.com/HanZephyr/TunnelBoard/internal/route"
)

// caddyJSON 是编译结果中与断言相关的字段子集，键名与目标配置一一对应。
type caddyJSON struct {
	Admin struct {
		Listen string `json:"listen"`
	} `json:"admin"`
	Apps struct {
		HTTP struct {
			HTTPSPort int `json:"https_port"`
			Servers   map[string]struct {
				Listen         []string `json:"listen"`
				AutomaticHTTPS struct {
					DisableRedirects bool `json:"disable_redirects"`
				} `json:"automatic_https"`
				Routes []struct {
					Match []struct {
						Host []string `json:"host"`
					} `json:"match"`
					Handle []struct {
						Handler   string `json:"handler"`
						Transport *struct {
							Protocol string `json:"protocol"`
							TLS      struct {
								ServerName string `json:"server_name"`
							} `json:"tls"`
						} `json:"transport"`
						Headers *struct {
							Request struct {
								Set map[string][]string `json:"set"`
							} `json:"request"`
						} `json:"headers"`
						Upstreams []struct {
							Dial string `json:"dial"`
						} `json:"upstreams"`
					} `json:"handle"`
					Terminal bool `json:"terminal"`
				} `json:"routes"`
			} `json:"servers"`
		} `json:"http"`
		TLS struct {
			Automation struct {
				Policies []struct {
					Subjects []string `json:"subjects"`
					Issuers  []struct {
						Module string `json:"module"`
					} `json:"issuers"`
				} `json:"policies"`
			} `json:"automation"`
		} `json:"tls"`
	} `json:"apps"`
}

func decodeCaddy(t *testing.T, raw []byte) caddyJSON {
	t.Helper()
	var cfg caddyJSON
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("compiled config is not valid JSON: %v\n%s", err, raw)
	}
	return cfg
}

// 没有任何 CaddyEnabled=true 的 Route 时不生成配置。
func TestCompileCaddyNoneEnabled(t *testing.T) {
	data := model.VaultData{
		Forwards:  []model.Forward{localForward(1, 5432)},
		WebRoutes: []model.WebRoute{{ID: 1, ForwardID: 1, Domain: "db.test", HostsEnabled: true}},
	}
	raw, err := route.CompileCaddy(data)
	if err != nil || raw != nil {
		t.Fatalf("CompileCaddy = (%v, %v), want (nil, nil)", raw, err)
	}
}

// 单个 HTTP 上游 Route：admin 监听、server 监听、host matcher 与 dial 地址都要正确。
func TestCompileCaddyHTTPUpstream(t *testing.T) {
	data := model.VaultData{
		Forwards:  []model.Forward{localForward(1, 5432)},
		WebRoutes: []model.WebRoute{{ID: 1, ForwardID: 1, Domain: "db.test", CaddyEnabled: true, UpstreamScheme: "http"}},
	}
	raw, err := route.CompileCaddy(data)
	if err != nil {
		t.Fatal(err)
	}
	cfg := decodeCaddy(t, raw)

	if cfg.Admin.Listen != "" {
		t.Fatalf("route compiler must not choose caddy admin transport, got %q", cfg.Admin.Listen)
	}
	server, ok := cfg.Apps.HTTP.Servers["tunnelboard"]
	if !ok {
		t.Fatalf("servers = %v, want key tunnelboard", cfg.Apps.HTTP.Servers)
	}
	if len(server.Listen) != 1 || server.Listen[0] != loopbackhttps.BindAddr() {
		t.Fatalf("server.listen = %v, want [%s]", server.Listen, loopbackhttps.BindAddr())
	}
	if cfg.Apps.HTTP.HTTPSPort != loopbackhttps.BindPort() {
		t.Fatalf("http.https_port = %d, want %d", cfg.Apps.HTTP.HTTPSPort, loopbackhttps.BindPort())
	}
	if !server.AutomaticHTTPS.DisableRedirects {
		t.Fatal("automatic_https.disable_redirects must be true so Caddy does not bind :80")
	}
	if len(server.Routes) != 1 {
		t.Fatalf("routes = %d, want 1", len(server.Routes))
	}
	r := server.Routes[0]
	if len(r.Match) != 1 || len(r.Match[0].Host) != 1 || r.Match[0].Host[0] != "db.test" {
		t.Fatalf("match = %+v, want host [db.test]", r.Match)
	}
	if len(r.Handle) != 1 {
		t.Fatalf("handle = %d handlers, want 1", len(r.Handle))
	}
	h := r.Handle[0]
	if h.Handler != "reverse_proxy" {
		t.Fatalf("handler = %q, want reverse_proxy", h.Handler)
	}
	if len(h.Upstreams) != 1 || h.Upstreams[0].Dial != "127.0.0.1:5432" {
		t.Fatalf("upstreams = %+v, want dial 127.0.0.1:5432", h.Upstreams)
	}
	if h.Transport != nil || h.Headers != nil {
		t.Fatalf("http upstream must not set transport/headers, got %+v / %+v", h.Transport, h.Headers)
	}
	if !r.Terminal {
		t.Fatal("route must be terminal")
	}
	if got := cfg.Apps.TLS.Automation.Policies[0].Subjects; len(got) != 1 || got[0] != "db.test" {
		t.Fatalf("subjects = %v, want [db.test]", got)
	}
	// pki.install_trust 必须为 false：根证书信任只允许经特权辅助服务安装，
	// 且 Caddy 自动安装步骤在无控制台环境会挂起启动。
	if !strings.Contains(string(raw), `"install_trust":false`) {
		t.Fatal("pki install_trust must be disabled")
	}
}

// HTTPS 上游默认保留当前请求 Host；多个本地域名都使用同一个动态占位符，TLS SNI 仍独立配置。
func TestCompileCaddyHTTPSUpstreamPreservesOriginalHost(t *testing.T) {
	data := model.VaultData{
		Forwards: []model.Forward{localForward(1, 8443), localForward(2, 9443)},
		WebRoutes: []model.WebRoute{
			{ID: 1, ForwardID: 1, Domain: "admin.vip-duo-duo-buy.localhost", CaddyEnabled: true, UpstreamScheme: "https", TLSSNI: "vip-duo-duo-buy.lifepoem.cn", UpstreamHostMode: model.UpstreamHostModeOriginal},
			{ID: 2, ForwardID: 2, Domain: "preview.vip-duo-duo-buy.localhost", CaddyEnabled: true, UpstreamScheme: "https", TLSSNI: "vip-duo-duo-buy.lifepoem.cn", UpstreamHostMode: model.UpstreamHostModeOriginal},
		},
	}
	raw, err := route.CompileCaddy(data)
	if err != nil {
		t.Fatal(err)
	}
	cfg := decodeCaddy(t, raw)

	routes := cfg.Apps.HTTP.Servers["tunnelboard"].Routes
	if len(routes) != 2 {
		t.Fatalf("routes = %d, want 2", len(routes))
	}
	for i, wantDial := range []string{"127.0.0.1:8443", "127.0.0.1:9443"} {
		h := routes[i].Handle[0]
		if h.Transport == nil {
			t.Fatalf("route[%d] https upstream must set transport", i)
		}
		if h.Transport.Protocol != "http" {
			t.Fatalf("route[%d] transport.protocol = %q, want http", i, h.Transport.Protocol)
		}
		if h.Transport.TLS.ServerName != "vip-duo-duo-buy.lifepoem.cn" {
			t.Fatalf("route[%d] tls.server_name = %q, want vip-duo-duo-buy.lifepoem.cn", i, h.Transport.TLS.ServerName)
		}
		if h.Headers == nil {
			t.Fatalf("route[%d] https upstream must set dynamic Host header", i)
		}
		if got := h.Headers.Request.Set["Host"]; len(got) != 1 || got[0] != "{http.request.host}" {
			t.Fatalf("route[%d] Host header = %v, want [{http.request.host}]", i, got)
		}
		if len(h.Upstreams) != 1 || h.Upstreams[0].Dial != wantDial {
			t.Fatalf("route[%d] upstreams = %+v, want dial %s", i, h.Upstreams, wantDial)
		}
	}
	if bytes.Contains(raw, []byte("insecure_skip_verify")) {
		t.Fatalf("config must not contain insecure_skip_verify:\n%s", raw)
	}
	if strings.Contains(strings.ToLower(string(raw)), "acme") {
		t.Fatalf("config must not contain any ACME issuer:\n%s", raw)
	}
	policies := cfg.Apps.TLS.Automation.Policies
	if len(policies) != 1 || len(policies[0].Issuers) != 1 || policies[0].Issuers[0].Module != "internal" {
		t.Fatalf("policies = %+v, want single internal issuer", policies)
	}
}

// 三种 Host 模式分别生成动态 Host、TLS SNI 和自定义 Host；旧记录的显式 Host 仍保持自定义语义。
func TestCompileCaddyHTTPSUpstreamHostModes(t *testing.T) {
	cases := []struct {
		name         string
		mode         model.UpstreamHostMode
		upstreamHost string
		wantHost     string
	}{
		{name: "original", mode: model.UpstreamHostModeOriginal, wantHost: "{http.request.host}"},
		{name: "tls sni", mode: model.UpstreamHostModeTLSSNI, wantHost: "grafana.internal"},
		{name: "custom", mode: model.UpstreamHostModeCustom, upstreamHost: "localhost:8443", wantHost: "localhost:8443"},
		{name: "legacy custom", upstreamHost: "legacy.internal", wantHost: "legacy.internal"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := model.VaultData{
				Forwards:  []model.Forward{localForward(1, 8443)},
				WebRoutes: []model.WebRoute{{ID: 1, ForwardID: 1, Domain: "grafana.example.com", CaddyEnabled: true, UpstreamScheme: "https", TLSSNI: "grafana.internal", UpstreamHostMode: tc.mode, UpstreamHost: tc.upstreamHost}},
			}
			raw, err := route.CompileCaddy(data)
			if err != nil {
				t.Fatal(err)
			}
			h := decodeCaddy(t, raw).Apps.HTTP.Servers["tunnelboard"].Routes[0].Handle[0]
			if got := h.Headers.Request.Set["Host"]; len(got) != 1 || got[0] != tc.wantHost {
				t.Fatalf("Host header = %v, want [%s]", got, tc.wantHost)
			}
			if h.Transport == nil || h.Transport.TLS.ServerName != "grafana.internal" {
				t.Fatalf("TLS transport changed: %+v", h.Transport)
			}
		})
	}
}

// 没有 upstreamHostMode 的旧 Vault：没有显式旧 Host 时按新默认保留原始 Host。
func TestCompileCaddyHTTPSUpstreamDefaultsToOriginalHost(t *testing.T) {
	data := model.VaultData{
		Forwards: []model.Forward{localForward(1, 8443)},
		WebRoutes: []model.WebRoute{
			{ID: 1, ForwardID: 1, Domain: "grafana.example.com", CaddyEnabled: true, UpstreamScheme: "https", TLSSNI: "grafana.internal"},
		},
	}
	raw, err := route.CompileCaddy(data)
	if err != nil {
		t.Fatal(err)
	}
	h := decodeCaddy(t, raw).Apps.HTTP.Servers["tunnelboard"].Routes[0].Handle[0]
	if got := h.Headers.Request.Set["Host"]; len(got) != 1 || got[0] != "{http.request.host}" {
		t.Fatalf("default Host header = %v, want [{http.request.host}]", got)
	}
}

// 多条 Route：路由按域名排序，subjects 为全部启用域名排序去重。
func TestCompileCaddyMultiRouteOrderAndSubjects(t *testing.T) {
	data := model.VaultData{
		Forwards: []model.Forward{localForward(1, 5432), localForward(2, 3000)},
		WebRoutes: []model.WebRoute{
			{ID: 1, ForwardID: 1, Domain: "zeta.test", CaddyEnabled: true},
			{ID: 2, ForwardID: 2, Domain: "db.test", CaddyEnabled: true},
			{ID: 3, ForwardID: 2, Domain: "db.test", CaddyEnabled: true},
			{ID: 4, ForwardID: 1, Domain: "off.test"},
		},
	}
	raw, err := route.CompileCaddy(data)
	if err != nil {
		t.Fatal(err)
	}
	cfg := decodeCaddy(t, raw)

	routes := cfg.Apps.HTTP.Servers["tunnelboard"].Routes
	if len(routes) != 3 {
		t.Fatalf("routes = %d, want 3 (disabled route excluded)", len(routes))
	}
	for i, want := range []string{"db.test", "db.test", "zeta.test"} {
		if routes[i].Match[0].Host[0] != want {
			t.Fatalf("routes[%d].host = %q, want %q", i, routes[i].Match[0].Host[0], want)
		}
	}
	if got := cfg.Apps.TLS.Automation.Policies[0].Subjects; len(got) != 2 || got[0] != "db.test" || got[1] != "zeta.test" {
		t.Fatalf("subjects = %v, want [db.test zeta.test]", got)
	}
}

// 纯函数：同一份输入编译两次必须字节一致。
func TestCompileCaddyDeterministic(t *testing.T) {
	data := model.VaultData{
		Forwards: []model.Forward{localForward(1, 5432), localForward(2, 8443)},
		WebRoutes: []model.WebRoute{
			{ID: 1, ForwardID: 1, Domain: "db.test", CaddyEnabled: true},
			{ID: 2, ForwardID: 2, Domain: "grafana.example.com", CaddyEnabled: true, UpstreamScheme: "https", TLSSNI: "grafana.internal"},
		},
	}
	first, err := route.CompileCaddy(data)
	if err != nil {
		t.Fatal(err)
	}
	second, err := route.CompileCaddy(data)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("two compiles differ:\n%s\n%s", first, second)
	}
}

// Route 引用的 Forward 缺失或不是 local 模式属于数据不一致，必须报错。
func TestCompileCaddyInconsistentReference(t *testing.T) {
	t.Run("Forward 缺失", func(t *testing.T) {
		data := model.VaultData{
			WebRoutes: []model.WebRoute{{ID: 1, ForwardID: 99, Domain: "db.test", CaddyEnabled: true}},
		}
		if _, err := route.CompileCaddy(data); err == nil {
			t.Fatal("want error for missing forward")
		}
	})
	t.Run("Forward 非 local", func(t *testing.T) {
		data := model.VaultData{
			Forwards:  []model.Forward{{ID: 1, Mode: "dynamic", LocalHost: "127.0.0.1", LocalPort: 1080}},
			WebRoutes: []model.WebRoute{{ID: 1, ForwardID: 1, Domain: "db.test", CaddyEnabled: true}},
		}
		if _, err := route.CompileCaddy(data); err == nil {
			t.Fatal("want error for non-local forward")
		}
	})
}
