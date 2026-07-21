package diag

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type LogSource string

const (
	LogTunnelBoard LogSource = "tunnelboard"
	LogCaddy       LogSource = "caddy"
)

type LogCursor struct {
	Generation uint64 `json:"generation"`
	Offset     int64  `json:"offset"`
}

type LogTailResult struct {
	Lines        []string  `json:"lines"`
	NextCursor   LogCursor `json:"nextCursor"`
	Rotated      bool      `json:"rotated"`
	Truncated    bool      `json:"truncated"`
	DroppedBytes int64     `json:"droppedBytes"`
}

type LogStore interface {
	Append(source LogSource, line []byte)
	Tail(source LogSource, cursor *LogCursor) (LogTailResult, error)
	CloseSource(source LogSource) error
	Close() error
}

type logStoreLimits struct {
	fileBytes    int64
	archives     int
	lineBytes    int
	queueBytes   int
	tailBytes    int
	tailLines    int
	closeTimeout time.Duration
}

func defaultLogStoreLimits() logStoreLimits {
	return logStoreLimits{
		fileBytes: 5 << 20, archives: 3, lineBytes: 64 << 10, queueBytes: 1 << 20,
		tailBytes: 256 << 10, tailLines: 500, closeTimeout: 3 * time.Second,
	}
}

type logCommand struct {
	line    []byte
	barrier chan struct{}
	close   chan error
}

type logSourceState struct {
	name             LogSource
	path             string
	limits           logStoreLimits
	commands         chan logCommand
	sendMu           sync.Mutex
	queueMu          sync.Mutex
	queued           int
	dropped          int64
	droppedSinceTail int64
	fileMu           sync.Mutex
	file             *os.File
	size             int64
	generation       uint64
	closed           bool
	done             chan struct{}
	writeHook        func()
}

type logStore struct {
	root    string
	limits  logStoreLimits
	mu      sync.RWMutex
	sources map[LogSource]*logSourceState
	closed  bool
}

func NewLogStore(root string) (LogStore, error) {
	return newLogStore(root, defaultLogStoreLimits())
}

func newLogStore(root string, limits logStoreLimits) (*logStore, error) {
	if limits.fileBytes <= 0 || limits.archives < 0 || limits.lineBytes <= 0 || limits.queueBytes <= 0 || limits.tailBytes <= 0 || limits.tailLines <= 0 {
		return nil, errors.New("diag: invalid log store limits")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("diag: create log directory: %w", err)
	}
	store := &logStore{root: root, limits: limits, sources: make(map[LogSource]*logSourceState, 2)}
	for _, source := range []LogSource{LogTunnelBoard, LogCaddy} {
		state, err := openLogSource(root, source, limits)
		if err != nil {
			_ = store.Close()
			return nil, err
		}
		store.sources[source] = state
	}
	return store, nil
}

