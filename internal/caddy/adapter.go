// Package caddy 管理 TunnelBoard 当前应用 generation 唯一拥有的 Caddy 进程。
// 管理 API 只暴露在当前用户运行目录的 AF_UNIX socket，不监听 TCP 端口。
package caddy

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const EnvPathOverride = "TUNNELBOARD_CADDY_PATH"

var ErrNotFound = errors.New("caddy: binary not found")

type ApplyOutcome string

const (
	OutcomeApplied      ApplyOutcome = "applied"
	OutcomeUnchanged    ApplyOutcome = "unchanged"
	OutcomePortConflict ApplyOutcome = "port_conflict"
)

type ApplyResult struct {
	Outcome ApplyOutcome `json:"outcome"`
	Detail  string       `json:"detail,omitempty"`
}

type Status struct {
	Owned      bool   `json:"owned"`
	PID        int    `json:"pid,omitempty"`
	Generation string `json:"generation"`
	Ready      bool   `json:"ready"`
	LastError  string `json:"lastError,omitempty"`
}

// Process 是 Supervisor 持有的真实子进程句柄；PID 文件和 socket 均不构成所有权证据。
type Process interface {
	PID() int
	Wait() error
	Kill() error
}

type Adapter struct {
	DataDir        string
	RuntimeBaseDir string
	ExpectedSHA256 string
	Candidates     []string
	ReadyTimeout   time.Duration
	StartProcess   func(bin string, args []string, dir string, env []string, stdout, stderr io.Writer) (Process, error)
	ValidateConfig func(ctx context.Context, bin, configPath, dir string, env []string, output io.Writer) error
	CheckPort      func() error
	Output         io.Writer

	mu           sync.Mutex
	generation   string
	runtimeDir   string
	adminSocket  string
	adminListen  string
	httpClient   *http.Client
	process      Process
	processDone  chan error
	ready        bool
	lastDigest   [sha256.Size]byte
	hasDigest    bool
	lastRevision string
	lastError    string
}

func New(dataDir string) *Adapter {
	return newAdapter(dataDir, defaultRuntimeBase(dataDir))
}

// NewWithRuntimeBase 允许测试和受控嵌入环境提供同样为当前用户独占的短路径。
// 普通应用代码必须使用 New，以避免把 runtime 路径耦合到可重定向 Vault。
func NewWithRuntimeBase(dataDir, runtimeBase string) *Adapter {
	return newAdapter(dataDir, runtimeBase)
}

func newAdapter(dataDir, runtimeBase string) *Adapter {
	generation := randomGeneration()
	runtimeDir := filepath.Join(runtimeBase, generation)
	socketPath := filepath.Join(runtimeDir, "admin-a.sock")
	a := &Adapter{
		DataDir:        dataDir,
		RuntimeBaseDir: runtimeBase,
		ReadyTimeout:   5 * time.Second,
		generation:     generation,
		runtimeDir:     runtimeDir,
		adminSocket:    socketPath,
		adminListen:    unixListenAddress(socketPath),
	}
	a.StartProcess = startOwned
	a.ValidateConfig = validateConfig
	a.CheckPort = a.DiagnosePort
	a.httpClient = newUnixHTTPClient(socketPath)
	/*
		Caddy 在 Windows 热加载时会重建 Admin listener；Supervisor 因而在
		admin-a/admin-b 之间换代，避免旧 listener 清理时 unlink 新 listener。
	*/
	if exe, err := os.Executable(); err == nil {
		a.Candidates = append(a.Candidates, filepath.Join(filepath.Dir(exe), "caddy", binaryName()))
	}
	a.Candidates = append(a.Candidates, filepath.Join(dataDir, "caddy", binaryName()))
	return a
}

func newUnixHTTPClient(socketPath string) *http.Client {
	return &http.Client{
		Timeout: 2 * time.Second,
		Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
		}},
	}
}

func defaultRuntimeBase(dataDir string) string {
	if runtime.GOOS == "windows" {
		if localAppData := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); localAppData != "" {
			return filepath.Join(localAppData, "TunnelBoard", "runtime")
		}
	}
	if cacheDir, err := os.UserCacheDir(); err == nil {
		return filepath.Join(cacheDir, "tunnelboard", "runtime")
	}
	return filepath.Join(dataDir, "runtime")
}

func randomGeneration() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return fmt.Sprintf("fallback-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(value[:])
}

