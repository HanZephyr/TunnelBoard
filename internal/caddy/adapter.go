// Package caddy 是内置 Caddy 的 Adapter：二进制定位与完整性校验、全局单进程管理、
// 经回环 admin API（127.0.0.1:2019）热重载配置、443 冲突诊断、本地 CA 根证书读取。
package caddy

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// EnvPathOverride 允许开发/测试环境指定 Caddy 二进制路径（跳过完整性校验）。
const EnvPathOverride = "TUNNELBOARD_CADDY_PATH"

const defaultAdminURL = "http://127.0.0.1:2019"

// ErrNotFound 表示按查找顺序找不到可用的 Caddy 二进制。
var ErrNotFound = errors.New("caddy: binary not found")

// Adapter 管理全局唯一的 Caddy 进程；状态以 admin API 可达性为准（无内存进程句柄）。
type Adapter struct {
	// DataDir 是应用数据目录：caddy.json 与 Caddy 自身存储（caddy-data 经 XDG_DATA_HOME 注入）在其下。
	DataDir string
	// ExpectedSHA256 是打包钉版的二进制哈希；为空跳过校验（仅开发期）。
	ExpectedSHA256 string
	// Candidates 是按优先级查找的二进制路径（New 计算默认，测试可替换）。
	Candidates []string
	// AdminURL 默认 127.0.0.1:2019；测试可指向 httptest.Server。
	AdminURL string

	// StartProcess 是进程启动接缝（默认 detach 启动，测试可替换）；
	// logFile 接收子进程 stdout/stderr（Caddy 日志）。
	StartProcess func(bin string, args []string, dir string, env []string, logFile *os.File) error
	httpClient   *http.Client
}

// New 返回按默认查找顺序（环境变量 → 可执行文件同级 caddy/ → 数据目录 caddy/）配置的 Adapter。
func New(dataDir string) *Adapter {
	a := &Adapter{
		DataDir:  dataDir,
		AdminURL: defaultAdminURL,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
	a.StartProcess = startDetached
	if exe, err := os.Executable(); err == nil {
		a.Candidates = append(a.Candidates, filepath.Join(filepath.Dir(exe), "caddy", binaryName()))
	}
	a.Candidates = append(a.Candidates, filepath.Join(dataDir, "caddy", binaryName()))
	return a
}

func binaryName() string {
	if runtime.GOOS == "windows" {
		return "caddy.exe"
	}
	return "caddy"
}

// Locate 按查找顺序返回第一个存在的 Caddy 二进制；环境变量覆盖优先（开发/测试，跳过校验）。
func (a *Adapter) Locate() (string, error) {
	if p := strings.TrimSpace(os.Getenv(EnvPathOverride)); p != "" {
		if _, err := os.Stat(p); err != nil {
			return "", fmt.Errorf("%w: %s=%s unusable: %v", ErrNotFound, EnvPathOverride, p, err)
		}
		return p, nil
	}
	for _, c := range a.Candidates {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c, nil
		}
	}
	return "", fmt.Errorf("%w: searched %s", ErrNotFound, strings.Join(a.Candidates, ", "))
}

// VerifySHA256 校验二进制完整性；ExpectedSHA256 为空（开发期）时跳过。
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

// DiagnosePort 预检 127.0.0.1:443 可绑定；冲突返回错误（Caddy 不应启动，Route 保持 hosts-only）。
func (a *Adapter) DiagnosePort() error {
	return CheckAddr("127.0.0.1:443")
}

// CheckAddr 预检地址可绑定（实际绑定后立即释放）。
func CheckAddr(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("caddy: %s unavailable: %w", addr, err)
	}
	return ln.Close()
}

// Running 以 admin API 可达性判定 Caddy 是否在运行。
func (a *Adapter) Running() bool {
	req, err := http.NewRequest(http.MethodGet, a.AdminURL+"/config/", nil)
	if err != nil {
		return false
	}
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// Start 冷启动全局 Caddy 进程（配置须已写入 caddy.json）。
// 经 XDG_DATA_HOME 把 Caddy 存储钉在数据目录内，本地 CA 路径因而确定；
// stdout/stderr 追加写入 logs/caddy.log 供日志页查阅。
// 环境变量覆盖的二进制（开发/测试）跳过完整性校验。
func (a *Adapter) Start() error {
	bin, err := a.Locate()
	if err != nil {
		return err
	}
	if bin != strings.TrimSpace(os.Getenv(EnvPathOverride)) {
		if err := a.VerifySHA256(bin); err != nil {
			return err
		}
	}
	logFile, err := a.openLogFile()
	if err != nil {
		return err
	}
	env := append(os.Environ(), "XDG_DATA_HOME="+a.DataDir)
	err = a.StartProcess(bin, []string{"run", "--config", a.configPath()}, a.DataDir, env, logFile)
	// 父进程句柄可立即关闭：子进程持有自己的副本继续写入（Windows 上不关闭会锁住文件）。
	_ = logFile.Close()
	return err
}

// openLogFile 打开（创建）Caddy 的日志文件；进程生命周期随父进程退出，无需显式关闭。
func (a *Adapter) openLogFile() (*os.File, error) {
	dir := filepath.Join(a.DataDir, "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("caddy: create log dir: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(dir, "caddy.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("caddy: open log file: %w", err)
	}
	return f, nil
}

// Reload 原子写入 caddy.json；进程在运行则经 admin API /load 热重载，否则冷启动。
func (a *Adapter) Reload(config []byte) error {
	if err := a.writeConfigAtomic(config); err != nil {
		return err
	}
	if !a.Running() {
		return a.Start()
	}
	resp, err := a.httpClient.Post(a.AdminURL+"/load", "application/json", bytes.NewReader(config))
	if err != nil {
		return fmt.Errorf("caddy: reload via admin api: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("caddy: reload rejected (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

// Stop 经 admin API /stop 停止进程并等待其退出；未运行时幂等。
func (a *Adapter) Stop() error {
	if !a.Running() {
		return nil
	}
	resp, err := a.httpClient.Post(a.AdminURL+"/stop", "", nil)
	if err != nil {
		return fmt.Errorf("caddy: stop via admin api: %w", err)
	}
	_ = resp.Body.Close()
	deadline := time.Now().Add(5 * time.Second)
	for a.Running() {
		if time.Now().After(deadline) {
			return fmt.Errorf("caddy: process did not exit within 5s after /stop")
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil
}

// RootCACert 读取本地 CA 根证书（DER），供 TrustLocalCA 使用。
// Caddy 在首个 tls internal 策略加载时生成 CA，这里轮询等待其出现。
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

func (a *Adapter) configPath() string {
	return filepath.Join(a.DataDir, "caddy.json")
}

// writeConfigAtomic 写临时文件后 rename，避免半写配置被热重载。
func (a *Adapter) writeConfigAtomic(config []byte) error {
	if err := os.MkdirAll(a.DataDir, 0o700); err != nil {
		return fmt.Errorf("caddy: create data dir: %w", err)
	}
	tmp := a.configPath() + ".tmp"
	if err := os.WriteFile(tmp, config, 0o600); err != nil {
		return fmt.Errorf("caddy: write config: %w", err)
	}
	if err := os.Rename(tmp, a.configPath()); err != nil {
		return fmt.Errorf("caddy: replace config: %w", err)
	}
	return nil
}
