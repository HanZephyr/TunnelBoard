//go:build linux

package helper

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// ValidateLinuxPrivilegedParent 同时核对 PID start-time、调用用户和固定安装路径。
// PID 重用、普通程序伪造参数或直接运行 root helper 都不能获得会话能力。
func ValidateLinuxPrivilegedParent(pid int, expectedStartTime uint64) error {
	if os.Geteuid() != 0 {
		return errors.New("helper: Linux privileged session requires root")
	}
	if pid <= 1 {
		return errors.New("helper: invalid Linux privileged parent pid")
	}
	observedStartTime, err := linuxProcessStartTime(pid)
	if err != nil {
		return err
	}
	if observedStartTime != expectedStartTime {
		return errors.New("helper: Linux privileged parent process was replaced")
	}
	executable, err := os.Readlink(filepath.Join("/proc", strconv.Itoa(pid), "exe"))
	if err != nil {
		return fmt.Errorf("helper: resolve Linux privileged parent executable: %w", err)
	}
	if filepath.Clean(executable) != linuxInstalledApplicationPath {
		return fmt.Errorf("helper: Linux privileged parent executable %q is not TunnelBoard", executable)
	}
	uidText := strings.TrimSpace(os.Getenv("PKEXEC_UID"))
	uid, err := strconv.ParseUint(uidText, 10, 32)
	if err != nil {
		return errors.New("helper: pkexec caller identity is unavailable")
	}
	info, err := os.Stat(filepath.Join("/proc", strconv.Itoa(pid)))
	if err != nil {
		return fmt.Errorf("helper: inspect Linux privileged parent owner: %w", err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || uint64(stat.Uid) != uid {
		return errors.New("helper: Linux privileged parent does not belong to the pkexec caller")
	}
	return nil
}

// BindLinuxPrivilegedParent 建立 helper 与其实际 pkexec 父进程的死亡联动，并在
// PR_SET_PDEATHSIG 的竞态窗口后重新核对。CLI 传入的 PID 绝不能替代此检查。
func BindLinuxPrivilegedParent(pid int) error {
	if pid <= 1 || os.Getppid() != pid {
		return errors.New("helper: Linux privileged helper parent pid does not match pkexec caller")
	}
	if err := unix.Prctl(unix.PR_SET_PDEATHSIG, uintptr(unix.SIGTERM), 0, 0, 0); err != nil {
		return fmt.Errorf("helper: bind Linux privileged helper parent death signal: %w", err)
	}
	if os.Getppid() != pid {
		return errors.New("helper: Linux privileged helper parent exited during startup")
	}
	return nil
}

func linuxProcessStartTime(pid int) (uint64, error) {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return 0, fmt.Errorf("helper: read Linux process start time: %w", err)
	}
	closing := strings.LastIndex(string(data), ")")
	if closing < 0 {
		return 0, errors.New("helper: malformed Linux process stat")
	}
	fields := strings.Fields(string(data[closing+1:]))
	const startTimeIndexAfterCommand = 19 // /proc/<pid>/stat field 22, fields start at field 3.
	if len(fields) <= startTimeIndexAfterCommand {
		return 0, errors.New("helper: incomplete Linux process stat")
	}
	start, err := strconv.ParseUint(fields[startTimeIndexAfterCommand], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("helper: parse Linux process start time: %w", err)
	}
	return start, nil
}
