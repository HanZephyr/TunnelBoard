//go:build !windows

package caddy

import (
	"fmt"
	"os"
)

func prepareRuntimeDir(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return fmt.Errorf("caddy: create runtime dir: %w", err)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return fmt.Errorf("caddy: protect runtime dir: %w", err)
	}
	return nil
}
