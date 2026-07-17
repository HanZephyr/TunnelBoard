package main

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/HanZephyr/TunnelBoard/internal/autostart"
	"github.com/HanZephyr/TunnelBoard/internal/biz"
	"github.com/HanZephyr/TunnelBoard/internal/model"
	"github.com/HanZephyr/TunnelBoard/internal/traytext"
	"github.com/HanZephyr/TunnelBoard/internal/uilocale"
	"github.com/HanZephyr/TunnelBoard/internal/updater"
	"github.com/HanZephyr/TunnelBoard/internal/vault"
	"github.com/energye/systray"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// App 是 Wails 的唯一绑定入口（应用 Module）：把目录与配置 Module、Vault
// 和应用偏好转换为 UI 可用的结果，不承载业务规则。
type App struct {
	ctx     context.Context
	store   *vault.Store
	catalog *biz.CatalogBiz
	runtime *biz.RuntimeBiz
	updater *updater.Service
	initErr error

	trayMu   sync.Mutex
	trayShow *systray.MenuItem
	trayQuit *systray.MenuItem

	allowClose atomic.Bool
}

// NewApp 打开默认数据目录下的 Vault 并组装应用 Module。
// 打开失败（含密钥遗失 ErrKeyUnavailable）时仅记录 initErr，由绑定调用方通过 ensureReady 感知。
func NewApp() *App {
	store, err := vault.OpenDefault()
	if err != nil {
		return &App{initErr: err}
	}
	return &App{
		store:   store,
		catalog: biz.NewCatalogBiz(store),
		runtime: biz.NewRuntimeBiz(store),
		updater: updater.NewDefaultService(),
	}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	slog.Info("app startup")
	if err := a.ensureReady(); err == nil {
		a.syncAutoRunWithConfig()
		go func() {
			if errs, err := a.runtime.StartAutoStart(); err != nil {
				slog.Error("auto start forwards failed", "err", err)
			} else {
				for id, startErr := range errs {
					slog.Error("auto start forward failed", "forward_id", id, "err", startErr)
				}
			}
		}()
	}
}

// shutdown 在显式退出时停止全部 Forward（CONTEXT.md：显式退出才停止 Forward 与 Caddy）。
func (a *App) shutdown(ctx context.Context) {
	_ = ctx
	slog.Info("app shutdown")
	if a.runtime != nil {
		a.runtime.Shutdown()
	}
}

func (a *App) ensureReady() error {
	if a.initErr != nil {
		return a.initErr
	}
	if a.store == nil || a.catalog == nil || a.runtime == nil {
		return fmt.Errorf("app is not initialized")
	}
	return nil
}

// PrepareForQuit 标记下一次窗口关闭为显式退出（托盘菜单退出路径）。
func (a *App) PrepareForQuit() {
	a.allowClose.Store(true)
}

// beforeClose 在 Windows 上把关窗拦截为隐藏到托盘；显式退出（allowClose）才放行。
// macOS 由 HideWindowOnClose 处理，不经过此分支。
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

// UILocale 返回持久化的界面语言，未设置时按系统环境推断；供 main 初始化托盘文案。
func (a *App) UILocale() string {
	if a.store == nil {
		return uilocale.DetectFromEnv()
	}
	data, err := a.store.Load()
	if err != nil || data.Prefs.UILocale == "" {
		return uilocale.DetectFromEnv()
	}
	return uilocale.Normalize(data.Prefs.UILocale)
}

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
	a.applyTrayLocaleUnlocked(uilocale.Normalize(locale))
}

// SaveUILocale 把界面语言持久化到 Vault 偏好并刷新托盘（UI 切换语言时调用）。
func (a *App) SaveUILocale(locale string) error {
	if err := a.ensureReady(); err != nil {
		return err
	}
	tag := uilocale.Normalize(locale)
	if _, err := a.store.Update(func(d *model.VaultData) error {
		d.Prefs.UILocale = tag
		return nil
	}); err != nil {
		return err
	}
	a.trayMu.Lock()
	defer a.trayMu.Unlock()
	a.applyTrayLocaleUnlocked(tag)
	return nil
}

