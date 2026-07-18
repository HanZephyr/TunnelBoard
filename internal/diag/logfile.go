package diag

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// LogFile 是定长滚动日志文件：写入超过 maxBytes 时，把当前文件改名为 <name>.1
// （覆盖旧档）并重新开始。滚动仅保留一档，避免无界增长。
type LogFile struct {
	mu   sync.Mutex
	path string
	max  int64
	file *os.File
	size int64
}

// OpenLogFile 打开（或创建）滚动日志文件；已有内容继续追加。
func OpenLogFile(path string, maxBytes int64) (*LogFile, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("diag: create log dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("diag: open log file: %w", err)
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return &LogFile{path: path, max: maxBytes, file: f, size: st.Size()}, nil
}

// Write 追加一行；超限先滚动。实现 io.Writer。
func (l *LogFile) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.size+int64(len(p)) > l.max {
		if err := l.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := l.file.Write(p)
	l.size += int64(n)
	return n, err
}

// Close 关闭文件。
func (l *LogFile) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.file.Close()
}

// rotate 关闭当前文件并改名为 .1（覆盖），再开新文件。
func (l *LogFile) rotate() error {
	if err := l.file.Close(); err != nil {
		return err
	}
	if err := os.Rename(l.path, l.path+".1"); err != nil {
		return fmt.Errorf("diag: rotate log file: %w", err)
	}
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("diag: reopen log file: %w", err)
	}
	l.file = f
	l.size = 0
	return nil
}
