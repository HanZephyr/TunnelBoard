package application

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HanZephyr/TunnelBoard/internal/biz"
)

func TestKeyFileLeaseHidesSourcePathAndConsumesBytesAfterSave(t *testing.T) {
	sourcePath := `/home/alice/.ssh/id_ed25519`
	secret := []byte("PRIVATE-KEY-SENTINEL")
	paths, views, err := makeKeyFileLease([]string{sourcePath}, map[string][]byte{sourcePath: secret})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(views)
	if strings.Contains(string(encoded), "home/alice") || strings.Contains(string(encoded), string(secret)) {
		t.Fatalf("key view leaked source data: %s", encoded)
	}
	if len(views) != 1 || views[0].Name != "id_ed25519" || views[0].ID == "" || views[0].Size != len(secret) {
		t.Fatalf("views = %+v", views)
	}

	stage := &stagedImport{
		token: "stage-token", expiresAt: time.Now().Add(time.Minute), committed: true,
		backup:   biz.StagedBackup{KeyFiles: map[string][]byte{sourcePath: append([]byte(nil), secret...)}},
		keyPaths: paths, keyViews: views, exporting: map[string]bool{},
	}
	service := &Service{importStage: stage}
	destination := filepath.Join(t.TempDir(), "saved-key")
	if err := service.SaveImportKeyFile(context.Background(), "stage-token", views[0].ID, destination); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(destination)
	if err != nil || string(content) != string(secret) {
		t.Fatalf("saved content=%q err=%v", content, err)
	}
	if service.importStage != nil {
		t.Fatal("last exported key must destroy the lease")
	}
	if err := service.SaveImportKeyFile(context.Background(), "stage-token", views[0].ID, destination); err == nil {
		t.Fatal("consumed key lease was accepted twice")
	}
}

func TestWritePrivateKeyAtomicReplacesExistingDestination(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(destination, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writePrivateKeyAtomic(context.Background(), destination, []byte("new")); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(destination)
	if err != nil || string(content) != "new" {
		t.Fatalf("content=%q err=%v", content, err)
	}
}
