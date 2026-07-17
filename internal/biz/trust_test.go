package biz_test

import (
	"errors"
	"testing"

	"github.com/HanZephyr/TunnelBoard/internal/model"
)

// 指纹变化必须阻断（TrustMismatch）；已登记端点不可重复 Enroll；
// 显式 Replace 后才放行新指纹，且保留首次信任时间。
func TestHostKeyMismatchAndReplace(t *testing.T) {
	c := newCatalog()
	enrolled, err := c.EnrollHostKey("10.0.0.1", 22, "ssh-ed25519", "SHA256:abc")
	if err != nil {
		t.Fatalf("EnrollHostKey: %v", err)
	}

	entry, status, err := c.HostKeyStatus("10.0.0.1", 22, "SHA256:evil")
	if err != nil {
		t.Fatalf("HostKeyStatus: %v", err)
	}
	if status != model.TrustMismatch || entry.FingerprintSHA256 != "SHA256:abc" {
		t.Fatalf("status = %q entry = %+v, want mismatch with stored fingerprint", status, entry)
	}

	if _, err := c.EnrollHostKey("10.0.0.1", 22, "ssh-ed25519", "SHA256:evil"); !errors.Is(err, model.ErrDuplicateHostKey) {
		t.Fatalf("re-enroll err = %v, want ErrDuplicateHostKey", err)
	}

	replaced, err := c.ReplaceHostKey("10.0.0.1", 22, "ssh-ed25519", "SHA256:evil")
	if err != nil {
		t.Fatalf("ReplaceHostKey: %v", err)
	}
	if replaced.FingerprintSHA256 != "SHA256:evil" {
		t.Fatalf("fingerprint not replaced: %+v", replaced)
	}
	if !replaced.FirstSeenAt.Equal(enrolled.FirstSeenAt) {
		t.Fatalf("FirstSeenAt must be preserved: got %v, want %v", replaced.FirstSeenAt, enrolled.FirstSeenAt)
	}

	if _, status, _ := c.HostKeyStatus("10.0.0.1", 22, "SHA256:evil"); status != model.TrustTrusted {
		t.Fatalf("after replace status = %q, want trusted", status)
	}
	data, _ := c.Data()
	if len(data.HostKeys) != 1 {
		t.Fatalf("replace must keep single entry per endpoint, got %d", len(data.HostKeys))
	}
}

// 首次见到的指纹为 TrustUnknown；用户确认后 Enroll 落库；再次核验一致即放行。
func TestHostKeyEnrollAndVerify(t *testing.T) {
	c := newCatalog()

	_, status, err := c.HostKeyStatus("10.0.0.1", 22, "SHA256:abc")
	if err != nil {
		t.Fatalf("HostKeyStatus: %v", err)
	}
	if status != model.TrustUnknown {
		t.Fatalf("status = %q, want unknown", status)
	}

	enrolled, err := c.EnrollHostKey("10.0.0.1", 22, "ssh-ed25519", "SHA256:abc")
	if err != nil {
		t.Fatalf("EnrollHostKey: %v", err)
	}
	if enrolled.ID == 0 || enrolled.FirstSeenAt.IsZero() || enrolled.LastSeenAt.IsZero() {
		t.Fatalf("enrolled entry incomplete: %+v", enrolled)
	}

	entry, status, err := c.HostKeyStatus("10.0.0.1", 22, "SHA256:abc")
	if err != nil {
		t.Fatalf("HostKeyStatus: %v", err)
	}
	if status != model.TrustTrusted || entry.ID != enrolled.ID {
		t.Fatalf("status = %q entry = %+v, want trusted id %d", status, entry, enrolled.ID)
	}

	// 不同端口是独立端点。
	if _, status, _ := c.HostKeyStatus("10.0.0.1", 2222, "SHA256:abc"); status != model.TrustUnknown {
		t.Fatalf("different port must be unknown, got %q", status)
	}
}
