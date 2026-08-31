package application

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
)

// DefaultSSHKeyFilename 是生成私钥时保存对话框的默认文件名。
const DefaultSSHKeyFilename = "id_ed25519_tunnelboard"

// ErrSSHKeyExists 表示目标私钥或公钥文件已存在；生成流程从不覆盖既有密钥。
var ErrSSHKeyExists = errors.New("application: SSH key file already exists")

type GenerateSSHKeyRequest struct {
	// Destination 是私钥目标路径（支持 ~ 前缀）；由前端保存对话框提供，必填。
	Destination string `json:"destination"`
	// Passphrase 是可选的私钥口令；为空时生成不加密的 OpenSSH 私钥。
	Passphrase string `json:"passphrase,omitempty"`
	// Comment 是公钥行尾的注释；为空时使用 tunnelboard@<hostname>。
	Comment string `json:"comment,omitempty"`
}

type GenerateSSHKeyResult struct {
	// KeyPath 是实际写入的私钥绝对路径，前端直接回填到 SSHHost.KeyPath。
	KeyPath string `json:"keyPath"`
	// PublicKeyPath 是与私钥同目录的 OpenSSH 公钥文件路径。
	PublicKeyPath string `json:"publicKeyPath"`
	// PublicKey 是单行 OpenSSH authorized_keys 格式公钥；同时写入 PublicKeyPath 供复制或部署。
	PublicKey string `json:"publicKey"`
}

// GenerateSSHKeyPair 生成 ed25519 SSH 密钥对：私钥以 OpenSSH PEM 格式原子写入
// 目标路径（0600/受限 ACL），公钥以 authorized_keys 格式写入目标路径 + ".pub"。
// 任一目标已存在时拒绝覆盖。
func GenerateSSHKeyPair(ctx context.Context, request GenerateSSHKeyRequest) (GenerateSSHKeyResult, error) {
	if err := ctx.Err(); err != nil {
		return GenerateSSHKeyResult{}, err
	}
	destination, err := expandHomePath(request.Destination)
	if err != nil {
		return GenerateSSHKeyResult{}, err
	}
	if destination == "" {
		return GenerateSSHKeyResult{}, errors.New("application: private key destination is required")
	}
	publicKeyDestination := destination + ".pub"
	if _, err := os.Stat(destination); err == nil {
		return GenerateSSHKeyResult{}, fmt.Errorf("%w: %s", ErrSSHKeyExists, destination)
	}
	if _, err := os.Stat(publicKeyDestination); err == nil {
		return GenerateSSHKeyResult{}, fmt.Errorf("%w: %s", ErrSSHKeyExists, publicKeyDestination)
	}
	if dir := filepath.Dir(destination); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return GenerateSSHKeyResult{}, fmt.Errorf("application: create key directory: %w", err)
		}
	}

	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return GenerateSSHKeyResult{}, fmt.Errorf("application: generate ed25519 key: %w", err)
	}
	defer clear(private)
	comment := resolveKeyComment(request.Comment)
	var block *pem.Block
	if request.Passphrase != "" {
		passphrase := []byte(request.Passphrase)
		defer clear(passphrase)
		block, err = ssh.MarshalPrivateKeyWithPassphrase(private, comment, passphrase)
	} else {
		block, err = ssh.MarshalPrivateKey(private, comment)
	}
	if err != nil {
		return GenerateSSHKeyResult{}, fmt.Errorf("application: marshal private key: %w", err)
	}
	privatePEM := pem.EncodeToMemory(block)
	defer clear(privatePEM)
	if err := writePrivateKeyAtomicExclusive(ctx, destination, privatePEM); err != nil {
		return GenerateSSHKeyResult{}, err
	}

	authorizedKey, err := ssh.NewPublicKey(public)
	if err != nil {
		return GenerateSSHKeyResult{}, fmt.Errorf("application: derive public key: %w", err)
	}
	keyLine := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(authorizedKey)))
	publicKey := fmt.Sprintf("%s %s\n", keyLine, comment)
	if err := writePublicKeyAtomicExclusive(ctx, publicKeyDestination, []byte(publicKey)); err != nil {
		_ = os.Remove(destination)
		return GenerateSSHKeyResult{}, err
	}
	return GenerateSSHKeyResult{
		KeyPath:       filepath.Clean(destination),
		PublicKeyPath: filepath.Clean(publicKeyDestination),
		PublicKey:     strings.TrimSpace(publicKey),
	}, nil
}

func resolveKeyComment(comment string) string {
	trimmed := strings.Join(strings.Fields(comment), " ")
	if trimmed != "" {
		return trimmed
	}
	host, err := os.Hostname()
	if err != nil || host == "" {
		return "tunnelboard"
	}
	return "tunnelboard@" + host
}

func expandHomePath(path string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "~" {
		return os.UserHomeDir()
	}
	if !strings.HasPrefix(trimmed, "~/") {
		return trimmed, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("application: resolve home dir: %w", err)
	}
	return filepath.Join(home, strings.TrimPrefix(trimmed, "~/")), nil
}
