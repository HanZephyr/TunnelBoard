package conf

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/HanZephyr/TunnelBoard/internal/model"
)

const defaultConfigPath = "config.toml"

// DefaultConfigFileName is the config file basename inside the config directory.
const DefaultConfigFileName = defaultConfigPath

const currentConfigVersion = 1

// isDirWritable checks if a directory is writable by attempting to create a temp file.
func isDirWritable(dir string) bool {
	tmpFile, err := os.CreateTemp(dir, ".write_test_*")
	if err != nil {
		return false
	}
	tmpFile.Close()
	os.Remove(tmpFile.Name())
	return true
}

const appConfigDirName = "TunnelBoard"

// getDefaultConfigDir returns the operating system's per-user application data
// directory. TunnelBoard deliberately has no current-directory or portable-mode
// fallback because daily data must not be written beside the executable/source.
func getDefaultConfigDir() string {
	if configDir, err := os.UserConfigDir(); err == nil && strings.TrimSpace(configDir) != "" {
		return filepath.Join(configDir, appConfigDirName)
	}

	homeDir, err := os.UserHomeDir()
	if err == nil && strings.TrimSpace(homeDir) != "" {
		return filepath.Join(homeDir, ".config", appConfigDirName)
	}

	return filepath.Join(os.TempDir(), appConfigDirName)
}

// Config is persisted in TOML storage.
type Config struct {
	Version int            `toml:"version"`
	Jumpers []model.Jumper `toml:"jumpers"`
	Tunnels []model.Tunnel `toml:"tunnels"`
	AutoRun bool           `toml:"auto_run"`
	License LicenseConfig  `toml:"license"`
}

type LicenseConfig struct {
	Code string `toml:"code"`
}

// ResolveConfigPath returns the effective config file path: implicit location
// from runtime mode, then config.root redirection when valid.
func ResolveConfigPath() string {
	return ResolveEffectiveConfigPath(ResolveImplicitConfigPath())
}

// DefaultConfig creates an empty config.
func DefaultConfig() *Config {
	return &Config{
		Version: currentConfigVersion,
		Jumpers: []model.Jumper{},
		Tunnels: []model.Tunnel{},
		AutoRun: false,
		License: LicenseConfig{},
	}
}

// Clone returns a detached copy.
func (c *Config) Clone() *Config {
	if c == nil {
		return DefaultConfig()
	}

	out := &Config{Version: c.Version, AutoRun: c.AutoRun, License: c.License}
	out.Jumpers = append(out.Jumpers, c.Jumpers...)
	out.Tunnels = append(out.Tunnels, c.Tunnels...)
	return out
}

// Normalize ensures stable defaults before save.
func (c *Config) Normalize() {
	if c.Version <= 0 {
		c.Version = currentConfigVersion
	}
	if c.Jumpers == nil {
		c.Jumpers = []model.Jumper{}
	}
	if c.Tunnels == nil {
		c.Tunnels = []model.Tunnel{}
	}
	c.License.Code = strings.TrimSpace(c.License.Code)
	// AutoRun defaults to false; no need to set if already present
	for i := range c.Tunnels {
		c.Tunnels[i].JumperIDs = normalizeJumperIDs(c.Tunnels[i].JumperIDs)
	}
}

func normalizeJumperIDs(ids []int) []int {
	out := make([]int, 0, len(ids))
	seen := make(map[int]struct{}, len(ids)+1)
	appendID := func(id int) {
		if id <= 0 {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	for _, id := range ids {
		appendID(id)
	}
	return out
}