// GetVaultData 返回当前 Vault 数据快照（文件夹、SSH 主机、Forward、Web Route、偏好）。
func (a *App) GetVaultData() (model.VaultData, error) {
	if err := a.ensureReady(); err != nil {
		return model.VaultData{}, err
	}
	return a.catalog.Data()
}

// CreateFolder 在 parentID（0 为顶层）下新建文件夹。
func (a *App) CreateFolder(name string, parentID int) (model.Folder, error) {
	if err := a.ensureReady(); err != nil {
		return model.Folder{}, err
	}
	return a.catalog.CreateFolder(name, parentID)
}

// MoveForward 把 Forward 移到目标文件夹。
func (a *App) MoveForward(forwardID, targetFolderID int) error {
	if err := a.ensureReady(); err != nil {
		return err
	}
	return a.catalog.MoveForward(forwardID, targetFolderID)
}

// SaveSSHHost 新建（ID 为 0）或更新 SSH 主机。
func (a *App) SaveSSHHost(host model.SSHHost) (model.SSHHost, error) {
	if err := a.ensureReady(); err != nil {
		return model.SSHHost{}, err
	}
	return a.catalog.SaveSSHHost(host)
}

// SaveForward 新建（ID 为 0）或更新 Forward；运行中的 Forward 必须先停止再编辑。
func (a *App) SaveForward(forward model.Forward) (model.Forward, error) {
	if err := a.ensureReady(); err != nil {
		return model.Forward{}, err
	}
	if forward.ID != 0 {
		if st, ok := a.runtime.Status(forward.ID); ok && (st.Status == biz.RuntimeStateRunning || st.Status == biz.RuntimeStateReconnecting) {
			return model.Forward{}, fmt.Errorf("forward %d is running, stop it before editing", forward.ID)
		}
	}
	return a.catalog.SaveForward(forward)
}

// DeleteSelection 批量删除文件夹、SSH 主机与 Forward；非空文件夹需 CascadeFolders。
// 涉及的运行中 Forward 先停止（含级联删除文件夹内的）。
func (a *App) DeleteSelection(sel biz.DeleteSelection) error {
	if err := a.ensureReady(); err != nil {
		return err
	}
	for _, id := range sel.ForwardIDs {
		_ = a.runtime.Stop(id)
	}
	if sel.CascadeFolders && len(sel.FolderIDs) > 0 {
		data, err := a.catalog.Data()
		if err != nil {
			return err
		}
		deletedFolders := map[int]bool{}
		for _, fid := range sel.FolderIDs {
			deletedFolders[fid] = true
		}
		for _, f := range data.Folders {
			if deletedFolders[f.ParentID] {
				deletedFolders[f.ID] = true
			}
		}
		for _, fw := range data.Forwards {
			if deletedFolders[fw.FolderID] {
				_ = a.runtime.Stop(fw.ID)
			}
		}
	}
	return a.catalog.DeleteSelection(sel)
}

// StartForward 启动单条 Forward 的运行时。
func (a *App) StartForward(id int) error {
	if err := a.ensureReady(); err != nil {
		return err
	}
	return a.runtime.Start(id)
}

// StopForward 停止单条 Forward；手动停止不触发自动重连。
func (a *App) StopForward(id int) error {
	if err := a.ensureReady(); err != nil {
		return err
	}
	return a.runtime.Stop(id)
}

// StartManyForwards 批量启动 Forward；返回启动失败的 id → 错误信息（成功项不出现）。
func (a *App) StartManyForwards(ids []int) (map[int]string, error) {
	if err := a.ensureReady(); err != nil {
		return nil, err
	}
	errs := a.runtime.StartMany(ids)
	out := make(map[int]string, len(errs))
	for id, err := range errs {
		if err != nil {
			out[id] = err.Error()
		}
	}
	return out, nil
}

// GetRuntimeSnapshot 返回全部 Forward 的运行时状态快照。
func (a *App) GetRuntimeSnapshot() ([]biz.RuntimeStatus, error) {
	if err := a.ensureReady(); err != nil {
		return nil, err
	}
	return a.runtime.Snapshot(), nil
}

