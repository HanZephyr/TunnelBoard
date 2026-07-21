//go:build windows

package helper

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const HelperBinaryName = "tunnelboard-helper.exe"
const HelperBinaryEnvVar = "TUNNELBOARD_HELPER_PATH"

var ErrAuthorizationCancelled = errors.New("helper: user cancelled administrator authorization")

func launchElevatedSessionHelper(pipePath string, parentPID uint32, protocol string) (elevatedProcess, error) {
	exe, err := helperBinaryPath()
	if err != nil {
		return nil, err
	}
	if err := verifyAuthenticode(exe); err != nil {
		return nil, fmt.Errorf("helper: verify helper signature: %w", err)
	}
	application, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("helper: resolve application publisher: %w", err)
	}
	if err := verifySameAuthenticodePublisher(application, exe); err != nil {
		return nil, err
	}
	parameters := windowsCommandLine(
		"--session-helper",
		"--pipe", pipePath,
		"--parent-pid", strconv.FormatUint(uint64(parentPID), 10),
		"--protocol", protocol,
	)
	process, err := shellExecuteRunAs(exe, parameters)
	if err != nil {
		if errors.Is(err, syscall.Errno(windows.ERROR_CANCELLED)) {
			return nil, ErrAuthorizationCancelled
		}
		return nil, fmt.Errorf("helper: launch elevated session helper: %w", err)
	}
	return process, nil
}

func helperBinaryPath() (string, error) {
	if path := strings.TrimSpace(os.Getenv(HelperBinaryEnvVar)); path != "" {
		if expectedBinarySHA256() != "" {
			return "", fmt.Errorf("helper: %s is disabled in formal builds", HelperBinaryEnvVar)
		}
		absolute, err := filepath.Abs(path)
		if err != nil {
			return "", err
		}
		info, err := os.Stat(absolute)
		if err != nil || info.IsDir() {
			return "", fmt.Errorf("helper: %s=%s is not a usable file", HelperBinaryEnvVar, path)
		}
		return absolute, nil
	}
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("helper: resolve application executable: %w", err)
	}
	path := filepath.Join(filepath.Dir(executable), HelperBinaryName)
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return "", fmt.Errorf("helper: %s not found beside application", path)
	}
	if err := VerifyBundledBinary(path); err != nil {
		return "", err
	}
	return path, nil
}

func windowsCommandLine(args ...string) string {
	escaped := make([]string, len(args))
	for index, argument := range args {
		escaped[index] = syscall.EscapeArg(argument)
	}
	return strings.Join(escaped, " ")
}

type shellExecuteInfo struct {
	Size       uint32
	Mask       uint32
	HWND       uintptr
	Verb       *uint16
	File       *uint16
	Parameters *uint16
	Directory  *uint16
	Show       int32
	Instance   windows.Handle
	IDList     uintptr
	Class      *uint16
	ClassKey   windows.Handle
	HotKey     uint32
	Icon       windows.Handle
	Process    windows.Handle
}

const seeMaskNoCloseProcess = 0x00000040

var procShellExecuteExW = windows.NewLazySystemDLL("shell32.dll").NewProc("ShellExecuteExW")

func shellExecuteRunAs(executable, parameters string) (*windowsElevatedProcess, error) {
	verb, err := windows.UTF16PtrFromString("runas")
	if err != nil {
		return nil, err
	}
	file, err := windows.UTF16PtrFromString(executable)
	if err != nil {
		return nil, err
	}
	params, err := windows.UTF16PtrFromString(parameters)
	if err != nil {
		return nil, err
	}
	info := shellExecuteInfo{
		Size:       uint32(unsafe.Sizeof(shellExecuteInfo{})),
		Mask:       seeMaskNoCloseProcess,
		Verb:       verb,
		File:       file,
		Parameters: params,
		Show:       windows.SW_HIDE,
	}
	result, _, callErr := procShellExecuteExW.Call(uintptr(unsafe.Pointer(&info)))
	if result == 0 {
		if callErr != nil && callErr != syscall.Errno(0) {
			return nil, callErr
		}
		return nil, errors.New("ShellExecuteExW failed")
	}
	if info.Process == 0 {
		return nil, errors.New("ShellExecuteExW returned no process handle")
	}
	pid, err := windows.GetProcessId(info.Process)
	if err != nil {
		_ = windows.CloseHandle(info.Process)
		return nil, fmt.Errorf("helper: get elevated process id: %w", err)
	}
	return &windowsElevatedProcess{handle: info.Process, pid: pid}, nil
}

type windowsElevatedProcess struct {
	handle windows.Handle
	pid    uint32
}

