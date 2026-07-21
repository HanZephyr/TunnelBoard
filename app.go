package main

import (
	"context"
	"encoding/json"
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
	"time"

	"github.com/HanZephyr/TunnelBoard/internal/autostart"
	"github.com/HanZephyr/TunnelBoard/internal/biz"
	"github.com/HanZephyr/TunnelBoard/internal/caddy"
	"github.com/HanZephyr/TunnelBoard/internal/diag"
	"github.com/HanZephyr/TunnelBoard/internal/forward"
	"github.com/HanZephyr/TunnelBoard/internal/helper"
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
	ctx      context.Context
	store    *vault.Store
	catalog  *biz.CatalogBiz
	runtime  *biz.RuntimeBiz
	router   *biz.RouterBiz
	backup   *biz.BackupBiz
	diagBuf  *diag.RingBuffer
	logStore diag.LogStore
	caddy    *caddy.Adapter
	updater  *updater.Service
	initErr  error

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
		diagBuf := diag.NewRingBuffer(slog.NewTextHandler(os.Stderr, nil), 2000)
		slog.SetDefault(slog.New(diag.NewSafeLogHandler(diagBuf)))
		return &App{initErr: err, diagBuf: diagBuf}
	}
	logStore, logErr := diag.NewLogStore(filepath.Join(store.Dir(), "logs"))
	if logErr != nil {
		diagBuf := diag.NewRingBuffer(slog.NewTextHandler(os.Stderr, nil), 2000)
		slog.SetDefault(slog.New(diag.NewSafeLogHandler(diagBuf)))
		return &App{initErr: logErr, store: store, diagBuf: diagBuf}
	}
	logWriter := io.MultiWriter(diag.NewSourceWriter(logStore, diag.LogTunnelBoard), os.Stderr)
	diagBuf := diag.NewRingBuffer(slog.NewTextHandler(logWriter, nil), 2000)
	slog.SetDefault(slog.New(diag.NewSafeLogHandler(diagBuf)))
	catalog := biz.NewCatalogBiz(store)
	caddyAdapter := caddy.New(store.Dir())
	caddyAdapter.ExpectedSHA256 = caddyBundleSHA256
	caddyAdapter.Output = diag.NewSourceWriter(logStore, diag.LogCaddy)
	return &App{
		store:   store,
		catalog: catalog,
		runtime: biz.NewRuntimeBiz(store),
		router: biz.NewRouterBiz(
			store, catalog, helper.NewOperator(), caddyAdapter,
			helper.SystemHostsPath(), filepath.Join(store.Dir(), "caddy.json"),
		),
		backup:   biz.NewBackupBiz(store),
		diagBuf:  diagBuf,
		logStore: logStore,
		caddy:    caddyAdapter,
		updater:  updater.NewDefaultService(),
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
		go func() {
			if err := a.router.ResumeCaddy(); err != nil {
				slog.Error("resume caddy failed", "err", err)
			}
		}()
	}
}

// shutdown 在显式退出时停止全部 Forward 与 Caddy（CONTEXT.md:55）。
func (a *App) shutdown(ctx context.Context) {
	slog.Info("app shutdown")
	if a.runtime != nil {
		a.runtime.Shutdown()
	}
	if a.caddy != nil {
		stopCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := a.caddy.Stop(stopCtx); err != nil {
			slog.Error("stop caddy on shutdown failed", "err", err)
		}
	}
	if a.logStore != nil {
		_ = a.logStore.Close()
	}
}

