package store

import (
	"errors"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// LockWait waits for a lock another holder releases, and gives up with
// ErrLockTimeout if it is not.
func TestLockWait(t *testing.T) {
	switch runtime.GOOS {
	case "windows", "linux", "darwin", "freebsd", "openbsd", "netbsd", "dragonfly":
	default:
		t.Skip("no file lock on this system")
	}
	path := filepath.Join(t.TempDir(), "x.lock")
	first, err := LockWait(path, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LockWait(path, 50*time.Millisecond); !errors.Is(err, ErrLockTimeout) {
		t.Fatalf("a held lock: got %v, want ErrLockTimeout", err)
	}
	go func() { time.Sleep(100 * time.Millisecond); first() }()
	second, err := LockWait(path, 5*time.Second)
	if err != nil {
		t.Fatalf("after release: %v", err)
	}
	second()
}
