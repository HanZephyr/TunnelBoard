package helper

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// CertificateStore 是操作系统当前用户证书存储的最小 Adapter seam。
// SHA-256 指纹由 LocalCATrust 从固定 Caddy authority 计算，业务调用方不能提供。
type CertificateStore interface {
	ContainsSHA256(ctx context.Context, fingerprint string) (bool, error)
	AddDER(ctx context.Context, certDER []byte) error
	RemoveSHA256(ctx context.Context, fingerprint string) error
}

type CAIdentity struct {
	SHA256 string `json:"sha256"`
}

type CATrustState string

const (
	CATrusted              CATrustState = "trusted"
	CAConfirmationRequired CATrustState = "confirmation_required"
	CAUnavailable          CATrustState = "unavailable"
)

type CATrustStatus struct {
	State    CATrustState `json:"state"`
	Identity CAIdentity   `json:"identity"`
}

// LocalCATrust 只从构造时钉死的当前用户 Caddy authority 读取证书。
// 公开操作不接受 DER、路径或指纹，避免把任意根证书写入能力暴露给调用者。
type LocalCATrust interface {
	EnsureCurrentCaddyCATrusted(ctx context.Context) (CAIdentity, error)
	RemoveCurrentCaddyCA(ctx context.Context) error
	Status(ctx context.Context) (CATrustStatus, error)
}

type localCATrust struct {
	authorityPath string
	recordPath    string
	store         CertificateStore
	now           func() time.Time
}

// NewLocalCATrust 连接固定 authority、当前用户信任记录与平台证书存储。
// authorityPath/recordPath 仅在应用组装处确定，不属于业务请求参数。
func NewLocalCATrust(authorityPath, recordPath string, store CertificateStore) LocalCATrust {
	return &localCATrust{authorityPath: authorityPath, recordPath: recordPath, store: store, now: time.Now}
}

type caTrustRecord struct {
	Schema      int       `json:"schema"`
	SHA256      string    `json:"sha256"`
	ConfirmedAt time.Time `json:"confirmedAt"`
}

func (t *localCATrust) EnsureCurrentCaddyCATrusted(ctx context.Context) (CAIdentity, error) {
	identity, der, err := t.currentAuthority(ctx)
	if err != nil {
		return CAIdentity{}, err
	}
	present, err := t.store.ContainsSHA256(ctx, identity.SHA256)
	if err != nil {
		return CAIdentity{}, fmt.Errorf("helper: query CurrentUser Root: %w", err)
	}
	if !present {
		if err := t.store.AddDER(ctx, der); err != nil {
			return CAIdentity{}, fmt.Errorf("helper: add CA to CurrentUser Root: %w", err)
		}
	}
	if err := t.writeRecord(caTrustRecord{Schema: 1, SHA256: identity.SHA256, ConfirmedAt: t.now().UTC()}); err != nil {
		if !present {
			_ = t.store.RemoveSHA256(context.Background(), identity.SHA256)
		}
		return CAIdentity{}, fmt.Errorf("helper: persist current-user CA record: %w", err)
	}
	return identity, nil
}

func (t *localCATrust) RemoveCurrentCaddyCA(ctx context.Context) error {
	record, err := t.readRecord()
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("helper: read current-user CA record: %w", err)
	}
	if err := t.store.RemoveSHA256(ctx, record.SHA256); err != nil {
		return fmt.Errorf("helper: remove CA from CurrentUser Root: %w", err)
	}
	if err := os.Remove(t.recordPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("helper: remove current-user CA record: %w", err)
	}
	return nil
}

func (t *localCATrust) Status(ctx context.Context) (CATrustStatus, error) {
	identity, _, err := t.currentAuthority(ctx)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return CATrustStatus{State: CAUnavailable}, nil
		}
		return CATrustStatus{}, err
	}
	record, recordErr := t.readRecord()
	present, err := t.store.ContainsSHA256(ctx, identity.SHA256)
	if err != nil {
		return CATrustStatus{}, fmt.Errorf("helper: query CurrentUser Root: %w", err)
	}
	if recordErr == nil && record.Schema == 1 && record.SHA256 == identity.SHA256 && present {
		return CATrustStatus{State: CATrusted, Identity: identity}, nil
	}
	if recordErr != nil && !errors.Is(recordErr, os.ErrNotExist) {
		return CATrustStatus{}, fmt.Errorf("helper: read current-user CA record: %w", recordErr)
	}
	return CATrustStatus{State: CAConfirmationRequired, Identity: identity}, nil
}

func (t *localCATrust) currentAuthority(ctx context.Context) (CAIdentity, []byte, error) {
	if err := ctx.Err(); err != nil {
		return CAIdentity{}, nil, err
	}
	data, err := os.ReadFile(t.authorityPath)
	if err != nil {
		return CAIdentity{}, nil, fmt.Errorf("helper: read current Caddy CA: %w", err)
	}
	block, rest := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" || len(rest) != 0 {
		return CAIdentity{}, nil, errors.New("helper: current Caddy CA must contain exactly one PEM certificate")
	}
	sum := sha256.Sum256(block.Bytes)
	fingerprint := hex.EncodeToString(sum[:])
	if err := ValidateLocalCA(block.Bytes, fingerprint); err != nil {
		return CAIdentity{}, nil, err
	}
	return CAIdentity{SHA256: fingerprint}, block.Bytes, nil
}

func (t *localCATrust) readRecord() (caTrustRecord, error) {
	data, err := os.ReadFile(t.recordPath)
	if err != nil {
		return caTrustRecord{}, err
	}
	var record caTrustRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return caTrustRecord{}, err
	}
	if record.Schema != 1 || len(record.SHA256) != sha256.Size*2 {
		return caTrustRecord{}, errors.New("unsupported or invalid CA trust record")
	}
	return record, nil
}

func (t *localCATrust) writeRecord(record caTrustRecord) error {
	if err := os.MkdirAll(filepath.Dir(t.recordPath), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(t.recordPath), ".ca-trust-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, t.recordPath)
}
