package helper

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

// LinuxPrivilegedSessionOptions 是包内 root 子进程启动时一次性注入的边界。
// ParentAlive 必须验证发起会话的已安装 TunnelBoard 进程，不能只检查 PID 存在。
type LinuxPrivilegedSessionOptions struct {
	SessionID           string
	Effects             LinuxPrivilegedEffects
	ParentAlive         func() error
	AuthorizationActive func(context.Context) error
	MaxLifetime         time.Duration
}

// ServeLinuxPrivilegedSession 在 pkexec 子进程的标准输入/输出上运行 NDJSON 协议。
// 标准输出只保留协议响应，调用方退出导致 stdin EOF 时立即停止；到期后亦停止，
// 因而没有可以被下一次应用进程复用的常驻 root 服务。
func ServeLinuxPrivilegedSession(ctx context.Context, input io.Reader, output io.Writer, options LinuxPrivilegedSessionOptions) error {
	if options.SessionID == "" {
		return errors.New("helper: Linux privileged session id is required")
	}
	if options.Effects == nil {
		return errors.New("helper: Linux privileged effects are required")
	}
	if options.MaxLifetime <= 0 || options.MaxLifetime > linuxPrivilegeSessionTTL {
		options.MaxLifetime = linuxPrivilegeSessionTTL
	}
	server := newLinuxPrivilegedSessionServer(options.SessionID, options.Effects)
	decoder := json.NewDecoder(bufio.NewReader(input))
	encoder := json.NewEncoder(output)
	timeout := time.NewTimer(options.MaxLifetime)
	defer timeout.Stop()

	for {
		request := linuxPrivilegedRequest{}
		type decoded struct {
			request linuxPrivilegedRequest
			err     error
		}
		requests := make(chan decoded, 1)
		go func() {
			err := decoder.Decode(&request)
			requests <- decoded{request: request, err: err}
		}()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timeout.C:
			return nil
		case message := <-requests:
			if errors.Is(message.err, io.EOF) {
				return nil
			}
			if message.err != nil {
				return fmt.Errorf("helper: decode Linux privileged request: %w", message.err)
			}
			if options.ParentAlive != nil {
				if err := options.ParentAlive(); err != nil {
					response := linuxPrivilegeFailure(fmt.Errorf("helper: Linux privileged parent validation: %w", err))
					_ = encoder.Encode(response)
					return err
				}
			}
			if message.request.Operation != linuxPrivilegeRevoke && options.AuthorizationActive != nil {
				if err := options.AuthorizationActive(ctx); err != nil {
					response := linuxPrivilegeFailure(fmt.Errorf("helper: Linux temporary authorization validation: %w", err))
					_ = encoder.Encode(response)
					return err
				}
			}
			response := server.handle(ctx, message.request)
			if err := encoder.Encode(response); err != nil {
				return fmt.Errorf("helper: write Linux privileged response: %w", err)
			}
			if response.Revoked {
				return nil
			}
		}
	}
}
