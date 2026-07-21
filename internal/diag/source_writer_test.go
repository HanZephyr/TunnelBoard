package diag_test

import (
	"bytes"
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

func TestSourceWriterBoundsLineWithoutNewline(t *testing.T) {
	store, err := diag.NewLogStore(filepath.Join(t.TempDir(), "logs"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	writer := diag.NewSourceWriter(store, diag.LogCaddy)
	for range 8 {
		if _, err := writer.Write(bytes.Repeat([]byte{'x'}, 32<<10)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := writer.Write([]byte("\n")); err != nil {
		t.Fatal(err)
	}
	result, err := store.Tail(diag.LogCaddy, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Lines) != 1 || len(result.Lines[0]) > 64<<10 || !bytes.Contains([]byte(result.Lines[0]), []byte("[truncated")) {
		t.Fatalf("bounded line = %#v", result.Lines)
	}
}
