// Package desktop keeps desktop-session lifecycle decisions behind one seam.
package desktop

import "strings"

// Platform identifies the operating system relevant to desktop lifecycle behavior.
type Platform string

const (
	PlatformLinux   Platform = "linux"
	PlatformWindows Platform = "windows"
	PlatformDarwin  Platform = "darwin"
)

// CloseAction is the next action for a main-window close request.
type CloseAction uint8

const (
	CloseExit CloseAction = iota
	CloseHide
	CloseAskUser
)

// CloseChoice is the user's response to a no-tray close prompt.
type CloseChoice string

const (
	CloseChoiceExit   CloseChoice = "exit"
	CloseChoiceHide   CloseChoice = "hide"
	CloseChoiceCancel CloseChoice = "cancel"
)

// ClosePrompt contains the native dialog text for a no-tray Linux close request.
type ClosePrompt struct {
	Title       string
	Message     string
	ExitLabel   string
	HideLabel   string
	CancelLabel string
}

// Lifecycle holds the desktop capabilities determined at process startup.
// On Linux, trayAvailable means a StatusNotifier watcher accepted the app's tray model.
type Lifecycle struct {
	platform      Platform
	trayAvailable bool
}

// NewLifecycle creates the lifecycle policy for one desktop session.
func NewLifecycle(platform Platform, trayAvailable bool) Lifecycle {
	return Lifecycle{platform: Platform(strings.ToLower(string(platform))), trayAvailable: trayAvailable}
}

// CloseAction returns how an ordinary window close should be handled.
// Explicit quit always exits. Linux only hides automatically when the StatusNotifier tray is available.
func (l Lifecycle) CloseAction(explicitQuit bool) CloseAction {
	if explicitQuit {
		return CloseExit
	}
	if l.platform == PlatformLinux {
		if l.trayAvailable {
			return CloseHide
		}
		return CloseAskUser
	}
	if l.platform == PlatformWindows {
		return CloseHide
	}
	if l.platform == PlatformDarwin {
		// 窗口红灯由 Wails HideWindowOnClose 在 Cocoa 层 [NSApp hide]；
		// OnBeforeClose 在 Darwin 上只会出现在 Cmd+Q、Dock 退出和 runtime.Quit。
		return CloseExit
	}
	return CloseExit
}

// StartHidden reports whether a login autostart should avoid showing the main window.
// A Linux session without a usable tray must show the window so the user can recover it.
func (l Lifecycle) StartHidden(autostartInvocation bool) bool {
	return autostartInvocation && l.platform == PlatformLinux && l.trayAvailable
}

// HasTray reports whether this session can use TunnelBoard's tray adapter.
func (l Lifecycle) HasTray() bool {
	return l.trayAvailable
}

// ClosePromptForLocale returns a clear native prompt for Linux sessions without a tray.
func ClosePromptForLocale(locale string) ClosePrompt {
	if strings.HasPrefix(strings.ToLower(locale), "zh") {
		return ClosePrompt{
			Title:       "关闭 TunnelBoard？",
			Message:     "当前桌面没有可用的系统托盘。选择“是”退出应用；选择“否”隐藏窗口并继续在后台运行。之后可再次启动 TunnelBoard 显示窗口。",
			ExitLabel:   "退出应用",
			HideLabel:   "隐藏到后台",
			CancelLabel: "取消",
		}
	}
	return ClosePrompt{
		Title:       "Close TunnelBoard?",
		Message:     "This desktop has no usable system tray. Choose Yes to quit the app, or No to hide the window and keep it running in the background. Launch TunnelBoard again to show the window.",
		ExitLabel:   "Quit app",
		HideLabel:   "Hide in background",
		CancelLabel: "Cancel",
	}
}
