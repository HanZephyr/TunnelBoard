package vault

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"golang.org/x/crypto/argon2"

	"github.com/HanZephyr/TunnelBoard/internal/model"
)

func encodeUncheckedBackup(t *testing.T, payload backupPayload, password string, kdf BackupKDF) []byte {
	t.Helper()
	rawPayload, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	header := make([]byte, 0, len(backupMagic)+backupSaltSize+backupKDFParams+nonceSize)
	header = append(header, backupMagic...)
	salt := make([]byte, backupSaltSize)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		t.Fatal(err)
	}
	header = append(header, salt...)
	header = binary.LittleEndian.AppendUint32(header, kdf.Time)
	header = binary.LittleEndian.AppendUint32(header, kdf.Memory)
	header = binary.LittleEndian.AppendUint32(header, kdf.Parallelism)
	key := argon2.IDKey([]byte(password), salt, kdf.Time, kdf.Memory, uint8(kdf.Parallelism), backupKeySize)
	aead, err := newAEAD(key)
	if err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, nonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		t.Fatal(err)
	}
	header = append(header, nonce...)
	return aead.Seal(header, nonce, rawPayload, header)
}

func TestParseBackupRejectsKDFOutsidePortableBudget(t *testing.T) {
	valid := BackupKDF{Time: 2, Memory: 32 * 1024, Parallelism: 1}
	raw := encodeUncheckedBackup(t, backupPayload{Vault: model.VaultData{Version: 1}}, "pw", valid)

	tests := []struct {
		name   string
		offset int
		value  uint32
	}{
		{name: "memory below 32 MiB", offset: 4, value: 31 * 1024},
		{name: "memory above 256 MiB", offset: 4, value: 256*1024 + 1},
		{name: "time below two", offset: 0, value: 1},
		{name: "time above six", offset: 0, value: 7},
		{name: "parallelism above eight", offset: 8, value: 9},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mutated := append([]byte(nil), raw...)
			kdfOff := len(backupMagic) + backupSaltSize
			binary.LittleEndian.PutUint32(mutated[kdfOff+tt.offset:], tt.value)
			_, _, err := ParseBackup(mutated, "pw")
			if !errors.Is(err, ErrBackupResourceLimit) {
				t.Fatalf("ParseBackup error = %v, want ErrBackupResourceLimit", err)
			}
		})
	}
}

func TestParseBackupRejectsPackageLargerThan64MiBBeforeKDF(t *testing.T) {
	raw := make([]byte, MaxBackupPackageBytes+1)
	copy(raw, backupMagic)
	_, _, err := ParseBackup(raw, "pw")
	if !errors.Is(err, ErrBackupResourceLimit) {
		t.Fatalf("ParseBackup error = %v, want ErrBackupResourceLimit", err)
	}
}

func TestParseBackupRejectsEntityAndStringBudgets(t *testing.T) {
	kdf := BackupKDF{Time: 2, Memory: 32 * 1024, Parallelism: 1}

	t.Run("folders", func(t *testing.T) {
		folders := make([]model.Folder, 501)
		for i := range folders {
			folders[i] = model.Folder{ID: i + 1, Name: "f"}
		}
		raw := encodeUncheckedBackup(t, backupPayload{Vault: model.VaultData{Version: 1, Folders: folders}}, "pw", kdf)
		_, _, err := ParseBackup(raw, "pw")
		if !errors.Is(err, ErrBackupResourceLimit) {
			t.Fatalf("ParseBackup error = %v, want ErrBackupResourceLimit", err)
		}
	})

	t.Run("notes", func(t *testing.T) {
		host := model.SSHHost{ID: 1, Name: "h", Host: "example.test", Port: 22, AuthType: "ssh_agent", Notes: strings.Repeat("x", 16*1024+1)}
		raw := encodeUncheckedBackup(t, backupPayload{Vault: model.VaultData{Version: 1, SSHHosts: []model.SSHHost{host}}}, "pw", kdf)
		_, _, err := ParseBackup(raw, "pw")
		if !errors.Is(err, ErrBackupResourceLimit) {
			t.Fatalf("ParseBackup error = %v, want ErrBackupResourceLimit", err)
		}
	})

	t.Run("ssh chain", func(t *testing.T) {
		fw := model.Forward{ID: 1, FolderID: 1, Name: "f", ChainHostIDs: make([]int, 17)}
		raw := encodeUncheckedBackup(t, backupPayload{Vault: model.VaultData{Version: 1, Forwards: []model.Forward{fw}}}, "pw", kdf)
		_, _, err := ParseBackup(raw, "pw")
		if !errors.Is(err, ErrBackupResourceLimit) {
			t.Fatalf("ParseBackup error = %v, want ErrBackupResourceLimit", err)
		}
	})
}