// HostKeyStatusResult 是 SSH 主机指纹核验结果（绑定层单返回值包装）。
type HostKeyStatusResult struct {
	Entry  model.HostKey    `json:"entry"`
	Status model.TrustStatus `json:"status"`
}

// GetHostKeyStatus 查询指定端点指纹的信任状态（unknown/trusted/mismatch）。
func (a *App) GetHostKeyStatus(host string, port int, fingerprint string) (HostKeyStatusResult, error) {
	if err := a.ensureReady(); err != nil {
		return HostKeyStatusResult{}, err
	}
	entry, status, err := a.catalog.HostKeyStatus(host, port, fingerprint)
	if err != nil {
		return HostKeyStatusResult{}, err
	}
	return HostKeyStatusResult{Entry: entry, Status: status}, nil
}

// EnrollHostKey 在用户首次确认后保存端点指纹。
func (a *App) EnrollHostKey(host string, port int, keyType, fingerprint string) (model.HostKey, error) {
	if err := a.ensureReady(); err != nil {
		return model.HostKey{}, err
	}
	return a.catalog.EnrollHostKey(host, port, keyType, fingerprint)
}

// ReplaceHostKey 在用户显式确认指纹变更后替换端点指纹。
func (a *App) ReplaceHostKey(host string, port int, keyType, fingerprint string) (model.HostKey, error) {
	if err := a.ensureReady(); err != nil {
		return model.HostKey{}, err
	}
	return a.catalog.ReplaceHostKey(host, port, keyType, fingerprint)
}

// CheckForUpdates 查询 GitHub Releases 是否有新版本（只读检查，不做自更新）。
func (a *App) CheckForUpdates(currentVersion string) (updater.Result, error) {
	if err := a.ensureReady(); err != nil {
		return updater.Result{}, err
	}
	if a.updater == nil {
		return updater.Result{}, fmt.Errorf("updater service is not initialized")
	}
	return a.updater.Check(context.Background(), currentVersion)
}

// GetUpdateCheckEnabled returns whether TunnelBoard should check GitHub Releases on startup.
func (a *App) GetUpdateCheckEnabled() (bool, error) {
	if err := a.ensureReady(); err != nil {
		return false, err
	}
	data, err := a.store.Load()
	if err != nil {
		return false, err
	}
	return data.Prefs.UpdateCheckEnabled, nil
}

// SetUpdateCheckEnabled persists the startup update-check preference.
func (a *App) SetUpdateCheckEnabled(enabled bool) error {
	if err := a.ensureReady(); err != nil {
		return err
	}
	_, err := a.store.Update(func(d *model.VaultData) error {
		d.Prefs.UpdateCheckEnabled = enabled
		return nil
	})
	return err
}

// syncAutoRunWithConfig aligns OS auto-run (launch at login) state with the Vault preference.
func (a *App) syncAutoRunWithConfig() {
	data, err := a.store.Load()
	if err != nil {
		return
	}
	enabled, _ := autostart.IsEnabled()
	if data.Prefs.AutoRun && !enabled {
		_ = autostart.Enable()
	} else if !data.Prefs.AutoRun && enabled {
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

// SetAutoRunEnabled enables or disables launch at login and persists the preference.
func (a *App) SetAutoRunEnabled(enabled bool) error {
	if err := a.ensureReady(); err != nil {
		return err
	}
	if _, err := a.store.Update(func(d *model.VaultData) error {
		d.Prefs.AutoRun = enabled
		return nil
	}); err != nil {
		return err
	}
	if enabled {
		return autostart.Enable()
	}
	return autostart.Disable()
}

// GetConfigPath returns the absolute path of the current data directory.
func (a *App) GetConfigPath() (string, error) {
	if err := a.ensureReady(); err != nil {
		return "", err
	}
	abs, err := filepath.Abs(a.store.Dir())
	if err != nil {
		return a.store.Dir(), nil
	}
	return abs, nil
}

// OpenConfigDir opens the data directory in the OS file manager.
// It supports macOS, Windows and Linux (via xdg-open).
func (a *App) OpenConfigDir() error {
	if err := a.ensureReady(); err != nil {
		return err
	}
	dir := a.store.Dir()

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
