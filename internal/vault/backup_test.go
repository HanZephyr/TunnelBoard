package vault_test

import (
	"bytes"
	"testing"

	"github.com/HanZephyr/TunnelBoard/internal/model"
	"github.com/HanZephyr/TunnelBoard/internal/vault"
)

// 测试用小参数 KDF（默认 64MiB 在生产使用，测试提速）。
var fastKDF = vault.BackupKDF{Time: 1, Memory: 8 * 1024, Parallelism: 1}

func sampleBackupData() model.VaultData {
	return model.VaultData{
		Version: 1,
		Folders: []model.Folder{{ID: 1, Name: "工作"}},
		SSHHosts: []model.SSHHost{
			{ID: 1, Name: "h", Host: "10.0.0.1", Port: 22, AuthType: "ssh_key", KeyPath: "C:/keys/id_ed25519", Password: "key-passphrase"},
		},
		Forwards: []model.Forward{
			{ID: 1, FolderID: 1, Name: "db", Mode: "local", ChainHostIDs: []int{1}, LocalHost: "127.0.0.1", LocalPort: 5432, RemoteHost: "db", RemotePort: 5432},
		},
	}
}

// 备份包 roundtrip：密码加密导出，同密码解析还原全部数据（含私钥口令与可选私钥文件）。
func TestBackupRoundTrip(t *testing.T) {
	data := sampleBackupData()
	keyFiles := map[string][]byte{"C:/keys/id_ed25519": []byte("PRIVATE KEY MATERIAL")}

	raw, err := vault.ExportBackup(data, keyFiles, "correct horse", fastKDF)
	if err != nil {
		t.Fatalf("ExportBackup: %v", err)
	}
	if !bytes.HasPrefix(raw, []byte("TBBACKUP")) {
		t.Fatal("backup must carry TBBACKUP magic")
	}
	if bytes.Contains(raw, []byte("key-passphrase")) || bytes.Contains(raw, []byte("PRIVATE KEY MATERIAL")) {
		t.Fatal("backup must not contain plaintext secrets")
	}

	got, gotKeys, err := vault.ParseBackup(raw, "correct horse")
	if err != nil {
		t.Fatalf("ParseBackup: %v", err)
	}
	if len(got.SSHHosts) != 1 || got.SSHHosts[0].Password != "key-passphrase" {
		t.Fatalf("vault data mismatch: %+v", got.SSHHosts)
	}
	if string(gotKeys["C:/keys/id_ed25519"]) != "PRIVATE KEY MATERIAL" {
		t.Fatalf("key files mismatch: %+v", gotKeys)
	}

	// 不含私钥文件的导出
	raw2, err := vault.ExportBackup(data, nil, "pw", fastKDF)
	if err != nil {
		t.Fatalf("ExportBackup: %v", err)
	}
	_, gotKeys2, err := vault.ParseBackup(raw2, "pw")
	if err != nil {
		t.Fatalf("ParseBackup: %v", err)
	}
	if len(gotKeys2) != 0 {
		t.Fatalf("no key files expected, got %+v", gotKeys2)
	}
}

// 错误密码、篡改、坏 magic 都必须失败且不泄露内容。
func TestBackupRejectsWrongPasswordAndTampering(t *testing.T) {
	raw, err := vault.ExportBackup(sampleBackupData(), nil, "right", fastKDF)
	if err != nil {
		t.Fatalf("ExportBackup: %v", err)
	}

	if _, _, err := vault.ParseBackup(raw, "wrong"); err == nil {
		t.Fatal("wrong password must fail")
	}

	tampered := append([]byte(nil), raw...)
	tampered[len(tampered)-1] ^= 0xff
	if _, _, err := vault.ParseBackup(tampered, "right"); err == nil {
		t.Fatal("tampered ciphertext must fail")
	}

	bad := append([]byte(nil), raw...)
	copy(bad, "EVILMAG!")
	if _, _, err := vault.ParseBackup(bad, "right"); err == nil {
		t.Fatal("bad magic must fail")
	}
}

// 恶意构造的超大 KDF 内存参数必须被拒绝，防止解析即内存耗尽。
func TestBackupRejectsHostileKDFParams(t *testing.T) {
	raw, err := vault.ExportBackup(sampleBackupData(), nil, "pw", fastKDF)
	if err != nil {
		t.Fatalf("ExportBackup: %v", err)
	}
	// memory 字段位于 magic(8) + salt(16) + time(4) 之后
	raw[8+16+4] = 0xff
	raw[8+16+4+1] = 0xff
	raw[8+16+4+2] = 0xff
	raw[8+16+4+3] = 0x7f
	if _, _, err := vault.ParseBackup(raw, "pw"); err == nil {
		t.Fatal("hostile kdf memory must be rejected before derivation")
	}
}
