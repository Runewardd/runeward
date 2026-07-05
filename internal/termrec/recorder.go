// Package termrec records governed terminal sessions as asciinema v2 "cast"
// files and replays them. A cast file is a single JSON header line followed by
// one JSON array per output frame: [elapsedSeconds, "o", "data"].
//
// The package is deliberately path-agnostic and depends only on the standard
// library so it can be teed into any terminal PTY stream via io.MultiWriter.
package termrec

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"sync"
	"time"
)

// header is the first line of an asciinema v2 cast file.
type header struct {
	Version   int               `json:"version"`
	Width     int               `json:"width"`
	Height    int               `json:"height"`
	Timestamp int64             `json:"timestamp"`
	Env       map[string]string `json:"env,omitempty"`
}

// Recorder implements io.Writer and captures every write as an asciinema v2
// output frame. It is safe for concurrent use.
//
// Recorder is designed to sit inside an io.MultiWriter(realConn, recorder) tee:
// Write always reports the full byte count and a nil error, even if frame
// encoding fails, so a recording problem can never corrupt or stall the real
// terminal stream.
type Recorder struct {
	mu      sync.Mutex
	bw      *bufio.Writer
	closer  io.Closer
	width   int
	height  int
	started bool
	start   time.Time

	// now is the clock used to compute frame timestamps. It defaults to
	// time.Now and may be overridden within the package for deterministic
	// tests.
	now func() time.Time
}

// NewRecorder wraps w and records writes as asciinema v2 output frames with the
// given terminal dimensions.
func NewRecorder(w io.Writer, width, height int) *Recorder {
	return &Recorder{
		bw:     bufio.NewWriter(w),
		width:  width,
		height: height,
		now:    time.Now,
	}
}

// NewFileRecorder creates (truncating) the cast file at path with 0o600
// permissions and returns a Recorder that closes the file on Close.
func NewFileRecorder(path string, width, height int) (*Recorder, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, err
	}
	r := NewRecorder(f, width, height)
	r.closer = f
	return r, nil
}

// Start writes the cast header if it has not been written yet, anchoring the
// recording clock. It is safe to call multiple times; only the first call has
// an effect. Write calls Start implicitly, so most callers never need it.
func (r *Recorder) Start() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.startLocked()
}

// startLocked writes the header exactly once. The caller must hold r.mu.
func (r *Recorder) startLocked() error {
	if r.started {
		return nil
	}
	r.started = true
	r.start = r.now()
	h := header{
		Version:   2,
		Width:     r.width,
		Height:    r.height,
		Timestamp: r.start.Unix(),
		Env:       map[string]string{"TERM": "xterm-256color"},
	}
	line, err := json.Marshal(h)
	if err != nil {
		return err
	}
	if _, err := r.bw.Write(line); err != nil {
		return err
	}
	return r.bw.WriteByte('\n')
}

// Write records p as a single output frame and returns len(p), nil. Frame
// encoding errors are intentionally swallowed so the enclosing io.MultiWriter
// tee reports the same byte count for every writer and the real terminal stream
// is never disrupted.
func (r *Recorder) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := r.startLocked(); err != nil {
		return len(p), nil
	}

	elapsed := r.now().Sub(r.start).Seconds()
	if elapsed < 0 {
		elapsed = 0
	}

	frame := []interface{}{elapsed, "o", string(p)}
	line, err := json.Marshal(frame)
	if err != nil {
		return len(p), nil
	}
	if _, err := r.bw.Write(line); err != nil {
		return len(p), nil
	}
	if err := r.bw.WriteByte('\n'); err != nil {
		return len(p), nil
	}
	return len(p), nil
}

// Close flushes buffered frames and closes the underlying file if the Recorder
// owns one.
func (r *Recorder) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Ensure a header exists even for an empty session so the file is a valid
	// cast.
	if err := r.startLocked(); err != nil {
		return err
	}
	if err := r.bw.Flush(); err != nil {
		return err
	}
	if r.closer != nil {
		return r.closer.Close()
	}
	return nil
}
