package helper

import (
	"context"
	"testing"

	"github.com/HanZephyr/TunnelBoard/internal/route"
)

// IPC 意外断开时，GUI 进程仍可能存活；不能只关掉 root 子进程而遗留
// polkit 的 temporary authorization。清理必须按本会话取得的 exact ID 进行。
func TestLinuxPrivilegedSessionRevokesExactAuthorizationAfterConnectionFailure(t *testing.T) {
	starter := &fakeLinuxSessionStarter{}
	session, authorizer := newTestLinuxPrivilegedSession(starter)
	request := Request{Op: OpApplyManagedHosts, Hosts: []route.HostEntry{{IP: "127.0.0.1", Domain: "db.test"}}}
	if _, err := session.Call(context.Background(), request); err != nil {
		t.Fatalf("start session: %v", err)
	}

	starter.connections[0].closed = true
	if _, err := session.Call(context.Background(), request); err == nil {
		t.Fatal("call after broken root connection unexpectedly succeeded")
	}
	if got, want := authorizer.revokes, []string{"polkit-authorization-1"}; !equalStrings(got, want) {
		t.Fatalf("polkit revokes = %v, want exact active authorization %v", got, want)
	}
}
