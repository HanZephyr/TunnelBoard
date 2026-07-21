package diag

import (
	"context"
	"log/slog"
	"strings"
)

const (
	maxSafeLogMessageBytes = 16 << 10
	maxSafeLogAttrBytes    = 8 << 10
)

var safeLogAttrs = map[string]struct{}{
	"addr": {}, "attempt": {}, "ca_trusted": {}, "caddy_enabled": {}, "count": {},
	"dest": {}, "dir": {}, "duration": {}, "entries": {}, "err": {}, "flattened_folders": {},
	"folder": {}, "forward_id": {}, "host_id": {}, "hosts_entries": {}, "imported": {},
	"include_key_files": {}, "key_files": {}, "level": {}, "name": {}, "op": {}, "path": {},
	"route_id": {}, "routes_deactivated": {}, "sha256_prefix": {}, "skipped_hosts": {},
	"source": {}, "status": {}, "timeout": {}, "value": {}, "wait": {}, "warnings": {},
}

var sensitiveLogAttrs = map[string]struct{}{
	"authorization": {}, "cookie": {}, "password": {}, "passphrase": {}, "private_key": {},
	"secret": {}, "token": {},
}

// SafeLogHandler is the seam that sanitizes a record before any disk, memory or console sink.
type SafeLogHandler struct{ downstream slog.Handler }

func NewSafeLogHandler(downstream slog.Handler) *SafeLogHandler {
	return &SafeLogHandler{downstream: downstream}
}

func (h *SafeLogHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.downstream.Enabled(ctx, level)
}

func (h *SafeLogHandler) Handle(ctx context.Context, record slog.Record) error {
	message := truncateUTF8Bytes(RedactLogText(record.Message), maxSafeLogMessageBytes)
	safe := slog.NewRecord(record.Time, record.Level, message, record.PC)
	remaining := maxSafeLogAttrBytes
	record.Attrs(func(attr slog.Attr) bool {
		clean, ok := sanitizeLogAttr(attr)
		if !ok {
			return true
		}
		cost := len(clean.Key) + len(clean.Value.String())
		if cost > remaining {
			return false
		}
		remaining -= cost
		safe.AddAttrs(clean)
		return true
	})
	return h.downstream.Handle(ctx, safe)
}

func (h *SafeLogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clean := make([]slog.Attr, 0, len(attrs))
	remaining := maxSafeLogAttrBytes
	for _, attr := range attrs {
		safe, ok := sanitizeLogAttr(attr)
		if !ok {
			continue
		}
		cost := len(safe.Key) + len(safe.Value.String())
		if cost > remaining {
			break
		}
		remaining -= cost
		clean = append(clean, safe)
	}
	return &SafeLogHandler{downstream: h.downstream.WithAttrs(clean)}
}

func (h *SafeLogHandler) WithGroup(name string) slog.Handler {
	return &SafeLogHandler{downstream: h.downstream.WithGroup(truncateUTF8Bytes(name, 128))}
}

func sanitizeLogAttr(attr slog.Attr) (slog.Attr, bool) {
	attr.Value = attr.Value.Resolve()
	key := strings.ToLower(attr.Key)
	if _, sensitive := sensitiveLogAttrs[key]; sensitive {
		return slog.String(attr.Key, "[REDACTED]"), true
	}
	if _, allowed := safeLogAttrs[key]; !allowed {
		return slog.Attr{}, false
	}
	switch attr.Value.Kind() {
	case slog.KindString:
		return slog.String(attr.Key, truncateUTF8Bytes(RedactLogText(attr.Value.String()), 4<<10)), true
	case slog.KindAny:
		return slog.String(attr.Key, truncateUTF8Bytes(RedactLogText(attr.Value.String()), 4<<10)), true
	case slog.KindGroup:
		return slog.Attr{}, false
	default:
		return attr, true
	}
}

func truncateUTF8Bytes(value string, max int) string {
	if len(value) <= max {
		return value
	}
	marker := " [truncated]"
	keep := max - len(marker)
	if keep < 0 {
		keep = 0
	}
	for keep > 0 && (value[keep]&0xc0) == 0x80 {
		keep--
	}
	return value[:keep] + marker
}
