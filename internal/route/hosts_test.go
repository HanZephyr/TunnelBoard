package route_test

import (
	"testing"

	"github.com/HanZephyr/TunnelBoard/internal/model"
	"github.com/HanZephyr/TunnelBoard/internal/route"
)

func localForward(id, port int) model.Forward {
	return model.Forward{ID: id, Mode: "local", LocalHost: "127.0.0.1", LocalPort: port}
}

// 命中的 Route（HostsEnabled=true + local Forward）产生一条 127.0.0.1 映射。
func TestPlanHostsEmitsEntry(t *testing.T) {
	data := model.VaultData{
		Forwards:  []model.Forward{localForward(1, 5432)},
		WebRoutes: []model.WebRoute{{ID: 1, ForwardID: 1, Domain: "db.test", HostsEnabled: true}},
	}
	entries, confirm := route.PlanHosts(data)
	if len(entries) != 1 || entries[0] != (route.HostEntry{Domain: "db.test", IP: "127.0.0.1"}) {
		t.Fatalf("entries = %+v, want single 127.0.0.1 db.test", entries)
	}
	if len(confirm) != 0 {
		t.Fatalf("requiresConfirmation = %v, want empty for .test domain", confirm)
	}
}

// HostsEnabled=false 的 Route 不产生 hosts 记录。
func TestPlanHostsSkipsDisabled(t *testing.T) {
	data := model.VaultData{
		Forwards:  []model.Forward{localForward(1, 5432)},
		WebRoutes: []model.WebRoute{{ID: 1, ForwardID: 1, Domain: "db.test"}},
	}
	entries, confirm := route.PlanHosts(data)
	if len(entries) != 0 || len(confirm) != 0 {
		t.Fatalf("entries = %+v, confirm = %v, want both empty", entries, confirm)
	}
}

// CaddyEnabled 与 hosts 规划互不影响：只开 Caddy 不出行，开 hosts 不开 Caddy 正常出行。
func TestPlanHostsIgnoresCaddyEnabled(t *testing.T) {
	data := model.VaultData{
		Forwards: []model.Forward{localForward(1, 5432), localForward(2, 3000)},
		WebRoutes: []model.WebRoute{
			{ID: 1, ForwardID: 1, Domain: "caddy-only.test", CaddyEnabled: true},
			{ID: 2, ForwardID: 2, Domain: "hosts-only.test", HostsEnabled: true},
		},
	}
	entries, _ := route.PlanHosts(data)
	if len(entries) != 1 || entries[0].Domain != "hosts-only.test" {
		t.Fatalf("entries = %+v, want only hosts-only.test", entries)
	}
}

// Forward 缺失或不是 local 模式时不产生记录（规划层容错跳过，编译层才报错）。
func TestPlanHostsSkipsNonLocalOrMissingForward(t *testing.T) {
	data := model.VaultData{
		Forwards: []model.Forward{
			localForward(1, 5432),
			{ID: 2, Mode: "remote", LocalHost: "127.0.0.1", LocalPort: 2222},
		},
		WebRoutes: []model.WebRoute{
			{ID: 1, ForwardID: 2, Domain: "remote.test", HostsEnabled: true},
			{ID: 2, ForwardID: 99, Domain: "missing.test", HostsEnabled: true},
			{ID: 3, ForwardID: 1, Domain: "db.test", HostsEnabled: true},
		},
	}
	entries, _ := route.PlanHosts(data)
	if len(entries) != 1 || entries[0].Domain != "db.test" {
		t.Fatalf("entries = %+v, want only db.test", entries)
	}
}

// 输出按域名排序；同一域名多条 Route 只出一行。
func TestPlanHostsSortedAndDeduped(t *testing.T) {
	data := model.VaultData{
		Forwards: []model.Forward{localForward(1, 5432), localForward(2, 3000)},
		WebRoutes: []model.WebRoute{
			{ID: 1, ForwardID: 1, Domain: "zeta.test", HostsEnabled: true},
			{ID: 2, ForwardID: 2, Domain: "alpha.test", HostsEnabled: true},
			{ID: 3, ForwardID: 2, Domain: "alpha.test", HostsEnabled: true},
		},
	}
	entries, _ := route.PlanHosts(data)
	if len(entries) != 2 || entries[0].Domain != "alpha.test" || entries[1].Domain != "zeta.test" {
		t.Fatalf("entries = %+v, want sorted [alpha.test zeta.test]", entries)
	}
}

// 非 .test/.localhost 后缀的域名进入确认列表；后缀判断按小写归一。
func TestPlanHostsRequiresConfirmation(t *testing.T) {
	data := model.VaultData{
		Forwards: []model.Forward{localForward(1, 5432), localForward(2, 3000), localForward(3, 8080), localForward(4, 9090)},
		WebRoutes: []model.WebRoute{
			{ID: 1, ForwardID: 1, Domain: "grafana.example.com", HostsEnabled: true},
			{ID: 2, ForwardID: 2, Domain: "db.test", HostsEnabled: true},
			{ID: 3, ForwardID: 3, Domain: "x.localhost", HostsEnabled: true},
			{ID: 4, ForwardID: 4, Domain: "FOO.TEST", HostsEnabled: true},
		},
	}
	entries, confirm := route.PlanHosts(data)
	if len(entries) != 4 {
		t.Fatalf("entries = %+v, want all 4 domains", entries)
	}
	if len(confirm) != 1 || confirm[0] != "grafana.example.com" {
		t.Fatalf("requiresConfirmation = %v, want only [grafana.example.com]", confirm)
	}
}
