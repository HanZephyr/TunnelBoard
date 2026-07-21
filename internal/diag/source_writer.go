package diag

import (
	"bytes"
	"sync"
)

// SourceWriter 把进程 stdout/stderr 或 slog 文本安全地送入指定 LogStore source。
type SourceWriter struct {
	store  LogStore
	source LogSource
	mu     sync.Mutex
	buffer []byte
}

func NewSourceWriter(store LogStore, source LogSource) *SourceWriter {
	return &SourceWriter{store: store, source: source}
}

func (w *SourceWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buffer = append(w.buffer, p...)
	for {
		index := bytes.IndexByte(w.buffer, '\n')
		if index < 0 {
			break
		}
		line := append([]byte(nil), w.buffer[:index]...)
		w.buffer = w.buffer[index+1:]
		w.store.Append(w.source, line)
	}
	return len(p), nil
}
