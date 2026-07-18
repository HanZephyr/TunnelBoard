//go:build windows

package helper

import (
	"context"
	"log/slog"
	"os"

	"golang.org/x/sys/windows/svc/eventlog"
)

// eventSourceName 是 helper 在 Windows 事件日志中的来源名。
// 服务以 SYSTEM 运行、无控制台，stderr 不可见：事件日志是服务侧唯一的原生诊断通道
// （事件查看器筛选来源 TunnelBoardHelper，或 wevtutil qe Application /q:"*[System[Provider[@Name='TunnelBoardHelper']]]"）。
const eventSourceName = "TunnelBoardHelper"

// InstallEventSource 注册事件来源（需管理员，随 -install 执行）；已注册时忽略。
func InstallEventSource() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	// 已注册时 Install 返回错误，幂等忽略。
	_ = eventlog.Install(eventSourceName, exe, true, 0)
}

// SetupEventLogging 把 helper 的默认日志切到 Windows 事件日志（服务路径调用）。
// 打开失败时保持 stderr 兜底（交互调试场景）。
func SetupEventLogging() {
	el, err := eventlog.Open(eventSourceName)
	if err != nil {
		return
	}
	slog.SetDefault(slog.New(&eventLogHandler{el: el}))
}

// eventLogHandler 把 slog 记录映射为事件日志条目（级别 → Info/Warning/Error）。
type eventLogHandler struct {
	el *eventlog.Log
}

func (h *eventLogHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *eventLogHandler) Handle(_ context.Context, record slog.Record) error {
	msg := record.Message
	record.Attrs(func(attr slog.Attr) bool {
		msg += " " + attr.Key + "=" + attr.Value.String()
		return true
	})
	switch {
	case record.Level >= slog.LevelError:
		return h.el.Error(3, msg)
	case record.Level >= slog.LevelWarn:
		return h.el.Warning(2, msg)
	default:
		return h.el.Info(1, msg)
	}
}

func (h *eventLogHandler) WithAttrs(attrs []slog.Attr) slog.Handler { return h }
func (h *eventLogHandler) WithGroup(name string) slog.Handler       { return h }
