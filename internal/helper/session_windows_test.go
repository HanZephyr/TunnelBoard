//go:build windows

package helper

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/HanZephyr/TunnelBoard/internal/route"
)

type inProcessElevatedProcess struct{ pid uint32 }

func (p *inProcessElevatedProcess) PID() uint32                { return p.pid }
func (p *inProcessElevatedProcess) Wait(context.Context) error { return nil }
func (p *inProcessElevatedProcess) Close() error               { return nil }

func TestWindowsSessionUsesRandomPipeExactPIDAndPersistentConnection(t *testing.T) {
	hostsPath := filepath.Join(t.TempDir(), "hosts")
	var mu sync.Mutex
	var pipePaths []string
	var helperDone chan error
	backend := &windowsSessionBackend{
		timeout:  3 * time.Second,
		ownerSID: func() (string, error) { return CurrentUserSID() },
		launch: func(pipePath string, parentPID uint32, protocol string) (elevatedProcess, error) {
			mu.Lock()
			pipePaths = append(pipePaths, pipePath)
			helperDone = make(chan error, 1)
			mu.Unlock()
			go func() {
				helperDone <- RunSessionHelper(SessionHelperOptions{
					PipePath: pipePath, ParentPID: parentPID, Protocol: protocol,
					Environment:         Environment{HostsPath: hostsPath, Version: protocol},
					VerifyParent:        func(uint32) error { return nil },
					SkipLegacyMigration: true,
				})
			}()
			return &inProcessElevatedProcess{pid: uint32(os.Getpid())}, nil
		},
	}
	session := NewPrivilegedSession(backend)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	request := Request{Op: OpApplyManagedHosts, Hosts: []route.HostEntry{{Domain: "app.test", IP: "127.0.0.1"}}}
	if _, err := session.Call(ctx, request); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if _, err := session.Call(ctx, request); err != nil {
		t.Fatalf("second call: %v", err)
	}
	mu.Lock()
	paths := append([]string(nil), pipePaths...)
	done := helperDone
	mu.Unlock()
	if len(paths) != 1 || !strings.HasPrefix(paths[0], `\\.\pipe\tunnelboard-helper-`) || paths[0] == PipePath {
		t.Fatalf("pipe paths = %#v, want one random per-session path", paths)
	}
	if err := session.Close(ctx); err != nil {
		t.Fatalf("close: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("helper exit: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("session helper did not exit after close")
	}
}

func TestWindowsSessionRejectsUnexpectedClientPID(t *testing.T) {
	backend := &windowsSessionBackend{
		timeout:  500 * time.Millisecond,
		ownerSID: func() (string, error) { return CurrentUserSID() },
		launch: func(pipePath string, parentPID uint32, protocol string) (elevatedProcess, error) {
			go func() {
				_ = RunSessionHelper(SessionHelperOptions{
					PipePath: pipePath, ParentPID: parentPID, Protocol: protocol,
					Environment:         Environment{HostsPath: filepath.Join(t.TempDir(), "hosts"), Version: protocol},
					VerifyParent:        func(uint32) error { return nil },
					SkipLegacyMigration: true,
				})
			}()
			return &inProcessElevatedProcess{pid: uint32(os.Getpid()) + 1}, nil
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := NewPrivilegedSession(backend).Call(ctx, Request{Op: OpPing}); err == nil || !strings.Contains(err.Error(), "PID") {
		t.Fatalf("err = %v, want PID mismatch rejection", err)
	}
}
