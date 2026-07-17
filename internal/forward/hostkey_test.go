package forward

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"

	"github.com/HanZephyr/TunnelBoard/internal/model"

	"golang.org/x/crypto/ssh"
)

func testSSHPublicKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate ed25519 key failed: %v", err)
	}
	key, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("build ssh public key failed: %v", err)
	}
	return key
}

func TestHostKeyCallback_AllowPassesVerifierArgs(t *testing.T) {
	key := testSSHPublicKey(t)
	var gotHost string
	var gotPort int
	var gotKey ssh.PublicKey
	cb, err := makeHostKeyCallback("example.com", 2222, func(host string, port int, k ssh.PublicKey) error {
		gotHost, gotPort, gotKey = host, port, k
		return nil
	})
	if err != nil {
		t.Fatalf("makeHostKeyCallback failed: %v", err)
	}

	if err := cb("example.com:2222", nil, key); err != nil {
		t.Fatalf("callback returned error: %v", err)
	}
	if gotHost != "example.com" || gotPort != 2222 {
		t.Fatalf("verifier received %s:%d, want example.com:2222", gotHost, gotPort)
	}
	if gotKey == nil || string(gotKey.Marshal()) != string(key.Marshal()) {
		t.Fatalf("verifier received a different public key")
	}
}

func TestHostKeyCallback_RejectPropagatesError(t *testing.T) {
	key := testSSHPublicKey(t)
	want := errors.New("host key mismatch")
	cb, err := makeHostKeyCallback("example.com", 22, func(host string, port int, k ssh.PublicKey) error {
		return want
	})
	if err != nil {
		t.Fatalf("makeHostKeyCallback failed: %v", err)
	}

	if err := cb("example.com:22", nil, key); !errors.Is(err, want) {
		t.Fatalf("callback error = %v, want %v", err, want)
	}
}

func TestHostKeyCallback_RejectWrapsSentinel(t *testing.T) {
	key := testSSHPublicKey(t)
	want := errors.New("host key mismatch")
	cb, err := makeHostKeyCallback("example.com", 22, func(host string, port int, k ssh.PublicKey) error {
		return want
	})
	if err != nil {
		t.Fatalf("makeHostKeyCallback failed: %v", err)
	}

	cbErr := cb("example.com:22", nil, key)
	if !errors.Is(cbErr, ErrHostKeyRejected) {
		t.Fatalf("callback error = %v, want errors.Is ErrHostKeyRejected", cbErr)
	}
	if !errors.Is(cbErr, want) {
		t.Fatalf("callback error = %v, want errors.Is original verifier error", cbErr)
	}
}

func TestMakeHostKeyCallback_NilVerifierFailsClosed(t *testing.T) {
	if _, err := makeHostKeyCallback("example.com", 22, nil); err == nil {
		t.Fatalf("expected error when verifier is nil")
	}
}

func TestMakeSSHClientConfig_NilVerifierFailsClosed(t *testing.T) {
	host := model.SSHHost{
		Host:     "example.com",
		Port:     22,
		User:     "tester",
		AuthType: "password",
		Password: "secret",
	}
	if _, err := makeSSHClientConfig(host, nil); err == nil {
		t.Fatalf("expected error when verifier is nil")
	}
}
