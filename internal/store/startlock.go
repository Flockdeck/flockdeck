package store

import (
	"errors"
	"path/filepath"
	"sync"
)

// ErrStartLocked is TryLockStart's answer while another launch holds the lock.
var ErrStartLocked = errors.New("another flockdeck is starting")

// TryLockStart takes the lock a launch holds from looking for a running
// instance until it has put itself on record.
//
// A launch records itself only once its layout is restored, its agents are
// started and its server is answering, which takes a few hundred milliseconds
// on an empty state and longer with a real layout. Two launches inside that
// time, as an impatient second double-click makes, each found nothing on
// record and started an instance, and both resumed the same conversations.
// With the lock the second waits, finds the first on record and joins it.
//
// The lock belongs to the process: the system lets it go if the process dies
// holding it, so a crash cannot leave every later launch waiting. release may
// be called more than once.
func TryLockStart() (release func(), err error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	unlock, err := lockFile(filepath.Join(dir, "start.lock"))
	if err != nil {
		return nil, err
	}
	var once sync.Once
	return func() { once.Do(unlock) }, nil
}
