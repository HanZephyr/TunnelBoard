//go:build windows

package main

import (
	"errors"
	"testing"
)

func TestRunLegacyCleanupAcceptsOnlyExactInstallerCommand(t *testing.T) {
	calls := 0
	cleanup := func() error { calls++; return nil }
	handled, err := runLegacyCleanup([]string{"--cleanup-legacy-service"}, cleanup)
	if !handled || err != nil || calls != 1 {
		t.Fatalf("handled=%v err=%v calls=%d", handled, err, calls)
	}
	for _, args := range [][]string{{}, {"--cleanup-legacy-service", "extra"}, {"--session-helper"}} {
		handled, err = runLegacyCleanup(args, cleanup)
		if handled || err != nil {
			t.Fatalf("args=%v handled=%v err=%v", args, handled, err)
		}
	}
	if calls != 1 {
		t.Fatalf("unexpected cleanup calls=%d", calls)
	}
}

func TestRunLegacyCleanupPropagatesFailure(t *testing.T) {
	want := errors.New("service still exists")
	handled, err := runLegacyCleanup([]string{"--cleanup-legacy-service"}, func() error { return want })
	if !handled || !errors.Is(err, want) {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
}

func TestRunCurrentUserCleanupAcceptsOnlyExactUninstallCommand(t *testing.T) {
	calls := 0
	handled, err := runCurrentUserCleanup([]string{"--cleanup-current-user-ca"}, func() error { calls++; return nil })
	if !handled || err != nil || calls != 1 {
		t.Fatalf("handled=%v err=%v calls=%d", handled, err, calls)
	}
	if handled, _ := runCurrentUserCleanup([]string{"--cleanup-current-user-ca", "extra"}, func() error { calls++; return nil }); handled {
		t.Fatal("cleanup command accepted unexpected arguments")
	}
}
