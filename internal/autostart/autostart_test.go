package autostart

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLinuxAutostartWritesXDGEntryWithInvocationMarker(t *testing.T) {
	configHome := t.TempDir()
	path, err := writeLinuxAutostart(configHome, "", "/opt/tunnelboard/tunnelboard")
	if err != nil {
		t.Fatalf("writeLinuxAutostart() error = %v", err)
	}

	wantPath := filepath.Join(configHome, "autostart", appName+".desktop")
	if path != wantPath {
		t.Fatalf("desktop entry path = %q, want %q", path, wantPath)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read desktop entry: %v", err)
	}
	if got, want := string(data), "Exec=\"/opt/tunnelboard/tunnelboard\" \"--autostart\""; !strings.Contains(got, want) {
		t.Fatalf("desktop entry missing %q:\n%s", want, got)
	}
}

func TestIsAutostartInvocationRequiresExactMarker(t *testing.T) {
	if !IsAutostartInvocation([]string{"--autostart"}) {
		t.Fatal("IsAutostartInvocation(--autostart) = false")
	}
	if IsAutostartInvocation([]string{"--autostart=true"}) {
		t.Fatal("IsAutostartInvocation(--autostart=true) = true")
	}
}
