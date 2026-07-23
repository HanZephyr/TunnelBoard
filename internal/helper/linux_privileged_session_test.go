package helper

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/HanZephyr/TunnelBoard/internal/route"
)

type linuxSessionEffectRecorder struct {
	hostsCalls   []Request
	trustedDER   [][]byte
	untrustedSHA []string
}

func (r *linuxSessionEffectRecorder) ApplyManagedHosts(_ context.Context, request Request) (Response, error) {
	r.hostsCalls = append(r.hostsCalls, request)
	return Response{OK: true, ManagedDigest: ManagedEntriesDigest(request.Hosts)}, nil
}

func (r *linuxSessionEffectRecorder) TrustCurrentCaddyCA(_ context.Context, certDER []byte) error {
	r.trustedDER = append(r.trustedDER, append([]byte(nil), certDER...))
	return nil
}

func (r *linuxSessionEffectRecorder) UntrustCurrentCaddyCA(_ context.Context, fingerprint string) error {
	r.untrustedSHA = append(r.untrustedSHA, fingerprint)
	return nil
}

// Root 端只接受高层、结构化的 TunnelBoard 操作，绝不转发命令行或任意文件路径。
func TestLinuxPrivilegedSessionRejectsNonWhitelistedOperation(t *testing.T) {
	effects := &linuxSessionEffectRecorder{}
	server := newLinuxPrivilegedSessionServer("session-1", effects)
	response := server.handle(context.Background(), linuxPrivilegedRequest{
		Version:   linuxPrivilegeProtocolVersion,
		SessionID: "session-1",
		Operation: "run-command",
	})
	if response.OK || !strings.Contains(response.Error, "unsupported") {
		t.Fatalf("response = %+v, want unsupported operation rejection", response)
	}
	if len(effects.hostsCalls) != 0 || len(effects.trustedDER) != 0 || len(effects.untrustedSHA) != 0 {
		t.Fatalf("rejected request must have no effects: %+v", effects)
	}
}

func TestLinuxPrivilegedSessionRequiresExactSessionID(t *testing.T) {
	effects := &linuxSessionEffectRecorder{}
	server := newLinuxPrivilegedSessionServer("session-1", effects)
	response := server.handle(context.Background(), linuxPrivilegedRequest{
		Version:   linuxPrivilegeProtocolVersion,
		SessionID: "another-session",
		Operation: linuxPrivilegeApplyManagedHosts,
		Hosts:     []route.HostEntry{{IP: "127.0.0.1", Domain: "db.test"}},
	})
	if response.OK || !strings.Contains(response.Error, "session") {
		t.Fatalf("response = %+v, want exact session id rejection", response)
	}
	if len(effects.hostsCalls) != 0 {
		t.Fatal("wrong session id must not mutate hosts")
	}
}

type fakeLinuxSessionStarter struct {
	connections []*fakeLinuxSessionConnection
}

func (s *fakeLinuxSessionStarter) Start(_ context.Context, sessionID, _ string) (linuxPrivilegedSessionConnection, error) {
	connection := &fakeLinuxSessionConnection{sessionID: sessionID}
	s.connections = append(s.connections, connection)
	return connection, nil
}

type fakeLinuxPolkitAuthorizer struct {
	grants  []string
	revokes []string
}

func (a *fakeLinuxPolkitAuthorizer) Authorize(context.Context) (string, error) {
	id := "polkit-authorization-" + string(rune('1'+len(a.grants)))
	a.grants = append(a.grants, id)
	return id, nil
}

func (a *fakeLinuxPolkitAuthorizer) Revoke(_ context.Context, authorizationID string) error {
	a.revokes = append(a.revokes, authorizationID)
	return nil
}

func newTestLinuxPrivilegedSession(starter *fakeLinuxSessionStarter) (*linuxPrivilegedSession, *fakeLinuxPolkitAuthorizer) {
	authorizer := &fakeLinuxPolkitAuthorizer{}
	return newLinuxPrivilegedSessionWithAuthorizer(starter, authorizer), authorizer
}

