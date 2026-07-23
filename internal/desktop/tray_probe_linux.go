//go:build linux

package desktop

import (
	"context"
	"time"

	"github.com/godbus/dbus/v5"
)

func statusNotifierWatcherAvailable() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return false
	}
	defer conn.Close()

	var available bool
	call := conn.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.NameHasOwner", 0, "org.kde.StatusNotifierWatcher")
	if call.Err != nil {
		return false
	}
	return call.Store(&available) == nil && available
}
