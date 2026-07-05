package termrec

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// maxGap caps how long Replay will sleep between two frames in realtime mode so
// idle pauses in a recorded session don't stall playback.
const maxGap = 2 * time.Second

// Replay parses a cast file from r and writes its output frames to w. When
// realtime is true, it sleeps between frames to honor their timestamps (each
// gap capped at maxGap); otherwise output frames are dumped back-to-back.
//
// The header line is validated as asciinema v2 JSON. Only "o" (output) frames
// are replayed; input and other event types are ignored.
func Replay(r io.Reader, w io.Writer, realtime bool) error {
	sc := bufio.NewScanner(r)
	// Terminal frames can be large; grow the scanner buffer well past the
	// default 64KiB line limit.
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

	// First non-empty line is the header.
	var h header
	haveHeader := false
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		if err := json.Unmarshal(line, &h); err != nil {
			return fmt.Errorf("termrec: invalid cast header: %w", err)
		}
		if h.Version != 2 {
			return fmt.Errorf("termrec: unsupported cast version %d", h.Version)
		}
		haveHeader = true
		break
	}
	if err := sc.Err(); err != nil {
		return err
	}
	if !haveHeader {
		return fmt.Errorf("termrec: empty cast: missing header")
	}

	var last float64
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var frame []json.RawMessage
		if err := json.Unmarshal(line, &frame); err != nil {
			return fmt.Errorf("termrec: invalid frame: %w", err)
		}
		if len(frame) != 3 {
			continue
		}

		var t float64
		if err := json.Unmarshal(frame[0], &t); err != nil {
			return fmt.Errorf("termrec: invalid frame timestamp: %w", err)
		}
		var kind string
		if err := json.Unmarshal(frame[1], &kind); err != nil {
			return fmt.Errorf("termrec: invalid frame type: %w", err)
		}
		if kind != "o" {
			continue
		}
		var data string
		if err := json.Unmarshal(frame[2], &data); err != nil {
			return fmt.Errorf("termrec: invalid frame data: %w", err)
		}

		if realtime {
			gap := time.Duration((t - last) * float64(time.Second))
			if gap > maxGap {
				gap = maxGap
			}
			if gap > 0 {
				time.Sleep(gap)
			}
		}
		last = t

		if _, err := io.WriteString(w, data); err != nil {
			return err
		}
	}
	return sc.Err()
}
