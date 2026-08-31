package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
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

	"github.com/HanZephyr/TunnelBoard/internal/application"
	"github.com/HanZephyr/TunnelBoard/internal/autostart"
	"github.com/HanZephyr/TunnelBoard/internal/biz"
	"github.com/HanZephyr/TunnelBoard/internal/caddy"
	"github.com/HanZephyr/TunnelBoard/internal/desktop"
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
	ctx            context.Context
	startupCATrust struct {
		sync.RWMutex
		request StartupCATrustRequest
	}
	store          *vault.Store
	catalog        *biz.CatalogBiz
	runtime        *biz.RuntimeBiz
	router         *biz.RouterBiz
	backup         *biz.BackupBiz
	application    *application.Service
	recovery       *application.RecoveryStore
	restoreEffects *application.RestoreEffectsAdapter
	diagBuf        *diag.RingBuffer
	logStore       diag.LogStore
	caddy          *caddy.Adapter
	updater        *updater.Service
	initErr        error
	helperClose    func(context.Context) error

	trayMu   sync.Mutex
	trayShow *systray.MenuItem
	trayQuit *systray.MenuItem

	allowClose atomic.Bool

	desktopLifecycle desktop.Lifecycle
	closePrompt      func(context.Context, desktop.ClosePrompt) (desktop.CloseChoice, error)
	hideWindow       func(context.Context)
}

// StartupCATrustRequest is the explicit local-CA trust decision awaiting the
// current desktop user after Caddy resumes at application startup.
type StartupCATrustRequest struct {
	Required    bool   `json:"required"`
	Fingerprint string `json:"fingerprint,omitempty"`
}

const startupCATrustEvent = "tunnelboard:startup-ca-trust-required"

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
	platformDataDir, platformErr := helper.CurrentUserDataDir()
	if platformErr != nil {
		_ = logStore.Close()
		return &App{initErr: platformErr, store: store, diagBuf: diagBuf}
	}
	helperOperator, caTrust, helperClose, integrationErr := helper.NewPlatformIntegration(platformDataDir)
	if integrationErr != nil {
		_ = logStore.Close()
		return &App{initErr: integrationErr, store: store, diagBuf: diagBuf}
	}
	helper.SetExpectedBinarySHA256(helperBundleSHA256)
	caddyAdapter := caddy.New(platformDataDir)
	caddyAdapter.ExpectedSHA256 = caddyBundleSHA256
	caddyAdapter.Output = diag.NewSourceWriter(logStore, diag.LogCaddy)
	runtimeBiz := biz.NewRuntimeBiz(store)
	routerBiz := biz.NewRouterBiz(
		store, catalog, helperOperator, caTrust, caddyAdapter,
		helper.SystemHostsPath(), filepath.Join(platformDataDir, "caddy.json"),
	)
	backupBiz := biz.NewBackupBiz(store)
	recovery := application.NewRecoveryStore(store.Dir())
	restoreEffects := application.NewRestoreEffects(store, runtimeBiz, routerBiz, recovery)
	packages := biz.NewBackupPackage(newApplicationGeneration())
	restoreCoordinator := biz.NewRestoreCoordinator(packages, restoreEffects)
	updateService := updater.NewDefaultService()
	applicationService := application.NewService(application.Dependencies{
		Store: store, Catalog: catalog, Runtime: runtimeBiz, Routes: routerBiz,
		Restore: restoreCoordinator, Recovery: recovery, Backup: backupBiz, Packages: packages,
		Updates: updateService, AppVersion: appVersion,
	})
	app := &App{
		store:            store,
		catalog:          catalog,
		runtime:          runtimeBiz,
		router:           routerBiz,
		backup:           backupBiz,
		application:      applicationService,
		recovery:         recovery,
		restoreEffects:   restoreEffects,
		diagBuf:          diagBuf,
		logStore:         logStore,
		caddy:            caddyAdapter,
		updater:          updateService,
		desktopLifecycle: desktop.NewLifecycle(desktop.Platform(runtime.GOOS), true),
		helperClose:      helperClose,
	}
	if app.helperClose == nil {
		if closer, ok := helperOperator.(interface{ Close(context.Context) error }); ok {
			app.helperClose = closer.Close
		}
	}
	return app
}

