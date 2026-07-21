//go:build !windows

package caddy

import (
	"fmt"
	"io"
	"os/exec"
	"syscall"
)

type ownedProcess struct{ cmd *exec.Cmd }

func startOwned(bin string, args []string, dir string, env []string, stdout, stderr io.Writer) (Process, error) {
	cmd := exec.Command(bin, args...)
	cmd.Dir, cmd.Env, cmd.Stdout, cmd.Stderr = dir, env, stdout, stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("caddy: start process: %w", err)
	}
	return &ownedProcess{cmd: cmd}, nil
}

func (p *ownedProcess) PID() int { return p.cmd.Process.Pid }
func (p *ownedProcess) Kill() error {
	return syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
}
func (p *ownedProcess) Wait() error { return p.cmd.Wait() }
