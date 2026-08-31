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
	publicKeyDestination := destination + ".pub"
	if result.PublicKeyPath != filepath.Clean(publicKeyDestination) {
		t.Fatalf("public key path = %q, want %q", result.PublicKeyPath, filepath.Clean(publicKeyDestination))
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
	publicRaw, err := os.ReadFile(publicKeyDestination)
	if err != nil {
		t.Fatalf("read public key: %v", err)
	}
	if string(publicRaw) != result.PublicKey+"\n" {
		t.Fatalf("public key file = %q, want %q", publicRaw, result.PublicKey+"\n")
	}
	parsedPublic, _, _, _, err := ssh.ParseAuthorizedKey(publicRaw)
	if err != nil {
		t.Fatalf("parse public key file: %v", err)
	}
	if strings.TrimSpace(string(ssh.MarshalAuthorizedKey(parsedPublic))) != string(authorizedKeyBytes(signer.PublicKey())) {
		t.Fatalf("public key file does not match private key")
	}

	if runtime.GOOS != "windows" {
		info, err := os.Stat(destination)
		if err != nil {
			t.Fatalf("stat private key: %v", err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("private key perm = %o, want 600", perm)
		}
		publicInfo, err := os.Stat(publicKeyDestination)
		if err != nil {
			t.Fatalf("stat public key: %v", err)
		}
		if perm := publicInfo.Mode().Perm(); perm != 0o644 {
			t.Fatalf("public key perm = %o, want 644", perm)
		}
	}
}

func authorizedKeyBytes(public ssh.PublicKey) []byte {
	return []byte(strings.TrimSpace(string(ssh.MarshalAuthorizedKey(public))))
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
	if _, err := os.Stat(destination + ".pub"); err != nil {
		t.Fatalf("generated public key missing after first generation: %v", err)
	}
}

func TestGenerateSSHKeyPairRefusesExistingPublicKey(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "id_ed25519_test")
	publicDestination := destination + ".pub"
	want := []byte("existing public key\n")
	if err := os.WriteFile(publicDestination, want, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := GenerateSSHKeyPair(context.Background(), GenerateSSHKeyRequest{Destination: destination})
	if !errors.Is(err, ErrSSHKeyExists) {
		t.Fatalf("error = %v, want ErrSSHKeyExists", err)
	}
	if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("private key should not be created when public key exists: %v", err)
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

func TestGenerateSSHKeyPairOverwritesAfterExplicitConfirmation(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "id_ed25519_test")
	if err := os.WriteFile(destination, []byte("old private key"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination+".pub", []byte("old public key\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := GenerateSSHKeyPair(context.Background(), GenerateSSHKeyRequest{
		Destination: destination,
		Overwrite:   true,
		Comment:     "replacement@test",
	})
	if err != nil {
		t.Fatalf("overwrite generate: %v", err)
	}
	privateRaw, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(privateRaw) == "old private key" {
		t.Fatal("private key was not replaced")
	}
	publicRaw, err := os.ReadFile(destination + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	if string(publicRaw) != result.PublicKey+"\n" || !strings.HasSuffix(result.PublicKey, " replacement@test") {
		t.Fatalf("public key was not replaced: %q", publicRaw)
	}
	if _, err := ssh.ParsePrivateKey(privateRaw); err != nil {
		t.Fatalf("replacement private key is not parseable: %v", err)
	}
}

func TestGenerateSSHKeyPairExpandsHomePath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("resolve home dir: %v", err)
	}
	got, err := expandHomePath("~/.ssh")
	if err != nil {
		t.Fatalf("expand home path: %v", err)
	}
	want := filepath.Join(home, ".ssh")
	if got != want {
		t.Fatalf("expanded path=%q, want %q", got, want)
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
