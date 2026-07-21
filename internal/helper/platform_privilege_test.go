package helper_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/HanZephyr/TunnelBoard/internal/helper"
)

type recordedCommand struct {
	executable string
	args       []string
}

type recordingRunner struct {
	calls  []recordedCommand
	failAt int
}

func (r *recordingRunner) Run(_ context.Context, executable string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, recordedCommand{executable: executable, args: append([]string(nil), args...)})
	if r.failAt == len(r.calls) {
		return []byte("denied"), errors.New("command failed")
	}
	return nil, nil
}

func TestLinuxPrivilegePassesMaliciousLookingPathAsOneArgument(t *testing.T) {
	root := filepath.Join(t.TempDir(), "space ; $() ` quote'")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	runner := &recordingRunner{}
	privilege, err := helper.NewPlatformPrivilege(helper.PlatformPrivilegeOptions{
		Platform: "linux", TempRoot: root, Runner: runner,
	})
	if err != nil {
		t.Fatal(err)
	}
	der := makeSelfSignedCA(t, "TunnelBoard Local CA")
	if err := privilege.TrustLocalCA(context.Background(), der); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("calls = %+v, want copy + refresh", runner.calls)
	}
	copyCall := runner.calls[0]
	if copyCall.executable != "/usr/bin/pkexec" || !reflect.DeepEqual(copyCall.args[:3], []string{"/bin/cp", "--", copyCall.args[2]}) {
		t.Fatalf("copy call = %+v, want fixed pkexec /bin/cp --", copyCall)
	}
	if len(copyCall.args) != 4 || !strings.Contains(copyCall.args[2], "space ; $()") || copyCall.args[3] != "/usr/local/share/ca-certificates/tunnelboard-local-ca.crt" {
		t.Fatalf("dynamic path must remain one argv element: %+v", copyCall)
	}
	if runner.calls[1].executable != "/usr/bin/pkexec" || !reflect.DeepEqual(runner.calls[1].args, []string{"/usr/sbin/update-ca-certificates"}) {
		t.Fatalf("refresh call = %+v", runner.calls[1])
	}
}

func TestLinuxPrivilegeCompensatesFailedTrustRefresh(t *testing.T) {
	runner := &recordingRunner{failAt: 2}
	privilege, err := helper.NewPlatformPrivilege(helper.PlatformPrivilegeOptions{Platform: "linux", TempRoot: t.TempDir(), Runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	err = privilege.TrustLocalCA(context.Background(), makeSelfSignedCA(t, "TunnelBoard Local CA"))
	if err == nil || len(runner.calls) != 4 {
		t.Fatalf("err/calls = %v/%+v, want refresh error plus remove+refresh compensation", err, runner.calls)
	}
	if !reflect.DeepEqual(runner.calls[2].args, []string{"/bin/rm", "-f", "--", "/usr/local/share/ca-certificates/tunnelboard-local-ca.crt"}) {
		t.Fatalf("remove compensation = %+v", runner.calls[2])
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
