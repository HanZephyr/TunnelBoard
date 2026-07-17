package vault

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// RootPointerFileName 是锚定目录中的指针文件，内容为一行：重定向目标的绝对目录路径。
const RootPointerFileName = "config.root"

const appDataDirName = "TunnelBoard"

// DefaultDataDir 返回系统每用户应用数据目录。TunnelBoard 故意不提供当前目录或
// 便携模式回退：日常数据不得写在可执行文件或源码旁边。
func DefaultDataDir() string {
	if configDir, err := os.UserConfigDir(); err == nil && strings.TrimSpace(configDir) != "" {
		return filepath.Join(configDir, appDataDirName)
	}
	if homeDir, err := os.UserHomeDir(); err == nil && strings.TrimSpace(homeDir) != "" {
		return filepath.Join(homeDir, ".config", appDataDirName)
	}
	return filepath.Join(os.TempDir(), appDataDirName)
}

// ResolveDataDir 在隐式数据目录旁存在有效 config.root 时返回其指向的目标目录，
// 任何校验失败都原样返回 implicitDir。
func ResolveDataDir(implicitDir string) string {
	implicitDir = strings.TrimSpace(implicitDir)
	if implicitDir == "" {
		return DefaultDataDir()
	}

	data, err := os.ReadFile(filepath.Join(implicitDir, RootPointerFileName))
	if err != nil {
		return implicitDir
	}

	line := strings.TrimSpace(strings.Split(string(data), "\n")[0])
	raw := filepath.Clean(line)
	if raw == "." || raw == "" {
		return implicitDir
	}
	if !filepath.IsAbs(raw) {
		slog.Warn("config.root must contain an absolute directory path", "value", raw)
		return implicitDir
	}
	if !isDirReadableAndWritable(raw) {
		slog.Warn("config.root target directory unusable, using implicit data dir", "dir", raw)
		return implicitDir
	}
	if sameDir(implicitDir, raw) {
		return implicitDir
	}
	return raw
}

// OpenDefault 按默认数据目录与 config.root 重定向打开 Vault，是应用层的标准入口。
func OpenDefault() (*Store, error) {
	return Open(ResolveDataDir(DefaultDataDir()))
}

func sameDir(a, b string) bool {
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return false
	}
	return filepath.Clean(absA) == filepath.Clean(absB)
}

// isDirReadableAndWritable 要求目录存在、可打开且可写入（以临时文件实测）。
func isDirReadableAndWritable(dir string) bool {
	st, err := os.Stat(dir)
	if err != nil || !st.IsDir() {
		return false
	}
	f, err := os.Open(dir)
	if err != nil {
		return false
	}
	_ = f.Close()
	tmp, err := os.CreateTemp(dir, ".write_test_*")
	if err != nil {
		return false
	}
	_ = tmp.Close()
	_ = os.Remove(tmp.Name())
	return true
}
