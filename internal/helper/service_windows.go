//go:build windows

package helper

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

// ServiceName 是 helper 的 Windows 服务名。
const ServiceName = "TunnelBoardHelper"

const serviceDisplayName = "TunnelBoard Helper"

// errServiceExists 对应 ERROR_SERVICE_EXISTS（服务已注册）。
var errServiceExists = syscall.Errno(1073)

// InstallService 注册并启动 helper 的 Windows 服务（需要管理员权限，由提升的 -install 调用）。
// 服务已存在时改为确保其启动（幂等，供 EnsureInstalled 的更新路径复用）。
func InstallService() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("helper: resolve executable: %w", err)
	}
	exe, err = filepath.Abs(exe)
	if err != nil {
		return fmt.Errorf("helper: resolve executable path: %w", err)
	}

	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("helper: connect service manager (elevation required): %w", err)
	}
	defer m.Disconnect()

	s, err := m.CreateService(ServiceName, exe, mgr.Config{
		StartType:   mgr.StartAutomatic,
		DisplayName: serviceDisplayName,
		Description: "TunnelBoard 受限特权辅助服务：仅执行受托管 hosts 写入与本地 CA 信任操作。",
	}, "-serve")
	if err != nil {
		if !errors.Is(err, errServiceExists) {
			return fmt.Errorf("helper: create service: %w", err)
		}
		existing, openErr := m.OpenService(ServiceName)
		if openErr != nil {
			return fmt.Errorf("helper: open existing service: %w", openErr)
		}
		s = existing
	}
	defer s.Close()

	if err := s.Start(); err != nil && !errors.Is(err, syscall.Errno(1056)) { // ERROR_SERVICE_ALREADY_RUNNING
		return fmt.Errorf("helper: start service: %w", err)
	}
	return nil
}

// UninstallService 停止并删除 helper 服务（需要管理员权限）。
func UninstallService() error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("helper: connect service manager (elevation required): %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(ServiceName)
	if err != nil {
		return fmt.Errorf("helper: open service: %w", err)
	}
	defer s.Close()

	_, _ = s.Control(svc.Stop) // 已停止时忽略错误
	if err := s.Delete(); err != nil {
		return fmt.Errorf("helper: delete service: %w", err)
	}
	return nil
}

// RunServiceMain 运行 helper 服务：服务控制管理器启动时走 svc.Run，
// 交互式启动（调试）时直接监听管道。
func RunServiceMain(env Environment) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	inService, err := svc.IsWindowsService()
	if err != nil {
		return fmt.Errorf("helper: detect service context: %w", err)
	}
	if !inService {
		return ServePipe(ctx, env, PipePath)
	}
	return svc.Run(ServiceName, &pipeService{ctx: ctx, cancel: cancel, env: env})
}

// pipeService 适配 Windows 服务控制：Stop/Shutdown 时取消 ServePipe 的上下文。
type pipeService struct {
	ctx    context.Context
	cancel context.CancelFunc
	env    Environment
}

func (s *pipeService) Execute(args []string, requests <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- ServePipe(s.ctx, s.env, PipePath)
	}()
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}

	for {
		select {
		case err := <-serveDone:
			status <- svc.Status{State: svc.Stopped}
			if err != nil {
				return true, 1
			}
			return false, 0
		case c := <-requests:
			if c.Cmd == svc.Stop || c.Cmd == svc.Shutdown {
				status <- svc.Status{State: svc.StopPending}
				s.cancel()
				<-serveDone
				status <- svc.Status{State: svc.Stopped}
				return false, 0
			}
		}
	}
}