func (a *App) ensureReady() error {
	if a.initErr != nil {
		return a.initErr
	}
	if a.store == nil || a.catalog == nil || a.runtime == nil || a.router == nil || a.backup == nil {
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
	err := a.catalog.DeleteSelection(sel)
	if err == nil {
		// Forward 删除会级联清理 WebRoute；按 CONTEXT.md:67 同步撤销其 hosts 记录。
		if _, recErr := a.router.ReconcileRoutes(); recErr != nil {
			return fmt.Errorf("deleted, but failed to reconcile routes: %w", recErr)
		}
	}
	return err
}

// SaveWebRoute 新建（ID 为 0）或更新 Web Route（域名、hosts/Caddy 开关、HTTPS SNI）。
func (a *App) SaveWebRoute(route model.WebRoute) (model.WebRoute, error) {
	if err := a.ensureReady(); err != nil {
		return model.WebRoute{}, err
	}
	return a.catalog.SaveWebRoute(route)
}

// PreviewRoute 返回应用前预览：将写入的 hosts 记录、需要确认的域名、443 冲突与 CA 信任需求。
func (a *App) PreviewRoute(routeID int) (biz.RoutePreview, error) {
	if err := a.ensureReady(); err != nil {
		return biz.RoutePreview{}, err
	}
	return a.router.PreviewRoute(routeID)
}

// ApplyRoute 把单条 Route 应用到系统；非本地域名需在 confirmedDomains 中显式确认。
func (a *App) ApplyRoute(routeID int, confirmedDomains []string) (biz.RouteApplyResult, error) {
	if err := a.ensureReady(); err != nil {
		return biz.RouteApplyResult{}, err
	}
	return a.router.ApplyRoute(routeID, confirmedDomains)
}

// RemoveRoute 删除 Route 并重推系统（撤销 hosts 记录；最后一个 Caddy Route 移除后停 Caddy 并撤 CA）。
func (a *App) RemoveRoute(routeID int) (biz.RouteApplyResult, error) {
	if err := a.ensureReady(); err != nil {
		return biz.RouteApplyResult{}, err
	}
	return a.router.RemoveRoute(routeID)
}

// GetRouteStatus 返回全部 Route 的系统生效状态。
func (a *App) GetRouteStatus() ([]biz.RouteStatusItem, error) {
	if err := a.ensureReady(); err != nil {
		return nil, err
	}
	return a.router.RouteStatus()
}

// ExportBackupWithDialog 创建密码加密备份包并经保存对话框写盘；返回风险提示（如包含的私钥文件）。
func (a *App) ExportBackupWithDialog(password string, includeKeyFiles bool) ([]string, error) {
	if err := a.ensureReady(); err != nil {
		return nil, err
	}
	raw, warnings, err := a.backup.CreateBackup(password, includeKeyFiles)
	if err != nil {
		return nil, err
	}
	destPath, err := wailsruntime.SaveFileDialog(a.ctx, wailsruntime.SaveDialogOptions{
		DefaultFilename: "tunnelboard-backup.tbbak",
		Filters:         []wailsruntime.FileFilter{{DisplayName: "TunnelBoard Backup (*.tbbak)", Pattern: "*.tbbak"}},
	})
	if err != nil {
		return nil, fmt.Errorf("file dialog: %w", err)
	}
	if strings.TrimSpace(destPath) == "" {
		return nil, nil // 用户取消
	}
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return nil, fmt.Errorf("create destination directory: %w", err)
	}
	if err := os.WriteFile(destPath, raw, 0o600); err != nil {
		return nil, fmt.Errorf("write backup file: %w", err)
	}
	slog.Info("backup exported", "dest", destPath)
	return warnings, nil
}

// SelectBackupFile 打开文件选择对话框，返回备份包路径（取消为空串）。
func (a *App) SelectBackupFile() (string, error) {
	if err := a.ensureReady(); err != nil {
		return "", err
	}
	srcPath, err := wailsruntime.OpenFileDialog(a.ctx, wailsruntime.OpenDialogOptions{
		Filters: []wailsruntime.FileFilter{{DisplayName: "TunnelBoard Backup (*.tbbak)", Pattern: "*.tbbak"}},
	})
	if err != nil {
		return "", fmt.Errorf("file dialog: %w", err)
	}
	return strings.TrimSpace(srcPath), nil
}

