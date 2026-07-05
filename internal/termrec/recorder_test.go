package termrec

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// failingWriter always errors, to verify Recorder.Write never propagates
// underlying failures back to the tee.
type failingWriter struct{}

func (failingWriter) Write(p []byte) (int, error) { return 0, errors.New("boom") }

func TestRecorderHeaderAndFrames(t *testing.T) {
	var buf bytes.Buffer
	rec := NewRecorder(&buf, 120, 40)

	// Inject a deterministic clock: advance one second per call.
	base := time.Unix(1_700_000_000, 0)
	var tick int
	rec.now = func() time.Time {
		d := time.Duration(tick) * time.Second
		tick++
		return base.Add(d)
	}

	writes := []string{"hello ", "world\n", "second frame"}
	for _, w := range writes {
		n, err := rec.Write([]byte(w))
		if err != nil {
			t.Fatalf("Write returned error: %v", err)
		}
		if n != len(w) {
			t.Fatalf("Write returned %d, want %d", n, len(w))
		}
	}
	if err := rec.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != len(writes)+1 {
		t.Fatalf("got %d lines, want %d", len(lines), len(writes)+1)
	}

	// Header must be valid asciinema v2 JSON.
	var h header
	if err := json.Unmarshal([]byte(lines[0]), &h); err != nil {
		t.Fatalf("header not valid JSON: %v", err)
	}
	if h.Version != 2 || h.Width != 120 || h.Height != 40 {
		t.Fatalf("unexpected header: %+v", h)
	}
	if h.Timestamp != base.Unix() {
		t.Fatalf("header timestamp = %d, want %d", h.Timestamp, base.Unix())
	}

	// Each frame must be [float, "o", data].
	for i, line := range lines[1:] {
		var frame []json.RawMessage
		if err := json.Unmarshal([]byte(line), &frame); err != nil {
			t.Fatalf("frame %d not valid JSON: %v", i, err)
		}
		if len(frame) != 3 {
			t.Fatalf("frame %d has %d elements, want 3", i, len(frame))
		}
		var ts float64
		if err := json.Unmarshal(frame[0], &ts); err != nil {
			t.Fatalf("frame %d timestamp: %v", i, err)
		}
		var kind string
		if err := json.Unmarshal(frame[1], &kind); err != nil {
			t.Fatalf("frame %d kind: %v", i, err)
		}
		if kind != "o" {
			t.Fatalf("frame %d kind = %q, want o", i, kind)
		}
	}
}

func TestReplayDumpRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	rec := NewRecorder(&buf, 80, 24)

	base := time.Unix(1_700_000_000, 0)
	var tick int
	rec.now = func() time.Time {
		d := time.Duration(tick) * 500 * time.Millisecond
		tick++
		return base.Add(d)
	}

	writes := []string{"\x1b[32mgreen\x1b[0m ", "line1\n", "üñïçødé", "\x00\x01\x02"}
	var want strings.Builder
	for _, w := range writes {
		want.WriteString(w)
		if _, err := rec.Write([]byte(w)); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	if err := rec.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	var out bytes.Buffer
	if err := Replay(bytes.NewReader(buf.Bytes()), &out, false); err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if out.String() != want.String() {
		t.Fatalf("replay output mismatch:\ngot  %q\nwant %q", out.String(), want.String())
	}
}

func TestWriteSwallowsUnderlyingError(t *testing.T) {
	rec := NewRecorder(failingWriter{}, 80, 24)
	// Small buffer would still buffer; force flushes by writing a large blob so
	// bufio flushes to the failing writer. Regardless, Write must report the
	// full count and nil error.
	big := strings.Repeat("x", 128*1024)
	n, err := rec.Write([]byte(big))
	if err != nil {
		t.Fatalf("Write returned error on failing writer: %v", err)
	}
	if n != len(big) {
		t.Fatalf("Write returned %d, want %d", n, len(big))
	}

	// A subsequent normal-sized write must also be safe.
	n, err = rec.Write([]byte("more"))
	if err != nil {
		t.Fatalf("second Write error: %v", err)
	}
	if n != len("more") {
		t.Fatalf("second Write returned %d, want %d", n, len("more"))
	}
}
