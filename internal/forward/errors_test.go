package forward

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/HanZephyr/TunnelBoard/internal/model"

	"golang.org/x/crypto/ssh"
)

func TestIsTerminalError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "host key rejected wrapped",
			err:  fmt.Errorf("ssh dial example.com:22 failed: %w", fmt.Errorf("%w: fingerprint changed", ErrHostKeyRejected)),
			want: true,
		},
		{
			name: "auth failure text",
			err:  errors.New("ssh: handshake failed: ssh: unable to authenticate, attempted methods [none]"),
			want: true,
		},
		{
			name: "partial success error",
			err:  &ssh.PartialSuccessError{},
			want: true,
		},
		{
			name: "connection refused is retryable",
			err:  fmt.Errorf("dial tcp 127.0.0.1:22: connectex: connection refused"),
			want: false,
		},
		{
			name: "nil error",
			err:  nil,
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsTerminalError(tc.err); got != tc.want {
				t.Fatalf("IsTerminalError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// swapDialChain 替换拨号接缝并注册恢复。
func swapDialChain(t *testing.T, fake func(hosts []model.SSHHost, verifier HostKeyVerifier) (*ssh.Client, func(), error)) {
	t.Helper()
	orig := dialChain
	dialChain = fake
	t.Cleanup(func() { dialChain = orig })
}

func newReconnectTestForward() *LocalForward {
	lf := NewLocalForward(
		model.Forward{ID: 1, Name: "t", Mode: "local"},
		[]model.SSHHost{{Host: "example.com", User: "tester"}},
		func(host string, port int, key ssh.PublicKey) error { return nil },
		nil, // 默认拨号器：经 dialChain 变量独占拨号，shared 必为 false
	)
	// reconnectWithBackoff 以 keepStop 为停止信号；未 Start 的实例需要手动补一个。
	lf.keepStop = make(chan struct{})
	lf.runCtx = context.Background()
	return lf
}

func reconnectSeed(lf *LocalForward) ChainLease {
	return &chainLease{rebuild: func(ctx context.Context) (ChainLease, error) {
		return defaultChainDialer(lf.hosts, lf.verifier)
	}}
}

// 终态错误：第一次拨号失败即返回，错误原样上抛（errors.Is 命中），不进退避循环。
func TestReconnectWithBackoff_TerminalErrorAbortsImmediately(t *testing.T) {
	termErr := fmt.Errorf("%w: 10.0.0.1:22 fingerprint changed", ErrHostKeyRejected)
	calls := 0
	swapDialChain(t, func(hosts []model.SSHHost, verifier HostKeyVerifier) (*ssh.Client, func(), error) {
		calls++
		return nil, nil, termErr
	})

	lf := newReconnectTestForward()
	start := time.Now()
	lease, err := lf.reconnectWithBackoff(reconnectSeed(lf))
	elapsed := time.Since(start)

	if !errors.Is(err, ErrHostKeyRejected) {
		t.Fatalf("err = %v, want errors.Is ErrHostKeyRejected", err)
	}
	if !errors.Is(err, termErr) {
		t.Fatalf("err = %v, want errors.Is original terminal error", err)
	}
	if lease != nil {
		t.Fatalf("lease should be nil on terminal error")
	}
	if calls != 1 {
		t.Fatalf("dial calls = %d, want 1 (no retry after terminal error)", calls)
	}
	// 循环结构是先等待一个 initReconnectWait 再首次拨号；若误入退避继续重试，
	// 至少再花 1s 第二次等待。此处留出充分余量防止 flaky。
	if elapsed > 4*initReconnectWait {
		t.Fatalf("elapsed = %v, terminal error should return before further backoff", elapsed)
	}
}

// 非终态错误（如 connection refused）：不提前返回，按退避重试直至成功。
func TestReconnectWithBackoff_NonTerminalErrorRetries(t *testing.T) {
	calls := 0
	swapDialChain(t, func(hosts []model.SSHHost, verifier HostKeyVerifier) (*ssh.Client, func(), error) {
		calls++
		if calls == 1 {
			return nil, nil, fmt.Errorf("dial tcp 127.0.0.1:22: connectex: connection refused")
		}
		return &ssh.Client{}, func() {}, nil
	})

	lf := newReconnectTestForward()
	lease, err := lf.reconnectWithBackoff(reconnectSeed(lf))
	if err != nil {
		t.Fatalf("err = %v, want nil (retry should eventually succeed)", err)
	}
	if lease == nil || lease.Terminal() == nil {
		t.Fatalf("lease/terminal should be non-nil after successful retry")
	}
	lease.Release()
	if calls != 2 {
		t.Fatalf("dial calls = %d, want 2 (first fails, retry succeeds)", calls)
	}
}
