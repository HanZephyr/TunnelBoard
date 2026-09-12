package route_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/HanZephyr/TunnelBoard/internal/model"
	"github.com/HanZephyr/TunnelBoard/internal/route"
)

func TestCaddyProxyHeadersIntegration(t *testing.T) {
	binary := os.Getenv("TUNNELBOARD_TEST_CADDY_BINARY")
	if binary == "" {
		t.Skip("set TUNNELBOARD_TEST_CADDY_BINARY to run real Caddy request tests")
	}
	for _, remove := range []bool{false, true} {
		for _, mode := range []model.UpstreamHostMode{model.UpstreamHostModeOriginal, model.UpstreamHostModeCustom} {
			t.Run(fmt.Sprintf("remove=%t/host=%s", remove, mode), func(t *testing.T) {
				type observation struct {
					Host    string
					Headers http.Header
				}
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(observation{Host: r.Host, Headers: r.Header})
				}))
				defer upstream.Close()
				_, portText, err := net.SplitHostPort(upstream.Listener.Addr().String())
				if err != nil {
					t.Fatal(err)
				}
				port, err := strconv.Atoi(portText)
				if err != nil {
					t.Fatal(err)
				}
				upstreamHost, expectedHost := "", "headers.test"
				if mode == model.UpstreamHostModeCustom {
					upstreamHost, expectedHost = "backend.internal:8080", "backend.internal:8080"
				}
				compiled, err := route.CompileCaddy(model.VaultData{
					Forwards: []model.Forward{localForward(1, port)},
					WebRoutes: []model.WebRoute{{ID: 1, ForwardID: 1, Domain: "headers.test", CaddyEnabled: true,
						UpstreamScheme: "http", UpstreamHostMode: mode,
						UpstreamHost: upstreamHost, RemoveProxyHeaders: remove}},
				})
				if err != nil {
					t.Fatal(err)
				}
				var source struct {
					Apps struct {
						HTTP struct {
							Servers map[string]struct {
								Routes []struct {
									Handle []json.RawMessage `json:"handle"`
								} `json:"routes"`
							} `json:"servers"`
						} `json:"http"`
					} `json:"apps"`
				}
				if err := json.Unmarshal(compiled, &source); err != nil {
					t.Fatal(err)
				}
				routes := source.Apps.HTTP.Servers["tunnelboard"].Routes
				if len(routes) != 1 || len(routes[0].Handle) != 1 {
					t.Fatalf("unexpected compiled routes: %s", compiled)
				}
				// 只复用代理 handler；生产 TLS、PKI、admin 和监听配置不进入测试进程。
				listener, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				address := listener.Addr().String()
				if err := listener.Close(); err != nil {
					t.Fatal(err)
				}
				temporary := t.TempDir()
				config := map[string]any{
					"admin":   map[string]any{"disabled": true},
					"storage": map[string]any{"module": "file_system", "root": filepath.Join(temporary, "storage")},
					"apps": map[string]any{"http": map[string]any{"servers": map[string]any{"test": map[string]any{
						"listen": []string{address}, "automatic_https": map[string]any{"disable": true},
						"routes": []any{map[string]any{"handle": routes[0].Handle}},
					}}}},
				}
				raw, err := json.Marshal(config)
				if err != nil {
					t.Fatal(err)
				}
				configPath := filepath.Join(temporary, "caddy.json")
				if err := os.WriteFile(configPath, raw, 0600); err != nil {
					t.Fatal(err)
				}
				logPath := filepath.Join(temporary, "caddy.log")
				logFile, err := os.Create(logPath)
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				command := exec.CommandContext(ctx, binary, "run", "--config", configPath)
				command.Dir = temporary
				command.Env = append(os.Environ(), "XDG_CONFIG_HOME="+temporary, "XDG_DATA_HOME="+temporary)
				command.Stdout, command.Stderr = logFile, logFile
				if err := command.Start(); err != nil {
					cancel()
					_ = logFile.Close()
					t.Fatal(err)
				}
				defer func() {
					cancel()
					_ = command.Wait()
					_ = logFile.Close()
					if t.Failed() {
						contents, _ := os.ReadFile(logPath)
						t.Logf("Caddy output:\n%s", contents)
					}
				}()
				transport := &http.Transport{Proxy: nil}
				defer transport.CloseIdleConnections()
				client := &http.Client{Transport: transport, Timeout: time.Second}
				ready := false
				for deadline := time.Now().Add(8 * time.Second); time.Now().Before(deadline); {
					response, err := client.Get("http://" + address + "/ready")
					if err == nil {
						_ = response.Body.Close()
						ready = response.StatusCode == http.StatusOK
						if ready {
							break
						}
					}
					time.Sleep(50 * time.Millisecond)
				}
				if !ready {
					t.Fatal("isolated Caddy did not become ready")
				}
				request, err := http.NewRequest(http.MethodGet, "http://"+address+"/echo", nil)
				if err != nil {
					t.Fatal(err)
				}
				request.Host = "headers.test"
				for key, value := range map[string]string{
					"Forwarded": "for=192.0.2.10;proto=https", "X-Forwarded-For": "192.0.2.10",
					"X-Forwarded-Host": "client.example", "X-Forwarded-Proto": "https",
					"X-Forwarded-Custom": "custom-proxy", "X-Real-IP": "192.0.2.11",
					"Authorization": "Bearer integration-only", "X-Business-Context": "keep-me",
				} {
					request.Header.Set(key, value)
				}
				response, err := client.Do(request)
				if err != nil {
					t.Fatal(err)
				}
				defer response.Body.Close()
				if response.StatusCode != http.StatusOK {
					t.Fatalf("upstream status = %d", response.StatusCode)
				}
				var got observation
				if err := json.NewDecoder(response.Body).Decode(&got); err != nil {
					t.Fatal(err)
				}
				if got.Host != expectedHost || got.Headers.Get("Authorization") != "Bearer integration-only" || got.Headers.Get("X-Business-Context") != "keep-me" {
					t.Fatalf("Host or business headers changed: %+v", got)
				}
				if remove {
					for key := range got.Headers {
						lower := strings.ToLower(key)
						if lower == "forwarded" || lower == "via" || lower == "x-real-ip" || strings.HasPrefix(lower, "x-forwarded-") {
							t.Errorf("proxy header reached upstream: %s=%q", key, got.Headers[key])
						}
					}
				} else {
					if !strings.Contains(got.Headers.Get("Via"), "Caddy") {
						t.Errorf("default Via header does not identify Caddy: %q", got.Headers.Get("Via"))
					}
					for _, key := range []string{"X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "Forwarded", "X-Real-IP", "X-Forwarded-Custom"} {
						if got.Headers.Get(key) == "" {
							t.Errorf("default proxy header missing: %s", key)
						}
					}
				}
			})
		}
	}
}
