//go:build darwin

package desktop

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework AppKit
#import <AppKit/AppKit.h>

void *TBCurrentAppDelegate(void) {
	return (__bridge void *)[NSApp delegate];
}

void TBRestoreAppDelegate(void *delegate) {
	if (delegate != NULL) {
		[NSApp setDelegate:(__bridge id)delegate];
	}
}
*/
import "C"

// StartTrayPreservingAppDelegate runs energye nativeStart without letting it
// keep NSApp's delegate. nativeStart always setDelegate's itself, which would
// otherwise steal Wails' terminate/file-open handlers and can drop the status item.
func StartTrayPreservingAppDelegate(start func()) {
	if start == nil {
		return
	}
	previous := C.TBCurrentAppDelegate()
	start()
	if previous != nil {
		C.TBRestoreAppDelegate(previous)
	}
}
