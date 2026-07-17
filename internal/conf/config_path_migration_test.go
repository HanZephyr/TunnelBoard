package conf

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveConfigPath_UsesSystemAppData(t *testing.T) {
	configRoot := t.TempDir()
	setFakeUserConfigDir(t, configRoot)

	for _, devserver := range []string{"", "1"} {
		t.Run("devserver="+devserver, func(t *testing.T) {
			t.Setenv("devserver", devserver)
			path := ResolveConfigPath()
			want := filepath.Join(configRoot, "TunnelBoard", defaultConfigPath)
			if canonicalPath(path) != canonicalPath(want) {
				t.Fatalf("ResolveConfigPath() = %q, want %q", path, want)
			}
		})
	}
}

func TestNewDefaultStorage_DoesNotImportCurrentDirectoryConfig(t *testing.T) {
	configRoot := t.TempDir()
	setFakeUserConfigDir(t, configRoot)

	cwd := t.TempDir()
	setWorkingDir(t, cwd)
	localPath := filepath.Join(cwd, defaultConfigPath)
	localContent := []byte("version = 99\n")
	if err := os.WriteFile(localPath, localContent, 0o644); err != nil {
		t.Fatal(err)
	}

	storage, err := NewDefaultStorage()
	if err != nil {
		t.Fatal(err)
	}
	wantPath := filepath.Join(configRoot, "TunnelBoard", defaultConfigPath)
	if canonicalPath(storage.Path()) != canonicalPath(wantPath) {
		t.Fatalf("storage.Path() = %q, want %q", storage.Path(), wantPath)
	}
	if _, err := storage.Load(); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(localContent) {
		t.Fatalf("current-directory config was modified: got %q", got)
	}
}

func setWorkingDir(t *testing.T, dir string) {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd failed: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir %q failed: %v", dir, err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
}

func setFakeHome(t *testing.T, home string) {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOMEDRIVE", "")
	t.Setenv("HOMEPATH", "")
}

func setFakeUserConfigDir(t *testing.T, root string) {
	t.Helper()
	t.Setenv("AppData", root)
	t.Setenv("XDG_CONFIG_HOME", root)
	setFakeHome(t, root)
}

func canonicalPath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return filepath.Clean(p)
	}
	dir := filepath.Dir(abs)
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(filepath.Join(realDir, filepath.Base(abs)))
}
