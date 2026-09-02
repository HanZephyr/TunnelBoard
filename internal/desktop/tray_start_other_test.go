//go:build !darwin

package desktop_test

import (
	"testing"

	"github.com/HanZephyr/TunnelBoard/internal/desktop"
)

func TestStartTrayPreservingAppDelegateInvokesStart(t *testing.T) {
	called := false
	desktop.StartTrayPreservingAppDelegate(func() { called = true })
	if !called {
		t.Fatal("start callback was not invoked")
	}
	desktop.StartTrayPreservingAppDelegate(nil)
}
