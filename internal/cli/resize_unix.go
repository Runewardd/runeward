//go:build !windows

package cli

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/Runewardd/runeward/internal/backend"
)

// watchResize primes resize with the current terminal size and then keeps it
// updated as the controlling terminal is resized, using SIGWINCH. The returned
// stop function detaches the signal handler and should be deferred by the
// caller.
func watchResize(resize chan<- backend.TermSize) (stop func()) {
	sendSize(resize)
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	go func() {
		for range winch {
			sendSize(resize)
		}
	}()
	return func() { signal.Stop(winch) }
}