type fakeLinuxSessionConnection struct {
	sessionID string
	requests  []linuxPrivilegedRequest
	closed    bool
}

func (c *fakeLinuxSessionConnection) Call(_ context.Context, request linuxPrivilegedRequest) (linuxPrivilegedResponse, error) {
	if c.closed {
		return linuxPrivilegedResponse{}, errors.New("closed")
	}
	c.requests = append(c.requests, request)
	return linuxPrivilegedResponse{OK: true, ManagedDigest: ManagedEntriesDigest(request.Hosts)}, nil
}

func (c *fakeLinuxSessionConnection) Close(context.Context) error {
	c.closed = true
	return nil
}

// 授权缓存只属于当前主程序会话：超过五分钟会撤销旧 ID 并新建会话，不能被新进程复用。
func TestLinuxPrivilegedSessionExpiresAndRevokesOnlyItsOwnID(t *testing.T) {
	now := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	starter := &fakeLinuxSessionStarter{}
	session, authorizer := newTestLinuxPrivilegedSession(starter)
	session.now = func() time.Time { return now }
	session.newID = func() (string, error) { return "session-" + now.Format("150405"), nil }

	request := Request{Op: OpApplyManagedHosts, Hosts: []route.HostEntry{{IP: "127.0.0.1", Domain: "db.test"}}}
	if _, err := session.Call(context.Background(), request); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if len(starter.connections) != 1 || starter.connections[0].sessionID != "session-120000" {
		t.Fatalf("first connection = %+v", starter.connections)
	}

	now = now.Add(linuxPrivilegeSessionTTL + time.Second)
	if _, err := session.Call(context.Background(), request); err != nil {
		t.Fatalf("call after ttl: %v", err)
	}
	if len(starter.connections) != 2 {
		t.Fatalf("connections = %d, want new authorization after ttl", len(starter.connections))
	}
	oldRequests := starter.connections[0].requests
	if len(oldRequests) != 2 || oldRequests[1].Operation != linuxPrivilegeRevoke || oldRequests[1].SessionID != "session-120000" {
		t.Fatalf("old session revoke = %+v, want its exact id", oldRequests)
	}
	if starter.connections[1].sessionID == starter.connections[0].sessionID {
		t.Fatal("a new authorization must receive a non-reusable session id")
	}
	if got, want := authorizer.revokes, []string{"polkit-authorization-1"}; !equalStrings(got, want) {
		t.Fatalf("polkit revokes = %v, want %v", got, want)
	}
}

func TestLinuxPrivilegedSessionCloseRevokesOnlyActiveSession(t *testing.T) {
	starter := &fakeLinuxSessionStarter{}
	session, authorizer := newTestLinuxPrivilegedSession(starter)
	session.newID = func() (string, error) { return "exact-session", nil }
	if _, err := session.Call(context.Background(), Request{Op: OpRemoveManagedHosts}); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}
	requests := starter.connections[0].requests
	if len(requests) != 2 || requests[1].Operation != linuxPrivilegeRevoke || requests[1].SessionID != "exact-session" {
		t.Fatalf("close requests = %+v, want exact revoke", requests)
	}
	if got, want := authorizer.revokes, []string{"polkit-authorization-1"}; !equalStrings(got, want) {
		t.Fatalf("polkit revokes = %v, want %v", got, want)
	}
}

func TestLinuxPrivilegedSessionNeverMapsPingToHostsMutation(t *testing.T) {
	starter := &fakeLinuxSessionStarter{}
	session, _ := newTestLinuxPrivilegedSession(starter)
	if _, err := session.Call(context.Background(), Request{Op: OpPing}); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("ping err = %v, want unsupported request", err)
	}
	if len(starter.connections) != 0 {
		t.Fatalf("connections = %d, unsupported request must not start an authorization", len(starter.connections))
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
