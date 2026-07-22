//go:build windows

package helper

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPinnedPortableBundleLaunchesUnsignedHelper(t *testing.T) {
	dir := t.TempDir()
	helperPath := filepath.Join(dir, HelperBinaryName)
	payload := []byte("portable-helper")
	if err := os.WriteFile(helperPath, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	SetExpectedBinarySHA256(hex.EncodeToString(digest[:]))
	t.Cleanup(func() { SetExpectedBinarySHA256("") })

	originalExecutablePath := currentExecutablePath
	currentExecutablePath = func() (string, error) { return filepath.Join(dir, "TunnelBoard.exe"), nil }
	t.Cleanup(func() { currentExecutablePath = originalExecutablePath })
	originalStart := startElevatedSessionHelper
	started := false
	startElevatedSessionHelper = func(executable, parameters string) (elevatedProcess, error) {
		if executable != helperPath {
			t.Fatalf("executable = %q, want %q", executable, helperPath)
		}
		started = true
		return &inProcessElevatedProcess{pid: 1}, nil
	}
	t.Cleanup(func() { startElevatedSessionHelper = originalStart })

	if _, err := launchElevatedSessionHelper(`\\.\pipe\tunnelboard-helper-test`, 1, SessionProtocolVersion); err != nil {
		t.Fatalf("pinned portable helper should launch without Authenticode: %v", err)
	}
	if !started {
		t.Fatal("pinned portable helper was not started")
	}
}

func TestFormalBuildRejectsHelperPathOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tunnelboard-helper.exe")
	if err := os.WriteFile(path, []byte("override"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(HelperBinaryEnvVar, path)
	SetExpectedBinarySHA256(strings.Repeat("a", 64))
	t.Cleanup(func() { SetExpectedBinarySHA256("") })
	if _, err := helperBinaryPath(); err == nil || !strings.Contains(err.Error(), "disabled in formal builds") {
		t.Fatalf("helperBinaryPath error=%v", err)
	}
}
