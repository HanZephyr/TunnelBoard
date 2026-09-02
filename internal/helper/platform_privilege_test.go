package helper_test

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/pem"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/HanZephyr/TunnelBoard/internal/helper"
)

type recordedCommand struct {
	executable string
	args       []string
}

type recordingRunner struct {
	calls          []recordedCommand
	trustedCertPEM []byte
	output         func(executable string, args []string) ([]byte, error)
}

func (r *recordingRunner) Run(_ context.Context, executable string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, recordedCommand{executable: executable, args: append([]string(nil), args...)})
	if len(args) > 4 && args[3] == "trust-ca" {
		r.trustedCertPEM, _ = os.ReadFile(args[4])
	}
	if r.output != nil {
		return r.output(executable, args)
	}
	if len(args) > 3 && args[3] == "ensure-https-redirect" && len(args) > 4 {
		dir := args[4]
		for _, name := range []string{"anchor", "pf.conf", "plist"} {
			if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
				return nil, err
			}
		}
	}
	return nil, nil
}

func TestLinuxRefusesLegacyGenericPrivilegeAdapter(t *testing.T) {
	privilege, err := helper.NewPlatformPrivilege(helper.PlatformPrivilegeOptions{Platform: "linux", TempRoot: t.TempDir()})
	if err == nil || privilege != nil || !strings.Contains(err.Error(), "restricted polkit session") {
		t.Fatalf("privilege/err = %v/%v, want Linux restricted session rejection", privilege, err)
	}
}

func TestDarwinPrivilegeKeepsAppleScriptConstantAndUsesArgv(t *testing.T) {
	runner := &recordingRunner{}
	root := filepath.Join(t.TempDir(), "quote' ; $(touch nope)")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	privilege, err := helper.NewPlatformPrivilege(helper.PlatformPrivilegeOptions{Platform: "darwin", TempRoot: root, Runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	if err := privilege.TrustLocalCA(context.Background(), makeSelfSignedCA(t, "TunnelBoard Local CA")); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 1 || runner.calls[0].executable != "/usr/bin/osascript" {
		t.Fatalf("calls = %+v", runner.calls)
	}
	args := runner.calls[0].args
	if len(args) != 7 || args[0] != "-e" || args[2] != "--" || args[3] != "trust-ca" {
		t.Fatalf("osascript argv = %#v", args)
	}
	if strings.Contains(args[1], root) || !strings.Contains(args[4], "quote' ; $(touch nope)") {
		t.Fatalf("dynamic path must be absent from script and present as one argv: %#v", args)
	}
	if args[5] != "/Library/Keychains/System.keychain" || args[6] != "/usr/bin/security" {
		t.Fatalf("fixed keychain/security argv missing: %#v", args)
	}
	block, _ := pem.Decode(runner.trustedCertPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		t.Fatalf("privileged trust input must be PEM, got %q", runner.trustedCertPEM)
	}
}

func TestDarwinUntrustLocalCADeletesSystemKeychainBySHA1(t *testing.T) {
	der := makeSelfSignedCA(t, "TunnelBoard Local CA")
	sha1Sum := sha1.Sum(der)
	sha1Hex := strings.ToUpper(hex.EncodeToString(sha1Sum[:]))
	listing := []byte("SHA-1 hash: " + sha1Hex + "\n" + string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})))

	runner := &recordingRunner{
		output: func(executable string, args []string) ([]byte, error) {
			if executable == "/usr/bin/security" && len(args) > 0 && args[0] == "find-certificate" {
				return listing, nil
			}
			return nil, nil
		},
	}
	privilege, err := helper.NewPlatformPrivilege(helper.PlatformPrivilegeOptions{Platform: "darwin", TempRoot: t.TempDir(), Runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	if err := privilege.UntrustLocalCA(context.Background(), sha256Hex(der)); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("calls = %+v, want find-certificate then osascript", runner.calls)
	}
	if runner.calls[0].executable != "/usr/bin/security" || runner.calls[0].args[0] != "find-certificate" {
		t.Fatalf("first call = %+v, want unprivileged System keychain listing", runner.calls[0])
	}
	args := runner.calls[1].args
	if len(args) < 6 || args[3] != "untrust-ca" || args[4] != sha1Hex || args[5] != "/Library/Keychains/System.keychain" {
		t.Fatalf("osascript argv = %#v, want SHA-1 delete from System.keychain", args)
	}
}

func TestDarwinUntrustLocalCANoopsWhenCertificateAbsent(t *testing.T) {
	runner := &recordingRunner{}
	privilege, err := helper.NewPlatformPrivilege(helper.PlatformPrivilegeOptions{Platform: "darwin", TempRoot: t.TempDir(), Runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	if err := privilege.UntrustLocalCA(context.Background(), strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 1 || runner.calls[0].executable != "/usr/bin/security" {
		t.Fatalf("absent cert must only list the keychain: %+v", runner.calls)
	}
	for _, call := range runner.calls {
		if call.executable == "/usr/bin/osascript" {
			t.Fatal("missing certificate must not prompt for administrator authorization")
		}
	}
}

func TestDarwinEnsureLoopbackHTTPSRedirectUsesSourceDirArgv(t *testing.T) {
	runner := &recordingRunner{}
	root := t.TempDir()
	privilege, err := helper.NewPlatformPrivilege(helper.PlatformPrivilegeOptions{Platform: "darwin", TempRoot: root, Runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	if err := privilege.EnsureLoopbackHTTPSRedirect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) == 0 {
		if runtime.GOOS == "darwin" {
			t.Skip("host already has the TunnelBoard pf anchor; skipping argv assertion")
		}
		t.Fatal("expected an osascript elevation")
	}
	if len(runner.calls) != 1 || runner.calls[0].executable != "/usr/bin/osascript" {
		t.Fatalf("calls = %+v", runner.calls)
	}
	args := runner.calls[0].args
	if len(args) != 7 || args[3] != "ensure-https-redirect" {
		t.Fatalf("osascript argv = %#v", args)
	}
	srcDir := args[4]
	if !strings.HasPrefix(srcDir, root) {
		t.Fatalf("payload dir %q must be under temp root %q", srcDir, root)
	}
	if strings.Contains(args[1], root) {
		t.Fatal("dynamic payload path must not be inlined into the AppleScript")
	}
}

func TestRepairDataDirOwnerRejectsUnsafeInputs(t *testing.T) {
	runner := &recordingRunner{}
	privilege, err := helper.NewPlatformPrivilege(helper.PlatformPrivilegeOptions{Platform: "darwin", TempRoot: t.TempDir(), Runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	if err := privilege.RepairDataDirOwner(context.Background(), "/tmp/TunnelBoard", "root; rm"); err == nil {
		t.Fatal("unsafe owner must be rejected")
	}
	if err := privilege.RepairDataDirOwner(context.Background(), "relative/TunnelBoard", "alice"); err == nil {
		t.Fatal("relative data dir must be rejected")
	}
	if len(runner.calls) != 0 {
		t.Fatalf("rejected repair must not elevate: %+v", runner.calls)
	}
}
