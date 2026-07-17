package vault_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/HanZephyr/TunnelBoard/internal/vault"
)

// ResolveDataDir 的重定向规则：config.root 有效时指向目标目录，否则一律回落隐式目录。
func TestResolveDataDirRedirection(t *testing.T) {
	writePointer := func(t *testing.T, anchor, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(anchor, "config.root"), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("无指针文件返回隐式目录", func(t *testing.T) {
		implicit := t.TempDir()
		if got := vault.ResolveDataDir(implicit); got != implicit {
			t.Fatalf("got %q, want implicit %q", got, implicit)
		}
	})

	t.Run("有效指针重定向到目标目录", func(t *testing.T) {
		implicit := t.TempDir()
		target := t.TempDir()
		writePointer(t, implicit, target+"\n")
		if got := vault.ResolveDataDir(implicit); got != target {
			t.Fatalf("got %q, want target %q", got, target)
		}
	})

	t.Run("指针内容为相对路径时回落", func(t *testing.T) {
		implicit := t.TempDir()
		writePointer(t, implicit, "relative/dir\n")
		if got := vault.ResolveDataDir(implicit); got != implicit {
			t.Fatalf("got %q, want implicit %q", got, implicit)
		}
	})

	t.Run("指针目标不存在时回落", func(t *testing.T) {
		implicit := t.TempDir()
		writePointer(t, implicit, filepath.Join(implicit, "no-such-dir")+"\n")
		if got := vault.ResolveDataDir(implicit); got != implicit {
			t.Fatalf("got %q, want implicit %q", got, implicit)
		}
	})

	t.Run("指针指向隐式目录自身时返回隐式目录", func(t *testing.T) {
		implicit := t.TempDir()
		writePointer(t, implicit, implicit+"\n")
		if got := vault.ResolveDataDir(implicit); got != implicit {
			t.Fatalf("got %q, want implicit %q", got, implicit)
		}
	})

	t.Run("空内容与空白输入回落", func(t *testing.T) {
		implicit := t.TempDir()
		writePointer(t, implicit, "\n")
		if got := vault.ResolveDataDir(implicit); got != implicit {
			t.Fatalf("got %q, want implicit %q", got, implicit)
		}
	})
}
