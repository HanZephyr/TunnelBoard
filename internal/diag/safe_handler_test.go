package diag_test

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/HanZephyr/TunnelBoard/internal/diag"
)

func TestSafeLogHandlerSanitizesBeforeDownstreamAndRing(t *testing.T) {
	down := &captureHandler{}
	ring := diag.NewRingBuffer(down, 8)
	safe := diag.NewSafeLogHandler(ring)
	record := slog.NewRecord(time.Now(), slog.LevelError, "dial password=hunter2 Authorization: Bearer abc", 0)
	record.AddAttrs(
		slog.String("forward_id", "7"),
		slog.String("token", "secret-token"),
		slog.String("unexpected_payload", "must-not-pass"),
	)
	if err := safe.Handle(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	if len(down.records) != 1 {
		t.Fatalf("downstream records = %d", len(down.records))
	}
	for _, observed := range []string{down.records[0].Message, ring.Snapshot()[0].Message} {
		if strings.Contains(observed, "hunter2") || strings.Contains(observed, "Bearer abc") || strings.Contains(observed, "secret-token") || strings.Contains(observed, "must-not-pass") {
			t.Fatalf("unsafe sink content: %q", observed)
		}
	}
	attrs := map[string]string{}
	down.records[0].Attrs(func(attr slog.Attr) bool { attrs[attr.Key] = attr.Value.String(); return true })
	if attrs["token"] != "[REDACTED]" || attrs["forward_id"] != "7" {
		t.Fatalf("attrs = %+v", attrs)
	}
	if _, ok := attrs["unexpected_payload"]; ok {
		t.Fatalf("unknown attr must be dropped: %+v", attrs)
	}
}
