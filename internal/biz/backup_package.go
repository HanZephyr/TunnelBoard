package biz

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/HanZephyr/TunnelBoard/internal/model"
	"github.com/HanZephyr/TunnelBoard/internal/vault"
)

type StagePurpose string

const (
	StagePurposeImport  StagePurpose = "import"
	StagePurposeRestore StagePurpose = "restore"
	backupStageTTL                   = 10 * time.Minute
)

var (
	ErrBackupPackageTooLarge = errors.New("biz: backup package exceeds 64 MiB")
	ErrBackupStageToken      = errors.New("biz: backup stage token is invalid")
	ErrBackupStageExpired    = errors.New("biz: backup stage token expired")
	ErrBackupStagePurpose    = errors.New("biz: backup stage purpose mismatch")
	ErrBackupStageStale      = errors.New("biz: backup stage is stale")
)

type StageRequest struct {
	Path          string
	Password      string
	Purpose       StagePurpose
	VaultRevision string
}

type BackupEntityCounts struct {
	Folders   int `json:"folders"`
	SSHHosts  int `json:"sshHosts"`
	Forwards  int `json:"forwards"`
	WebRoutes int `json:"webRoutes"`
	HostKeys  int `json:"hostKeys"`
	KeyFiles  int `json:"keyFiles"`
}

// StagePreview deliberately contains no password, decrypted entity, key material or source path.
type StagePreview struct {
	Token      string             `json:"token"`
	ExpiresAt  time.Time          `json:"expiresAt"`
	Counts     BackupEntityCounts `json:"counts"`
	FileDigest string             `json:"fileDigest"`
}

type TakeStageRequest struct {
	Token         string
	Purpose       StagePurpose
	VaultRevision string
}

// StagedBackup is backend-only ownership transferred by Take. It must never cross into WebView DTOs.
type StagedBackup struct {
	Vault      model.VaultData
	KeyFiles   map[string][]byte
	FileDigest string
}

type BackupPackage interface {
	Stage(context.Context, StageRequest) (StagePreview, error)
	Take(context.Context, TakeStageRequest) (StagedBackup, error)
	Cancel(token string)
}

type backupPackageOption func(*backupPackage)

func WithBackupClock(now func() time.Time) backupPackageOption {
	return func(p *backupPackage) { p.now = now }
}

type stagedPackage struct {
	token         string
	purpose       StagePurpose
	vaultRevision string
	appGeneration string
	expiresAt     time.Time
	fileDigest    string
	vault         model.VaultData
	keyFiles      map[string][]byte
}

type backupPackage struct {
	mu            sync.Mutex
	appGeneration string
	now           func() time.Time
	staged        *stagedPackage
}

func NewBackupPackage(appGeneration string, options ...backupPackageOption) BackupPackage {
	p := &backupPackage{appGeneration: appGeneration, now: time.Now}
	for _, option := range options {
		option(p)
	}
	return p
}

func (p *backupPackage) Stage(ctx context.Context, request StageRequest) (StagePreview, error) {
	if request.Purpose != StagePurposeImport && request.Purpose != StagePurposeRestore {
		return StagePreview{}, fmt.Errorf("%w: %q", ErrBackupStagePurpose, request.Purpose)
	}
	// Starting a new valid Stage attempt revokes any prior preview even when the new file later fails.
	p.Cancel("")
	raw, err := readBackupPackage(ctx, request.Path)
	if err != nil {
		return StagePreview{}, err
	}
	defer clearBytes(raw)
	if err := ctx.Err(); err != nil {
		return StagePreview{}, err
	}
	data, keyFiles, err := vault.ParseBackup(raw, request.Password)
	if err != nil {
		return StagePreview{}, err
	}
	if err := data.Validate(); err != nil {
		clearKeyFiles(keyFiles)
		return StagePreview{}, fmt.Errorf("biz: backup content invalid: %w", err)
	}
	if err := ctx.Err(); err != nil {
		clearKeyFiles(keyFiles)
		return StagePreview{}, err
	}
	tokenBytes := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, tokenBytes); err != nil {
		clearKeyFiles(keyFiles)
		return StagePreview{}, fmt.Errorf("biz: create backup stage token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(tokenBytes)
	clearBytes(tokenBytes)
	digest := sha256.Sum256(raw)
	now := p.now()
	staged := &stagedPackage{
		token: token, purpose: request.Purpose, vaultRevision: request.VaultRevision,
		appGeneration: p.appGeneration, expiresAt: now.Add(backupStageTTL),
		fileDigest: hex.EncodeToString(digest[:]), vault: data, keyFiles: keyFiles,
	}
	p.mu.Lock()
	p.clearLocked()
	p.staged = staged
	p.mu.Unlock()
	return StagePreview{
		Token: token, ExpiresAt: staged.expiresAt, FileDigest: staged.fileDigest,
		Counts: BackupEntityCounts{
			Folders: len(data.Folders), SSHHosts: len(data.SSHHosts), Forwards: len(data.Forwards),
			WebRoutes: len(data.WebRoutes), HostKeys: len(data.HostKeys), KeyFiles: len(keyFiles),
		},
	}, nil
}

func (p *backupPackage) Take(ctx context.Context, request TakeStageRequest) (StagedBackup, error) {
	if err := ctx.Err(); err != nil {
		return StagedBackup{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	staged := p.staged
	if staged == nil || request.Token == "" || request.Token != staged.token || staged.appGeneration != p.appGeneration {
		return StagedBackup{}, ErrBackupStageToken
	}
	if p.now().After(staged.expiresAt) {
		p.clearLocked()
		return StagedBackup{}, ErrBackupStageExpired
	}
	if request.Purpose != staged.purpose {
		return StagedBackup{}, ErrBackupStagePurpose
	}
	if request.VaultRevision != staged.vaultRevision {
		p.clearLocked()
		return StagedBackup{}, ErrBackupStageStale
	}
	result := StagedBackup{Vault: staged.vault, KeyFiles: staged.keyFiles, FileDigest: staged.fileDigest}
	staged.vault = model.VaultData{}
	staged.keyFiles = nil
	p.staged = nil
	return result, nil
}

func (p *backupPackage) Cancel(token string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.staged != nil && (token == "" || token == p.staged.token) {
		p.clearLocked()
	}
}

func (p *backupPackage) clearLocked() {
	if p.staged == nil {
		return
	}
	clearKeyFiles(p.staged.keyFiles)
	p.staged.vault = model.VaultData{}
	p.staged = nil
}

func readBackupPackage(ctx context.Context, path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("biz: open backup package: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("biz: stat backup package: %w", err)
	}
	if info.Size() > vault.MaxBackupPackageBytes {
		return nil, ErrBackupPackageTooLarge
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(io.LimitReader(f, vault.MaxBackupPackageBytes+1))
	if err != nil {
		return nil, fmt.Errorf("biz: read backup package: %w", err)
	}
	if len(raw) > vault.MaxBackupPackageBytes {
		clearBytes(raw)
		return nil, ErrBackupPackageTooLarge
	}
	return raw, nil
}

func clearKeyFiles(files map[string][]byte) {
	for path, content := range files {
		clearBytes(content)
		delete(files, path)
	}
}

func clearBytes(value []byte) {
	for i := range value {
		value[i] = 0
	}
}
