package store

import (
	"errors"
	"testing"
)

// One launch at a time holds the start lock, and the next gets it as soon as
// the first lets it go.
func TestStartLockIsHeldByOneLaunchAtATime(t *testing.T) {
	isolateConfig(t)
	release, err := TryLockStart()
	if err != nil {
		t.Fatal(err)
	}
	if other, err := TryLockStart(); !errors.Is(err, ErrStartLocked) {
		if other != nil {
			other()
		}
		t.Fatalf("a second launch got the lock while the first held it: %v", err)
	}
	release()
	release() // letting go twice is harmless

	again, err := TryLockStart()
	if err != nil {
		t.Fatalf("the lock was not let go: %v", err)
	}
	again()
}
