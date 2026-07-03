//go:build !linux

package egress

import (
	"errors"
	"log"
)

// TransparentProxy is a no-op placeholder on non-Linux platforms; transparent
// (SO_ORIGINAL_DST based) interception is only supported on Linux.
type TransparentProxy struct {
	Policy Policy
	Logger *log.Logger
}

// Serve always returns an error on non-Linux platforms.
func (t *TransparentProxy) Serve(addr string) error {
	return errors.New("transparent egress proxy is only supported on linux")
}
