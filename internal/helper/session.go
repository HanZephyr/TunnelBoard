package helper

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

var ErrSessionClosed = errors.New("helper: privileged session is closed")

// SessionConnection 是一次已经过身份和协议校验的 Helper IPC 连接。
type SessionConnection interface {
	Call(ctx context.Context, request Request) (Response, error)
	Close(ctx context.Context) error
}

// SessionBackend 隐藏平台特有的提权启动、IPC、身份校验和握手。
type SessionBackend interface {
	Start(ctx context.Context) (SessionConnection, error)
}

type PrivilegedSession interface {
	Ensure(ctx context.Context) error
	Call(ctx context.Context, request Request) (Response, error)
	Close(ctx context.Context) error
}

type privilegedSession struct {
	mu      sync.Mutex
	backend SessionBackend
	conn    SessionConnection
	closed  bool
}

func NewPrivilegedSession(backend SessionBackend) PrivilegedSession {
	return &privilegedSession{backend: backend}
}

func (s *privilegedSession) Ensure(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ensureLocked(ctx)
}

func (s *privilegedSession) ensureLocked(ctx context.Context) error {
	if s.closed {
		return ErrSessionClosed
	}
	if s.conn != nil {
		return nil
	}
	if s.backend == nil {
		return errors.New("helper: privileged session backend is unavailable")
	}
	conn, err := s.backend.Start(ctx)
	if err != nil {
		return fmt.Errorf("helper: start privileged session: %w", err)
	}
	s.conn = conn
	return nil
}

func (s *privilegedSession) Call(ctx context.Context, request Request) (Response, error) {
	if err := ValidateRequest(request); err != nil {
		return Response{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureLocked(ctx); err != nil {
		return Response{}, err
	}
	response, err := s.conn.Call(ctx, request)
	if err != nil {
		broken := s.conn
		s.conn = nil
		_ = broken.Close(context.Background())
		return Response{}, err
	}
	if !response.OK {
		return response, errors.New(response.Error)
	}
	return response, nil
}

func (s *privilegedSession) Close(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.conn == nil {
		return nil
	}
	conn := s.conn
	s.conn = nil
	return conn.Close(ctx)
}
