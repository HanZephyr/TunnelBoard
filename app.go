package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/HanZephyr/TunnelBoard/internal/autostart"
	"github.com/HanZephyr/TunnelBoard/internal/biz"
	"github.com/HanZephyr/TunnelBoard/internal/conf"
	"github.com/HanZephyr/TunnelBoard/internal/model"
	"github.com/HanZephyr/TunnelBoard/internal/sshconfig"
	"github.com/HanZephyr/TunnelBoard/internal/traytext"
	"github.com/HanZephyr/TunnelBoard/internal/uilocale"
	"github.com/HanZephyr/TunnelBoard/internal/updater"
	"github.com/energye/systray"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// App struct
type App struct {
	ctx     context.Context
	storage *conf.Storage
	jumper  *biz.JumperBiz
	tunnel  *biz.TunnelBiz
	updater *updater.Service
	initErr error

	trayMu   sync.Mutex
	trayShow *systray.MenuItem
	trayQuit *systray.MenuItem

	allowClose atomic.Bool
}

// NewApp creates a new App application struct
func NewApp() *App {
	storage, err := conf.NewDefaultStorage()
	if err != nil {
		return &App{initErr: err}
	}
	slog.Info("app initialized", "config", storage.Path())

	return &App{
		storage: storage,
		jumper:  biz.NewJumperBiz(storage),
		tunnel:  biz.NewTunnelBiz(storage),
		updater: updater.NewDefaultService(),
	}
}

// startup is called when the app starts. The context is saved
// so we can call the runtime methods
// SetTrayMenuItems wires systray menu entries created in main so locale changes can relabel them.
func (a *App) SetTrayMenuItems(show, quit *systray.MenuItem) {
	a.trayMu.Lock()
	defer a.trayMu.Unlock()
	a.trayShow = show
	a.trayQuit = quit
}

func (a *App) applyTrayLocaleUnlocked(tag string) {
	s := traytext.ForLocale(tag)
	if a.trayShow != nil {
		a.trayShow.SetTitle(s.ShowMainTitle)
		a.trayShow.SetTooltip(s.ShowMainTooltip)
	}
	if a.trayQuit != nil {
		a.trayQuit.SetTitle(s.QuitTitle)
		a.trayQuit.SetTooltip(s.QuitTooltip)
	}
	if runtime.GOOS != "darwin" {
		systray.SetTitle(s.AppTitle)
	}
	systray.SetTooltip(s.IconTooltip)
}

// ApplyTrayLocale updates tray icon tooltip and menu item titles to match a vue-i18n locale tag.
func (a *App) ApplyTrayLocale(locale string) {
	a.trayMu.Lock()
	defer a.trayMu.Unlock()
	tag := uilocale.Normalize(locale)
	if tag == "" {
		tag = "en"
	}
	a.applyTrayLocaleUnlocked(tag)
}

// SaveUILocale persists ui.locale beside config.toml and refreshes the tray (call when the UI language changes).
func (a *App) SaveUILocale(locale string) error {
	if a.storage == nil {
		return fmt.Errorf("storage unavailable")
	}
	dir := filepath.Dir(a.storage.Path())
	if err := uilocale.WriteFile(dir, locale); err != nil {
		return err
	}
	a.trayMu.Lock()
	defer a.trayMu.Unlock()
	tag := uilocale.Normalize(locale)
	if tag == "" {
		tag = "en"
	}
	a.applyTrayLocaleUnlocked(tag)
	return nil
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	slog.Info("app startup")
	if err := a.ensureReady(); err == nil {
		a.syncAutoRunWithConfig()
		go func() {
			if err := a.tunnel.StartAutoStart(); err != nil {
				slog.Error("auto start tunnel failed", "err", err)
			}
		}()
	}
}

func (a *App) PrepareForQuit() {
	a.allowClose.Store(true)
}

func (a *App) beforeClose(ctx context.Context) (prevent bool) {
	if runtime.GOOS != "windows" {
		return false
	}
	if a.allowClose.Load() {
		return false
	}
	slog.Info("window close intercepted; hiding to tray")
	wailsruntime.Hide(ctx)
	wailsruntime.WindowHide(ctx)
	return true
}

func (a *App) shutdown(ctx context.Context) {
	_ = ctx
	slog.Info("app shutdown")
	if a.tunnel != nil {
		a.tunnel.Shutdown()
	}
}

func (a *App) ensureReady() error {
	if a.initErr != nil {
		return a.initErr
	}
	if a.storage == nil || a.jumper == nil || a.tunnel == nil {
		return fmt.Errorf("app is not initialized")
	}
	return nil
}

// Greet returns a greeting for the given name
func (a *App) Greet(name string) string {
	return fmt.Sprintf("Hello %s, It's show time!", name)
}

