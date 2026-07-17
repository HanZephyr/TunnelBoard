package diag_test

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/HanZephyr/TunnelBoard/internal/diag"
)

type captureHandler struct{ records []slog.Record }

func (h *captureHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	h.records = append(h.records, r)
	return nil
}
func (h *captureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(string) slog.Handler      { return h }

// 缓冲写满后按环形覆盖，Snapshot 始终保持时间序。
func TestRingBufferWrapsAndKeepsOrder(t *testing.T) {
	down := &captureHandler{}
	buf := diag.NewRingBuffer(down, 3)
	ctx := context.Background()
	for i, msg := range []string{"m1", "m2", "m3", "m4", "m5"} {
		_ = buf.Handle(ctx, slog.NewRecord(time.Now().Add(time.Duration(i)*time.Second), slog.LevelInfo, msg, 0))
	}
	if len(down.records) != 5 {
		t.Fatalf("downstream must see all records, got %d", len(down.records))
	}
	snap := buf.Snapshot()
	if len(snap) != 3 {
		t.Fatalf("snapshot len = %d, want 3", len(snap))
	}
	if snap[0].Message != "m3" || snap[2].Message != "m5" {
		t.Fatalf("order wrong: %+v", snap)
	}
}

// WithAttrs 派生 handler 共享同一缓冲（无数据竞争，条目汇聚）。
func TestWithAttrsSharesBuffer(t *testing.T) {
	buf := diag.NewRingBuffer(&captureHandler{}, 8)
	child := buf.WithAttrs([]slog.Attr{slog.String("scope", "x")})
	logger := slog.New(child)
	logger.Info("from-child")
	buf.Handle(context.Background(), slog.NewRecord(time.Now(), slog.LevelInfo, "from-parent", 0))
	if got := len(buf.Snapshot()); got != 2 {
		t.Fatalf("shared buffer should have 2 entries, got %d", got)
	}
}

// 脱敏：秘密键值遮蔽，用户目录归一。
func TestSanitize(t *testing.T) {
	cases := map[string]string{
		`dial failed password=hunter2`:               `dial failed password=***`,
		`passphrase=abc123 end`:                      `passphrase=*** end`,
		`read C:\Users\alice\.ssh\id_ed25519 failed`: `read ~\.ssh\id_ed25519 failed`,
		`open /home/bob/.ssh/config`:                 `open ~/.ssh/config`,
		`nothing sensitive`:                          `nothing sensitive`,
	}
	for in, want := range cases {
		if got := diag.Sanitize(in); got != want {
			t.Errorf("Sanitize(%q) = %q, want %q", in, got, want)
		}
	}
}

// BuildBundle 汇总版本平台与脱敏日志。
func TestBuildBundle(t *testing.T) {
	buf := diag.NewRingBuffer(&captureHandler{}, 8)
	_ = buf.Handle(context.Background(), slog.NewRecord(time.Now(), slog.LevelWarn, "auth password=zzz failed", 0))
	bundle := buf.BuildBundle("1.2.3", "windows/amd64", map[string]interface{}{"forwards": 2})
	if bundle.AppVersion != "1.2.3" || bundle.Platform != "windows/amd64" {
		t.Fatalf("meta wrong: %+v", bundle)
	}
	if len(bundle.Logs) != 1 || !strings.Contains(bundle.Logs[0].Message, "password=***") {
		t.Fatalf("logs not sanitized: %+v", bundle.Logs)
	}
	if bundle.Summary["forwards"] != 2 {
		t.Fatalf("summary wrong: %+v", bundle.Summary)
	}
}
