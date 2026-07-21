// Package diag 实现运行期内存日志与脱敏诊断包导出。
// 按交接决定：默认不持久化运行日志，仅保留本次运行的内存日志，排障时手动导出。
package diag

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"sync"
	"time"
)

// LogEntry 是一条内存日志。
type LogEntry struct {
	Time    time.Time `json:"time"`
	Level   string    `json:"level"`
	Message string    `json:"message"`
}

// ringState 是 RingBuffer 及其派生 handler 共享的缓冲状态（WithAttrs/WithGroup 不复制）。
type ringState struct {
	mu      sync.Mutex
	entries []LogEntry
	head    int
	full    bool
}

// RingBuffer 是 slog.Handler：把记录同时写入下游 handler 与定长环形缓冲。
type RingBuffer struct {
	downstream slog.Handler
	state      *ringState
	cap        int
}

// NewRingBuffer 返回容量为 cap 的环形日志 handler。
func NewRingBuffer(downstream slog.Handler, cap int) *RingBuffer {
	return &RingBuffer{downstream: downstream, state: &ringState{entries: make([]LogEntry, 0, cap)}, cap: cap}
}

// Enabled 透传下游级别判定。
func (b *RingBuffer) Enabled(ctx context.Context, level slog.Level) bool {
	return b.downstream.Enabled(ctx, level)
}

// Handle 写入下游并追加到环形缓冲。
func (b *RingBuffer) Handle(ctx context.Context, record slog.Record) error {
	msg := record.Message
	record.Attrs(func(attr slog.Attr) bool {
		msg += " " + attr.Key + "=" + attr.Value.String()
		return true
	})
	b.state.mu.Lock()
	entry := LogEntry{Time: record.Time, Level: record.Level.String(), Message: msg}
	if len(b.state.entries) < b.cap {
		b.state.entries = append(b.state.entries, entry)
	} else {
		b.state.entries[b.state.head] = entry
		b.state.full = true
	}
	b.state.head = (b.state.head + 1) % b.cap
	b.state.mu.Unlock()
	return b.downstream.Handle(ctx, record)
}

// WithAttrs 透传下游并共享缓冲。
func (b *RingBuffer) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &RingBuffer{downstream: b.downstream.WithAttrs(attrs), state: b.state, cap: b.cap}
}

// WithGroup 透传下游并共享缓冲。
func (b *RingBuffer) WithGroup(name string) slog.Handler {
	return &RingBuffer{downstream: b.downstream.WithGroup(name), state: b.state, cap: b.cap}
}

// Snapshot 按时间顺序返回缓冲内容（最旧在前）。
func (b *RingBuffer) Snapshot() []LogEntry {
	b.state.mu.Lock()
	defer b.state.mu.Unlock()
	out := make([]LogEntry, 0, len(b.state.entries))
	if !b.state.full {
		return append(out, b.state.entries...)
	}
	out = append(out, b.state.entries[b.state.head:]...)
	out = append(out, b.state.entries[:b.state.head]...)
	return out
}

// 脱敏规则：键值形态的密码/口令/密钥材料一律遮蔽；用户目录路径归一为 ~。
var (
	homePattern = regexp.MustCompile(`(?i)([A-Z]:\\Users\\[^\\\s]+|/home/[^/\s]+|/Users/[^/\s]+)`)
)

// Sanitize 对单条文本脱敏：秘密键值遮蔽为 ***，用户目录归一为 ~。
func Sanitize(text string) string {
	return RedactLogText(text)
}

// Bundle 是诊断包内容：版本/平台信息、内存日志与上层附加的状态摘要。
type Bundle struct {
	GeneratedAt string                 `json:"generatedAt"`
	AppVersion  string                 `json:"appVersion"`
	Platform    string                 `json:"platform"`
	Logs        []LogEntry             `json:"logs"`
	Summary     map[string]interface{} `json:"summary"`
}

// BuildBundle 汇总版本、平台与脱敏后的内存日志；summary 由调用方提供（不得含秘密）。
func (b *RingBuffer) BuildBundle(appVersion, platform string, summary map[string]interface{}) Bundle {
	raw := b.Snapshot()
	logs := make([]LogEntry, len(raw))
	for i, e := range raw {
		logs[i] = LogEntry{Time: e.Time, Level: e.Level, Message: Sanitize(e.Message)}
	}
	return Bundle{
		GeneratedAt: time.Now().Format(time.RFC3339),
		AppVersion:  appVersion,
		Platform:    platform,
		Logs:        logs,
		Summary:     summary,
	}
}

// FormatLogLine 渲染单行日志（导出文本用）。
func FormatLogLine(e LogEntry) string {
	return fmt.Sprintf("%s [%s] %s", e.Time.Format("2006-01-02 15:04:05"), e.Level, e.Message)
}