func (a *App) GetState() (model.State, error) {
	if err := a.ensureReady(); err != nil {
		return model.State{}, err
	}
	jumpers, err := a.jumper.List()
	if err != nil {
		return model.State{}, err
	}
	tunnels, err := a.tunnel.List()
	if err != nil {
		return model.State{}, err
	}
	return model.State{
		Jumpers: append([]model.Jumper{}, jumpers...),
		Tunnels: append([]model.Tunnel{}, tunnels...),
	}, nil
}

func (a *App) ListJumpers() ([]model.Jumper, error) {
	if err := a.ensureReady(); err != nil {
		return nil, err
	}
	return a.jumper.List()
}

func (a *App) GetSSHConfigImportSources() ([]model.SSHConfigImportSource, error) {
	if err := a.ensureReady(); err != nil {
		return nil, err
	}
	return sshconfig.GetImportSources()
}

func (a *App) LoadSSHConfigJumpersByPath(configPath string) (model.SSHConfigImportResult, error) {
	if err := a.ensureReady(); err != nil {
		return model.SSHConfigImportResult{}, err
	}
	return sshconfig.LoadImportCandidates(configPath)
}

func (a *App) CreateJumper(payload model.JumperPayload) (model.Jumper, error) {
	if err := a.ensureReady(); err != nil {
		return model.Jumper{}, err
	}
	return a.jumper.Create(payload)
}

func (a *App) UpdateJumper(id int, payload model.JumperPayload) (model.Jumper, error) {
	if err := a.ensureReady(); err != nil {
		return model.Jumper{}, err
	}
	return a.jumper.Update(id, payload)
}

func (a *App) TestJumperConnection(payload model.JumperPayload) error {
	if err := a.ensureReady(); err != nil {
		return err
	}
	return a.jumper.TestConnection(payload)
}

func (a *App) DeleteJumper(id int) error {
	if err := a.ensureReady(); err != nil {
		return err
	}
	return a.jumper.Delete(id)
}

func (a *App) ListTunnels() ([]model.Tunnel, error) {
	if err := a.ensureReady(); err != nil {
		return nil, err
	}
	return a.tunnel.List()
}

func (a *App) CreateTunnel(payload model.TunnelPayload) (model.Tunnel, error) {
	if err := a.ensureReady(); err != nil {
		return model.Tunnel{}, err
	}
	return a.tunnel.Create(payload)
}

func (a *App) UpdateTunnel(id int, payload model.TunnelPayload) (model.Tunnel, error) {
	if err := a.ensureReady(); err != nil {
		return model.Tunnel{}, err
	}
	return a.tunnel.Update(id, payload)
}

func (a *App) TestTunnelConnection(payload model.TunnelPayload, inlineJumper *model.JumperPayload) (model.TunnelConnectionTestResult, error) {
	if err := a.ensureReady(); err != nil {
		return model.TunnelConnectionTestResult{}, err
	}
	latency, err := a.tunnel.TestConnection(payload, inlineJumper)
	if err != nil {
		return model.TunnelConnectionTestResult{}, err
	}
	return model.TunnelConnectionTestResult{
		LatencyMs: latency.Milliseconds(),
	}, nil
}

func (a *App) DeleteTunnel(id int) error {
	if err := a.ensureReady(); err != nil {
		return err
	}
	return a.tunnel.Delete(id)
}

func (a *App) ToggleTunnel(id int) (model.Tunnel, error) {
	if err := a.ensureReady(); err != nil {
		return model.Tunnel{}, err
	}
	return a.tunnel.Toggle(id)
}

func collectJumpersForApp(items []model.Jumper, ids []int) ([]model.Jumper, error) {
	if len(ids) == 0 {
		return nil, fmt.Errorf("at least one jumper is required")
	}
	index := make(map[int]model.Jumper, len(items))
	for _, item := range items {
		index[item.ID] = item
	}
	result := make([]model.Jumper, 0, len(ids))
	for _, id := range ids {
		jumper, ok := index[id]
		if !ok {
			return nil, biz.ErrJumperNotFound
		}
		result = append(result, jumper)
	}
	return result, nil
}

func (a *App) CheckForUpdates(currentVersion string) (updater.Result, error) {
	if err := a.ensureReady(); err != nil {
		return updater.Result{}, err
	}
	if a.updater == nil {
		return updater.Result{}, fmt.Errorf("updater service is not initialized")
	}
	return a.updater.Check(context.Background(), currentVersion)
}

// syncAutoRunWithConfig aligns OS auto-run (launch at login) state with config.
func (a *App) syncAutoRunWithConfig() {
	cfg, err := a.storage.Load()
	if err != nil {
		return
	}
	enabled, _ := autostart.IsEnabled()
	if cfg.AutoRun && !enabled {
		_ = autostart.Enable()
	} else if !cfg.AutoRun && enabled {
		_ = autostart.Disable()
	}
}

// GetAutoRunEnabled returns whether the app is currently set to launch at login (system state).
func (a *App) GetAutoRunEnabled() (bool, error) {
	if err := a.ensureReady(); err != nil {
		return false, err
	}
	return autostart.IsEnabled()
}