func unixListenAddress(path string) string {
	p := filepath.ToSlash(path)
	return "unix/" + p + "|0600"
}

func (a *Adapter) AdminListen() string { return a.adminListen }
func (a *Adapter) RuntimeDir() string  { return a.runtimeDir }

func binaryName() string {
	if runtime.GOOS == "windows" {
		return "caddy.exe"
	}
	return "caddy"
}

func (a *Adapter) Locate() (string, error) {
	if p := strings.TrimSpace(os.Getenv(EnvPathOverride)); p != "" {
		if _, err := os.Stat(p); err != nil {
			return "", fmt.Errorf("%w: %s=%s unusable: %v", ErrNotFound, EnvPathOverride, p, err)
		}
		return p, nil
	}
	for _, candidate := range a.Candidates {
		if stat, err := os.Stat(candidate); err == nil && !stat.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("%w: searched %s", ErrNotFound, strings.Join(a.Candidates, ", "))
}

func (a *Adapter) VerifySHA256(bin string) error {
	if a.ExpectedSHA256 == "" {
		return nil
	}
	f, err := os.Open(bin)
	if err != nil {
		return fmt.Errorf("caddy: open binary: %w", err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("caddy: hash binary: %w", err)
	}
	if got := hex.EncodeToString(h.Sum(nil)); !strings.EqualFold(got, a.ExpectedSHA256) {
		return fmt.Errorf("caddy: binary integrity mismatch: got %s, want %s", got, a.ExpectedSHA256)
	}
	return nil
}

func (a *Adapter) DiagnosePort() error { return CheckAddr("127.0.0.1:443") }

func CheckAddr(addr string) error {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("caddy: %s unavailable: %w", addr, err)
	}
	return listener.Close()
}

// Apply 串行完成配置注入、校验以及热重载/冷启动。只有持有的 Process 才能进入热重载分支。
func (a *Adapter) Apply(ctx context.Context, revision string, routeConfig []byte) (ApplyResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if len(routeConfig) == 0 {
		return ApplyResult{}, errors.New("caddy: empty config must use Stop")
	}
	running := a.ownedRunningLocked()
	digest := sha256.Sum256(routeConfig)
	if running && a.hasDigest && digest == a.lastDigest {
		return ApplyResult{Outcome: OutcomeUnchanged}, nil
	}
	targetSocket := a.adminSocket
	if running {
		targetSocket = a.nextAdminSocketLocked()
	}
	targetListen := unixListenAddress(targetSocket)
	config, err := injectAdmin(routeConfig, targetListen)
	if err != nil {
		return ApplyResult{}, err
	}
	validationConfig, err := injectValidationAdmin(routeConfig)
	if err != nil {
		return ApplyResult{}, err
	}
	bin, err := a.Locate()
	if err != nil {
		return ApplyResult{}, a.failLocked(err)
	}
	if bin != strings.TrimSpace(os.Getenv(EnvPathOverride)) {
		if err := a.VerifySHA256(bin); err != nil {
			return ApplyResult{}, a.failLocked(err)
		}
	}
	if err := prepareRuntimeDir(a.runtimeDir); err != nil {
		return ApplyResult{}, a.failLocked(err)
	}
	env := append(os.Environ(), "XDG_DATA_HOME="+a.DataDir)
	candidate, err := a.writeCandidate(validationConfig)
	if err != nil {
		return ApplyResult{}, a.failLocked(err)
	}
	defer os.Remove(candidate)
	var validation bytes.Buffer
	if err := a.ValidateConfig(ctx, bin, candidate, a.DataDir, env, &validation); err != nil {
		return ApplyResult{}, a.failLocked(fmt.Errorf("caddy: validate config: %w: %s", err, strings.TrimSpace(validation.String())))
	}

	if a.ownedRunningLocked() {
		oldSocket, oldClient := a.adminSocket, a.httpClient
		_ = os.Remove(targetSocket)
		loadErr := postWithClient(ctx, oldClient, "/load", "application/json", bytes.NewReader(config))
		targetClient := newUnixHTTPClient(targetSocket)
		if err := a.waitReadyWithLocked(ctx, targetClient); err != nil {
			if loadErr != nil {
				err = fmt.Errorf("%v; new admin endpoint: %w", loadErr, err)
			}
			return ApplyResult{}, a.failLocked(fmt.Errorf("caddy: hot reload: %w", err))
		}
		if transport, ok := oldClient.Transport.(*http.Transport); ok {
			transport.CloseIdleConnections()
		}
		a.adminSocket, a.adminListen, a.httpClient = targetSocket, targetListen, targetClient
		_ = os.Remove(oldSocket)
		if err := a.writeConfigAtomic(config); err != nil {
			return ApplyResult{}, a.failLocked(err)
		}
		a.markAppliedLocked(revision, digest)
		return ApplyResult{Outcome: OutcomeApplied}, nil
	}

	if err := a.CheckPort(); err != nil {
		a.lastError = err.Error()
		return ApplyResult{Outcome: OutcomePortConflict, Detail: err.Error()}, nil
	}
	if err := a.writeConfigAtomic(config); err != nil {
		return ApplyResult{}, a.failLocked(err)
	}
	logWriter := a.Output
	var logFile *os.File
	if logWriter == nil {
		logFile, err = a.openLogFile()
		if err != nil {
			return ApplyResult{}, a.failLocked(err)
		}
		logWriter = logFile
	}
	process, err := a.StartProcess(bin, []string{"run", "--config", a.configPath()}, a.DataDir, env, logWriter, logWriter)
	if logFile != nil {
		_ = logFile.Close()
	}
	if err != nil {
		return ApplyResult{}, a.failLocked(err)
	}
	a.process = process
	a.processDone = make(chan error, 1)
	done := a.processDone
	go func() { done <- process.Wait(); close(done) }()
	if err := a.waitReadyWithLocked(ctx, a.httpClient); err != nil {
		_ = process.Kill()
		<-done
		a.clearProcessLocked()
		if portErr := a.CheckPort(); portErr != nil {
			a.lastError = portErr.Error()
			return ApplyResult{Outcome: OutcomePortConflict, Detail: portErr.Error()}, nil
		}
		return ApplyResult{}, a.failLocked(err)
	}
	a.markAppliedLocked(revision, digest)
	return ApplyResult{Outcome: OutcomeApplied}, nil
}

func (a *Adapter) Stop(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.ownedRunningLocked() {
		a.clearProcessLocked()
		return nil
	}
	process, done := a.process, a.processDone
	stopErr := a.postLocked(ctx, "/stop", "", nil)
	if stopErr != nil {
		a.lastError = fmt.Sprintf("caddy: graceful stop request: %v", stopErr)
	}
	select {
	case <-done:
		a.clearProcessLocked()
		return nil
	case <-ctx.Done():
		_ = process.Kill()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
		a.clearProcessLocked()
		if stopErr != nil {
			return fmt.Errorf("caddy: graceful stop failed: %v; deadline: %w", stopErr, ctx.Err())
		}
		return fmt.Errorf("caddy: stop deadline: %w", ctx.Err())
	}
}

func (a *Adapter) Status(context.Context) Status {
	a.mu.Lock()
	defer a.mu.Unlock()
	owned := a.ownedRunningLocked()
	status := Status{Owned: owned, Generation: a.generation, Ready: owned && a.ready, LastError: a.lastError}
	if owned {
		status.PID = a.process.PID()
	}
	return status
}

// 兼容迁移期调用方；事实来源仍是自有进程句柄，而不是 Admin 可达性。
func (a *Adapter) Running() bool { return a.Status(context.Background()).Owned }

// Reload 是迁移期包装，业务层最终应直接使用 Apply。
func (a *Adapter) Reload(config []byte) error {
	_, err := a.Apply(context.Background(), "legacy", config)
	return err
}

func (a *Adapter) RootCACert(timeout time.Duration) ([]byte, error) {
	path := filepath.Join(a.DataDir, "caddy", "pki", "authorities", "local", "root.crt")
	deadline := time.Now().Add(timeout)
	for {
		data, err := os.ReadFile(path)
		if err == nil {
			block, _ := pem.Decode(data)
			if block == nil {
				return nil, fmt.Errorf("caddy: root CA file %s is not valid PEM", path)
			}
			return block.Bytes, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("caddy: root CA not found at %s after %s", path, timeout)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func injectAdmin(config []byte, listen string) ([]byte, error) {
	var root map[string]any
	if err := json.Unmarshal(config, &root); err != nil {
		return nil, fmt.Errorf("caddy: decode route config: %w", err)
	}
	root["admin"] = map[string]any{"listen": listen}
	result, err := json.Marshal(root)
	if err != nil {
		return nil, fmt.Errorf("caddy: encode owned config: %w", err)
	}
	return result, nil
}

func injectValidationAdmin(config []byte) ([]byte, error) {
	var root map[string]any
	if err := json.Unmarshal(config, &root); err != nil {
		return nil, fmt.Errorf("caddy: decode validation config: %w", err)
	}
	root["admin"] = map[string]any{"disabled": true}
	result, err := json.Marshal(root)
	if err != nil {
		return nil, fmt.Errorf("caddy: encode validation config: %w", err)
	}
	return result, nil
}

func validateConfig(ctx context.Context, bin, configPath, dir string, env []string, output io.Writer) error {
	cmd := exec.CommandContext(ctx, bin, "validate", "--config", configPath)
	cmd.Dir, cmd.Env, cmd.Stdout, cmd.Stderr = dir, env, output, output
	return cmd.Run()
}

func (a *Adapter) ownedRunningLocked() bool {
	if a.process == nil || a.processDone == nil {
		return false
	}
	select {
	case <-a.processDone:
		a.clearProcessLocked()
		return false
	default:
		return true
	}
}

func (a *Adapter) waitReadyWithLocked(ctx context.Context, client *http.Client) error {
	timeout := a.ReadyTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://unix/config/", nil)
		response, err := client.Do(request)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				a.ready = true
				return nil
			}
		}
		select {
		case err := <-a.processDone:
			return fmt.Errorf("caddy: process exited before readiness: %w", err)
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("caddy: readiness timeout")
		case <-ticker.C:
		}
	}
}

func (a *Adapter) postLocked(ctx context.Context, path, contentType string, body io.Reader) error {
	return postWithClient(ctx, a.httpClient, path, contentType, body)
}

func postWithClient(ctx context.Context, client *http.Client, path, contentType string, body io.Reader) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://unix"+path, body)
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", contentType)
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 512))
		return fmt.Errorf("admin rejected (%d): %s", response.StatusCode, strings.TrimSpace(string(message)))
	}
	return nil
}

