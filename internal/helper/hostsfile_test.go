package helper_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HanZephyr/TunnelBoard/internal/helper"
	"github.com/HanZephyr/TunnelBoard/internal/route"
)

func managedEntries() []route.HostEntry {
	return []route.HostEntry{
		{Domain: "app.test", IP: "127.0.0.1"},
		{Domain: "db.test", IP: "127.0.0.1"},
	}
}

// 无区块时在文末追加，原有内容字节不变。
func TestRenderManagedHostsAppendsBlock(t *testing.T) {
	content := "# base hosts\r\n\r\n127.0.0.1 localhost\r\n"
	got := helper.RenderManagedHosts(content, managedEntries())
	want := content +
		helper.BlockBegin + "\r\n" +
		"127.0.0.1 app.test\r\n" +
		"127.0.0.1 db.test\r\n" +
		helper.BlockEnd + "\r\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// 替换首个标记区块，区块外内容（含其他注释行）字节不变，条目按传入顺序输出。
func TestRenderManagedHostsReplacesFirstBlock(t *testing.T) {
	content := "before\r\n# keep me\r\n" +
		helper.BlockBegin + "\r\n127.0.0.1 old.test\r\n" + helper.BlockEnd + "\r\n" +
		"after\r\n"
	got := helper.RenderManagedHosts(content, []route.HostEntry{
		{Domain: "zeta.test", IP: "127.0.0.1"},
		{Domain: "alpha.test", IP: "127.0.0.1"},
	})
	want := "before\r\n# keep me\r\n" +
		helper.BlockBegin + "\r\n127.0.0.1 zeta.test\r\n127.0.0.1 alpha.test\r\n" + helper.BlockEnd + "\r\n" +
		"after\r\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// entries 为空时移除整个区块（含标记），其余内容原样；本来无区块则内容完全不变。
func TestRenderManagedHostsRemovesBlock(t *testing.T) {
	content := "before\r\n" +
		helper.BlockBegin + "\r\n127.0.0.1 db.test\r\n" + helper.BlockEnd + "\r\n" +
		"after\r\n"
	if got, want := helper.RenderManagedHosts(content, nil), "before\r\nafter\r\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got := helper.RenderManagedHosts("plain\r\n", nil); got != "plain\r\n" {
		t.Fatalf("no-block content changed: %q", got)
	}
}

// 空内容 + 空 entries → 空输出；空内容 + entries → 仅区块，结尾恰好一个换行。
func TestRenderManagedHostsEmptyContent(t *testing.T) {
	if got := helper.RenderManagedHosts("", nil); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
	got := helper.RenderManagedHosts("", managedEntries())
	want := helper.BlockBegin + "\r\n127.0.0.1 app.test\r\n127.0.0.1 db.test\r\n" + helper.BlockEnd + "\r\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// LF 输入归一为 CRLF 输出，不出现孤立 \n；区块外行内容保持。
func TestRenderManagedHostsNormalizesLFToCRLF(t *testing.T) {
	content := "# note\n10.0.0.1 legacy\n"
	got := helper.RenderManagedHosts(content, managedEntries())
	if strings.Contains(strings.ReplaceAll(got, "\r\n", ""), "\n") {
		t.Fatalf("output contains bare LF: %q", got)
	}
	if !strings.HasPrefix(got, "# note\r\n10.0.0.1 legacy\r\n") {
		t.Fatalf("outside content not preserved: %q", got)
	}
}

// 无尾换行的内容追加区块时先补换行，结尾保证恰好一个换行。
func TestRenderManagedHostsMissingTrailingNewline(t *testing.T) {
	got := helper.RenderManagedHosts("127.0.0.1 localhost", managedEntries())
	want := "127.0.0.1 localhost\r\n" +
		helper.BlockBegin + "\r\n127.0.0.1 app.test\r\n127.0.0.1 db.test\r\n" + helper.BlockEnd + "\r\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// 写入后可读出渲染结果；.bak 保存原始字节；临时文件已 rename 走。
func TestWriteManagedHostsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hosts")
	original := "# original content\r\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := helper.WriteManagedHosts(path, managedEntries()); err != nil {
		t.Fatalf("WriteManagedHosts: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := helper.RenderManagedHosts(original, managedEntries()); string(data) != want {
		t.Fatalf("hosts = %q, want %q", data, want)
	}
	bak, err := os.ReadFile(path + ".tunnelboard.bak")
	if err != nil {
		t.Fatalf("backup missing: %v", err)
	}
	if string(bak) != original {
		t.Fatalf("backup = %q, want original %q", bak, original)
	}
	if _, err := os.Stat(path + ".tunnelboard.tmp"); !os.IsNotExist(err) {
		t.Fatalf("temp file should be renamed away, stat err = %v", err)
	}
}

// 文件不存在时按空内容处理并创建，.bak 为空。
func TestWriteManagedHostsCreatesMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hosts")
	if err := helper.WriteManagedHosts(path, managedEntries()); err != nil {
		t.Fatalf("WriteManagedHosts: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := helper.RenderManagedHosts("", managedEntries()); string(data) != want {
		t.Fatalf("hosts = %q, want %q", data, want)
	}
	bak, err := os.ReadFile(path + ".tunnelboard.bak")
	if err != nil || len(bak) != 0 {
		t.Fatalf("backup = %q, err = %v, want empty backup", bak, err)
	}
}

// 读取失败（路径是目录）返回错误，且不产生 .bak。
func TestWriteManagedHostsReadFailure(t *testing.T) {
	dir := t.TempDir()
	if err := helper.WriteManagedHosts(dir, managedEntries()); err == nil {
		t.Fatal("want error for directory path")
	}
	if _, err := os.Stat(dir + ".tunnelboard.bak"); !os.IsNotExist(err) {
		t.Fatalf("backup should not exist, stat err = %v", err)
	}
}

// 临时文件写入失败时从 .bak 回滚，原文件内容不变。
func TestWriteManagedHostsRollbackOnFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hosts")
	original := "# precious\r\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	// 预先占住 .tmp 路径为目录，使临时文件写入失败。
	if err := os.Mkdir(path+".tunnelboard.tmp", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := helper.WriteManagedHosts(path, managedEntries()); err == nil {
		t.Fatal("want error when temp path is blocked")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != original {
		t.Fatalf("hosts = %q, want original %q after rollback", data, original)
	}
}

// ParseManagedHosts 回读受托管区块：用于应用前快照与失败回滚。
func TestParseManagedHosts(t *testing.T) {
	content := "# comment\r\n127.0.0.1 localhost\r\n" +
		helper.BlockBegin + "\r\n127.0.0.1 db.test\r\n127.0.0.1 grafana.example.com\r\n" + helper.BlockEnd + "\r\n"
	entries := helper.ParseManagedHosts(content)
	if len(entries) != 2 || entries[0].Domain != "db.test" || entries[1].Domain != "grafana.example.com" {
		t.Fatalf("entries = %+v", entries)
	}
	if got := helper.ParseManagedHosts("# nothing\r\n"); got != nil {
		t.Fatalf("no block should return nil, got %+v", got)
	}
	empty := helper.BlockBegin + "\r\n" + helper.BlockEnd + "\r\n"
	if got := helper.ParseManagedHosts(empty); len(got) != 0 {
		t.Fatalf("empty block should return no entries, got %+v", got)
	}
}
