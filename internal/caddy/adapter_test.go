package caddy_test

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
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
		t.Fatalf("Locate = %q, want %q", got, bin)
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
	if err != nil || got != bin {
		t.Fatalf("Locate = (%q, %v), want %q", got, err, bin)
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
		t.Fatalf("matching hash: %v", err)
	}
	a.ExpectedSHA256 = strings.Repeat("0", 64)
	if err := a.VerifySHA256(bin); err == nil {
		t.Fatal("mismatched hash must fail")
	}
}

func TestCheckAddrConflict(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	if err := caddy.CheckAddr(fmt.Sprintf("127.0.0.1:%d", port)); err == nil {
		t.Fatal("occupied addr must fail")
	}
}

type fakeProcess struct {
	pid  int
	done chan struct{}
	once sync.Once
}

func (p *fakeProcess) PID() int    { return p.pid }
func (p *fakeProcess) Wait() error { <-p.done; return nil }
func (p *fakeProcess) Kill() error { p.once.Do(func() { close(p.done) }); return nil }

type fakeAdmin struct {
	mu       sync.Mutex
	loads    int
	lastBody string
	listener net.Listener
	process  *fakeProcess
}

func installOwnedFake(t *testing.T, a *caddy.Adapter) *fakeAdmin {
	t.Helper()
	t.Cleanup(func() { _ = os.RemoveAll(a.RuntimeDir()) })
	bin := filepath.Join(t.TempDir(), "caddy.exe")
	if runtime.GOOS != "windows" {
		bin = filepath.Join(t.TempDir(), "caddy")
	}
	if err := os.WriteFile(bin, []byte("fake"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(caddy.EnvPathOverride, bin)
	a.ValidateConfig = func(context.Context, string, string, string, []string, io.Writer) error { return nil }
	a.CheckPort = func() error { return nil }
	admin := &fakeAdmin{process: &fakeProcess{pid: 4242, done: make(chan struct{})}}
	parseSocket := func(raw []byte) (string, error) {
		var cfg struct {
			Admin struct {
				Listen string `json:"listen"`
			} `json:"admin"`
		}
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return "", err
		}
		socket := strings.TrimSuffix(strings.TrimPrefix(cfg.Admin.Listen, "unix/"), "|0600")
		if runtime.GOOS == "windows" && strings.HasPrefix(socket, "/") {
			socket = strings.TrimPrefix(socket, "/")
		}
		return socket, nil
	}
	var startServer func(string) (net.Listener, error)
	startServer = func(socket string) (net.Listener, error) {
		_ = os.Remove(socket)
		listener, err := net.Listen("unix", socket)
		if err != nil {
			return nil, err
		}
		server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/config/":
				w.WriteHeader(http.StatusOK)
			case "/load":
				body, _ := io.ReadAll(r.Body)
				newSocket, parseErr := parseSocket(body)
				if parseErr != nil {
					http.Error(w, parseErr.Error(), http.StatusBadRequest)
					return
				}
				newListener, listenErr := startServer(newSocket)
				if listenErr != nil {
					http.Error(w, listenErr.Error(), http.StatusInternalServerError)
					return
				}
				admin.mu.Lock()
				admin.loads++
				admin.lastBody = string(body)
				admin.listener = newListener
				admin.mu.Unlock()
				w.WriteHeader(http.StatusOK)
				go func() { time.Sleep(10 * time.Millisecond); _ = listener.Close() }()
			case "/stop":
				w.WriteHeader(http.StatusOK)
				go func() {
					time.Sleep(10 * time.Millisecond)
					admin.process.once.Do(func() { close(admin.process.done) })
					_ = listener.Close()
				}()
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		})}
		go server.Serve(listener)
		return listener, nil
	}
	a.StartProcess = func(_ string, args []string, _ string, _ []string, _, _ io.Writer) (caddy.Process, error) {
		configPath := args[len(args)-1]
		raw, err := os.ReadFile(configPath)
		if err != nil {
			return nil, err
		}
		socket, err := parseSocket(raw)
		if err != nil {
			return nil, err
		}
		listener, err := startServer(socket)
		if err != nil {
			return nil, err
		}
		admin.listener = listener
		return admin.process, nil
	}
	return admin
}

func routeConfig(host string) []byte {
	return []byte(fmt.Sprintf(`{"apps":{"http":{"servers":{"tunnelboard":{"listen":[":443"],"routes":[]}}}},"testHost":%q}`, host))
}

