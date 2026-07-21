package diag

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testLogStore(t *testing.T, limits logStoreLimits) *logStore {
	t.Helper()
	store, err := newLogStore(t.TempDir(), limits)
	if err != nil {
		t.Fatalf("newLogStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestLogStoreKeepsThreeArchivesAndAdvancesGeneration(t *testing.T) {
	store := testLogStore(t, logStoreLimits{
		fileBytes: 32, archives: 3, lineBytes: 64, queueBytes: 1024,
		tailBytes: 256, tailLines: 500, closeTimeout: time.Second,
	})

	first, err := store.Tail(LogTunnelBoard, nil)
	if err != nil {
		t.Fatal(err)
	}
	oldCursor := first.NextCursor
	for _, line := range []string{"111111111111111", "222222222222222", "333333333333333", "444444444444444", "555555555555555", "666666666666666", "777777777777777", "888888888888888"} {
		store.Append(LogTunnelBoard, []byte(line))
	}
	result, err := store.Tail(LogTunnelBoard, &oldCursor)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Rotated || result.NextCursor.Generation <= oldCursor.Generation {
		t.Fatalf("rotation result = %+v, old cursor = %+v", result, oldCursor)
	}

	root := store.root
	for _, suffix := range []string{"", ".1", ".2", ".3"} {
		if _, err := os.Stat(filepath.Join(root, "tunnelboard.log") + suffix); err != nil {
			t.Fatalf("archive %q missing: %v", suffix, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "tunnelboard.log.4")); !os.IsNotExist(err) {
		t.Fatalf("fourth archive must not exist: %v", err)
	}
}

func TestLogStoreTailIsBoundedAndUsesLatestCompleteLines(t *testing.T) {
	store := testLogStore(t, logStoreLimits{
		fileBytes: 1024, archives: 3, lineBytes: 64, queueBytes: 1024,
		tailBytes: 20, tailLines: 2, closeTimeout: time.Second,
	})
	for _, line := range []string{"line-1", "line-2", "line-3", "line-4", "line-5"} {
		store.Append(LogCaddy, []byte(line))
	}
	result, err := store.Tail(LogCaddy, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Truncated || result.DroppedBytes <= 0 {
		t.Fatalf("tail should report skipped backlog: %+v", result)
	}
	if got := strings.Join(result.Lines, ","); got != "line-4,line-5" {
		t.Fatalf("lines = %q, want latest two complete lines", got)
	}
	if result.NextCursor.Offset != int64(len("line-1\nline-2\nline-3\nline-4\nline-5\n")) {
		t.Fatalf("next cursor = %+v", result.NextCursor)
	}
}

func TestLogStoreTruncatesAndSanitizesBeforeEverySink(t *testing.T) {
	store := testLogStore(t, logStoreLimits{
		fileBytes: 1024, archives: 3, lineBytes: 48, queueBytes: 1024,
		tailBytes: 256, tailLines: 500, closeTimeout: time.Second,
	})
	secret := "password=hunter2 Authorization: Bearer abc token=xyz " + strings.Repeat("x", 100)
	store.Append(LogTunnelBoard, []byte(secret))
	result, err := store.Tail(LogTunnelBoard, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Lines) != 1 || len(result.Lines[0]) > 48 || !strings.Contains(result.Lines[0], "[truncated]") {
		t.Fatalf("bounded line = %#v", result.Lines)
	}
	joined := strings.Join(result.Lines, "\n")
	if strings.Contains(joined, "hunter2") || strings.Contains(joined, "Bearer abc") || strings.Contains(joined, "xyz") {
		t.Fatalf("tail leaked secret: %q", joined)
	}
	if !strings.Contains(joined, "[REDACTED]") {
		t.Fatalf("tail must show redaction: %q", joined)
	}
	if err := store.CloseSource(LogTunnelBoard); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(store.root, "tunnelboard.log"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "hunter2") || strings.Contains(string(raw), "Bearer abc") || strings.Contains(string(raw), "xyz") {
		t.Fatalf("disk leaked secret: %q", raw)
	}
}

func TestLogStoreRejectsUnknownSource(t *testing.T) {
	store := testLogStore(t, defaultLogStoreLimits())
	store.Append(LogSource("../../other"), []byte("ignored"))
	if _, err := store.Tail(LogSource("../../other"), nil); err == nil {
		t.Fatal("unknown source must be rejected")
	}
}

func TestLogStoreDropsWithoutBackpressureAndReportsAggregate(t *testing.T) {
	store := testLogStore(t, logStoreLimits{
		fileBytes: 1024, archives: 3, lineBytes: 64, queueBytes: 24,
		tailBytes: 256, tailLines: 500, closeTimeout: time.Second,
	})
	state := store.sources[LogCaddy]
	started := make(chan struct{})
	release := make(chan struct{})
	state.writeHook = func() {
		select {
		case <-started:
		default:
			close(started)
		}
		<-release
	}
	store.Append(LogCaddy, []byte("blocking"))
	<-started
	for i := 0; i < 100; i++ {
		store.Append(LogCaddy, []byte("overflow"))
	}
	close(release)

	result, err := store.Tail(LogCaddy, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.DroppedBytes <= 0 {
		t.Fatalf("queue loss must be reported: %+v", result)
	}
	if !strings.Contains(strings.Join(result.Lines, "\n"), "dropped ") {
		t.Fatalf("aggregate marker missing: %+v", result.Lines)
	}
}
