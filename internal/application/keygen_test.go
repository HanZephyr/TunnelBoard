package application

import (
	"context"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestGenerateSSHKeyPairWritesParseableOpenSSHKey(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "nested", "id_ed25519_test")
	result, err := GenerateSSHKeyPair(context.Background(), GenerateSSHKeyRequest{
		Destination: destination,
		Comment:     "test@host",
	})
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}
	if result.KeyPath != filepath.Clean(destination) {
		t.Fatalf("key path = %q, want %q", result.KeyPath, filepath.Clean(destination))
	}
	if !strings.HasPrefix(result.PublicKey, "ssh-ed25519 ") || !strings.HasSuffix(result.PublicKey, " test@host") {
		t.Fatalf("public key line unexpected: %q", result.PublicKey)
	}

	raw, err := os.ReadFile(destination)
	if err != nil {
		t.Fatalf("read private key: %v", err)
	}
	if block, _ := pemBlockType(raw); block != "OPENSSH PRIVATE KEY" {
		t.Fatalf("private key PEM type = %q, want OPENSSH PRIVATE KEY", block)
	}
	signer, err := ssh.ParsePrivateKey(raw)
	if err != nil {
		t.Fatalf("parse generated private key: %v", err)
	}
	fileKey := strings.Fields(strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))))
	resultKey := strings.Fields(result.PublicKey)
	if len(fileKey) < 2 || len(resultKey) < 2 || fileKey[0] != resultKey[0] || fileKey[1] != resultKey[1] {
		t.Fatalf("public key mismatch: file=%q result=%q", fileKey, resultKey)
	}

	if runtime.GOOS != "windows" {
		info, err := os.Stat(destination)
		if err != nil {
			t.Fatalf("stat private key: %v", err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("private key perm = %o, want 600", perm)
		}
	}
}

func pemBlockType(raw []byte) (string, error) {
	block, _ := pem.Decode(raw)
	if block == nil {
		return "", errors.New("no PEM block")
	}
	return block.Type, nil
}

func TestGenerateSSHKeyPairRefusesOverwrite(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "id_ed25519_test")
	if _, err := GenerateSSHKeyPair(context.Background(), GenerateSSHKeyRequest{Destination: destination}); err != nil {
		t.Fatalf("first generate: %v", err)
	}
	raw, err := os.ReadFile(destination)
	if err != nil {
		t.Fatalf("read private key: %v", err)
	}
	_, err = GenerateSSHKeyPair(context.Background(), GenerateSSHKeyRequest{Destination: destination})
	if !errors.Is(err, ErrSSHKeyExists) {
		t.Fatalf("second generate error = %v, want ErrSSHKeyExists", err)
	}
	after, err := os.ReadFile(destination)
	if err != nil {
		t.Fatalf("reread private key: %v", err)
	}
	if string(raw) != string(after) {
		t.Fatal("existing private key was overwritten")
	}
}

func TestGenerateSSHKeyPairRefusesExistingDestinationWithoutChangingIt(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "id_ed25519_test")
	want := []byte("existing private key")
	if err := os.WriteFile(destination, want, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := GenerateSSHKeyPair(context.Background(), GenerateSSHKeyRequest{Destination: destination})
	if !errors.Is(err, ErrSSHKeyExists) {
		t.Fatalf("error = %v, want ErrSSHKeyExists", err)
	}
	got, readErr := os.ReadFile(destination)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != string(want) {
		t.Fatalf("existing destination changed to %q", got)
	}
}

func TestGenerateSSHKeyPairExpandsHomePath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("resolve home dir: %v", err)
	}
	// 只验证路径展开，不真的写进用户 ~/.ssh：使用不存在标记前缀使其失败。
	request := GenerateSSHKeyRequest{Destination: "~/.ssh"}
	if _, err := GenerateSSHKeyPair(context.Background(), request); err == nil {
		t.Fatal("expected failure writing to a directory path")
	} else if !strings.Contains(err.Error(), ".ssh") && !strings.Contains(err.Error(), home) {
		t.Fatalf("error does not reference expanded home path: %v", err)
	}
}

func TestGenerateSSHKeyPairRequiresDestination(t *testing.T) {
	if _, err := GenerateSSHKeyPair(context.Background(), GenerateSSHKeyRequest{}); err == nil {
		t.Fatal("expected error for empty destination")
	}
}

func TestGenerateSSHKeyPairDefaultComment(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "id_ed25519_test")
	result, err := GenerateSSHKeyPair(context.Background(), GenerateSSHKeyRequest{Destination: destination})
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}
	if !strings.Contains(result.PublicKey, "tunnelboard") {
		t.Fatalf("default comment missing from public key: %q", result.PublicKey)
	}
}

func TestGenerateSSHKeyPairNormalizesCommentToOneLine(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "id_ed25519_test")
	result, err := GenerateSSHKeyPair(context.Background(), GenerateSSHKeyRequest{
		Destination: destination,
		Comment:     "  team\nwork  ",
	})
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}
	if result.PublicKey != strings.TrimSpace(result.PublicKey) || strings.ContainsAny(result.PublicKey, "\r\n") {
		t.Fatalf("public key is not one line: %q", result.PublicKey)
	}
	if !strings.HasSuffix(result.PublicKey, " team work") {
		t.Fatalf("comment = %q, want normalized comment", result.PublicKey)
	}
}

func TestGenerateSSHKeyPairSupportsPassphrase(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "id_ed25519_test")
	if _, err := GenerateSSHKeyPair(context.Background(), GenerateSSHKeyRequest{
		Destination: destination,
		Passphrase:  "test-passphrase",
	}); err != nil {
		t.Fatalf("generate key pair: %v", err)
	}
	raw, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ssh.ParsePrivateKey(raw); err == nil {
		t.Fatal("encrypted private key parsed without passphrase")
	}
	if _, err := ssh.ParsePrivateKeyWithPassphrase(raw, []byte("test-passphrase")); err != nil {
		t.Fatalf("parse encrypted private key: %v", err)
	}
}
