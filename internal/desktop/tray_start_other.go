//go:build !darwin

package desktop

// StartTrayPreservingAppDelegate starts the tray after the desktop toolkit is up.
func StartTrayPreservingAppDelegate(start func()) {
	if start != nil {
		start()
	}
}
