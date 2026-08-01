package profile

import (
	"errors"
	"testing"
)

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

func TestPrintPropagatesWriterErrors(t *testing.T) {
	err := Print(failingWriter{}, &Profile{})
	if err == nil {
		t.Fatal("Print returned nil for a failing writer")
	}
}