// setDesktopLifecycle configures the capabilities established before Wails
// starts. It stays internal so Wails does not generate a frontend binding for
// a bootstrap-only platform seam.
func (a *App) setDesktopLifecycle(lifecycle desktop.Lifecycle) {
	a.desktopLifecycle = lifecycle
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	slog.Info("app startup")
	if err := a.ensureReady(); err == nil {
		a.syncAutoRunWithConfig()
		networkAllowed := true
		if err := a.restoreEffects.RecoverPending(ctx); err != nil {
			networkAllowed = false
			slog.Error("recover pending restore failed; keep network disabled", "err", err)
		} else if quarantined, _, stateErr := a.recovery.State(); stateErr != nil {
			networkAllowed = false
			slog.Error("read restore quarantine failed; keep network disabled", "err", stateErr)
		} else if quarantined {
			networkAllowed = false
			slog.Info("restore quarantine active; skip automatic network effects")
		}
		if !networkAllowed {
			return
		}
		go func() {
			result, err := a.application.StartupNetwork(ctx)
			if result.CATrustNeeded && result.CAFingerprint != "" {
				request := StartupCATrustRequest{Required: true, Fingerprint: result.CAFingerprint}
				a.startupCATrust.Lock()
				a.startupCATrust.request = request
				a.startupCATrust.Unlock()
				wailsruntime.EventsEmit(ctx, startupCATrustEvent, request)
			}
			if err != nil {
				if !errors.Is(err, application.ErrMaintenance) {
					slog.Error("automatic network startup failed", "err", err)
				}
				return
			}
			for id, message := range result.ForwardErrors {
				slog.Error("auto start forward failed", "forward_id", id, "err", message)
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
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := a.caddy.Stop(stopCtx); err != nil {
			slog.Error("stop caddy on shutdown failed", "err", err)
		}
	}
	if a.helperClose != nil {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := a.helperClose(closeCtx); err != nil {
			slog.Error("close privileged helper session failed", "err", err)
		}
		cancel()
	}
	if a.logStore != nil {
		_ = a.logStore.Close()
	}
}

func (a *App) ensureReady() error {
	if a.initErr != nil {
		return a.initErr
	}
	if a.store == nil || a.catalog == nil || a.runtime == nil || a.router == nil || a.backup == nil || a.application == nil {
		return fmt.Errorf("app is not initialized")
	}
	return nil
}

func newApplicationGeneration() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err == nil {
		return hex.EncodeToString(value[:])
	}
	return fmt.Sprintf("%d-%d", os.Getpid(), time.Now().UnixNano())
}

// PrepareForQuit 标记下一次窗口关闭为显式退出（托盘菜单退出路径）。
func (a *App) PrepareForQuit() {
	a.allowClose.Store(true)
}

// beforeClose applies the session lifecycle policy. Linux without a usable tray asks on every
// normal close; tray-capable sessions hide, and explicit exit always proceeds to shutdown.
func (a *App) beforeClose(ctx context.Context) (prevent bool) {
	switch a.desktopLifecycle.CloseAction(a.allowClose.Load()) {
	case desktop.CloseExit:
		return false
	case desktop.CloseHide:
		a.hideMainWindow(ctx)
		return true
	case desktop.CloseAskUser:
		choice, err := a.askCloseChoice(ctx)
		if err != nil {
			slog.Error("ask no-tray close choice failed; keep window open", "err", err)
			return true
		}
		switch choice {
		case desktop.CloseChoiceExit:
			a.allowClose.Store(true)
			return false
		case desktop.CloseChoiceHide:
			a.hideMainWindow(ctx)
			return true
		default:
			return true
		}
	default:
		return false
	}
}

func (a *App) askCloseChoice(ctx context.Context) (desktop.CloseChoice, error) {
	prompt := desktop.ClosePromptForLocale(a.UILocale())
	if a.closePrompt != nil {
		return a.closePrompt(ctx, prompt)
	}
	answer, err := wailsruntime.MessageDialog(ctx, wailsruntime.MessageDialogOptions{
		Type:          wailsruntime.QuestionDialog,
		Title:         prompt.Title,
		Message:       prompt.Message,
		Buttons:       []string{prompt.ExitLabel, prompt.HideLabel, prompt.CancelLabel},
		DefaultButton: prompt.HideLabel,
		CancelButton:  prompt.CancelLabel,
	})
	if err != nil {
		return desktop.CloseChoiceCancel, err
	}
	return closeChoiceFromDialogAnswer(prompt, answer), nil
}

func closeChoiceFromDialogAnswer(prompt desktop.ClosePrompt, answer string) desktop.CloseChoice {
	switch answer {
	case prompt.ExitLabel, "Yes":
		return desktop.CloseChoiceExit
	case prompt.HideLabel, "No":
		return desktop.CloseChoiceHide
	default:
		return desktop.CloseChoiceCancel
	}
}

func (a *App) hideMainWindow(ctx context.Context) {
	if a.hideWindow != nil {
		a.hideWindow(ctx)
		return
	}
	slog.Info("window close intercepted; hiding main window")
	wailsruntime.Hide(ctx)
	wailsruntime.WindowHide(ctx)
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

// GetSnapshot 是前端首屏唯一聚合查询；返回值不包含任何已保存秘密。
func (a *App) GetSnapshot() (application.AppSnapshot, error) {
	if err := a.ensureReady(); err != nil {
		return application.AppSnapshot{}, err
	}
	return a.application.GetSnapshot(context.Background())
}

// CreateFolder 在 parentID（0 为顶层）下新建文件夹。
func (a *App) CreateFolder(name string, parentID int) (model.Folder, error) {
	if err := a.ensureReady(); err != nil {
		return model.Folder{}, err
	}
	var created model.Folder
	err := a.application.LegacyMutation(context.Background(), func() error {
		var err error
		created, err = a.catalog.CreateFolder(name, parentID)
		return err
	})
	return created, err
}

// MoveForward 把 Forward 移到目标文件夹。
func (a *App) MoveForward(forwardID, targetFolderID int) error {
	if err := a.ensureReady(); err != nil {
		return err
	}
	_, err := a.application.MoveForwards(context.Background(), application.MoveForwardsCommand{ForwardIDs: []int{forwardID}, TargetFolderID: targetFolderID})
	return err
}

func (a *App) MoveForwardsCommand(command application.MoveForwardsCommand) (application.MoveForwardsResult, error) {
	if err := a.ensureReady(); err != nil {
		return application.MoveForwardsResult{}, err
	}
	return a.application.MoveForwards(context.Background(), command)
}

func (a *App) SaveSSHHostCommand(command application.SaveSSHHostCommand) (application.SaveSSHHostResult, error) {
	if err := a.ensureReady(); err != nil {
		return application.SaveSSHHostResult{}, err
	}
	return a.application.SaveSSHHost(context.Background(), command)
}

// PreviewSSHHostChange 为连接身份变更生成一次性短期确认 token；响应不包含认证秘密。
func (a *App) PreviewSSHHostChange(command application.SaveSSHHostCommand) (application.SSHHostChangePreview, error) {
	if err := a.ensureReady(); err != nil {
		return application.SSHHostChangePreview{}, err
	}
	return a.application.PreviewSSHHostChange(context.Background(), command)
}

// CommitSSHHostChange 消费 Preview token，先预检新 SSH 链，再编排旧运行集的有界重启。
func (a *App) CommitSSHHostChange(command application.CommitSSHHostChangeCommand) (application.CommitSSHHostChangeResult, error) {
	if err := a.ensureReady(); err != nil {
		return application.CommitSSHHostChangeResult{}, err
	}
	return a.application.CommitSSHHostChange(context.Background(), command)
}

// TestSSHHostConnectionCommand 对未保存的 SSH 主机草稿执行真实连通性检查，不产生持久化或运行时副作用。
func (a *App) TestSSHHostConnectionCommand(command application.TestSSHHostConnectionCommand) (application.ConnectionTestResult, error) {
	if err := a.ensureReady(); err != nil {
		return application.ConnectionTestResult{}, err
	}
	return a.application.TestSSHHostConnection(context.Background(), command)
}

// TestForwardConnectionCommand 对未保存的 Forward 草稿检查本地监听、SSH 链路及远端目标，不启动 Forward。
func (a *App) TestForwardConnectionCommand(command application.TestForwardConnectionCommand) (application.ConnectionTestResult, error) {
	if err := a.ensureReady(); err != nil {
		return application.ConnectionTestResult{}, err
	}
	return a.application.TestForwardConnection(context.Background(), command)
}

// SaveForward 新建（ID 为 0）或更新 Forward；运行中的 Forward 必须先停止再编辑。
func (a *App) SaveForward(forward model.Forward) (model.Forward, error) {
	if err := a.ensureReady(); err != nil {
		return model.Forward{}, err
	}
	var saved model.Forward
	err := a.application.LegacyMutation(context.Background(), func() error {
		if forward.ID != 0 {
			if st, ok := a.runtime.Status(forward.ID); ok && (st.Status == biz.RuntimeStateRunning || st.Status == biz.RuntimeStateReconnecting) {
				return fmt.Errorf("forward %d is running, stop it before editing", forward.ID)
			}
		}
		var err error
		saved, err = a.catalog.SaveForward(forward)
		return err
	})
	return saved, err
}

// DeleteSelection 批量删除文件夹、SSH 主机与 Forward；非空文件夹需 CascadeFolders。
// 涉及的运行中 Forward 先停止（含级联删除文件夹内的）。
func (a *App) DeleteSelection(sel biz.DeleteSelection) error {
	if err := a.ensureReady(); err != nil {
		return err
	}
	return a.application.RouteMutation(context.Background(), func() error {
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
	})
}

// SaveWebRoute 新建（ID 为 0）或更新 Web Route（域名、hosts/Caddy 开关、HTTPS SNI）。
func (a *App) SaveWebRoute(route model.WebRoute) (model.WebRoute, error) {
	if err := a.ensureReady(); err != nil {
		return model.WebRoute{}, err
	}
	var saved model.WebRoute
	err := a.application.LegacyMutation(context.Background(), func() error { var err error; saved, err = a.catalog.SaveWebRoute(route); return err })
	return saved, err
}

// PreviewRouteChange 预览一次 revision-bound Route 用户意图，不产生系统副作用。
func (a *App) PreviewRouteChange(intent application.RouteChangeIntent) (application.RouteChangePreview, error) {
	if err := a.ensureReady(); err != nil {
		return application.RouteChangePreview{}, err
	}
	return a.application.PreviewRouteChange(context.Background(), intent)
}

// CommitRouteChange 消费不透明 Preview token，原子保存 desired 后串行收敛系统状态。
func (a *App) CommitRouteChange(command application.CommitRouteChangeCommand) (application.RouteCommandResult, error) {
	if err := a.ensureReady(); err != nil {
		return application.RouteCommandResult{}, err
	}
	return a.application.CommitRouteChange(context.Background(), command)
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
	var result biz.RouteApplyResult
	err := a.application.RouteMutation(context.Background(), func() error { var err error; result, err = a.router.ApplyRoute(routeID, confirmedDomains); return err })
	return result, err
}

// RemoveRoute 删除 Route 并重推系统（撤销 hosts 记录；最后一个 Caddy Route 移除后停 Caddy 并撤 CA）。
func (a *App) RemoveRoute(routeID int) (biz.RouteApplyResult, error) {
	if err := a.ensureReady(); err != nil {
		return biz.RouteApplyResult{}, err
	}
	var result biz.RouteApplyResult
	err := a.application.RouteMutation(context.Background(), func() error { var err error; result, err = a.router.RemoveRoute(routeID); return err })
	return result, err
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

// StageImportCommand 有界读取并只解密一次，后续提交和私钥另存仅使用短期 token。
func (a *App) StageImportCommand(request application.StageImportRequest) (application.ImportStagePreview, error) {
	if err := a.ensureReady(); err != nil {
		return application.ImportStagePreview{}, err
	}
	request.Path = strings.TrimSpace(request.Path)
	return a.application.StageImport(context.Background(), request)
}

func (a *App) CommitImportCommand(command application.CommitImportCommand) (application.CommitImportResult, error) {
	if err := a.ensureReady(); err != nil {
		return application.CommitImportResult{}, err
	}
	return a.application.CommitImport(context.Background(), command)
}

// RestoreBackup 是旧前端兼容入口；内部仍严格执行零副作用 Stage + 事务 Commit。
func (a *App) RestoreBackup(srcPath, password string, confirmed bool) error {
	if err := a.ensureReady(); err != nil {
		return err
	}
	if !confirmed {
		return biz.ErrRestoreNotConfirmed
	}
	preview, err := a.application.StageRestore(context.Background(), biz.RestoreStageRequest{Path: strings.TrimSpace(srcPath), Password: password})
	if err != nil {
		return err
	}
	_, err = a.application.CommitRestore(context.Background(), biz.RestoreCommitRequest{Token: preview.Token, Confirmed: true})
	return err
}

func (a *App) StageRestoreCommand(request biz.RestoreStageRequest) (biz.RestorePreview, error) {
	if err := a.ensureReady(); err != nil {
		return biz.RestorePreview{}, err
	}
	return a.application.StageRestore(context.Background(), request)
}

func (a *App) CommitRestoreCommand(request biz.RestoreCommitRequest) (biz.RestoreCommitResult, error) {
	if err := a.ensureReady(); err != nil {
		return biz.RestoreCommitResult{}, err
	}
	return a.application.CommitRestore(context.Background(), request)
}

func (a *App) PreviewRestoredNetworkActivation() (application.RestoreActivationPreview, error) {
	if err := a.ensureReady(); err != nil {
		return application.RestoreActivationPreview{}, err
	}
	return a.application.PreviewRestoredNetworkActivation(context.Background())
}

func (a *App) ActivateRestoredNetwork(command application.ActivateRestoredNetworkCommand) error {
	if err := a.ensureReady(); err != nil {
		return err
	}
	return a.application.ActivateRestoredNetwork(context.Background(), command)
}

// SaveImportKeyFileCommand 只用 token/keyID 选择后端暂存私钥；私钥字节不进入 WebView。
func (a *App) SaveImportKeyFileCommand(command application.SaveImportKeyFileCommand) error {
	if err := a.ensureReady(); err != nil {
		return err
	}
	name := filepath.Base(strings.TrimSpace(command.SuggestedName))
	if name == "." || name == "" {
		name = "imported-ssh-key"
	}
	destPath, err := wailsruntime.SaveFileDialog(a.ctx, wailsruntime.SaveDialogOptions{
		DefaultFilename: name,
	})
	if err != nil {
		return fmt.Errorf("file dialog: %w", err)
	}
	if strings.TrimSpace(destPath) == "" {
		return nil // 用户取消
	}
	return a.application.SaveImportKeyFile(context.Background(), command.Token, command.KeyID, destPath)
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
	}
	if snapshot, err := a.GetRuntimeSnapshot(); err == nil {
		summary["runtime"] = snapshot
	}
	if routes, err := a.router.RouteStatus(); err == nil {
		summary["routes"] = routes
		caTrusted := false
		for _, item := range routes {
			caTrusted = caTrusted || item.CATrusted
		}
		summary["caTrusted"] = caTrusted
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
	errs := a.application.StartForwards(context.Background(), []int{id})
	if message := errs[id]; message != "" {
		return errors.New(message)
	}
	return nil
}

// StopForward 停止单条 Forward；手动停止不触发自动重连。
func (a *App) StopForward(id int) error {
	if err := a.ensureReady(); err != nil {
		return err
	}
	return a.application.StopForward(id)
}

// StartManyForwards 批量启动 Forward；返回启动失败的 id → 错误信息（成功项不出现）。
func (a *App) StartManyForwards(ids []int) (map[int]string, error) {
	if err := a.ensureReady(); err != nil {
		return nil, err
	}
	return a.application.StartForwards(context.Background(), ids), nil
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
	preview := a.application.PreviewLocalListener(context.Background(), application.PreviewLocalListenerCommand{Mode: "local", Host: host, Port: port})
	if preview.State == "available" || preview.State == "owned_by_self" {
		return nil
	}
	return fmt.Errorf("local listener preview: %s", preview.State)
}

func (a *App) PreviewLocalListenerCommand(command application.PreviewLocalListenerCommand) (application.LocalListenerPreview, error) {
	if err := a.ensureReady(); err != nil {
		return application.LocalListenerPreview{}, err
	}
	return a.application.PreviewLocalListener(context.Background(), command), nil
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
	result, err := a.CheckForUpdatesCommand(application.CheckForUpdatesCommand{Trigger: application.UpdateCheckManual})
	return result.Result, err
}

func (a *App) CheckForUpdatesCommand(command application.CheckForUpdatesCommand) (application.CheckForUpdatesResult, error) {
	if err := a.ensureReady(); err != nil {
		return application.CheckForUpdatesResult{}, err
	}
	return a.application.CheckForUpdates(context.Background(), command)
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

// GetConfigPath returns the absolute path of the Vault data directory.
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

// GetStartupCATrustRequest returns the startup trust request even when the
// frontend mounted after its Wails event was emitted.
func (a *App) GetStartupCATrustRequest() (StartupCATrustRequest, error) {
	if err := a.ensureReady(); err != nil {
		return StartupCATrustRequest{}, err
	}
	a.startupCATrust.RLock()
	defer a.startupCATrust.RUnlock()
	return a.startupCATrust.request, nil
}

// ConfirmStartupCATrust verifies the displayed fingerprint again and applies
// the current desired routes only after the user explicitly confirms it.
func (a *App) ConfirmStartupCATrust(fingerprint string) (biz.RouteApplyResult, error) {
	if err := a.ensureReady(); err != nil {
		return biz.RouteApplyResult{}, err
	}
	result, err := a.application.ConfirmStartupCATrust(context.Background(), fingerprint)
	if err != nil {
		return biz.RouteApplyResult{}, err
	}
	a.startupCATrust.Lock()
	a.startupCATrust.request = StartupCATrustRequest{}
	a.startupCATrust.Unlock()
	return result, nil
}

// OpenConfigDir opens the Vault data directory in the OS file manager.
// It supports macOS, Windows and Linux (via xdg-open).
func (a *App) OpenConfigDir() error {
	if err := a.ensureReady(); err != nil {
		return err
	}
	return openDirectory(a.store.Dir())
}

// GetCaddyDataPath returns the device-local Caddy runtime and local HTTPS
// credential directory. It is intentionally separate from the portable Vault.
func (a *App) GetCaddyDataPath() (string, error) {
	if err := a.ensureReady(); err != nil {
		return "", err
	}
	if a.caddy == nil || strings.TrimSpace(a.caddy.DataDir) == "" {
		return "", errors.New("caddy data directory is unavailable")
	}
	abs, err := filepath.Abs(a.caddy.DataDir)
	if err != nil {
		return a.caddy.DataDir, nil
	}
	return abs, nil
}

// OpenCaddyDataDir opens the device-local Caddy runtime and local HTTPS
// credential directory in the OS file manager.
func (a *App) OpenCaddyDataDir() error {
	if err := a.ensureReady(); err != nil {
		return err
	}
	path, err := a.GetCaddyDataPath()
	if err != nil {
		return err
	}
	return openDirectory(path)
}

func openDirectory(dir string) error {
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
