package loopbackhttps_test

import (
	"fmt"
	"runtime"
	"testing"

	"github.com/HanZephyr/TunnelBoard/internal/loopbackhttps"
)

func TestBindAddrFollowsPlatformPortPolicy(t *testing.T) {
	wantPort := loopbackhttps.PublicPort
	if runtime.GOOS == "darwin" {
		wantPort = loopbackhttps.DarwinUnprivilegedPort
	}
	if loopbackhttps.BindPort() != wantPort {
		t.Fatalf("BindPort = %d, want %d", loopbackhttps.BindPort(), wantPort)
	}
	want := fmt.Sprintf("127.0.0.1:%d", wantPort)
	if got := loopbackhttps.BindAddr(); got != want {
		t.Fatalf("BindAddr = %q, want %q", got, want)
	}
}
