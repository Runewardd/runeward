package backend

import (
	"bytes"
	"io"
	"testing"
)

func TestExecOutputWriterStreamsAndOptionallyCaptures(t *testing.T) {
	var capture, stream bytes.Buffer
	w := execOutputWriter(&capture, &stream, false)
	if _, err := io.WriteString(w, "first"); err != nil {
		t.Fatal(err)
	}
	if capture.String() != "first" || stream.String() != "first" {
		t.Fatalf("capture=%q stream=%q", capture.String(), stream.String())
	}

	capture.Reset()
	stream.Reset()
	w = execOutputWriter(&capture, &stream, true)
	if _, err := io.WriteString(w, "live-only"); err != nil {
		t.Fatal(err)
	}
	if capture.Len() != 0 || stream.String() != "live-only" {
		t.Fatalf("stream-only capture=%q stream=%q", capture.String(), stream.String())
	}
}
