package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFrontendDoesNotBootstrapTelemetry(t *testing.T) {
	for _, path := range []string{"frontend/index.html", "web/docs/.vitepress/config.mts"} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"googletagmanager.com", "gtag("} {
			if strings.Contains(string(data), forbidden) {
				t.Fatalf("%s contains telemetry marker %q", path, forbidden)
			}
		}
	}

	if _, err := os.Stat("frontend/src/utils/analytics.js"); !os.IsNotExist(err) {
		t.Fatalf("analytics module must be removed, stat error = %v", err)
	}
}

func TestOnlyUpdaterOwnsAnHTTPClient(t *testing.T) {
	err := filepath.WalkDir("internal", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(data), `"net/http"`) && filepath.ToSlash(filepath.Dir(path)) != "internal/updater" {
			t.Errorf("non-update HTTP client found in %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{
		"frontend/src/utils/backend-api.js",
		"frontend/.env.development",
		"frontend/.env.production",
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("commercial backend artifact %s must be removed, stat error = %v", path, err)
		}
	}
}

func TestCommercialAndAIDebugModulesStayRemoved(t *testing.T) {
	for _, path := range []string{
		"internal/aidebug/service.go",
		"internal/device/machine_id.go",
		"internal/license/client.go",
		"internal/model/ai_debug.go",
		"internal/model/license.go",
		"frontend/src/config/features.js",
		"frontend/src/components/common/AIDebugModal.vue",
		"frontend/src/components/common/AIDebugResultCard.vue",
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("removed module %s exists, stat error = %v", path, err)
		}
	}
}

func TestNewAppDoesNotCreatePersistentLog(t *testing.T) {
	configRoot := t.TempDir()
	t.Setenv("AppData", configRoot)
	t.Setenv("XDG_CONFIG_HOME", configRoot)
	t.Setenv("HOME", configRoot)
	t.Setenv("USERPROFILE", configRoot)

	app := NewApp()
	if app.initErr != nil {
		t.Fatal(app.initErr)
	}
	logPath := filepath.Join(configRoot, "TunnelBoard", "tunnelboard.log")
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatalf("persistent runtime log must not be created, stat error = %v", err)
	}
}
