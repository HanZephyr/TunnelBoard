package diag

import (
	"bytes"
	"fmt"
	"sync"
)

const sourceWriterLineBytes = (64 << 10) - 128

// SourceWriter 把进程 stdout/stderr 或 slog 文本安全地送入指定 LogStore source。
type SourceWriter struct {
	store    LogStore
	source   LogSource
	mu       sync.Mutex
	buffer   []byte
	dropping bool
	dropped  int64
}

func NewSourceWriter(store LogStore, source LogSource) *SourceWriter {
	return &SourceWriter{store: store, source: source}
}

func (w *SourceWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	written := len(p)
	for len(p) != 0 {
		if w.dropping {
			index := bytes.IndexByte(p, '\n')
			if index < 0 {
				w.dropped += int64(len(p))
				break
			}
			w.dropped += int64(index)
			w.flushTruncated()
			p = p[index+1:]
			continue
		}
		index := bytes.IndexByte(p, '\n')
		part := p
		complete := false
		if index >= 0 {
			part = p[:index]
			complete = true
		}
		remaining := sourceWriterLineBytes - len(w.buffer)
		if remaining > len(part) {
			remaining = len(part)
		}
		if remaining > 0 {
			w.buffer = append(w.buffer, part[:remaining]...)
		}
		if remaining < len(part) {
			w.dropping = true
			w.dropped += int64(len(part) - remaining)
		}
		if complete {
			if w.dropping {
				w.flushTruncated()
			} else {
				w.store.Append(w.source, append([]byte(nil), w.buffer...))
				w.buffer = w.buffer[:0]
			}
			p = p[index+1:]
		} else {
			break
		}
	}
	return written, nil
}

func (w *SourceWriter) flushTruncated() {
	marker := []byte(fmt.Sprintf(" ... [truncated %d bytes]", w.dropped))
	line := make([]byte, 0, len(w.buffer)+len(marker))
	line = append(line, w.buffer...)
	line = append(line, marker...)
	w.store.Append(w.source, line)
	w.buffer = w.buffer[:0]
	w.dropping = false
	w.dropped = 0
}
