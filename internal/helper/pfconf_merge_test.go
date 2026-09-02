package helper

import (
	"strings"
	"testing"

	"github.com/HanZephyr/TunnelBoard/internal/loopbackhttps"
)

func TestMergePFConfInsertsAnchorOnce(t *testing.T) {
	first := mergePFConf(sampleApplePFConf)
	if !strings.Contains(first, `rdr-anchor "com.hanzephyr.tunnelboard"`) {
		t.Fatalf("merged pf.conf missing rdr-anchor:\n%s", first)
	}
	if !strings.Contains(first, `rdr-anchor "com.apple/*"`) {
		t.Fatal("merged pf.conf must keep the original Apple anchors")
	}
	second := mergePFConf(first)
	if first != second {
		t.Fatal("merging an already-annotated pf.conf must be idempotent")
	}
}

func TestDarwinPFAnchorTargetsUnprivilegedLoopbackPort(t *testing.T) {
	got := darwinPFAnchorContents()
	if !strings.Contains(got, "127.0.0.1 port 443 -> 127.0.0.1 port 17443") {
		t.Fatalf("anchor = %q", got)
	}
	if loopbackhttps.DarwinUnprivilegedPort != 17443 {
		t.Fatal("pf anchor port drifted from loopbackhttps.DarwinUnprivilegedPort")
	}
}
