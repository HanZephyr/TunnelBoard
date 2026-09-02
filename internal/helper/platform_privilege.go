package helper

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	maxPrivilegedHostsBytes = 1 << 20
	maxPrivilegedCertBytes  = 16 << 10
	darwinSystemKeychain    = "/Library/Keychains/System.keychain"
)

// CommandRunner 是 Unix 提权 Adapter 唯一的进程执行 seam。
// executable 必须是 Adapter 内钉死的绝对路径；动态值只能作为独立 argv。
type CommandRunner interface {
	Run(ctx context.Context, executable string, args ...string) ([]byte, error)
}

type PlatformPrivilegeOptions struct {
	Platform string
	TempRoot string
	Runner   CommandRunner
}

type PlatformPrivilege interface {
	ApplyManagedHosts(ctx context.Context, content []byte) error
	TrustLocalCA(ctx context.Context, certDER []byte) error
	UntrustLocalCA(ctx context.Context, fingerprint string) error
}

type platformPrivilege struct {
	platform string
	tempRoot string
	runner   CommandRunner
}

func NewPlatformPrivilege(options PlatformPrivilegeOptions) (PlatformPrivilege, error) {
	if options.Platform == "linux" {
		return nil, errors.New("helper: Linux uses the restricted polkit session adapter, not direct privileged commands")
	}
	if options.Platform != "darwin" {
		return nil, fmt.Errorf("helper: unsupported privilege platform %q", options.Platform)
	}
	if options.Runner == nil {
		options.Runner = execCommandRunner{}
	}
	return &platformPrivilege{platform: options.Platform, tempRoot: options.TempRoot, runner: options.Runner}, nil
}

type execCommandRunner struct{}

func (execCommandRunner) Run(ctx context.Context, executable string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, executable, args...).CombinedOutput()
}

func (p *platformPrivilege) ApplyManagedHosts(ctx context.Context, content []byte) error {
	if len(content) > maxPrivilegedHostsBytes {
		return fmt.Errorf("helper: managed hosts content too large: %d bytes", len(content))
	}
	tempPath, cleanup, err := p.writePrivateTemp("hosts", content)
	if err != nil {
		return err
	}
	defer cleanup()
	return p.runDarwin(ctx, "copy", tempPath, "/etc/hosts", "/bin/cp")
}

func (p *platformPrivilege) TrustLocalCA(ctx context.Context, certDER []byte) error {
	if len(certDER) == 0 || len(certDER) > maxPrivilegedCertBytes {
		return fmt.Errorf("helper: certificate DER size %d is outside allowed range", len(certDER))
	}
	sum := sha256.Sum256(certDER)
	if err := ValidateLocalCA(certDER, hex.EncodeToString(sum[:])); err != nil {
		return err
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	tempPath, cleanup, err := p.writePrivateTemp("ca.pem", pemBytes)
	if err != nil {
		return err
	}
	defer cleanup()

	return p.runDarwin(ctx, "trust-ca", tempPath, darwinSystemKeychain, "/usr/bin/security")
}

func (p *platformPrivilege) UntrustLocalCA(ctx context.Context, fingerprint string) error {
	if err := validateFingerprint(fingerprint); err != nil {
		return err
	}
	// delete-certificate -Z 只接受 SHA-1；调用方持有的是 LocalCATrust 的 SHA-256。
	entries, err := listKeychainCertificates(ctx, p.runner, darwinSystemKeychain)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.sha256 != fingerprint {
			continue
		}
		if err := validateSHA1Fingerprint(entry.sha1); err != nil {
			return err
		}
		return p.runDarwin(ctx, "untrust-ca", entry.sha1, darwinSystemKeychain, "/usr/bin/security")
	}
	return nil
}

func (p *platformPrivilege) writePrivateTemp(kind string, content []byte) (string, func(), error) {
	dir, err := os.MkdirTemp(p.tempRoot, "tunnelboard-privilege-*")
	if err != nil {
		return "", nil, fmt.Errorf("helper: create private temp dir: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	if err := os.Chmod(dir, 0o700); err != nil {
		cleanup()
		return "", nil, err
	}
	path := filepath.Join(dir, kind)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		cleanup()
		return "", nil, err
	}
	if _, err := f.Write(content); err != nil {
		_ = f.Close()
		cleanup()
		return "", nil, err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		cleanup()
		return "", nil, err
	}
	if err := f.Close(); err != nil {
		cleanup()
		return "", nil, err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		cleanup()
		return "", nil, errors.New("helper: private temp input is not a regular file")
	}
	return path, cleanup, nil
}

func (p *platformPrivilege) run(ctx context.Context, executable string, args ...string) error {
	out, err := p.runner.Run(ctx, executable, args...)
	if err != nil {
		return fmt.Errorf("helper: privileged command %s failed: %w: %s", executable, err, strings.TrimSpace(string(out)))
	}
	return nil
}

const darwinPrivilegeScript = `on run argv
set operation to item 1 of argv
set valueOne to item 2 of argv
set valueTwo to item 3 of argv
set executablePath to item 4 of argv
if operation is "copy" then
  set commandText to executablePath & " -- " & quoted form of valueOne & " " & quoted form of valueTwo
else if operation is "trust-ca" then
  set commandText to executablePath & " add-trusted-cert -d -r trustRoot -k " & quoted form of valueTwo & " " & quoted form of valueOne
else if operation is "untrust-ca" then
  set commandText to executablePath & " delete-certificate -Z " & quoted form of valueOne & " " & quoted form of valueTwo
else
  error "unsupported TunnelBoard privilege operation"
end if
do shell script commandText with administrator privileges
end run`

func (p *platformPrivilege) runDarwin(ctx context.Context, operation, valueOne, valueTwo, executable string) error {
	return p.run(ctx, "/usr/bin/osascript", "-e", darwinPrivilegeScript, "--", operation, valueOne, valueTwo, executable)
}

func validateFingerprint(value string) error {
	if len(value) != sha256.Size*2 {
		return errors.New("helper: fingerprint must be 64 lowercase hexadecimal characters")
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return errors.New("helper: fingerprint must be 64 lowercase hexadecimal characters")
		}
	}
	return nil
}

func validateSHA1Fingerprint(value string) error {
	if len(value) != 40 {
		return errors.New("helper: certificate SHA-1 must be 40 hexadecimal characters")
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') && (char < 'A' || char > 'F') {
			return errors.New("helper: certificate SHA-1 must be 40 hexadecimal characters")
		}
	}
	return nil
}

func compensationError(errs ...error) error {
	var wrapped []error
	for _, err := range errs {
		if err != nil {
			wrapped = append(wrapped, fmt.Errorf("helper: privilege compensation failed: %w", err))
		}
	}
	return errors.Join(wrapped...)
}