func openLogSource(root string, source LogSource, limits logStoreLimits) (*logSourceState, error) {
	path := filepath.Join(root, string(source)+".log")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("diag: open %s log: %w", source, err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	state := &logSourceState{
		name: source, path: path, limits: limits, commands: make(chan logCommand, 4096),
		file: file, size: info.Size(), generation: 1, done: make(chan struct{}),
	}
	if state.size >= limits.fileBytes {
		if err := state.rotateLocked(); err != nil {
			_ = file.Close()
			return nil, err
		}
	}
	go state.run()
	return state, nil
}

func (s *logStore) source(source LogSource) (*logSourceState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	state, ok := s.sources[source]
	if !ok || s.closed {
		return nil, fmt.Errorf("diag: unknown or closed log source %q", source)
	}
	return state, nil
}

func (s *logStore) Append(source LogSource, line []byte) {
	state, err := s.source(source)
	if err != nil {
		return
	}
	state.append(line)
}

func (s *logStore) Tail(source LogSource, cursor *LogCursor) (LogTailResult, error) {
	state, err := s.source(source)
	if err != nil {
		return LogTailResult{}, err
	}
	if err := state.sync(); err != nil {
		return LogTailResult{}, err
	}
	result, err := state.tail(cursor)
	if err != nil {
		return LogTailResult{}, err
	}
	state.queueMu.Lock()
	result.DroppedBytes += state.droppedSinceTail
	state.droppedSinceTail = 0
	state.queueMu.Unlock()
	return result, nil
}

func (s *logStore) CloseSource(source LogSource) error {
	s.mu.Lock()
	state, ok := s.sources[source]
	if ok {
		delete(s.sources, source)
	}
	s.mu.Unlock()
	if !ok {
		return nil
	}
	return state.close(s.limits.closeTimeout)
}

func (s *logStore) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	states := make([]*logSourceState, 0, len(s.sources))
	for _, state := range s.sources {
		states = append(states, state)
	}
	s.sources = map[LogSource]*logSourceState{}
	s.mu.Unlock()
	var first error
	for _, state := range states {
		if err := state.close(s.limits.closeTimeout); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func (s *logSourceState) append(raw []byte) {
	line := prepareLogLine(raw, s.limits.lineBytes)
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	s.queueMu.Lock()
	if s.closed || s.queued+len(line) > s.limits.queueBytes {
		s.dropped += int64(len(line))
		s.droppedSinceTail += int64(len(line))
		s.queueMu.Unlock()
		return
	}
	s.queued += len(line)
	s.queueMu.Unlock()
	select {
	case s.commands <- logCommand{line: line}:
	default:
		s.queueMu.Lock()
		s.queued -= len(line)
		s.dropped += int64(len(line))
		s.droppedSinceTail += int64(len(line))
		s.queueMu.Unlock()
	}
}

func (s *logSourceState) run() {
	defer close(s.done)
	for command := range s.commands {
		if command.line != nil {
			s.queueMu.Lock()
			s.queued -= len(command.line)
			dropped := s.dropped
			s.dropped = 0
			s.queueMu.Unlock()
			if dropped > 0 {
				_ = s.writeLine(prepareLogLine([]byte(fmt.Sprintf("dropped %d bytes", dropped)), s.limits.lineBytes))
			}
			_ = s.writeLine(command.line)
		}
		if command.barrier != nil {
			s.flushDropped()
			close(command.barrier)
		}
		if command.close != nil {
			s.flushDropped()
			s.fileMu.Lock()
			err := s.file.Close()
			s.fileMu.Unlock()
			command.close <- err
			close(command.close)
			return
		}
	}
}

func (s *logSourceState) flushDropped() {
	s.queueMu.Lock()
	dropped := s.dropped
	s.dropped = 0
	s.queueMu.Unlock()
	if dropped > 0 {
		_ = s.writeLine(prepareLogLine([]byte(fmt.Sprintf("dropped %d bytes", dropped)), s.limits.lineBytes))
	}
}

func (s *logSourceState) sync() error {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	s.queueMu.Lock()
	closed := s.closed
	s.queueMu.Unlock()
	if closed {
		return errors.New("diag: log source is closed")
	}
	barrier := make(chan struct{})
	s.commands <- logCommand{barrier: barrier}
	<-barrier
	return nil
}

func (s *logSourceState) close(timeout time.Duration) error {
	s.sendMu.Lock()
	s.queueMu.Lock()
	if s.closed {
		s.queueMu.Unlock()
		s.sendMu.Unlock()
		return nil
	}
	s.closed = true
	s.queueMu.Unlock()
	result := make(chan error, 1)
	select {
	case s.commands <- logCommand{close: result}:
		s.sendMu.Unlock()
	case <-time.After(timeout):
		s.sendMu.Unlock()
		return errors.New("diag: close log source timed out while queuing")
	}
	select {
	case err := <-result:
		return err
	case <-time.After(timeout):
		return errors.New("diag: close log source timed out")
	}
}

func (s *logSourceState) writeLine(line []byte) error {
	if s.writeHook != nil {
		s.writeHook()
	}
	s.fileMu.Lock()
	defer s.fileMu.Unlock()
	if s.size > 0 && s.size+int64(len(line)) > s.limits.fileBytes {
		if err := s.rotateLocked(); err != nil {
			return err
		}
	}
	n, err := s.file.Write(line)
	s.size += int64(n)
	return err
}

func (s *logSourceState) rotateLocked() error {
	if err := s.file.Close(); err != nil {
		return err
	}
	for i := s.limits.archives; i >= 1; i-- {
		target := fmt.Sprintf("%s.%d", s.path, i)
		_ = os.Remove(target)
		if i == 1 {
			if _, err := os.Stat(s.path); err == nil {
				if err := os.Rename(s.path, target); err != nil {
					return fmt.Errorf("diag: rotate current log: %w", err)
				}
			}
		} else {
			source := fmt.Sprintf("%s.%d", s.path, i-1)
			if _, err := os.Stat(source); err == nil {
				if err := os.Rename(source, target); err != nil {
					return fmt.Errorf("diag: rotate archive: %w", err)
				}
			}
		}
	}
	file, err := os.OpenFile(s.path, os.O_CREATE|os.O_RDWR|os.O_TRUNC|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("diag: reopen rotated log: %w", err)
	}
	s.file = file
	s.size = 0
	s.generation++
	return nil
}

func (s *logSourceState) tail(cursor *LogCursor) (LogTailResult, error) {
	s.fileMu.Lock()
	defer s.fileMu.Unlock()
	info, err := s.file.Stat()
	if err != nil {
		return LogTailResult{}, err
	}
	size := info.Size()
	result := LogTailResult{NextCursor: LogCursor{Generation: s.generation}}
	start := int64(0)
	latest := cursor == nil
	if cursor != nil {
		if cursor.Generation != s.generation || cursor.Offset > size || cursor.Offset < 0 {
			result.Rotated = true
			latest = true
		} else {
			start = cursor.Offset
		}
	}
	if latest || size-start > int64(s.limits.tailBytes) {
		candidate := size - int64(s.limits.tailBytes)
		if candidate > 0 {
			start = candidate
			result.Truncated = true
		}
	}
	maxRead := size - start
	if maxRead > int64(s.limits.tailBytes) {
		maxRead = int64(s.limits.tailBytes)
	}
	buf := make([]byte, maxRead)
	n, readErr := s.file.ReadAt(buf, start)
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return LogTailResult{}, readErr
	}
	buf = buf[:n]
	if start > 0 {
		if newline := bytes.IndexByte(buf, '\n'); newline >= 0 {
			start += int64(newline + 1)
			buf = buf[newline+1:]
		} else {
			result.Truncated = true
			result.DroppedBytes += size
			result.NextCursor.Offset = size
			return result, nil
		}
	}
	lastNewline := bytes.LastIndexByte(buf, '\n')
	if lastNewline < 0 {
		result.NextCursor.Offset = start
		return result, nil
	}
	complete := buf[:lastNewline+1]
	completeEnd := start + int64(len(complete))
	lines := bytes.Split(bytes.TrimSuffix(complete, []byte{'\n'}), []byte{'\n'})
	if len(lines) > s.limits.tailLines {
		dropCount := len(lines) - s.limits.tailLines
		for _, line := range lines[:dropCount] {
			start += int64(len(line) + 1)
		}
		lines = lines[dropCount:]
		result.Truncated = true
	}
	result.Lines = make([]string, len(lines))
	for i, line := range lines {
		result.Lines[i] = string(line)
	}
	result.NextCursor.Offset = completeEnd
	if result.Truncated {
		base := int64(0)
		if cursor != nil && !result.Rotated {
			base = cursor.Offset
		}
		if start > base {
			result.DroppedBytes += start - base
		}
	}
	return result, nil
}

func prepareLogLine(raw []byte, maxBytes int) []byte {
	text := strings.TrimRight(string(raw), "\r\n")
	text = strings.ReplaceAll(text, "\r", `\r`)
	text = strings.ReplaceAll(text, "\n", `\n`)
	text = RedactLogText(text)
	if len(text) > maxBytes {
		text = truncateUTF8Bytes(text, maxBytes)
	}
	return append([]byte(text), '\n')
}
