package forward

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// ChainLease 隐藏 SSH 链的所有权和代际。调用方只使用末跳、等待当前链失效、
// 重建、释放，或在 Stop deadline 后精确终止捕获的首跳连接代。
type ChainLease interface {
	Terminal() *ssh.Client
	WaitLoss(context.Context) error
	Rebuild(context.Context) (ChainLease, error)
	Release()
	AbortGeneration()
}

type chainLease struct {
	terminal sshClient
	probe    bool
	interval time.Duration
	timeout  time.Duration
	release  func()
	abort    func()
	rebuild  func(context.Context) (ChainLease, error)
	once     sync.Once
}

func (l *chainLease) Terminal() *ssh.Client {
	client, _ := l.terminal.(*ssh.Client)
	return client
}

func (l *chainLease) WaitLoss(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	lost := make(chan error, 1)
	go func() {
		err := l.terminal.Wait()
		if err == nil {
			err = errors.New("ssh connection closed")
		}
		lost <- err
	}()
	if !l.probe || l.interval <= 0 {
		select {
		case <-ctx.Done():
			return nil
		case err := <-lost:
			return err
		}
	}
	for {
		timer := time.NewTimer(l.interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case err := <-lost:
			timer.Stop()
			return err
		case <-timer.C:
			if _, err := probeSSH(ctx, l.terminal, l.timeout); err != nil {
				_ = l.terminal.Close()
				return fmt.Errorf("ssh chain keepalive failed: %w", err)
			}
		}
	}
}

func (l *chainLease) Rebuild(ctx context.Context) (ChainLease, error) {
	if l.rebuild == nil {
		return nil, errors.New("ssh chain lease cannot be rebuilt")
	}
	return l.rebuild(ctx)
}

func (l *chainLease) Release() {
	l.once.Do(func() {
		if l.release != nil {
			l.release()
		}
	})
}

func (l *chainLease) AbortGeneration() {
	if l.abort != nil {
		l.abort()
	}
}
