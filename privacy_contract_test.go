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

// httpClientAllowedDirs 是允许 import net/http 的包：updater 负责唯一的出站请求（GitHub 更新检查）；
// caddy 仅访问 127.0.0.1:2019 的本地 admin API（回环 IPC，非网络出站）。
var httpClientAllowedDirs = map[string]bool{
	"internal/updater": true,
	"internal/caddy":   true,
}

func TestHTTPClientRestrictedToAllowlist(t *testing.T) {
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
		if strings.Contains(string(data), `"net/http"`) && !httpClientAllowedDirs[filepath.ToSlash(filepath.Dir(path))] {
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

// 运行日志按用户决策持久化到数据目录 logs/（滚动截断），且只写在该目录内。
func TestNewAppCreatesLogOnlyInsideDataDir(t *testing.T) {
	configRoot := t.TempDir()
	t.Setenv("AppData", configRoot)
	t.Setenv("XDG_CONFIG_HOME", configRoot)
	t.Setenv("HOME", configRoot)
	t.Setenv("USERPROFILE", configRoot)

	app := NewApp()
	if app.initErr != nil {
		t.Fatal(app.initErr)
	}
	if app.logStore != nil {
		t.Cleanup(func() { _ = app.logStore.Close() })
	}
	logPath := filepath.Join(configRoot, "TunnelBoard", "logs", "tunnelboard.log")
	if _, err := os.Stat(logPath); err != nil {
		t.Fatalf("runtime log should be created inside data dir logs/: %v", err)
	}
}
