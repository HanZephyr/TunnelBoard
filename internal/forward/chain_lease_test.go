package forward

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestChainLeaseMultiHopProbeDetectsBlockedTail(t *testing.T) {
	tail := newFakeSSHClient()
	tail.blockSend = true
	lease := &chainLease{
		terminal: tail,
		probe:    true,
		interval: 5 * time.Millisecond,
		timeout:  20 * time.Millisecond,
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := lease.WaitLoss(ctx)
	if !errors.Is(err, ErrKeepAliveTimeout) {
		t.Fatalf("WaitLoss err = %v, want keepalive timeout", err)
	}
	if !tail.isClosed() {
		t.Fatal("tail timeout must close only the captured tail client")
	}
}

func TestChainLeaseSinglePooledHopDoesNotDuplicatePoolProbe(t *testing.T) {
	first := newFakeSSHClient()
	lease := &chainLease{
		terminal: first,
		probe:    false,
		interval: 5 * time.Millisecond,
		timeout:  20 * time.Millisecond,
	}
	go func() {
		time.Sleep(30 * time.Millisecond)
		first.kill(errors.New("pool generation closed"))
	}()
	err := lease.WaitLoss(context.Background())
	if err == nil {
		t.Fatal("first-hop loss must be reported")
	}
	if first.sendCount() != 0 {
		t.Fatalf("single pooled hop sent %d duplicate probes", first.sendCount())
	}
}
