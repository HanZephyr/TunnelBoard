package biz_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/HanZephyr/TunnelBoard/internal/biz"
	"github.com/HanZephyr/TunnelBoard/internal/model"
	"github.com/HanZephyr/TunnelBoard/internal/vault"
)

var backupTestKDF = vault.BackupKDF{Time: 2, Memory: 32 * 1024, Parallelism: 1}

// makeBackup 构造一个备份包：1 个顶层文件夹（含 1 个第二层文件夹）、1 主机、2 Forward（其一在第二层）、
// 1 条 hosts+caddy 都启用的 Route、1 条指纹。
func makeBackup(t *testing.T) []byte {
	t.Helper()
	data := model.VaultData{
		Version: 1,
		Folders: []model.Folder{
			{ID: 1, Name: "工作"},
			{ID: 2, Name: "生产", ParentID: 1},
		},
		SSHHosts: []model.SSHHost{
			{ID: 1, Name: "跳板", Host: "10.0.0.1", Port: 22, User: "ops", AuthType: "password", Password: "s3cr3t"},
		},
		Forwards: []model.Forward{
			{ID: 1, FolderID: 1, Name: "db", Mode: "local", ChainHostIDs: []int{1}, LocalHost: "127.0.0.1", LocalPort: 5432, RemoteHost: "db", RemotePort: 5432},
			{ID: 2, FolderID: 2, Name: "redis", Mode: "local", ChainHostIDs: []int{1}, LocalHost: "127.0.0.1", LocalPort: 6379, RemoteHost: "redis", RemotePort: 6379},
		},
		WebRoutes: []model.WebRoute{
			{ID: 1, ForwardID: 1, Domain: "db.test", HostsEnabled: true, CaddyEnabled: true, UpstreamScheme: "http"},
		},
		HostKeys: []model.HostKey{
			{ID: 1, Host: "10.0.0.1", Port: 22, KeyType: "ssh-ed25519", FingerprintSHA256: "SHA256:abc"},
		},
	}
	raw, err := vault.ExportBackup(data, map[string][]byte{"C:/keys/id_ed25519": []byte("KEY")}, "pw", backupTestKDF)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func seedExisting(t *testing.T, fs *fakeStore) {
	t.Helper()
	c := biz.NewCatalogBiz(fs)
	folder, err := c.CreateFolder("现有", 0)
	if err != nil {
		t.Fatal(err)
	}
	host, err := c.SaveSSHHost(model.SSHHost{Name: "现有主机", Host: "10.0.0.1", Port: 22, User: "ops", AuthType: "password", Password: "old"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.SaveForward(model.Forward{FolderID: folder.ID, Name: "现有转发", Mode: "local", ChainHostIDs: []int{host.ID},
		LocalHost: "127.0.0.1", LocalPort: 8080, RemoteHost: "x", RemotePort: 80}); err != nil {
		t.Fatal(err)
	}
}

// CreateBackup 包含私钥文件选项：可读文件打包并提示，不可读文件降级为警告。
func TestCreateBackupKeyFiles(t *testing.T) {
	fs := &fakeStore{data: model.VaultData{Version: 1}}
	seedExisting(t, fs)
	c := biz.NewCatalogBiz(fs)
	if _, err := c.SaveSSHHost(model.SSHHost{Name: "带钥", Host: "10.0.0.9", User: "ops", AuthType: "ssh_key", KeyPath: "C:/keys/good"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SaveSSHHost(model.SSHHost{Name: "坏钥", Host: "10.0.0.10", User: "ops", AuthType: "ssh_key", KeyPath: "C:/keys/missing"}); err != nil {
		t.Fatal(err)
	}

	b := biz.NewBackupBiz(fs)
	b.SetReadFile(func(path string) ([]byte, error) {
		if path == "C:/keys/good" {
			return []byte("KEY MATERIAL"), nil
		}
		return nil, errors.New("no such file")
	})

	raw, warnings, err := b.CreateBackup("pw", true)
	if err != nil {
		t.Fatalf("CreateBackup: %v", err)
	}
	joined := strings.Join(warnings, "\n")
	if !strings.Contains(joined, "C:/keys/good") || !strings.Contains(joined, "C:/keys/missing") {
		t.Fatalf("warnings should cover both files: %v", warnings)
	}
	_, keyFiles, err := vault.ParseBackup(raw, "pw")
	if err != nil {
		t.Fatalf("ParseBackup: %v", err)
	}
	if string(keyFiles["C:/keys/good"]) != "KEY MATERIAL" {
		t.Fatalf("key file not packaged: %+v", keyFiles)
	}
}

// PreviewImport 报告实体计数、同 endpoint 主机冲突与私钥文件清单。
func TestPreviewImportConflicts(t *testing.T) {
	fs := &fakeStore{data: model.VaultData{Version: 1}}
	seedExisting(t, fs)
	b := biz.NewBackupBiz(fs)

	preview, err := b.PreviewImport(makeBackup(t), "pw")
	if err != nil {
		t.Fatalf("PreviewImport: %v", err)
	}
	if preview.Counts["folders"] != 2 || preview.Counts["sshHosts"] != 1 || preview.Counts["forwards"] != 2 {
		t.Fatalf("counts wrong: %+v", preview.Counts)
	}
	if len(preview.HostConflicts) != 1 || preview.HostConflicts[0].Imported.Name != "跳板" {
		t.Fatalf("conflicts = %+v", preview.HostConflicts)
	}
	if len(preview.KeyFiles) != 1 || preview.KeyFiles[0] != "C:/keys/id_ed25519" {
		t.Fatalf("keyFiles = %+v", preview.KeyFiles)
	}
	if !strings.HasPrefix(preview.FolderName, "导入备份 ") {
		t.Fatalf("folderName = %q", preview.FolderName)
	}
	raw, _ := json.Marshal(preview)
	if strings.Contains(string(raw), "s3cr3t") || strings.Contains(string(raw), "\"password\":") {
		t.Fatalf("import preview leaked a saved secret: %s", raw)
	}
}

// 追加导入：包装到新顶层文件夹、ID 重映射、第二层压平、Route 开关强制关闭、指纹去重、现有数据不动。
func TestApplyImportAppend(t *testing.T) {
	fs := &fakeStore{data: model.VaultData{Version: 1}}
	seedExisting(t, fs)
	b := biz.NewBackupBiz(fs)

	summary, err := b.ApplyImport(makeBackup(t), "pw", biz.ImportPlan{FolderName: "来自旧机"})
	if err != nil {
		t.Fatalf("ApplyImport: %v", err)
	}
	if summary.FlattenedFolders != 1 || summary.RoutesDeactivated != 1 || summary.SkippedHosts != 0 {
		t.Fatalf("summary = %+v", summary)
	}
	if summary.Imported["folders"] != 1 || summary.Imported["forwards"] != 2 || summary.Imported["webRoutes"] != 1 {
		t.Fatalf("imported counts = %+v", summary.Imported)
	}

	data, _ := fs.Load()
	// 现有 1 文件夹 + wrapper + 导入顶层 = 3；第二层被压平
	if len(data.Folders) != 3 {
		t.Fatalf("folders = %+v", data.Folders)
	}
	var wrapper, importedTop *model.Folder
	for i := range data.Folders {
		if data.Folders[i].Name == "来自旧机" {
			wrapper = &data.Folders[i]
		}
		if data.Folders[i].Name == "工作" {
			importedTop = &data.Folders[i]
		}
	}
	if wrapper == nil || importedTop == nil || importedTop.ParentID != wrapper.ID {
		t.Fatalf("wrapper structure wrong: %+v", data.Folders)
	}
	// 两个 Forward 都应落在导入顶层文件夹（redis 从第二层并入父）
	for _, fw := range data.Forwards {
		if fw.Name == "redis" && fw.FolderID != importedTop.ID {
			t.Fatalf("flattened forward should land in parent: %+v", fw)
		}
		if fw.Name == "db" && fw.FolderID != importedTop.ID {
			t.Fatalf("forward folder remap wrong: %+v", fw)
		}
	}
	// 主机冲突默认 rename 导入（未给 skip 决定）
	foundRenamed := false
	for _, h := range data.SSHHosts {
		if h.Name == "跳板-imported" {
			foundRenamed = true
		}
	}
	if !foundRenamed {
		t.Fatalf("conflicting host should be renamed: %+v", data.SSHHosts)
	}
	// Route 开关强制关闭
	for _, r := range data.WebRoutes {
		if r.HostsEnabled || r.CaddyEnabled {
			t.Fatalf("route switches must be forced off: %+v", r)
		}
	}
	// 指纹导入（本机无该端点）
	if len(data.HostKeys) != 1 {
		t.Fatalf("hostKeys = %+v", data.HostKeys)
	}
	// 现有数据保留
	if data.SSHHosts[0].Name != "现有主机" || data.Forwards[0].Name != "现有转发" {
		t.Fatalf("existing data must be preserved: %+v", data)
	}
}

// 冲突主机选择 skip：其 Forward 链变空，整条不导入。
func TestApplyImportSkipHost(t *testing.T) {
	fs := &fakeStore{data: model.VaultData{Version: 1}}
	seedExisting(t, fs)
	b := biz.NewBackupBiz(fs)

	plan := biz.ImportPlan{
		FolderName: "来自旧机",
		HostResolutions: []biz.HostResolution{
			{Host: "10.0.0.1", Port: 22, User: "ops", Action: "skip"},
		},
	}
	summary, err := b.ApplyImport(makeBackup(t), "pw", plan)
	if err != nil {
		t.Fatalf("ApplyImport: %v", err)
	}
	if summary.SkippedHosts != 1 || summary.Imported["forwards"] != 0 {
		t.Fatalf("summary = %+v", summary)
	}
	data, _ := fs.Load()
	if len(data.Forwards) != 1 || data.Forwards[0].Name != "现有转发" {
		t.Fatalf("forwards referencing skipped host must not import: %+v", data.Forwards)
	}
}

// 完全还原必须显式确认；确认后整体替换（现有实体被备份内容取代）。
func TestRestoreBackupRequiresConfirmation(t *testing.T) {
	fs := &fakeStore{data: model.VaultData{Version: 1}}
	seedExisting(t, fs)
	b := biz.NewBackupBiz(fs)
	raw := makeBackup(t)

	if err := b.RestoreBackup(raw, "pw", false); !errors.Is(err, biz.ErrRestoreNotConfirmed) {
		t.Fatalf("err = %v, want ErrRestoreNotConfirmed", err)
	}
	if err := b.RestoreBackup(raw, "pw", true); err != nil {
		t.Fatalf("RestoreBackup: %v", err)
	}
	data, _ := fs.Load()
	if len(data.SSHHosts) != 1 || data.SSHHosts[0].Name != "跳板" {
		t.Fatalf("vault should be fully replaced: %+v", data.SSHHosts)
	}
	if len(data.Folders) != 2 {
		t.Fatalf("folders = %+v", data.Folders)
	}
}
