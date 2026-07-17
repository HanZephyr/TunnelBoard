package conf

import (
	"path/filepath"
	"testing"
)

func TestUpdateCheckDefaultsToEnabledForExistingConfig(t *testing.T) {
	cfg, err := ParseConfigTOML([]byte("version = 1\nauto_run = false\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.UpdateCheckEnabled {
		t.Fatal("update checks should default to enabled when the field is absent")
	}
}

func TestUpdateCheckCanBeDisabledAndPersisted(t *testing.T) {
	storage, err := NewStorage(filepath.Join(t.TempDir(), "config.toml"))
	if err != nil {
		t.Fatal(err)
	}

	_, err = storage.Update(func(cfg *Config) error {
		cfg.UpdateCheckEnabled = false
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := storage.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UpdateCheckEnabled {
		t.Fatal("disabled update checks should remain disabled after reload")
	}
}
