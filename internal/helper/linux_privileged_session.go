package helper

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/HanZephyr/TunnelBoard/internal/route"
)

// Linux 特权协议不复用 Windows 命名管道协议：它只在主程序启动的短生命周期
// pkexec 子进程上运行，且每一条消息都绑定不可复用的会话 ID。
const (
	linuxPrivilegeProtocolVersion   = 1
	linuxPrivilegeSessionTTL        = 5 * time.Minute
	linuxAuthorizationRevokeTimeout = 5 * time.Second

	linuxPrivilegeApplyManagedHosts  = "apply_managed_hosts"
	linuxPrivilegeRemoveManagedHosts = "remove_managed_hosts"
	linuxPrivilegeTrustCaddyCA       = "trust_current_caddy_ca"
	linuxPrivilegeUntrustCaddyCA     = "untrust_current_caddy_ca"
	linuxPrivilegeRevoke             = "revoke"
)

type linuxPrivilegedRequest struct {
	Version               int               `json:"version"`
	SessionID             string            `json:"sessionId"`
	Operation             string            `json:"operation"`
	Hosts                 []route.HostEntry `json:"hosts,omitempty"`
	TransactionID         string            `json:"transactionId,omitempty"`
	ExpectedManagedDigest string            `json:"expectedManagedDigest,omitempty"`
	CertDER               []byte            `json:"certDer,omitempty"`
	Fingerprint           string            `json:"fingerprint,omitempty"`
}

type linuxPrivilegedResponse struct {
	OK            bool   `json:"ok"`
	ManagedDigest string `json:"managedDigest,omitempty"`
	Revoked       bool   `json:"revoked,omitempty"`
	Error         string `json:"error,omitempty"`
}

// LinuxPrivilegedEffects 是 root 子进程唯一允许执行的副作用面。
// 它没有命令、路径或 shell 参数，调用端只能表达已经确认的产品行为。
type LinuxPrivilegedEffects interface {
	ApplyManagedHosts(ctx context.Context, request Request) (Response, error)
	TrustCurrentCaddyCA(ctx context.Context, certDER []byte) error
	UntrustCurrentCaddyCA(ctx context.Context, fingerprint string) error
}

type linuxPrivilegedSessionServer struct {
	sessionID string
	effects   LinuxPrivilegedEffects
	revoked   bool
}

func newLinuxPrivilegedSessionServer(sessionID string, effects LinuxPrivilegedEffects) *linuxPrivilegedSessionServer {
	return &linuxPrivilegedSessionServer{sessionID: sessionID, effects: effects}
}

func (s *linuxPrivilegedSessionServer) handle(ctx context.Context, request linuxPrivilegedRequest) linuxPrivilegedResponse {
	if s.revoked {
		return linuxPrivilegeFailure(errors.New("helper: Linux privileged session is revoked"))
	}
	if request.Version != linuxPrivilegeProtocolVersion {
		return linuxPrivilegeFailure(fmt.Errorf("helper: unsupported Linux privilege protocol version %d", request.Version))
	}
	if request.SessionID == "" || request.SessionID != s.sessionID {
		return linuxPrivilegeFailure(errors.New("helper: Linux privileged session id does not match"))
	}
	if s.effects == nil {
		return linuxPrivilegeFailure(errors.New("helper: Linux privileged effects are unavailable"))
	}

	switch request.Operation {
	case linuxPrivilegeApplyManagedHosts:
		helperRequest := Request{
			Op:                    OpApplyManagedHosts,
			Hosts:                 request.Hosts,
			TransactionID:         request.TransactionID,
			ExpectedManagedDigest: request.ExpectedManagedDigest,
		}
		if err := ValidateRequest(helperRequest); err != nil {
			return linuxPrivilegeFailure(err)
		}
		response, err := s.effects.ApplyManagedHosts(ctx, helperRequest)
		if err != nil {
			return linuxPrivilegeFailure(err)
		}
		if !response.OK {
			return linuxPrivilegeFailure(errors.New(response.Error))
		}
		return linuxPrivilegedResponse{OK: true, ManagedDigest: response.ManagedDigest}
	case linuxPrivilegeRemoveManagedHosts:
		response, err := s.effects.ApplyManagedHosts(ctx, Request{Op: OpRemoveManagedHosts})
		if err != nil {
			return linuxPrivilegeFailure(err)
		}
		if !response.OK {
			return linuxPrivilegeFailure(errors.New(response.Error))
		}
		return linuxPrivilegedResponse{OK: true, ManagedDigest: response.ManagedDigest}
	case linuxPrivilegeTrustCaddyCA:
		if len(request.CertDER) == 0 || len(request.CertDER) > maxPrivilegedCertBytes {
			return linuxPrivilegeFailure(fmt.Errorf("helper: certificate DER size %d is outside allowed range", len(request.CertDER)))
		}
		fingerprint := sha256Fingerprint(request.CertDER)
		if err := ValidateLocalCA(request.CertDER, fingerprint); err != nil {
			return linuxPrivilegeFailure(err)
		}
		if err := s.effects.TrustCurrentCaddyCA(ctx, request.CertDER); err != nil {
			return linuxPrivilegeFailure(err)
		}
		return linuxPrivilegedResponse{OK: true}
	case linuxPrivilegeUntrustCaddyCA:
		if err := validateFingerprint(request.Fingerprint); err != nil {
			return linuxPrivilegeFailure(err)
		}
		if err := s.effects.UntrustCurrentCaddyCA(ctx, request.Fingerprint); err != nil {
			return linuxPrivilegeFailure(err)
		}
		return linuxPrivilegedResponse{OK: true}
	case linuxPrivilegeRevoke:
		s.revoked = true
		return linuxPrivilegedResponse{OK: true, Revoked: true}
	default:
		return linuxPrivilegeFailure(fmt.Errorf("helper: unsupported Linux privileged operation %q", request.Operation))
	}
}

