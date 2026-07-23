package main

import (
	"testing"

	"github.com/HanZephyr/TunnelBoard/internal/desktop"
)

func TestAutostartStartsHiddenOnlyWhenLinuxTrayIsUsable(t *testing.T) {
	if !startHiddenFor(desktop.NewLifecycle(desktop.PlatformLinux, true), []string{"--autostart"}) {
		t.Fatal("tray-capable Linux autostart should start hidden")
	}
	if startHiddenFor(desktop.NewLifecycle(desktop.PlatformLinux, false), []string{"--autostart"}) {
		t.Fatal("Linux autostart without a tray must show the main window")
	}
}
