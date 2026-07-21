package vault

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"runtime"

	"golang.org/x/crypto/argon2"

	"github.com/HanZephyr/TunnelBoard/internal/model"
)

// 备份包格式（ADR 0001：与用户密码独立加密，不携带设备本地密钥）：
// magic 8B "TBBACKUP" | salt 16B | time 4B | memory 4B | parallelism 4B | nonce 12B | ciphertext。
// 密钥 = Argon2id(密码, salt, 参数)；文件头整体作为 AAD。密码不可找回，包内无任何恢复材料。
const (
	backupMagic     = "TBBACKUP"
	backupSaltSize  = 16
	backupKeySize   = 32
	backupKDFParams = 12 // time/memory/parallelism 各 4B
)

// BackupKDF 是备份包的 Argon2id 参数，随文件头持久化以便未来调整。
type BackupKDF struct {
	Time        uint32
	Memory      uint32 // KiB
	Parallelism uint32
}

// DefaultBackupKDF 返回 RFC 9106 推荐档（m=64MiB, t=3, p=4）。
func DefaultBackupKDF() BackupKDF {
	parallelism := runtime.GOMAXPROCS(0)
	if parallelism > 4 {
		parallelism = 4
	}
	if parallelism < 1 {
		parallelism = 1
	}
	return BackupKDF{Time: 3, Memory: 64 * 1024, Parallelism: uint32(parallelism)}
}

// KDF 参数是可移植备份格式的一部分；导入使用固定安全预算。
const (
	minBackupKDFMemory      = 32 * 1024
	maxBackupKDFMemory      = 256 * 1024
	minBackupKDFTime        = 2
	maxBackupKDFTime        = 6
	maxBackupKDFParallelism = 8
)

// validate 校验 KDF 参数在安全边界内。
func (k BackupKDF) validate() error {
	if k.Time < minBackupKDFTime || k.Time > maxBackupKDFTime {
		return fmt.Errorf("%w: backup kdf time %d out of bounds", ErrBackupResourceLimit, k.Time)
	}
	if k.Memory < minBackupKDFMemory || k.Memory > maxBackupKDFMemory {
		return fmt.Errorf("%w: backup kdf memory %d KiB out of bounds", ErrBackupResourceLimit, k.Memory)
	}
	if k.Parallelism == 0 || k.Parallelism > maxBackupKDFParallelism {
		return fmt.Errorf("%w: backup kdf parallelism %d out of bounds", ErrBackupResourceLimit, k.Parallelism)
	}
	return nil
}

// backupPayload 是备份包密文内的载荷：Vault 快照 + 可选的外部私钥文件本体。
type backupPayload struct {
	Vault    model.VaultData   `json:"vault"`
	KeyFiles map[string][]byte `json:"keyFiles,omitempty"`
}

// ExportBackup 用用户密码加密导出 Vault 快照；keyFiles 为显式选择包含的私钥文件（路径→内容）。
func ExportBackup(data model.VaultData, keyFiles map[string][]byte, password string, kdf BackupKDF) ([]byte, error) {
	if err := validateBackupPassword(password); err != nil {
		return nil, err
	}
	if err := kdf.validate(); err != nil {
		return nil, err
	}
	if err := validateBackupPayload(data, keyFiles); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(backupPayload{Vault: data, KeyFiles: keyFiles})
	if err != nil {
		return nil, fmt.Errorf("vault: encode backup payload: %w", err)
	}

	header := make([]byte, 0, len(backupMagic)+backupSaltSize+backupKDFParams+nonceSize)
	header = append(header, backupMagic...)
	salt := make([]byte, backupSaltSize)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, fmt.Errorf("vault: random salt: %w", err)
	}
	header = append(header, salt...)
	header = binary.LittleEndian.AppendUint32(header, kdf.Time)
	header = binary.LittleEndian.AppendUint32(header, kdf.Memory)
	header = binary.LittleEndian.AppendUint32(header, kdf.Parallelism)

	backupKDFMu.Lock()
	key := argon2.IDKey([]byte(password), salt, kdf.Time, kdf.Memory, uint8(kdf.Parallelism), backupKeySize)
	backupKDFMu.Unlock()
	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, nonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("vault: random nonce: %w", err)
	}
	header = append(header, nonce...)

	out := aead.Seal(header, nonce, payload, header)
	return out, nil
}

// ParseBackup 解密并解析备份包；密码错误、篡改与格式不符统一返回错误，不泄露内容。
func ParseBackup(raw []byte, password string) (model.VaultData, map[string][]byte, error) {
	if len(raw) > MaxBackupPackageBytes {
		return model.VaultData{}, nil, resourceLimit("package bytes", len(raw), MaxBackupPackageBytes)
	}
	if err := validateBackupPassword(password); err != nil {
		return model.VaultData{}, nil, err
	}
	headLen := len(backupMagic) + backupSaltSize + backupKDFParams + nonceSize
	if len(raw) < headLen || !bytes.Equal(raw[:len(backupMagic)], []byte(backupMagic)) {
		return model.VaultData{}, nil, errors.New("vault: not a backup package")
	}
	header := raw[:headLen]
	salt := raw[len(backupMagic) : len(backupMagic)+backupSaltSize]
	kdfOff := len(backupMagic) + backupSaltSize
	kdf := BackupKDF{
		Time:        binary.LittleEndian.Uint32(raw[kdfOff:]),
		Memory:      binary.LittleEndian.Uint32(raw[kdfOff+4:]),
		Parallelism: binary.LittleEndian.Uint32(raw[kdfOff+8:]),
	}
	nonce := raw[headLen-nonceSize : headLen]

	if err := kdf.validate(); err != nil {
		return model.VaultData{}, nil, err
	}
	backupKDFMu.Lock()
	key := argon2.IDKey([]byte(password), salt, kdf.Time, kdf.Memory, uint8(kdf.Parallelism), backupKeySize)
	backupKDFMu.Unlock()
	aead, err := newAEAD(key)
	if err != nil {
		return model.VaultData{}, nil, err
	}
	payload, err := aead.Open(nil, nonce, raw[headLen:], header)
	if err != nil {
		return model.VaultData{}, nil, fmt.Errorf("vault: decrypt backup (wrong password or corrupted): %w", err)
	}
	if len(payload) > MaxBackupPackageBytes-headLen {
		return model.VaultData{}, nil, resourceLimit("decrypted payload bytes", len(payload), MaxBackupPackageBytes-headLen)
	}
	if err := validateBackupJSONDepth(payload); err != nil {
		return model.VaultData{}, nil, err
	}
	var parsed backupPayload
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&parsed); err != nil {
		return model.VaultData{}, nil, fmt.Errorf("vault: decode backup payload: %w", err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return model.VaultData{}, nil, errors.New("vault: backup payload contains trailing data")
	}
	if err := validateBackupPayload(parsed.Vault, parsed.KeyFiles); err != nil {
		return model.VaultData{}, nil, err
	}
	return parsed.Vault, parsed.KeyFiles, nil
}