func linuxPrivilegeFailure(err error) linuxPrivilegedResponse {
	return linuxPrivilegedResponse{OK: false, Error: err.Error()}
}

func sha256Fingerprint(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// linuxPrivilegedSessionConnection 是 GUI 到已授权 root 子进程的单一串行通道。
type linuxPrivilegedSessionConnection interface {
	Call(ctx context.Context, request linuxPrivilegedRequest) (linuxPrivilegedResponse, error)
	Close(ctx context.Context) error
}

type linuxPrivilegedSessionStarter interface {
	Start(ctx context.Context, sessionID, authorizationID string) (linuxPrivilegedSessionConnection, error)
}

// linuxPolkitAuthorizer 是对 Authority 的最小调用面。Authorize 必须返回 polkit
// 临时授权的 opaque ID；session 绝不把随机 IPC nonce 当作授权记录。
type linuxPolkitAuthorizer interface {
	Authorize(ctx context.Context) (string, error)
	Revoke(ctx context.Context, authorizationID string) error
}

type unavailableLinuxPolkitAuthorizer struct{}

func (unavailableLinuxPolkitAuthorizer) Authorize(context.Context) (string, error) {
	return "", errors.New("helper: Linux polkit authority adapter is unavailable")
}

func (unavailableLinuxPolkitAuthorizer) Revoke(context.Context, string) error { return nil }

type linuxPrivilegedSession struct {
	mu              sync.Mutex
	starter         linuxPrivilegedSessionStarter
	connection      linuxPrivilegedSessionConnection
	sessionID       string
	authorizationID string
	expiresAt       time.Time
	closed          bool
	now             func() time.Time
	newID           func() (string, error)
	authorizer      linuxPolkitAuthorizer
}

func newLinuxPrivilegedSession(starter linuxPrivilegedSessionStarter) *linuxPrivilegedSession {
	return newLinuxPrivilegedSessionWithAuthorizer(starter, unavailableLinuxPolkitAuthorizer{})
}

func newLinuxPrivilegedSessionWithAuthorizer(starter linuxPrivilegedSessionStarter, authorizer linuxPolkitAuthorizer) *linuxPrivilegedSession {
	if authorizer == nil {
		authorizer = unavailableLinuxPolkitAuthorizer{}
	}
	return &linuxPrivilegedSession{starter: starter, authorizer: authorizer, now: time.Now, newID: newLinuxPrivilegeSessionID}
}

func newLinuxPrivilegeSessionID() (string, error) {
	var value [32]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("helper: generate Linux privilege session id: %w", err)
	}
	return hex.EncodeToString(value[:]), nil
}

func (s *linuxPrivilegedSession) Call(ctx context.Context, request Request) (Response, error) {
	if err := ValidateRequest(request); err != nil {
		return Response{}, err
	}
	var operation string
	switch request.Op {
	case OpApplyManagedHosts:
		operation = linuxPrivilegeApplyManagedHosts
	case OpRemoveManagedHosts:
		operation = linuxPrivilegeRemoveManagedHosts
	default:
		return Response{}, fmt.Errorf("helper: unsupported Linux privileged request %q", request.Op)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Response{}, ErrSessionClosed
	}
	if err := s.ensureLocked(ctx); err != nil {
		return Response{}, err
	}
	response, err := s.callLocked(ctx, linuxPrivilegedRequest{
		Version:               linuxPrivilegeProtocolVersion,
		SessionID:             s.sessionID,
		Operation:             operation,
		Hosts:                 request.Hosts,
		TransactionID:         request.TransactionID,
		ExpectedManagedDigest: request.ExpectedManagedDigest,
	})
	if err != nil {
		return Response{}, err
	}
	return Response{OK: true, ManagedDigest: response.ManagedDigest}, nil
}

