// Package vault 实现 TunnelBoard 的本地加密数据底座（Vault Module 主体）。
// 文件布局：magic 8B "TBVAULT1" | nonce 12B | AES-256-GCM 密文；magic 同时作为 AAD。
// 密钥为首次初始化生成的 32 字节设备本地随机密钥，落盘 vault.key（0600）。
package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/HanZephyr/TunnelBoard/internal/model"
)

const (
	magic          = "TBVAULT1"
	nonceSize      = 12
	keySize        = 32
	payloadVersion = 1

	keyFileName  = "vault.key"
	dataFileName = "vault.dat"
)

// ErrKeyUnavailable 表示 vault.dat 存在但本机密钥遗失或损坏；
// 应用层此时只能引导用户导入备份包或显式初始化空 Vault，不得覆盖数据。
var ErrKeyUnavailable = errors.New("vault: key unavailable for existing vault data")

// Store 是 Vault 的读写收口：Load 读出解密后的数据，Update 是唯一写入口。
type Store struct {
	dir string
	key []byte
	mu  sync.Mutex
}

// Open 打开 dir 下的 Vault，按密钥生命周期矩阵处理：
// 密钥与数据均不存在则首次初始化；仅有密钥则以现有密钥初始化空 Vault；
// 仅有数据则密钥遗失，报错且不触碰数据文件。
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("vault: create dir: %w", err)
	}
	s := &Store{dir: dir}

	key, keyErr := readKey(filepath.Join(dir, keyFileName))
	_, datErr := os.Stat(filepath.Join(dir, dataFileName))
	datExists := datErr == nil

	switch {
	case keyErr != nil && datExists:
		return nil, fmt.Errorf("%w: %v", ErrKeyUnavailable, keyErr)
	case keyErr != nil:
		key, err := generateKey()
		if err != nil {
			return nil, err
		}
		if err := writeFileAtomic(dir, keyFileName, key, 0o600); err != nil {
			return nil, fmt.Errorf("vault: save key: %w", err)
		}
		s.key = key
		return s, s.saveLocked(model.VaultData{Version: payloadVersion})
	default:
		s.key = key
		if !datExists {
			return s, s.saveLocked(model.VaultData{Version: payloadVersion})
		}
		return s, nil
	}
}

// Dir 返回 Vault 所在的数据目录。
func (s *Store) Dir() string {
	return s.dir
}

// Load 读取并解密 Vault 数据。
func (s *Store) Load() (model.VaultData, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked()
}

// Update 在锁内读出数据、执行 mutate、原子写回，返回写回后的数据。
func (s *Store) Update(mutate func(*model.VaultData) error) (model.VaultData, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := s.loadLocked()
	if err != nil {
		return model.VaultData{}, err
	}
	if err := mutate(&data); err != nil {
		return model.VaultData{}, err
	}
	if err := s.saveLocked(data); err != nil {
		return model.VaultData{}, err
	}
	return data, nil
}

func (s *Store) loadLocked() (model.VaultData, error) {
	raw, err := os.ReadFile(filepath.Join(s.dir, dataFileName))
	if err != nil {
		return model.VaultData{}, fmt.Errorf("vault: read data: %w", err)
	}
	payload, err := s.open(raw)
	if err != nil {
		return model.VaultData{}, err
	}
	var data model.VaultData
	if err := json.Unmarshal(payload, &data); err != nil {
		return model.VaultData{}, fmt.Errorf("vault: decode payload: %w", err)
	}
	if data.Version > payloadVersion {
		return model.VaultData{}, fmt.Errorf("vault: unsupported payload version %d", data.Version)
	}
	return data, nil
}

func (s *Store) saveLocked(data model.VaultData) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("vault: encode payload: %w", err)
	}
	sealed, err := s.seal(payload)
	if err != nil {
		return err
	}
	if err := writeFileAtomic(s.dir, dataFileName, sealed, 0o600); err != nil {
		return fmt.Errorf("vault: save data: %w", err)
	}
	return nil
}

// seal 加密 payload：magic | nonce | ciphertext。
func (s *Store) seal(payload []byte) ([]byte, error) {
	aead, err := newAEAD(s.key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, nonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("vault: random nonce: %w", err)
	}
	out := make([]byte, 0, len(magic)+nonceSize+len(payload)+aead.Overhead())
	out = append(out, magic...)
	out = append(out, nonce...)
	out = aead.Seal(out, nonce, payload, []byte(magic))
	return out, nil
}

// open 解密 seal 产物；magic 不匹配或密文被篡改都会报错。
func (s *Store) open(raw []byte) ([]byte, error) {
	if len(raw) < len(magic)+nonceSize || string(raw[:len(magic)]) != magic {
		return nil, errors.New("vault: not a vault data file")
	}
	aead, err := newAEAD(s.key)
	if err != nil {
		return nil, err
	}
	nonce := raw[len(magic) : len(magic)+nonceSize]
	payload, err := aead.Open(nil, nonce, raw[len(magic)+nonceSize:], []byte(magic))
	if err != nil {
		return nil, fmt.Errorf("vault: decrypt data: %w", err)
	}
	return payload, nil
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("vault: cipher: %w", err)
	}
	return cipher.NewGCM(block)
}

func generateKey() ([]byte, error) {
	key := make([]byte, keySize)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("vault: generate key: %w", err)
	}
	return key, nil
}

// readKey 读取密钥文件；不存在或长度不符都视为不可用。
func readKey(path string) ([]byte, error) {
	key, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(key) != keySize {
		return nil, fmt.Errorf("vault: bad key length %d", len(key))
	}
	return key, nil
}

// writeFileAtomic 写临时文件后 rename，避免半写状态。
func writeFileAtomic(dir, name string, data []byte, perm os.FileMode) error {
	tmp := filepath.Join(dir, name+".tmp")
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, name))
}
