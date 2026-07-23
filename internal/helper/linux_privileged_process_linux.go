//go:build linux

package helper

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"sync"
)

const (
	linuxInstalledApplicationPath = "/opt/tunnelboard/tunnelboard"
	linuxInstalledHelperPath      = "/usr/libexec/tunnelboard/tunnelboard-linux-helper"
)

type linuxProcessSessionStarter struct{}

func newLinuxProcessSessionStarter() linuxPrivilegedSessionStarter {
	return linuxProcessSessionStarter{}
}

func (linuxProcessSessionStarter) Start(ctx context.Context, sessionID, authorizationID string) (linuxPrivilegedSessionConnection, error) {
	startTime, err := linuxProcessStartTime(os.Getpid())
	if err != nil {
		return nil, err
	}
	if err := ensureLinuxPrivilegedPayload(); err != nil {
		return nil, err
	}
	command := exec.CommandContext(
		ctx,
		"/usr/bin/pkexec",
		"--disable-internal-agent",
		linuxInstalledHelperPath,
		"session",
		"--session-id", sessionID,
		"--authorization-id", authorizationID,
		"--parent-pid", strconv.Itoa(os.Getpid()),
		"--parent-start-time", strconv.FormatUint(startTime, 10),
	)
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("helper: open Linux privilege stdin: %w", err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("helper: open Linux privilege stdout: %w", err)
	}
	stderr := &bytes.Buffer{}
	command.Stderr = stderr
	if err := command.Start(); err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("helper: start pkexec TunnelBoard helper: %w", err)
	}
	return &linuxProcessSessionConnection{
		command: command, input: stdin, output: bufio.NewReader(stdout), stderr: stderr,
	}, nil
}

func ensureLinuxPrivilegedPayload() error {
	for _, path := range []string{"/usr/bin/pkexec", linuxInstalledApplicationPath, linuxInstalledHelperPath} {
		info, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("helper: required Linux privileged payload %s is unavailable: %w", path, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("helper: required Linux privileged payload %s is not a regular file", path)
		}
	}
	return VerifyBundledBinary(linuxInstalledHelperPath)
}

type linuxProcessSessionConnection struct {
	mu      sync.Mutex
	command *exec.Cmd
	input   io.WriteCloser
	output  *bufio.Reader
	stderr  *bytes.Buffer
	closed  bool
}

func (c *linuxProcessSessionConnection) Call(ctx context.Context, request linuxPrivilegedRequest) (linuxPrivilegedResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return linuxPrivilegedResponse{}, ErrSessionClosed
	}
	if err := ctx.Err(); err != nil {
		return linuxPrivilegedResponse{}, err
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return linuxPrivilegedResponse{}, err
	}
	payload = append(payload, '\n')
	if _, err := c.input.Write(payload); err != nil {
		return linuxPrivilegedResponse{}, c.wrapProcessError("write Linux privileged request", err)
	}
	line, err := c.output.ReadBytes('\n')
	if err != nil {
		return linuxPrivilegedResponse{}, c.wrapProcessError("read Linux privileged response", err)
	}
	var response linuxPrivilegedResponse
	if err := json.Unmarshal(line, &response); err != nil {
		return linuxPrivilegedResponse{}, fmt.Errorf("helper: decode Linux privileged response: %w", err)
	}
	return response, nil
}

func (c *linuxProcessSessionConnection) Close(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	closeErr := c.input.Close()
	if c.command.Process == nil {
		return closeErr
	}
	done := make(chan error, 1)
	go func() { done <- c.command.Wait() }()
	select {
	case err := <-done:
		return errors.Join(closeErr, processExitError(err, c.stderr.String()))
	case <-ctx.Done():
		_ = c.command.Process.Kill()
		err := <-done
		return errors.Join(closeErr, ctx.Err(), processExitError(err, c.stderr.String()))
	}
}

func (c *linuxProcessSessionConnection) wrapProcessError(operation string, cause error) error {
	if c.command.ProcessState != nil {
		return fmt.Errorf("helper: %s: %w: %s", operation, cause, c.stderr.String())
	}
	return fmt.Errorf("helper: %s: %w", operation, cause)
}

func processExitError(err error, stderr string) error {
	if err == nil {
		return nil
	}
	if stderr == "" {
		return fmt.Errorf("helper: Linux privileged helper exited: %w", err)
	}
	return fmt.Errorf("helper: Linux privileged helper exited: %w: %s", err, stderr)
}
