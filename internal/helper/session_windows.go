//go:build windows

package helper

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	winio "github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

const SessionProtocolVersion = "1"
const sessionShutdownOp = "__session_shutdown"

type elevatedProcess interface {
	PID() uint32
	Wait(ctx context.Context) error
	Close() error
}

type windowsSessionBackend struct {
	timeout  time.Duration
	ownerSID func() (string, error)
	launch   func(pipePath string, parentPID uint32, protocol string) (elevatedProcess, error)
}

func newWindowsSessionBackend() SessionBackend {
	return &windowsSessionBackend{
		timeout:  15 * time.Second,
		ownerSID: CurrentUserSID,
		launch:   launchElevatedSessionHelper,
	}
}

type sessionHello struct {
	Protocol  string `json:"protocol"`
	ParentPID uint32 `json:"parentPid"`
}

type sessionHelloResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

func (b *windowsSessionBackend) Start(ctx context.Context) (SessionConnection, error) {
	timeout := b.timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	ownerSID, err := b.ownerSID()
	if err != nil {
		return nil, err
	}
	pipePath, err := randomSessionPipePath()
	if err != nil {
		return nil, err
	}
	sddl, err := pipeSDDL(ownerSID)
	if err != nil {
		return nil, err
	}
	listener, err := winio.ListenPipe(pipePath, &winio.PipeConfig{
		SecurityDescriptor: sddl,
		MessageMode:        false,
		InputBufferSize:    64 << 10,
		OutputBufferSize:   64 << 10,
	})
	if err != nil {
		return nil, fmt.Errorf("helper: create session pipe: %w", err)
	}
	defer listener.Close()

	process, err := b.launch(pipePath, uint32(os.Getpid()), SessionProtocolVersion)
	if err != nil {
		return nil, err
	}
	keepProcess := false
	defer func() {
		if !keepProcess {
			_ = process.Close()
		}
	}()

	acceptCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	accepted := make(chan struct {
		conn net.Conn
		err  error
	}, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		accepted <- struct {
			conn net.Conn
			err  error
		}{conn: conn, err: acceptErr}
	}()
	var conn net.Conn
	select {
	case result := <-accepted:
		if result.err != nil {
			return nil, fmt.Errorf("helper: accept session pipe: %w", result.err)
		}
		conn = result.conn
	case <-acceptCtx.Done():
		return nil, fmt.Errorf("helper: wait for elevated helper connection: %w", acceptCtx.Err())
	}

	closeConn := true
	defer func() {
		if closeConn {
			_ = conn.Close()
		}
	}()
	clientPID, err := namedPipeClientPID(conn)
	if err != nil {
		return nil, err
	}
	if clientPID != process.PID() {
		return nil, fmt.Errorf("helper: named-pipe client PID %d does not match launched helper PID %d", clientPID, process.PID())
	}
	_ = conn.SetDeadline(time.Now().Add(timeout))
	decoder := json.NewDecoder(bufio.NewReader(conn))
	encoder := json.NewEncoder(conn)
	var hello sessionHello
	if err := decoder.Decode(&hello); err != nil {
		return nil, fmt.Errorf("helper: read session handshake: %w", err)
	}
	if hello.Protocol != SessionProtocolVersion || hello.ParentPID != uint32(os.Getpid()) {
		_ = encoder.Encode(sessionHelloResponse{OK: false, Error: "protocol or parent identity mismatch"})
		return nil, fmt.Errorf("helper: session handshake mismatch (protocol=%q parent=%d)", hello.Protocol, hello.ParentPID)
	}
	if err := encoder.Encode(sessionHelloResponse{OK: true}); err != nil {
		return nil, fmt.Errorf("helper: acknowledge session handshake: %w", err)
	}
	_ = conn.SetDeadline(time.Time{})
	keepProcess = true
	closeConn = false
	return &windowsSessionConnection{conn: conn, decoder: decoder, encoder: encoder, process: process, timeout: timeout}, nil
}

func randomSessionPipePath() (string, error) {
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", fmt.Errorf("helper: generate session pipe name: %w", err)
	}
	return `\\.\pipe\tunnelboard-helper-` + hex.EncodeToString(token[:]), nil
}

func namedPipeClientPID(conn net.Conn) (uint32, error) {
	handleProvider, ok := conn.(interface{ Fd() uintptr })
	if !ok {
		return 0, errors.New("helper: named-pipe connection does not expose a Windows handle")
	}
	var pid uint32
	if err := windows.GetNamedPipeClientProcessId(windows.Handle(handleProvider.Fd()), &pid); err != nil {
		return 0, fmt.Errorf("helper: get named-pipe client PID: %w", err)
	}
	return pid, nil
}

func namedPipeServerPID(conn net.Conn) (uint32, error) {
	handleProvider, ok := conn.(interface{ Fd() uintptr })
	if !ok {
		return 0, errors.New("helper: named-pipe connection does not expose a Windows handle")
	}
	var pid uint32
	if err := windows.GetNamedPipeServerProcessId(windows.Handle(handleProvider.Fd()), &pid); err != nil {
		return 0, fmt.Errorf("helper: get named-pipe server PID: %w", err)
	}
	return pid, nil
}

