package helper_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HanZephyr/TunnelBoard/internal/helper"
)

func TestVerifyBundledBinaryAcceptsOnlyConfiguredDigest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tunnelboard-helper.exe")
	payload := []byte("signed-helper-fixture")
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	helper.SetExpectedBinarySHA256(hex.EncodeToString(digest[:]))
	t.Cleanup(func() { helper.SetExpectedBinarySHA256("") })
	if err := helper.VerifyBundledBinary(path); err != nil {
		t.Fatalf("matching helper rejected: %v", err)
	}
	if err := os.WriteFile(path, []byte("replaced-helper-fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := helper.VerifyBundledBinary(path); err == nil || !strings.Contains(err.Error(), "integrity mismatch") {
		t.Fatalf("replacement must be rejected, got %v", err)
	}
}

func TestVerifyBundledBinaryRejectsMissingBuildPin(t *testing.T) {
	helper.SetExpectedBinarySHA256("")
	if err := helper.VerifyBundledBinary(filepath.Join(t.TempDir(), "missing.exe")); err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("missing build pin must fail closed, got %v", err)
	}
}
