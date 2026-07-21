package helper

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
)

var bundledHelperPin struct {
	sync.RWMutex
	sha256 string
}

// SetExpectedBinarySHA256 由主程序启动时注入本次发行 Helper 的构建摘要。
// 正式构建通过 -ldflags 设置摘要；空值必须在 VerifyBundledBinary 中失败关闭。
func SetExpectedBinarySHA256(expected string) {
	bundledHelperPin.Lock()
	bundledHelperPin.sha256 = strings.TrimSpace(expected)
	bundledHelperPin.Unlock()
}

func expectedBinarySHA256() string {
	bundledHelperPin.RLock()
	defer bundledHelperPin.RUnlock()
	return bundledHelperPin.sha256
}

// VerifyBundledBinary 确认即将提权的 Helper 字节恰好属于当前主程序发行。
func VerifyBundledBinary(path string) error {
	bundledHelperPin.RLock()
	expected := bundledHelperPin.sha256
	bundledHelperPin.RUnlock()
	if expected == "" {
		return errors.New("helper: bundled binary sha256 is not configured")
	}
	if len(expected) != sha256.Size*2 {
		return errors.New("helper: bundled binary sha256 build pin is invalid")
	}
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("helper: open bundled binary: %w", err)
	}
	defer file.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return fmt.Errorf("helper: hash bundled binary: %w", err)
	}
	actual := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(actual, expected) {
		return fmt.Errorf("helper: bundled binary integrity mismatch: got %s, want %s", actual, expected)
	}
	return nil
}
