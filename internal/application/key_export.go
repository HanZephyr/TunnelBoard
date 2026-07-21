package application

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func writePrivateKeyAtomic(ctx context.Context, destination string, content []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	destination = strings.TrimSpace(destination)
	if destination == "" {
		return errors.New("application: private key destination is required")
	}
	dir := filepath.Dir(destination)
	file, err := os.CreateTemp(dir, ".tunnelboard-key-*.tmp")
	if err != nil {
		return fmt.Errorf("application: create private key candidate: %w", err)
	}
	temporary := file.Name()
	defer func() { _ = os.Remove(temporary) }()
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return fmt.Errorf("application: restrict private key candidate: %w", err)
	}
	if err := restrictPrivateKeyFile(temporary); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		return fmt.Errorf("application: write private key candidate: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("application: flush private key candidate: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("application: close private key candidate: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := replacePrivateKeyFile(temporary, destination); err != nil {
		return fmt.Errorf("application: replace private key destination: %w", err)
	}
	temporary = ""
	return nil
}
