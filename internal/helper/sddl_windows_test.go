//go:build windows

package helper

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pipeSDDL 必须同时授权 SYSTEM 与安装者 SID；空或含非法字符的 SID 拒绝。
func TestPipeSDDL(t *testing.T) {
	const sid = "S-1-5-21-1234567890-123456789-1234567890-1001"
	sddl, err := pipeSDDL(sid)
	if err != nil {
		t.Fatalf("pipeSDDL: %v", err)
	}
	if !strings.Contains(sddl, ";;;SY)") || !strings.HasSuffix(sddl, "(A;;GA;;;"+sid+")") {
		t.Fatalf("sddl = %q, want SYSTEM + owner grants", sddl)
	}
	for _, bad := range []string{"", "  ", "S-1-5 (bad)", "a\tb"} {
		if _, err := pipeSDDL(bad); err == nil {
			t.Fatalf("pipeSDDL(%q) should fail", bad)
		}
	}
}

// owner SID 落盘与回读（ProgramData 重定向到临时目录）。
func TestOwnerSIDRoundTrip(t *testing.T) {
	t.Setenv("ProgramData", t.TempDir())
	const sid = "S-1-5-21-1000"
	if err := writeOwnerSID(sid); err != nil {
		t.Fatalf("writeOwnerSID: %v", err)
	}
	got, err := readOwnerSID()
	if err != nil {
		t.Fatalf("readOwnerSID: %v", err)
	}
	if got != sid {
		t.Fatalf("readOwnerSID = %q, want %q", got, sid)
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("ProgramData"), "TunnelBoard", "helper-owner")); err != nil {
		t.Fatalf("owner file missing: %v", err)
	}
	if err := writeOwnerSID("  "); err == nil {
		t.Fatal("empty sid must be rejected")
	}
}

// elevatedInstallArgs 必须把整个 ArgumentList 作为带引号的字符串数组传递，
// 防止 PowerShell 把 -owner 解析为 Start-Process 的命名参数（线上复现的 NamedParameterNotFound）。
func TestElevatedInstallArgsQuotesArgumentList(t *testing.T) {
	args := elevatedInstallArgs(`C:\path with space\tunnelboard-helper.exe`, "S-1-5-21-1000")
	if len(args) != 5 || args[3] != "-Command" {
		t.Fatalf("args = %v", args)
	}
	ps := args[4]
	want := `Start-Process -Verb RunAs -Wait -FilePath 'C:\path with space\tunnelboard-helper.exe' -ArgumentList '-install','-owner','S-1-5-21-1000'`
	if ps != want {
		t.Fatalf("ps command =\n%s\nwant\n%s", ps, want)
	}
	// 单引号转义：路径含撇号时不得截断字符串
	esc := elevatedInstallArgs(`C:\o'brien\helper.exe`, "S-1-5")
	if !strings.Contains(esc[4], `o''brien`) {
		t.Fatalf("apostrophe must be doubled: %s", esc[4])
	}
}
