package store

import (
	"errors"
	"fmt"
	"time"
)

// ErrLockTimeout is what LockWait returns when another process holds the lock
// for the whole of the wait.
var ErrLockTimeout = errors.New("timed out waiting for another flockdeck process")

// LockWait takes an exclusive lock on the file at path, waiting up to timeout
// for another process to let it go, and returns what releases it.
//
// It is for a read-modify-write of a file two processes can both change, which
// a mutex in one process cannot cover and an atomic rename does not either:
// each reads the old file and renames its own over the other's change. The lock
// is on a file of its own, not the one being written, because the write
// replaces that file by rename. The system lets the lock go if the process
// dies holding it. Where there is no lock to take (see lockFile) it returns at
// once and holds nothing.
func LockWait(path string, timeout time.Duration) (release func(), err error) {
	deadline := time.Now().Add(timeout)
	for wait := 2 * time.Millisecond; ; {
		unlock, err := lockFile(path)
		if err == nil {
			return unlock, nil
		}
		if !errors.Is(err, ErrStartLocked) {
			return nil, fmt.Errorf("lock %s: %w", path, err)
		}
		if !time.Now().Before(deadline) {
			return nil, ErrLockTimeout
		}
		time.Sleep(wait)
		if wait < 25*time.Millisecond {
			wait *= 2
		}
	}
}
