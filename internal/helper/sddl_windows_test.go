//go:build windows

package helper

import (
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
	if !strings.Contains(sddl, ";;;SY)") || !strings.Contains(sddl, ";;;BA)") || !strings.HasSuffix(sddl, "(A;;GA;;;"+sid+")") {
		t.Fatalf("sddl = %q, want SYSTEM + Administrators + owner grants", sddl)
	}
	for _, bad := range []string{"", "  ", "S-1-5 (bad)", "a\tb"} {
		if _, err := pipeSDDL(bad); err == nil {
			t.Fatalf("pipeSDDL(%q) should fail", bad)
		}
	}
}