// PreviewImport 解密备份包并返回导入预览（实体计数、冲突与私钥文件清单）。
func (a *App) PreviewImport(srcPath, password string) (biz.ImportPreview, error) {
	if err := a.ensureReady(); err != nil {
		return biz.ImportPreview{}, err
	}
	raw, err := os.ReadFile(strings.TrimSpace(srcPath))
	if err != nil {
		return biz.ImportPreview{}, fmt.Errorf("read backup file: %w", err)
	}
	return a.backup.PreviewImport(raw, password)
}

// ApplyImport 追加导入到新顶层文件夹；不改变任何网络行为。
func (a *App) ApplyImport(srcPath, password string, plan biz.ImportPlan) (biz.ImportSummary, error) {
	if err := a.ensureReady(); err != nil {
		return biz.ImportSummary{}, err
	}
	raw, err := os.ReadFile(strings.TrimSpace(srcPath))
	if err != nil {
		return biz.ImportSummary{}, fmt.Errorf("read backup file: %w", err)
	}
	return a.backup.ApplyImport(raw, password, plan)
}

// RestoreBackup 完全还原：先停止全部 Forward，再整体替换 Vault；必须显式确认。
func (a *App) RestoreBackup(srcPath, password string, confirmed bool) error {
	if err := a.ensureReady(); err != nil {
		return err
	}
	raw, err := os.ReadFile(strings.TrimSpace(srcPath))
	if err != nil {
		return fmt.Errorf("read backup file: %w", err)
	}
	a.runtime.Shutdown()
	return a.backup.RestoreBackup(raw, password, confirmed)
}

// SaveImportKeyFile 从备份包中取出指定私钥文件并经保存对话框写盘（导入后用户显式另存）。
func (a *App) SaveImportKeyFile(srcPath, password, keyPath string) error {
	if err := a.ensureReady(); err != nil {
		return err
	}
	raw, err := os.ReadFile(strings.TrimSpace(srcPath))
	if err != nil {
		return fmt.Errorf("read backup file: %w", err)
	}
	_, keyFiles, err := vault.ParseBackup(raw, password)
	if err != nil {
		return err
	}
	content, ok := keyFiles[keyPath]
	if !ok {
		return fmt.Errorf("key file %s not found in backup", keyPath)
	}
	destPath, err := wailsruntime.SaveFileDialog(a.ctx, wailsruntime.SaveDialogOptions{
		DefaultFilename: filepath.Base(keyPath),
	})
	if err != nil {
		return fmt.Errorf("file dialog: %w", err)
	}
	if strings.TrimSpace(destPath) == "" {
		return nil // 用户取消
	}
	if err := os.WriteFile(destPath, content, 0o600); err != nil {
		return fmt.Errorf("write key file: %w", err)
	}
	return nil
}

// ExportDiagnosticsWithDialog 导出脱敏诊断包（内存日志 + 状态摘要，不含任何秘密）。
func (a *App) ExportDiagnosticsWithDialog() error {
	if err := a.ensureReady(); err != nil {
		return err
	}
	summary := map[string]interface{}{}
	if data, err := a.store.Load(); err == nil {
		summary["counts"] = map[string]int{
			"folders": len(data.Folders), "sshHosts": len(data.SSHHosts),
			"forwards": len(data.Forwards), "webRoutes": len(data.WebRoutes),
			"hostKeys": len(data.HostKeys),
		}
		summary["updateCheckEnabled"] = data.Prefs.UpdateCheckEnabled
		summary["caTrusted"] = data.Prefs.CATrustedSHA256 != ""
	}
	if snapshot, err := a.GetRuntimeSnapshot(); err == nil {
		summary["runtime"] = snapshot
	}
	if routes, err := a.router.RouteStatus(); err == nil {
		summary["routes"] = routes
	}

	bundle := a.diagBuf.BuildBundle(appVersion, runtime.GOOS+"/"+runtime.GOARCH, summary)
	payload, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return fmt.Errorf("encode diagnostics: %w", err)
	}
	destPath, err := wailsruntime.SaveFileDialog(a.ctx, wailsruntime.SaveDialogOptions{
		DefaultFilename: "tunnelboard-diagnostics.json",
		Filters:         []wailsruntime.FileFilter{{DisplayName: "JSON (*.json)", Pattern: "*.json"}},
	})
	if err != nil {
		return fmt.Errorf("file dialog: %w", err)
	}
	if strings.TrimSpace(destPath) == "" {
		return nil // 用户取消
	}
	if err := os.WriteFile(destPath, payload, 0o600); err != nil {
		return fmt.Errorf("write diagnostics: %w", err)
	}
	slog.Info("diagnostics exported", "dest", destPath)
	return nil
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