func shortRuntimeBase(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(root, ".s-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func TestApplyUsesOwnedProcessAndAFUnixAdmin(t *testing.T) {
	a := caddy.NewWithRuntimeBase(t.TempDir(), shortRuntimeBase(t))
	admin := installOwnedFake(t, a)
	if strings.Contains(a.AdminListen(), "127.0.0.1") || !strings.HasPrefix(a.AdminListen(), "unix/") {
		t.Fatalf("admin endpoint must be AF_UNIX, got %q", a.AdminListen())
	}
	first, err := a.Apply(context.Background(), "r1", routeConfig("one"))
	if err != nil || first.Outcome != caddy.OutcomeApplied {
		t.Fatalf("first Apply = (%+v, %v)", first, err)
	}
	status := a.Status(context.Background())
	if !status.Owned || !status.Ready || status.PID != 4242 {
		t.Fatalf("owned status = %+v", status)
	}

	unchanged, err := a.Apply(context.Background(), "r1", routeConfig("one"))
	if err != nil || unchanged.Outcome != caddy.OutcomeUnchanged {
		t.Fatalf("unchanged = (%+v, %v)", unchanged, err)
	}
	changed, err := a.Apply(context.Background(), "r2", routeConfig("two"))
	if err != nil || changed.Outcome != caddy.OutcomeApplied {
		t.Fatalf("changed = (%+v, %v)", changed, err)
	}
	admin.mu.Lock()
	loads, body := admin.loads, admin.lastBody
	admin.mu.Unlock()
	if loads != 1 || !strings.Contains(body, `"admin"`) || !strings.Contains(body, strings.TrimSuffix(a.AdminListen(), "|0600")) {
		t.Fatalf("hot reload loads=%d body=%s", loads, body)
	}
}

func TestStopOnlyStopsOwnedProcess(t *testing.T) {
	a := caddy.NewWithRuntimeBase(t.TempDir(), shortRuntimeBase(t))
	installOwnedFake(t, a)
	if err := a.Stop(context.Background()); err != nil {
		t.Fatalf("unowned Stop: %v", err)
	}
	if _, err := a.Apply(context.Background(), "r1", routeConfig("one")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := a.Stop(ctx); err != nil {
		t.Fatalf("owned Stop: %v", err)
	}
	if a.Status(context.Background()).Owned {
		t.Fatal("process must no longer be owned")
	}
}

func TestRootCACertReadsPEM(t *testing.T) {
	a := caddy.New(t.TempDir())
	der := []byte("fake-der-bytes")
	dir := filepath.Join(a.DataDir, "caddy", "pki", "authorities", "local")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "root.crt"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := a.RootCACert(time.Second)
	if err != nil || string(got) != string(der) {
		t.Fatalf("RootCACert = (%q, %v)", got, err)
	}
}

func TestPrepareRootCAGeneratesAuthorityWithoutStartingProcess(t *testing.T) {
	a := caddy.NewWithRuntimeBase(t.TempDir(), t.TempDir())
	bin := filepath.Join(t.TempDir(), "caddy.exe")
	if runtime.GOOS != "windows" {
		bin = filepath.Join(t.TempDir(), "caddy")
	}
	if err := os.WriteFile(bin, []byte("fake"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(caddy.EnvPathOverride, bin)
	wantDER := []byte("prepared-ca-der")
	a.ValidateConfig = func(_ context.Context, _, _, _ string, _ []string, _ io.Writer) error {
		dir := filepath.Join(a.DataDir, "caddy", "pki", "authorities", "local")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dir, "root.crt"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: wantDER}), 0o600)
	}
	a.StartProcess = func(string, []string, string, []string, io.Writer, io.Writer) (caddy.Process, error) {
		t.Fatal("PrepareRootCA must not start Caddy")
		return nil, nil
	}

	got, err := a.PrepareRootCA(context.Background(), routeConfig("prepare.test"))
	if err != nil {
		t.Fatalf("PrepareRootCA: %v", err)
	}
	if string(got) != string(wantDER) {
		t.Fatalf("PrepareRootCA DER = %q, want %q", got, wantDER)
	}
	if a.Status(context.Background()).Owned {
		t.Fatal("PrepareRootCA must not own a running process")
	}
}

// TestPinnedCaddyAFUnixPOC 是发布门禁使用的真二进制 POC；普通单元测试没有钉版二进制时跳过。
func TestPinnedCaddyAFUnixPOC(t *testing.T) {
	bin := strings.TrimSpace(os.Getenv("TUNNELBOARD_CADDY_POC_PATH"))
	if bin == "" {
		t.Skip("set TUNNELBOARD_CADDY_POC_PATH to run pinned Caddy AF_UNIX POC")
	}
	if absolute, err := filepath.Abs(bin); err == nil {
		bin = absolute
	}
	t.Setenv(caddy.EnvPathOverride, bin)
	a := caddy.NewWithRuntimeBase(t.TempDir(), shortRuntimeBase(t))
	t.Cleanup(func() { _ = os.RemoveAll(a.RuntimeDir()) })
	a.CheckPort = func() error { return nil }
	config := []byte(`{"apps":{"http":{"servers":{"poc":{"listen":["127.0.0.1:0"],"routes":[]}}},"tls":{"automation":{"policies":[{"subjects":["poc.test"],"issuers":[{"module":"internal"}]}]}},"pki":{"certificate_authorities":{"local":{"install_trust":false}}}}}`)
	der, err := a.PrepareRootCA(context.Background(), config)
	if err != nil {
		t.Fatalf("PrepareRootCA: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil || !cert.IsCA {
		t.Fatalf("prepared authority is not a CA: cert=%v err=%v", cert, err)
	}
	if a.Status(context.Background()).Owned {
		t.Fatal("PrepareRootCA must not start Caddy")
	}
	result, err := a.Apply(context.Background(), "poc-1", config)
	if err != nil || result.Outcome != caddy.OutcomeApplied {
		logBytes, _ := os.ReadFile(filepath.Join(a.DataDir, "logs", "caddy.log"))
		t.Fatalf("cold Apply = (%+v, %v)\n%s", result, err, logBytes)
	}
	changed := []byte(`{"apps":{"http":{"servers":{"poc":{"listen":["127.0.0.1:0"],"routes":[],"logs":{}}}}}}`)
	result, err = a.Apply(context.Background(), "poc-2", changed)
	if err != nil || result.Outcome != caddy.OutcomeApplied {
		t.Fatalf("hot Apply = (%+v, %v)", result, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.Stop(ctx); err != nil {
		logBytes, _ := os.ReadFile(filepath.Join(a.DataDir, "logs", "caddy.log"))
		t.Fatalf("Stop: %v\n%s", err, logBytes)
	}
}
