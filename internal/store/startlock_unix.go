//go:build linux || darwin || freebsd || openbsd || netbsd || dragonfly

package store

import (
	"errors"
	"os"
	"syscall"
)

// lockFile takes an exclusive flock on path without waiting for it. Each open
// is its own lock holder, so a second attempt is refused even from the same
// process.
func lockFile(path string) (unlock func(), err error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrStartLocked
		}
		return nil, err
	}
	return func() { _ = f.Close() }, nil
}