func (a *Adapter) nextAdminSocketLocked() string {
	if filepath.Base(a.adminSocket) == "admin-a.sock" {
		return filepath.Join(a.runtimeDir, "admin-b.sock")
	}
	return filepath.Join(a.runtimeDir, "admin-a.sock")
}

func (a *Adapter) markAppliedLocked(revision string, digest [sha256.Size]byte) {
	a.lastRevision, a.lastDigest, a.hasDigest, a.lastError = revision, digest, true, ""
}

func (a *Adapter) failLocked(err error) error {
	a.lastError = err.Error()
	return err
}

func (a *Adapter) clearProcessLocked() {
	a.process, a.processDone, a.ready = nil, nil, false
	a.hasDigest, a.lastRevision = false, ""
	_ = os.Remove(a.adminSocket)
}

func (a *Adapter) configPath() string { return filepath.Join(a.runtimeDir, "caddy.json") }

func (a *Adapter) writeCandidate(config []byte) (string, error) {
	file, err := os.CreateTemp(a.runtimeDir, "caddy-candidate-*.json")
	if err != nil {
		return "", fmt.Errorf("caddy: create candidate: %w", err)
	}
	name := file.Name()
	if err := file.Chmod(0o600); err == nil {
		_, err = file.Write(config)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(name)
		return "", fmt.Errorf("caddy: write candidate: %w", err)
	}
	return name, nil
}

func (a *Adapter) writeConfigAtomic(config []byte) error {
	file, err := os.CreateTemp(a.runtimeDir, "caddy-active-*.json")
	if err != nil {
		return fmt.Errorf("caddy: create active config: %w", err)
	}
	name := file.Name()
	defer os.Remove(name)
	if err := file.Chmod(0o600); err == nil {
		_, err = file.Write(config)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("caddy: write active config: %w", err)
	}
	if err := os.Rename(name, a.configPath()); err != nil {
		return fmt.Errorf("caddy: replace active config: %w", err)
	}
	return nil
}

func (a *Adapter) openLogFile() (*os.File, error) {
	dir := filepath.Join(a.DataDir, "logs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("caddy: create log dir: %w", err)
	}
	file, err := os.OpenFile(filepath.Join(dir, "caddy.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("caddy: open log file: %w", err)
	}
	return file, nil
}