func (p *windowsElevatedProcess) PID() uint32 { return p.pid }

func (p *windowsElevatedProcess) Wait(ctx context.Context) error {
	done := make(chan error, 1)
	go func() {
		_, err := windows.WaitForSingleObject(p.handle, windows.INFINITE)
		done <- err
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *windowsElevatedProcess) Close() error {
	if p.handle == 0 {
		return nil
	}
	err := windows.CloseHandle(p.handle)
	p.handle = 0
	return err
}

func verifyParentProcess(parentPID uint32) error {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, parentPID)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)
	buffer := make([]uint16, 32768)
	size := uint32(len(buffer))
	if err := windows.QueryFullProcessImageName(handle, 0, &buffer[0], &size); err != nil {
		return err
	}
	path := windows.UTF16ToString(buffer[:size])
	if !strings.EqualFold(filepath.Base(path), "tunnelboard.exe") {
		return fmt.Errorf("unexpected parent executable %q", filepath.Base(path))
	}
	if err := verifyAuthenticode(path); err != nil {
		return err
	}
	helperPath, err := os.Executable()
	if err != nil {
		return err
	}
	return verifySameAuthenticodePublisher(path, helperPath)
}

func verifySameAuthenticodePublisher(applicationPath, helperPath string) error {
	applicationIdentity, err := authenticodeCertificateSHA256(applicationPath)
	if err != nil {
		return fmt.Errorf("helper: read application publisher identity: %w", err)
	}
	helperIdentity, err := authenticodeCertificateSHA256(helperPath)
	if err != nil {
		return fmt.Errorf("helper: read Helper publisher identity: %w", err)
	}
	return requireMatchingPublisherIdentity(applicationIdentity, helperIdentity)
}

func requireMatchingPublisherIdentity(applicationIdentity, helperIdentity string) error {
	applicationIdentity = strings.TrimSpace(strings.ToLower(applicationIdentity))
	helperIdentity = strings.TrimSpace(strings.ToLower(helperIdentity))
	if applicationIdentity == "" || helperIdentity == "" {
		return errors.New("helper: Authenticode publisher identity is empty")
	}
	if applicationIdentity != helperIdentity {
		return errors.New("helper: application and Helper Authenticode publishers differ")
	}
	return nil
}

func authenticodeCertificateSHA256(path string) (string, error) {
	powerShell := filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	const script = `$s=Get-AuthenticodeSignature -LiteralPath $env:TUNNELBOARD_AUTHENTICODE_PATH; if($s.Status -ne 'Valid' -or -not $s.SignerCertificate){exit 42}; $h=[Security.Cryptography.SHA256]::Create(); (($h.ComputeHash($s.SignerCertificate.RawData) | ForEach-Object {$_.ToString('x2')}) -join '')`
	cmd := exec.Command(powerShell, "-NoProfile", "-NonInteractive", "-Command", script)
	cmd.Env = append(os.Environ(), "TUNNELBOARD_AUTHENTICODE_PATH="+path)
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("publisher certificate query failed: %w", err)
	}
	identity := strings.TrimSpace(string(output))
	if len(identity) != sha256.Size*2 {
		return "", errors.New("publisher certificate query returned an invalid SHA-256 identity")
	}
	return identity, nil
}

func verifyAuthenticode(path string) error {
	pathUTF16, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	fileInfo := windows.WinTrustFileInfo{
		Size:     uint32(unsafe.Sizeof(windows.WinTrustFileInfo{})),
		FilePath: pathUTF16,
	}
	data := windows.WinTrustData{
		Size:                            uint32(unsafe.Sizeof(windows.WinTrustData{})),
		UIChoice:                        windows.WTD_UI_NONE,
		RevocationChecks:                windows.WTD_REVOKE_NONE,
		UnionChoice:                     windows.WTD_CHOICE_FILE,
		StateAction:                     windows.WTD_STATEACTION_VERIFY,
		ProvFlags:                       windows.WTD_CACHE_ONLY_URL_RETRIEVAL,
		FileOrCatalogOrBlobOrSgnrOrCert: unsafe.Pointer(&fileInfo),
	}
	verifyErr := windows.WinVerifyTrustEx(windows.InvalidHWND, &windows.WINTRUST_ACTION_GENERIC_VERIFY_V2, &data)
	data.StateAction = windows.WTD_STATEACTION_CLOSE
	closeErr := windows.WinVerifyTrustEx(windows.InvalidHWND, &windows.WINTRUST_ACTION_GENERIC_VERIFY_V2, &data)
	return errors.Join(verifyErr, closeErr)
}