// GetRuntimeSnapshot 返回全部 Forward 的运行时状态快照；读取失败时返回错误（前端据此提示而非静默回退）。
func (a *App) GetRuntimeSnapshot() ([]biz.RuntimeStatus, error) {
	if err := a.ensureReady(); err != nil {
		return nil, err
	}
	return a.runtime.Snapshot()
}

// GetConnPoolStats 返回 SSH 连接池快照（首跳主机的活跃连接与复用引用数）。
func (a *App) GetConnPoolStats() ([]forward.PoolStat, error) {
	if err := a.ensureReady(); err != nil {
		return nil, err
	}
	return a.runtime.PoolStats(), nil
}

// CheckLocalPortAvailable 预检本地监听端口是否可绑定，供编辑 Forward 时尽早提示端口冲突。
func (a *App) CheckLocalPortAvailable(host string, port int) error {
	if err := a.ensureReady(); err != nil {
		return err
	}
	return forward.CheckLocalPortAvailable(host, port)
}

// HostKeyStatusResult 是 SSH 主机指纹核验结果（绑定层单返回值包装）。
type HostKeyStatusResult struct {
	Entry  model.HostKey     `json:"entry"`
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

// LogTailResult 是日志文件的增量读取结果：新行与下一次读取偏移。
type LogTailResult struct {
	Lines        []string `json:"lines"`
	Offset       int64    `json:"offset"`
	Generation   uint64   `json:"generation"`
	Rotated      bool     `json:"rotated"`
	Truncated    bool     `json:"truncated"`
	DroppedBytes int64    `json:"droppedBytes"`
}

// GetLogTail 从 offset 增量读取日志文件（name 仅允许 tunnelboard | caddy）。
// offset 超过文件大小（文件被滚动替换）时重置到末尾，不回放旧内容。
func (a *App) GetLogTail(name string, offset int64) (LogTailResult, error) {
	if err := a.ensureReady(); err != nil {
		return LogTailResult{}, err
	}
	source, err := parseLogSource(name)
	if err != nil {
		return LogTailResult{}, err
	}
	cursor := &diag.LogCursor{Generation: 1, Offset: offset}
	if offset == 0 {
		cursor = nil
	}
	result, err := a.logStore.Tail(source, cursor)
	if err != nil {
		return LogTailResult{}, err
	}
	return LogTailResult{Lines: result.Lines, Offset: result.NextCursor.Offset, Generation: result.NextCursor.Generation, Rotated: result.Rotated, Truncated: result.Truncated, DroppedBytes: result.DroppedBytes}, nil
}

func (a *App) GetLogTailV2(name string, cursor *diag.LogCursor) (diag.LogTailResult, error) {
	if err := a.ensureReady(); err != nil {
		return diag.LogTailResult{}, err
	}
	source, err := parseLogSource(name)
	if err != nil {
		return diag.LogTailResult{}, err
	}
	return a.logStore.Tail(source, cursor)
}

func parseLogSource(name string) (diag.LogSource, error) {
	switch name {
	case string(diag.LogTunnelBoard):
		return diag.LogTunnelBoard, nil
	case string(diag.LogCaddy):
		return diag.LogCaddy, nil
	default:
		return "", fmt.Errorf("unknown log name %q", name)
	}
}

// GetAppVersion 返回应用版本（构建常量，界面与更新检查的唯一来源）。
func (a *App) GetAppVersion() string {
	return appVersion
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
