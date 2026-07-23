package desktop

import "testing"

type watcherProbe bool

func (p watcherProbe) HasStatusNotifierWatcher() bool { return bool(p) }

func TestLinuxWithoutStatusNotifierAsksBeforeClosing(t *testing.T) {
	lifecycle := NewLifecycle(PlatformLinux, false)

	if got, want := lifecycle.CloseAction(false), CloseAskUser; got != want {
		t.Fatalf("CloseAction(false) = %v, want %v", got, want)
	}
}

func TestLinuxWithStatusNotifierHidesAndAutostartStaysHidden(t *testing.T) {
	lifecycle := NewLifecycle(PlatformLinux, true)

	if got, want := lifecycle.CloseAction(false), CloseHide; got != want {
		t.Fatalf("CloseAction(false) = %v, want %v", got, want)
	}
	if !lifecycle.StartHidden(true) {
		t.Fatal("StartHidden(true) = false, want true when tray is available")
	}
}

func TestLinuxWithoutStatusNotifierAutostartShowsWindow(t *testing.T) {
	lifecycle := NewLifecycle(PlatformLinux, false)

	if lifecycle.StartHidden(true) {
		t.Fatal("StartHidden(true) = true, want false without a usable tray")
	}
}

func TestExplicitQuitAlwaysExits(t *testing.T) {
	lifecycle := NewLifecycle(PlatformLinux, false)

	if got, want := lifecycle.CloseAction(true), CloseExit; got != want {
		t.Fatalf("CloseAction(true) = %v, want %v", got, want)
	}
}

func TestLinuxTrayAvailabilityRequiresStatusNotifierWatcher(t *testing.T) {
	if TrayAvailable(PlatformLinux, watcherProbe(false)) {
		t.Fatal("TrayAvailable() = true without StatusNotifierWatcher")
	}
	if !TrayAvailable(PlatformLinux, watcherProbe(true)) {
		t.Fatal("TrayAvailable() = false with StatusNotifierWatcher")
	}
}
