package main

import (
	"context"
	"embed"
	"os"
	"runtime"

	"github.com/HanZephyr/TunnelBoard/internal/autostart"
	"github.com/HanZephyr/TunnelBoard/internal/desktop"
	"github.com/HanZephyr/TunnelBoard/internal/traytext"
	"github.com/energye/systray"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed build/windows/icon.ico
var trayIconWindows []byte

//go:embed build/appicon.png
var trayIconFallback []byte

const (
	applicationTitle = "TunnelBoard"
	singleInstanceID = "tunnelboard-single-instance"
	appVersion       = "1.0.18"
)

func main() {
	// Create an instance of the app structure
	app := NewApp()
	lifecycle := desktop.NewLifecycle(desktop.Platform(runtime.GOOS), desktop.SystemTrayAvailable())
	app.setDesktopLifecycle(lifecycle)

	showMainWindow := func() {
		if app == nil || app.ctx == nil {
			return
		}
		wailsruntime.Show(app.ctx)
		wailsruntime.WindowShow(app.ctx)
		wailsruntime.WindowUnminimise(app.ctx)
	}

	var startTray, endTray func()
	if lifecycle.HasTray() {
		trayLabels := traytext.ForLocale(app.UILocale())
		startTray, endTray = systray.RunWithExternalLoop(func() {
			iconBytes := trayIconFallback
			if runtime.GOOS == "windows" && len(trayIconWindows) > 0 {
				iconBytes = trayIconWindows
			}
			if len(iconBytes) > 0 {
				systray.SetIcon(iconBytes)
			}
			systray.SetTitle(trayLabels.AppTitle)
			systray.SetTooltip(trayLabels.IconTooltip)

			showWinItem := systray.AddMenuItem(trayLabels.ShowMainTitle, trayLabels.ShowMainTooltip)
			showWinItem.Click(showMainWindow)
			quitMenu := systray.AddMenuItem(trayLabels.QuitTitle, trayLabels.QuitTooltip)
			quitMenu.Click(func() {
				if app != nil && app.ctx != nil {
					app.PrepareForQuit()
					wailsruntime.Quit(app.ctx)
					return
				}
				os.Exit(0)
			})

			popupTrayMenu := func(menu systray.IMenu) {
				if menu != nil {
					_ = menu.ShowMenu()
				}
			}
			switch runtime.GOOS {
			case "darwin":
				systray.CreateMenu()
			default:
				systray.SetOnClick(popupTrayMenu)
				systray.SetOnRClick(popupTrayMenu)
			}

			app.SetTrayMenuItems(showWinItem, quitMenu)
		}, func() {})
		defer func() {
			if endTray != nil {
				endTray()
			}
		}()
	}

	err := wails.Run(&options.App{
		Title:         applicationTitle,
		Width:         1024,
		Height:        768,
		MinWidth:      1024,
		MinHeight:     768,
		DisableResize: false,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 27, G: 38, B: 54, A: 1},
		OnStartup: func(ctx context.Context) {
			app.startup(ctx)
			if startTray != nil {
				desktop.StartTrayPreservingAppDelegate(startTray)
			}
		},
		OnBeforeClose:     app.beforeClose,
		OnShutdown:        app.shutdown,
		HideWindowOnClose: runtime.GOOS == "darwin",
		StartHidden:       startHiddenFor(lifecycle, os.Args[1:]),
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId: singleInstanceID,
			OnSecondInstanceLaunch: func(secondInstanceData options.SecondInstanceData) {
				_ = secondInstanceData
				showMainWindow()
			},
		},
		Bind: []interface{}{
			app,
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
	// Ensure tray resources are released when the window exits directly.
	if lifecycle.HasTray() {
		systray.Quit()
	}
}

func startHiddenFor(lifecycle desktop.Lifecycle, args []string) bool {
	return lifecycle.StartHidden(autostart.IsAutostartInvocation(args))
}
