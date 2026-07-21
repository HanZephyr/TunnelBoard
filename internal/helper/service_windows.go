//go:build windows

package helper

import (
	"errors"
	"fmt"
	"syscall"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const ServiceName = "TunnelBoardHelper"

var errServiceDoesNotExist = syscall.Errno(1060)

// RemoveLegacyService 是新会话 Helper 唯一保留的 SCM 能力：停止并删除旧版
// AUTO_START LocalSystem 服务。新版本不存在创建、启动或更新服务的代码路径。
func RemoveLegacyService() error {
	manager, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("helper: connect service manager for legacy cleanup: %w", err)
	}
	defer manager.Disconnect()
	service, err := manager.OpenService(ServiceName)
	if errors.Is(err, errServiceDoesNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("helper: open legacy service: %w", err)
	}
	defer service.Close()

	status, queryErr := service.Query()
	if queryErr == nil && status.State != svc.Stopped {
		_, _ = service.Control(svc.Stop)
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			status, queryErr = service.Query()
			if queryErr == nil && status.State == svc.Stopped {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if queryErr == nil && status.State != svc.Stopped {
			return errors.New("helper: legacy service did not stop within 10s")
		}
	}
	if err := service.Delete(); err != nil && !errors.Is(err, errServiceDoesNotExist) {
		return fmt.Errorf("helper: delete legacy service: %w", err)
	}
	return nil
}
