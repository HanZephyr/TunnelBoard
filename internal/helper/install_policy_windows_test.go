//go:build windows

package helper

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestMatchingPublisherIdentityRejectsDifferentCertificates(t *testing.T) {
	if err := requireMatchingPublisherIdentity("app-sha256", "helper-sha256"); err == nil {
		t.Fatal("different publisher certificates must be rejected")
	}
	if err := requireMatchingPublisherIdentity("same-sha256", "same-sha256"); err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{{"", "same"}, {"same", ""}} {
		if err := requireMatchingPublisherIdentity(pair[0], pair[1]); err == nil {
			t.Fatalf("empty publisher identity accepted: %v", pair)
		}
	}
}