// SetAutoRunEnabled enables or disables launch at login and persists to config.
func (a *App) SetAutoRunEnabled(enabled bool) error {
	if err := a.ensureReady(); err != nil {
		return err
	}
	_, err := a.storage.Update(func(cfg *conf.Config) error {
		cfg.AutoRun = enabled
		return nil
	})
	if err != nil {
		return err
	}
	if enabled {
		return autostart.Enable()
	}
	return autostart.Disable()
}

// GetConfigPath returns the absolute path of the current config file.
func (a *App) GetConfigPath() (string, error) {
	if err := a.ensureReady(); err != nil {
		return "", err
	}
	abs, err := filepath.Abs(a.storage.Path())
	if err != nil {
		return a.storage.Path(), nil
	}
	return abs, nil
}

// ExportConfig copies the current config.toml to destPath.
// ExportConfigWithDialog opens a save-file dialog, then copies the config.
// Returns empty string if the user cancelled.
func (a *App) ExportConfigWithDialog() error {
	if err := a.ensureReady(); err != nil {
		return err
	}
	destPath, err := wailsruntime.SaveFileDialog(a.ctx, wailsruntime.SaveDialogOptions{
		DefaultFilename: "config.toml",
		Filters: []wailsruntime.FileFilter{
			{DisplayName: "TOML Config (*.toml)", Pattern: "*.toml"},
		},
	})
	if err != nil {
		return fmt.Errorf("file dialog: %w", err)
	}
	if strings.TrimSpace(destPath) == "" {
		return nil // user cancelled
	}
	return a.ExportConfig(destPath)
}

func (a *App) ExportConfig(destPath string) error {
	if err := a.ensureReady(); err != nil {
		return err
	}
	destPath = strings.TrimSpace(destPath)
	if destPath == "" {
		return fmt.Errorf("destination path is empty")
	}

	src, err := os.Open(a.storage.Path())
	if err != nil {
		return fmt.Errorf("open config file: %w", err)
	}
	defer src.Close()

	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return fmt.Errorf("create destination directory: %w", err)
	}

	dst, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("create destination file: %w", err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return fmt.Errorf("copy config file: %w", err)
	}
	slog.Info("config exported", "dest", destPath)
	return nil
}

// SelectImportFile opens a file picker and returns the selected path. Returns empty string if cancelled.
func (a *App) SelectImportFile() (string, error) {
	if err := a.ensureReady(); err != nil {
		return "", err
	}
	srcPath, err := wailsruntime.OpenFileDialog(a.ctx, wailsruntime.OpenDialogOptions{
		Filters: []wailsruntime.FileFilter{
			{DisplayName: "TOML Config (*.toml)", Pattern: "*.toml"},
		},
	})
	if err != nil {
		return "", fmt.Errorf("file dialog: %w", err)
	}
	return strings.TrimSpace(srcPath), nil
}

// ImportConfig replaces the current config with the TOML file at srcPath.
// It validates the file first, then stops all running tunnels, replaces the
// config file, and restarts auto-start tunnels.
func (a *App) ImportConfig(srcPath string) error {
	if err := a.ensureReady(); err != nil {
		return err
	}
	srcPath = strings.TrimSpace(srcPath)
	if srcPath == "" {
		return fmt.Errorf("source path is empty")
	}

	// Validate: can we parse it as a valid config?
	data, err := os.ReadFile(srcPath)
	if err != nil {
		return fmt.Errorf("read source file: %w", err)
	}
	if _, err := conf.ParseConfigTOML(data); err != nil {
		return fmt.Errorf("invalid config file: %w", err)
	}

	// Stop all running tunnels.
	if a.tunnel != nil {
		a.tunnel.Shutdown()
	}

	// Overwrite config file atomically.
	tmpPath := a.storage.Path() + ".import.tmp"
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return fmt.Errorf("write temp config: %w", err)
	}
	if err := os.Rename(tmpPath, a.storage.Path()); err != nil {
		return fmt.Errorf("replace config file: %w", err)
	}

	// Reinitialise biz layer so the new config takes effect.
	a.jumper = biz.NewJumperBiz(a.storage)
	a.tunnel = biz.NewTunnelBiz(a.storage)

	// Restart auto-start tunnels.
	_ = a.tunnel.StartAutoStart()

	slog.Info("config imported", "src", srcPath)
	return nil
}

// OpenConfigDir opens the config file's parent directory in the OS file manager.
// It supports macOS, Windows and Linux (via xdg-open).
func (a *App) OpenConfigDir() error {
	if err := a.ensureReady(); err != nil {
		return err
	}
	dir := filepath.Dir(a.storage.Path())

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", dir)
	case "windows":
		cmd = exec.Command("explorer", dir)
	default:
		// Most desktop Linux environments provide xdg-open.
		cmd = exec.Command("xdg-open", dir)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("open config dir: %w", err)
	}
	return nil
}
