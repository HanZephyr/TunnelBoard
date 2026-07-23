//go:build !linux

package desktop

func statusNotifierWatcherAvailable() bool {
	return false
}
