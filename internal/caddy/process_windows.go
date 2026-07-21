//go:build windows

package caddy

import (
	"fmt"
	"io"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

type ownedProcess struct {
	cmd *exec.Cmd
	job windows.Handle
}

func startOwned(bin string, args []string, dir string, env []string, stdout, stderr io.Writer) (Process, error) {
	cmd := exec.Command(bin, args...)
	cmd.Dir, cmd.Env, cmd.Stdout, cmd.Stderr = dir, env, stdout, stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | 0x08000000}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("caddy: start process: %w", err)
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, fmt.Errorf("caddy: create job object: %w", err)
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(job)
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, fmt.Errorf("caddy: configure job object: %w", err)
	}
	processHandle, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err == nil {
		err = windows.AssignProcessToJobObject(job, processHandle)
		windows.CloseHandle(processHandle)
	}
	if err != nil {
		windows.CloseHandle(job)
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, fmt.Errorf("caddy: assign process to job: %w", err)
	}
	return &ownedProcess{cmd: cmd, job: job}, nil
}

func (p *ownedProcess) PID() int    { return p.cmd.Process.Pid }
func (p *ownedProcess) Kill() error { return p.cmd.Process.Kill() }
func (p *ownedProcess) Wait() error {
	err := p.cmd.Wait()
	if p.job != 0 {
		_ = windows.CloseHandle(p.job)
		p.job = 0
	}
	return err
}