type windowsSessionConnection struct {
	mu      sync.Mutex
	conn    net.Conn
	decoder *json.Decoder
	encoder *json.Encoder
	process elevatedProcess
	timeout time.Duration
	closed  bool
}

func (c *windowsSessionConnection) Call(ctx context.Context, request Request) (Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return Response{}, ErrSessionClosed
	}
	_ = c.conn.SetDeadline(sessionDeadline(ctx, c.timeout))
	if err := c.encoder.Encode(request); err != nil {
		return Response{}, fmt.Errorf("helper: write session request: %w", err)
	}
	var response Response
	if err := c.decoder.Decode(&response); err != nil {
		return Response{}, fmt.Errorf("helper: read session response: %w", err)
	}
	_ = c.conn.SetDeadline(time.Time{})
	return response, nil
}

func (c *windowsSessionConnection) Close(ctx context.Context) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	_ = c.conn.SetDeadline(sessionDeadline(ctx, 3*time.Second))
	_ = c.encoder.Encode(Request{Op: sessionShutdownOp})
	var response Response
	_ = c.decoder.Decode(&response)
	_ = c.conn.Close()
	c.mu.Unlock()
	waitErr := c.process.Wait(ctx)
	closeErr := c.process.Close()
	return errors.Join(waitErr, closeErr)
}

func sessionDeadline(ctx context.Context, fallback time.Duration) time.Time {
	if deadline, ok := ctx.Deadline(); ok {
		return deadline
	}
	return time.Now().Add(fallback)
}

type SessionHelperOptions struct {
	PipePath            string
	ParentPID           uint32
	Protocol            string
	Environment         Environment
	VerifyParent        func(parentPID uint32) error
	SkipLegacyMigration bool
}

// RunSessionHelper 作为提权后的短生命周期客户端连接父进程预建的随机管道。
func RunSessionHelper(options SessionHelperOptions) error {
	if !strings.HasPrefix(options.PipePath, `\\.\pipe\tunnelboard-helper-`) {
		return errors.New("helper: invalid session pipe path")
	}
	if options.Protocol != SessionProtocolVersion || options.ParentPID == 0 {
		return errors.New("helper: invalid session identity")
	}
	verifyParent := options.VerifyParent
	if verifyParent == nil {
		verifyParent = verifyParentProcess
	}
	if err := verifyParent(options.ParentPID); err != nil {
		return fmt.Errorf("helper: verify parent process: %w", err)
	}
	if !options.SkipLegacyMigration {
		if err := RemoveLegacyService(); err != nil {
			return fmt.Errorf("helper: remove legacy service before starting session: %w", err)
		}
	}

	timeout := 15 * time.Second
	conn, err := winio.DialPipe(options.PipePath, &timeout)
	if err != nil {
		return fmt.Errorf("helper: connect session pipe: %w", err)
	}
	defer conn.Close()
	serverPID, err := namedPipeServerPID(conn)
	if err != nil {
		return err
	}
	if serverPID != options.ParentPID {
		return fmt.Errorf("helper: named-pipe server PID %d does not match parent PID %d", serverPID, options.ParentPID)
	}

	parent, err := windows.OpenProcess(windows.SYNCHRONIZE, false, options.ParentPID)
	if err != nil {
		return fmt.Errorf("helper: open parent process for monitoring: %w", err)
	}
	defer windows.CloseHandle(parent)
	parentDone := make(chan struct{})
	go func() {
		_, _ = windows.WaitForSingleObject(parent, windows.INFINITE)
		close(parentDone)
		_ = conn.Close()
	}()

	decoder := json.NewDecoder(bufio.NewReader(conn))
	encoder := json.NewEncoder(conn)
	if err := encoder.Encode(sessionHello{Protocol: options.Protocol, ParentPID: options.ParentPID}); err != nil {
		return fmt.Errorf("helper: send session handshake: %w", err)
	}
	var handshake sessionHelloResponse
	if err := decoder.Decode(&handshake); err != nil {
		return fmt.Errorf("helper: receive session handshake: %w", err)
	}
	if !handshake.OK {
		return fmt.Errorf("helper: session handshake rejected: %s", handshake.Error)
	}

	for {
		var request Request
		if err := decoder.Decode(&request); err != nil {
			select {
			case <-parentDone:
				return nil
			default:
			}
			if errors.Is(err, net.ErrClosed) || errors.Is(err, os.ErrClosed) || strings.Contains(err.Error(), "closed") || strings.Contains(err.Error(), "EOF") {
				return nil
			}
			return fmt.Errorf("helper: read session request: %w", err)
		}
		if request.Op == sessionShutdownOp {
			_ = encoder.Encode(Response{OK: true})
			return nil
		}
		if err := encoder.Encode(HandleRequest(request, options.Environment)); err != nil {
			return fmt.Errorf("helper: write session response: %w", err)
		}
	}
}
