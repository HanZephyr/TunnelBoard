//go:build windows

package helper

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows/svc/eventlog"

	"github.com/HanZephyr/TunnelBoard/internal/diag"
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

// SetupEventLogging 把 helper 的默认日志切到 Windows 事件日志 + 本地日志文件
// （ProgramData\TunnelBoard\helper.log，滚动 2MiB）。两者都失败时保持 stderr 兜底。
func SetupEventLogging() {
	var handlers []slog.Handler
	if el, err := eventlog.Open(eventSourceName); err == nil {
		handlers = append(handlers, &eventLogHandler{el: el})
	}
	if lf, err := diag.OpenLogFile(filepath.Join(programDataDir(), "helper.log"), 2<<20); err == nil {
		handlers = append(handlers, slog.NewTextHandler(lf, nil))
	}
	if len(handlers) > 0 {
		slog.SetDefault(slog.New(diag.NewFanout(handlers...)))
	}
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
