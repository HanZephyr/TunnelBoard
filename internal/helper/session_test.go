package helper_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/HanZephyr/TunnelBoard/internal/helper"
)

type fakeSessionConnection struct {
	mu         sync.Mutex
	calls      int
	closed     int
	callErr    error
	closeError error
}

func (c *fakeSessionConnection) Call(_ context.Context, _ helper.Request) (helper.Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	if c.callErr != nil {
		return helper.Response{}, c.callErr
	}
	return helper.Response{OK: true}, nil
}

func (c *fakeSessionConnection) Close(context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed++
	return c.closeError
}

type fakeSessionBackend struct {
	mu          sync.Mutex
	connections []*fakeSessionConnection
	starts      int
}

func (b *fakeSessionBackend) Start(context.Context) (helper.SessionConnection, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.starts++
	connection := &fakeSessionConnection{}
	b.connections = append(b.connections, connection)
	return connection, nil
}

func TestPrivilegedSessionReusesOneConnectionUntilClosed(t *testing.T) {
	backend := &fakeSessionBackend{}
	session := helper.NewPrivilegedSession(backend)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if _, err := session.Call(ctx, helper.Request{Op: helper.OpPing}); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if backend.starts != 1 || backend.connections[0].calls != 3 {
		t.Fatalf("starts/calls = %d/%d, want one elevated session reused", backend.starts, backend.connections[0].calls)
	}
	if err := session.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if backend.connections[0].closed != 1 {
		t.Fatalf("closed = %d, want 1", backend.connections[0].closed)
	}
	if _, err := session.Call(ctx, helper.Request{Op: helper.OpPing}); !errors.Is(err, helper.ErrSessionClosed) {
		t.Fatalf("call after close err = %v, want ErrSessionClosed", err)
	}
}

func TestPrivilegedSessionDropsBrokenConnectionAndCanReauthorize(t *testing.T) {
	backend := &fakeSessionBackend{}
	session := helper.NewPrivilegedSession(backend)
	ctx := context.Background()
	if err := session.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	backend.connections[0].callErr = errors.New("pipe disconnected")
	if _, err := session.Call(ctx, helper.Request{Op: helper.OpPing}); err == nil {
		t.Fatal("broken connection must fail current call")
	}
	if backend.connections[0].closed != 1 {
		t.Fatal("broken connection must be closed and forgotten")
	}
	if _, err := session.Call(ctx, helper.Request{Op: helper.OpPing}); err != nil {
		t.Fatalf("next operation may start a new elevated session: %v", err)
	}
	if backend.starts != 2 {
		t.Fatalf("starts = %d, want 2", backend.starts)
	}
}
