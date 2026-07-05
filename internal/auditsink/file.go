package auditsink

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sync"

	"github.com/Runewardd/runeward/internal/ledger"
)

// fileSink appends each event as one JSON line to a file. Local disk writes
// are fast, so writes happen synchronously under a mutex; Emit still never
// fails the caller (write errors are logged, not returned).
type fileSink struct {
	mu     sync.Mutex
	f      *os.File
	path   string
	logger *slog.Logger

	loggedErr bool // avoid spamming identical write errors
}

// NewFileSink opens (creating if needed) the JSON Lines file at path for
// appending. The file is opened O_APPEND|O_CREATE|O_WRONLY with mode 0o600.
func NewFileSink(path string, logger *slog.Logger) (Sink, error) {
	if logger == nil {
		logger = slog.Default()
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open audit file %q: %w", path, err)
	}
	return &fileSink{f: f, path: path, logger: logger}, nil
}

// Emit appends ev as one JSON line. Errors are logged but never returned.
func (s *fileSink) Emit(ev ledger.Event) {
	line, err := json.Marshal(ev)
	if err != nil {
		s.logger.Warn("auditsink: marshal event failed", "err", err, "seq", ev.Seq)
		return
	}
	line = append(line, '\n')

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.f == nil {
		return
	}
	if _, err := s.f.Write(line); err != nil && !s.loggedErr {
		s.logger.Warn("auditsink: write to audit file failed", "path", s.path, "err", err)
		s.loggedErr = true
	}
}

// Close flushes and closes the underlying file.
func (s *fileSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.f == nil {
		return nil
	}
	err := s.f.Close()
	s.f = nil
	return err
}
