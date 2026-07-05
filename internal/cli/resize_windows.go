//go:build windows

package cli

import "github.com/Runewardd/runeward/internal/backend"

// watchResize primes resize with the current terminal size. Windows has no
// SIGWINCH, so this is best-effort: it sends the initial size once and does not
// follow subsequent resizes. The returned stop function is a no-op.
func watchResize(resize chan<- backend.TermSize) (stop func()) {
	sendSize(resize)
	return func() {}
}
