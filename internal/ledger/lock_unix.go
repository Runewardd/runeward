//go:build !windows

package ledger

import (
	"fmt"
	"os"
	"syscall"
)

// lockFile takes a non-blocking, advisory exclusive lock on the ledger file so
// that only one runeward process can write a given ledger at a time. Two
// processes appending to the same file each keep their own in-memory sequence
// number and chain tip, so concurrent writes interleave into an out-of-order,
// broken chain. Failing fast here turns that silent corruption into a clear
// startup error. The lock is released automatically when the file is closed.
func lockFile(f *os.File) error {
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fmt.Errorf("ledger: %q is already in use by another runeward process (only one writer per ledger; set a distinct $RUNEWARD_STATE_DIR): %w", f.Name(), err)
	}
	return nil
}
