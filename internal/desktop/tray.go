package desktop

import (
	"runtime"
	"strings"
)

// StatusNotifierProbe reports whether this user session has a StatusNotifier watcher.
// TunnelBoard's Linux tray adapter uses that protocol, so other legacy tray protocols
// do not count as an available tray.
type StatusNotifierProbe interface {
	HasStatusNotifierWatcher() bool
}

// TrayAvailable decides whether this process can offer its tray-only lifecycle behavior.
func TrayAvailable(platform Platform, probe StatusNotifierProbe) bool {
	if Platform(strings.ToLower(string(platform))) != PlatformLinux {
		return true
	}
	return probe != nil && probe.HasStatusNotifierWatcher()
}

// SystemTrayAvailable probes the current desktop session. On Linux it requires a
// StatusNotifier watcher; on other supported desktop platforms the existing native tray
// implementation remains authoritative.
func SystemTrayAvailable() bool {
	return TrayAvailable(Platform(runtime.GOOS), watcherAvailability(statusNotifierWatcherAvailable()))
}

type watcherAvailability bool

func (a watcherAvailability) HasStatusNotifierWatcher() bool {
	return bool(a)
}
