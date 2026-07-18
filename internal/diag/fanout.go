package diag

import (
	"context"
	"log/slog"
)

// Fanout 把 slog 记录广播到多个 handler（主程序：stderr + 文件 + 内存环；
// helper：事件日志 + 文件）。
type Fanout struct {
	handlers []slog.Handler
}

// NewFanout 组合多个 handler。
func NewFanout(handlers ...slog.Handler) *Fanout {
	return &Fanout{handlers: handlers}
}

// Enabled 任一 handler 接收即启用。
func (f *Fanout) Enabled(ctx context.Context, level slog.Level) bool {
	for _, h := range f.handlers {
		if h.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

// Handle 广播到全部 handler，返回首个错误。
func (f *Fanout) Handle(ctx context.Context, record slog.Record) error {
	var first error
	for _, h := range f.handlers {
		if err := h.Handle(ctx, record); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// WithAttrs 逐个派生。
func (f *Fanout) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := make([]slog.Handler, len(f.handlers))
	for i, h := range f.handlers {
		next[i] = h.WithAttrs(attrs)
	}
	return &Fanout{handlers: next}
}

// WithGroup 逐个派生。
func (f *Fanout) WithGroup(name string) slog.Handler {
	next := make([]slog.Handler, len(f.handlers))
	for i, h := range f.handlers {
		next[i] = h.WithGroup(name)
	}
	return &Fanout{handlers: next}
}
