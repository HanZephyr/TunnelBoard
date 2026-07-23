package autostart

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/emersion/go-autostart"
)

const (
	appName         = "tunnelboard"
	appDisplayName  = "TunnelBoard"
	autostartMarker = "--autostart"
)

// isSupported returns true if the current OS supports launch-at-login.
func isSupported() bool {
	return runtime.GOOS == "darwin" || runtime.GOOS == "windows" || runtime.GOOS == "linux"
}

func newApp() *autostart.App {
	execPath, _ := os.Executable()
	if execPath == "" {
		execPath = "tunnelboard"
	}
	return &autostart.App{
		Name:        appName,
		DisplayName: appDisplayName,
		Exec:        []string{execPath},
	}
}

// IsEnabled returns whether launch at login is currently enabled.
// On unsupported platforms it returns false, nil.
func IsEnabled() (bool, error) {
	if !isSupported() {
		return false, nil
	}
	if runtime.GOOS == "linux" {
		_, err := os.Stat(currentLinuxAutostartPath())
		if err == nil {
			return true, nil
		}
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return newApp().IsEnabled(), nil
}

// Enable registers the app to start at user login.
// On unsupported platforms it is a no-op and returns nil.
func Enable() error {
	if !isSupported() {
		return nil
	}
	if runtime.GOOS == "linux" {
		execPath, err := os.Executable()
		if err != nil {
			return fmt.Errorf("determine TunnelBoard executable for XDG autostart: %w", err)
		}
		if strings.TrimSpace(execPath) == "" {
			return fmt.Errorf("determine TunnelBoard executable for XDG autostart: empty executable path")
		}
		_, err = writeLinuxAutostart(os.Getenv("XDG_CONFIG_HOME"), linuxHomeDir(), execPath)
		return err
	}
	return newApp().Enable()
}

// Disable removes the app from login startup.
// On unsupported platforms it is a no-op and returns nil.
func Disable() error {
	if !isSupported() {
		return nil
	}
	if runtime.GOOS == "linux" {
		err := os.Remove(currentLinuxAutostartPath())
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return newApp().Disable()
}

func currentLinuxAutostartPath() string {
	return linuxAutostartPath(os.Getenv("XDG_CONFIG_HOME"), linuxHomeDir())
}

func linuxHomeDir() string {
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		return home
	}
	return os.Getenv("HOME")
}

func linuxAutostartPath(xdgConfigHome, home string) string {
	configHome := strings.TrimSpace(xdgConfigHome)
	if !filepath.IsAbs(configHome) {
		configHome = ""
	}
	if configHome == "" {
		configHome = filepath.Join(strings.TrimSpace(home), ".config")
	}
	return filepath.Join(configHome, "autostart", appName+".desktop")
}

func writeLinuxAutostart(xdgConfigHome, home, execPath string) (string, error) {
	path := linuxAutostartPath(xdgConfigHome, home)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("create XDG autostart directory: %w", err)
	}
	entry := "[Desktop Entry]\n" +
		"Type=Application\n" +
		"Name=" + appDisplayName + "\n" +
		"Exec=" + strconv.Quote(execPath) + " " + strconv.Quote(autostartMarker) + "\n" +
		"X-GNOME-Autostart-enabled=true\n"
	if err := os.WriteFile(path, []byte(entry), 0o644); err != nil {
		return "", fmt.Errorf("write XDG autostart entry: %w", err)
	}
	return path, nil
}

// IsAutostartInvocation reports whether this process was launched by TunnelBoard's XDG entry.
func IsAutostartInvocation(args []string) bool {
	for _, arg := range args {
		if arg == autostartMarker {
			return true
		}
	}
	return false
}
