package caddy_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HanZephyr/TunnelBoard/internal/caddy"
)

func TestLocatePrefersEnvOverride(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "caddy.exe")
	if err := os.WriteFile(bin, []byte("fake"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(caddy.EnvPathOverride, bin)
	a := caddy.New(t.TempDir())
	got, err := a.Locate()
	if err != nil {
		t.Fatalf("Locate: %v", err)
	}
	if got != bin {
		t.Fatalf("Locate = %q, want env override %q", got, bin)
	}
}

func TestLocateScansCandidates(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "caddy.exe")
	if err := os.WriteFile(bin, []byte("fake"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(caddy.EnvPathOverride, "")
	a := caddy.New(t.TempDir())
	a.Candidates = []string{filepath.Join(t.TempDir(), "missing.exe"), bin}
	got, err := a.Locate()
	if err != nil {
		t.Fatalf("Locate: %v", err)
	}
	if got != bin {
		t.Fatalf("Locate = %q, want %q", got, bin)
	}

	a.Candidates = []string{filepath.Join(t.TempDir(), "nothing.exe")}
	if _, err := a.Locate(); err == nil {
		t.Fatal("missing binary must return ErrNotFound")
	}
}

func TestVerifySHA256(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "caddy.exe")
	content := []byte("caddy-binary-content")
	if err := os.WriteFile(bin, content, 0o755); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)

	a := caddy.New(t.TempDir())
	a.ExpectedSHA256 = hex.EncodeToString(sum[:])
	if err := a.VerifySHA256(bin); err != nil {
		t.Fatalf("matching hash should pass: %v", err)
	}
	a.ExpectedSHA256 = strings.Repeat("0", 64)
	if err := a.VerifySHA256(bin); err == nil {
		t.Fatal("mismatched hash must fail")
	}
	a.ExpectedSHA256 = ""
	if err := a.VerifySHA256(bin); err != nil {
		t.Fatalf("empty expected hash (dev mode) should skip: %v", err)
	}
}

func TestCheckAddrConflict(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	if err := caddy.CheckAddr(fmt.Sprintf("127.0.0.1:%d", port)); err == nil {
		t.Fatal("occupied addr must fail diagnose")
	}
}

func TestReloadUsesAdminAPIWhenRunning(t *testing.T) {
	var gotMethod, gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/config/":
			w.WriteHeader(http.StatusOK)
		case "/load":
			gotMethod, gotPath = r.Method, r.URL.Path
			buf := make([]byte, r.ContentLength)
			_, _ = r.Body.Read(buf)
			gotBody = string(buf)
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	a := caddy.New(t.TempDir())
	a.AdminURL = srv.URL
	started := false
	a.StartProcess = func(bin string, args []string, dir string, env []string, logFile *os.File) error {
		started = true
		return nil
	}

	config := []byte(`{"admin":{"listen":"127.0.0.1:2019"}}`)
	if err := a.Reload(config); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if started {
		t.Fatal("running caddy must not be cold-started on reload")
	}
	if gotMethod != http.MethodPost || gotPath != "/load" {
		t.Fatalf("reload call = %s %s, want POST /load", gotMethod, gotPath)
	}
	if gotBody != string(config) {
		t.Fatalf("reload body = %q, want %q", gotBody, config)
	}
	// 配置文件已原子落盘
	saved, err := os.ReadFile(filepath.Join(a.DataDir, "caddy.json"))
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}
	if string(saved) != string(config) {
		t.Fatalf("saved config = %q, want %q", saved, config)
	}
}

func TestReloadColdStartsWhenNotRunning(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()

	a := caddy.New(t.TempDir())
	a.AdminURL = srv.URL
	var startedBin string
	a.StartProcess = func(bin string, args []string, dir string, env []string, logFile *os.File) error {
		startedBin = bin
		return nil
	}
	t.Setenv(caddy.EnvPathOverride, filepath.Join(t.TempDir(), "caddy.exe"))
	_ = os.WriteFile(os.Getenv(caddy.EnvPathOverride), []byte("fake"), 0o755)

	if err := a.Reload([]byte(`{}`)); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if startedBin == "" {
		t.Fatal("not-running caddy must be cold-started on reload")
	}
}

func TestStopCallsAdminAndWaitsExit(t *testing.T) {
	alive := true
	stopCalled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/config/":
			if alive {
				w.WriteHeader(http.StatusOK)
			} else {
				w.WriteHeader(http.StatusServiceUnavailable)
			}
		case "/stop":
			stopCalled = true
			alive = false
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()

	a := caddy.New(t.TempDir())
	a.AdminURL = srv.URL
	if err := a.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if !stopCalled {
		t.Fatal("/stop must be called on running caddy")
	}

	alive = false
	stopCalled = false
	if err := a.Stop(); err != nil {
		t.Fatalf("Stop when not running must be idempotent: %v", err)
	}
	if stopCalled {
		t.Fatal("/stop must not be called when not running")
	}
}

func TestRootCACertReadsPEM(t *testing.T) {
	a := caddy.New(t.TempDir())
	der := []byte("fake-der-bytes")
	dir := filepath.Join(a.DataDir, "caddy", "pki", "authorities", "local")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(filepath.Join(dir, "root.crt"), pemBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := a.RootCACert(time.Second)
	if err != nil {
		t.Fatalf("RootCACert: %v", err)
	}
	if string(got) != string(der) {
		t.Fatalf("RootCACert = %q, want %q", got, der)
	}

	empty := caddy.New(t.TempDir())
	if _, err := empty.RootCACert(300 * time.Millisecond); err == nil {
		t.Fatal("missing root CA must time out with error")
	}
}
