package helper_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HanZephyr/TunnelBoard/internal/helper"
)

type recordedCommand struct {
	executable string
	args       []string
}

type recordingRunner struct {
	calls []recordedCommand
}

func (r *recordingRunner) Run(_ context.Context, executable string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, recordedCommand{executable: executable, args: append([]string(nil), args...)})
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
}
