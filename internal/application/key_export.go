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
	return writePrivateKeyAtomicWith(ctx, destination, content, replacePrivateKeyFile, "replace")
}

// writePrivateKeyAtomicExclusive 与普通原子写入共用候选文件流程，但最终提交不覆盖已有文件。
// 密钥生成使用该版本，避免并发请求在 Stat 检查后发生覆盖竞态。
func writePrivateKeyAtomicExclusive(ctx context.Context, destination string, content []byte) error {
	return writeKeyAtomicExclusive(ctx, destination, content, 0o600, restrictPrivateKeyFile, linkPrivateKeyFile, "private key")
}

// writePublicKeyAtomicExclusive 以不覆盖的原子方式写入公开的 authorized_keys 公钥文件。
// 公钥不需要私钥的受限 ACL，但同样不能因为并发请求覆盖既有文件。
func writePublicKeyAtomicExclusive(ctx context.Context, destination string, content []byte) error {
	return writeKeyAtomicExclusive(ctx, destination, content, 0o644, func(string) error { return nil }, linkPrivateKeyFile, "public key")
}

type privateKeyCommit func(source, destination string) error

func writePrivateKeyAtomicWith(ctx context.Context, destination string, content []byte, commit privateKeyCommit, operation string) error {
	return writeKeyAtomic(ctx, destination, content, 0o600, restrictPrivateKeyFile, commit, operation+" private key")
}

func writeKeyAtomicExclusive(ctx context.Context, destination string, content []byte, mode os.FileMode, restrict func(string) error, commit privateKeyCommit, description string) error {
	return writeKeyAtomic(ctx, destination, content, mode, restrict, commit, "create "+description)
}

func writeKeyAtomic(ctx context.Context, destination string, content []byte, mode os.FileMode, restrict func(string) error, commit privateKeyCommit, operation string) error {
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
	if err := file.Chmod(mode); err != nil {
		_ = file.Close()
		return fmt.Errorf("application: restrict private key candidate: %w", err)
	}
	if err := restrict(temporary); err != nil {
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
	if err := commit(temporary, destination); err != nil {
		if strings.HasPrefix(operation, "create ") && errors.Is(err, os.ErrExist) {
			return fmt.Errorf("%w: %s", ErrSSHKeyExists, destination)
		}
		return fmt.Errorf("application: %s destination: %w", operation, err)
	}
	temporary = ""
	return nil
}
