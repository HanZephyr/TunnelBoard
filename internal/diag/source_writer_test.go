package diag_test

import (
	"path/filepath"
	"testing"

	"github.com/HanZephyr/TunnelBoard/internal/diag"
)

func TestSourceWriterSplitsAndRedactsProcessLines(t *testing.T) {
	store, err := diag.NewLogStore(filepath.Join(t.TempDir(), "logs"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	writer := diag.NewSourceWriter(store, diag.LogCaddy)
	if _, err := writer.Write([]byte("first password=hunter2\nsecond\n")); err != nil {
		t.Fatal(err)
	}
	result, err := store.Tail(diag.LogCaddy, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Lines) != 2 || result.Lines[0] == "first password=hunter2" {
		t.Fatalf("lines = %#v", result.Lines)
	}
}