func (s *linuxPrivilegedSession) TrustCurrentCaddyCA(ctx context.Context, certDER []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrSessionClosed
	}
	if err := s.ensureLocked(ctx); err != nil {
		return err
	}
	_, err := s.callLocked(ctx, linuxPrivilegedRequest{
		Version: linuxPrivilegeProtocolVersion, SessionID: s.sessionID,
		Operation: linuxPrivilegeTrustCaddyCA, CertDER: append([]byte(nil), certDER...),
	})
	return err
}

func (s *linuxPrivilegedSession) UntrustCurrentCaddyCA(ctx context.Context, fingerprint string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrSessionClosed
	}
	if err := s.ensureLocked(ctx); err != nil {
		return err
	}
	_, err := s.callLocked(ctx, linuxPrivilegedRequest{
		Version: linuxPrivilegeProtocolVersion, SessionID: s.sessionID,
		Operation: linuxPrivilegeUntrustCaddyCA, Fingerprint: fingerprint,
	})
	return err
}

func (s *linuxPrivilegedSession) ensureLocked(ctx context.Context) error {
	if s.connection != nil && !s.now().Before(s.expiresAt) {
		if err := s.revokeAndCloseLocked(context.Background()); err != nil {
			return err
		}
	}
	if s.connection != nil {
		return nil
	}
	if s.starter == nil {
		return errors.New("helper: Linux privileged session starter is unavailable")
	}
	if s.authorizer == nil {
		return errors.New("helper: Linux polkit authority adapter is unavailable")
	}
	sessionID, err := s.newID()
	if err != nil {
		return err
	}
	authorizationID, err := s.authorizer.Authorize(ctx)
	if err != nil {
		return fmt.Errorf("helper: request Linux temporary authorization: %w", err)
	}
	if authorizationID == "" {
		return errors.New("helper: Linux polkit did not return a temporary authorization id")
	}
	connection, err := s.starter.Start(ctx, sessionID, authorizationID)
	if err != nil {
		_ = s.authorizer.Revoke(context.Background(), authorizationID)
		return fmt.Errorf("helper: start Linux privileged session: %w", err)
	}
	s.connection = connection
	s.sessionID = sessionID
	s.authorizationID = authorizationID
	s.expiresAt = s.now().Add(linuxPrivilegeSessionTTL)
	return nil
}

func (s *linuxPrivilegedSession) callLocked(ctx context.Context, request linuxPrivilegedRequest) (linuxPrivilegedResponse, error) {
	response, err := s.connection.Call(ctx, request)
	if err != nil {
		connection := s.connection
		authorizationID := s.authorizationID
		s.connection = nil
		s.sessionID = ""
		s.authorizationID = ""
		s.expiresAt = time.Time{}
		cleanupCtx, cancel := context.WithTimeout(context.Background(), linuxAuthorizationRevokeTimeout)
		closeErr := connection.Close(cleanupCtx)
		var authorizationErr error
		if authorizationID != "" && s.authorizer != nil {
			authorizationErr = s.authorizer.Revoke(cleanupCtx, authorizationID)
		}
		cancel()
		return linuxPrivilegedResponse{}, errors.Join(err, closeErr, authorizationErr)
	}
	if !response.OK {
		return linuxPrivilegedResponse{}, errors.New(response.Error)
	}
	return response, nil
}

func (s *linuxPrivilegedSession) Close(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	return s.revokeAndCloseLocked(ctx)
}

func (s *linuxPrivilegedSession) revokeAndCloseLocked(ctx context.Context) error {
	if s.connection == nil {
		return nil
	}
	connection := s.connection
	sessionID := s.sessionID
	authorizationID := s.authorizationID
	s.connection = nil
	s.sessionID = ""
	s.authorizationID = ""
	s.expiresAt = time.Time{}
	cleanupCtx, cancel := context.WithTimeout(context.Background(), linuxAuthorizationRevokeTimeout)
	defer cancel()
	_, revokeErr := connection.Call(cleanupCtx, linuxPrivilegedRequest{
		Version: linuxPrivilegeProtocolVersion, SessionID: sessionID, Operation: linuxPrivilegeRevoke,
	})
	closeErr := connection.Close(cleanupCtx)
	var authorizationErr error
	if authorizationID != "" && s.authorizer != nil {
		authorizationErr = s.authorizer.Revoke(cleanupCtx, authorizationID)
	}
	return errors.Join(revokeErr, closeErr, authorizationErr)
}
