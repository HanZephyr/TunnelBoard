package diag_test

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HanZephyr/TunnelBoard/internal/diag"
)

// 滚动：写满后旧内容进入 .1，新文件从 0 开始。
func TestLogFileRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.log")
	lf, err := diag.OpenLogFile(path, 100)
	if err != nil {
		t.Fatalf("OpenLogFile: %v", err)
	}
	if _, err := lf.Write([]byte(strings.Repeat("a", 80) + "\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := lf.Write([]byte(strings.Repeat("b", 40) + "\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = lf.Close()

	rotated, err := os.ReadFile(path + ".1")
	if err != nil {
		t.Fatalf("rotated file missing: %v", err)
	}
	if !strings.HasPrefix(string(rotated), strings.Repeat("a", 80)) {
		t.Fatalf("rotated content wrong: %q", rotated[:20])
	}
	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read current: %v", err)
	}
	if !strings.HasPrefix(string(current), strings.Repeat("b", 40)) {
		t.Fatalf("current content wrong: %q", current)
	}
}

// Fanout 广播到全部 handler。
func TestFanoutBroadcasts(t *testing.T) {
	a, b := &captureHandler{}, &captureHandler{}
	f := diag.NewFanout(a, b)
	_ = f.Handle(context.Background(), slog.NewRecord(time.Now(), slog.LevelInfo, "x", 0))
	if len(a.records) != 1 || len(b.records) != 1 {
		t.Fatalf("fanout should reach all handlers: %d/%d", len(a.records), len(b.records))
	}
}
