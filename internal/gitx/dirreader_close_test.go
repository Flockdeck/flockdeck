package gitx

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

// expiringContext is a context that runs out of time when expire is called, and
// not before: Err reports context.DeadlineExceeded, as a deadline's does, from
// the moment Done is closed. A test uses it to end a scan at a point it has
// chosen instead of after a number of seconds, which a loaded machine can reach
// early or late.
func expiringContext() (ctx context.Context, expire func()) {
	parent, cancel := context.WithCancel(context.Background())
	c := &expiring{Context: parent}
	return c, func() {
		c.expired.Store(true)
		cancel()
	}
}

type expiring struct {
	context.Context
	expired atomic.Bool
}

func (c *expiring) Err() error {
	if c.expired.Load() {
		return context.DeadlineExceeded
	}
	return c.Context.Err()
}

// A ReadDir that was given up on is still running when the scan moves on and
// closes the folder. Closing the handle under it freed the directory buffer
// that the read was draining, and on Windows the read died with a nil pointer
// dereference in os.(*File).readdir, taking the whole program with it.
//
// The read is held still inside the call, the scan is ended so the call is
// given up on, and the folder is closed while the read is known to be running.
// Nothing here depends on how long anything takes.
func TestClosingAFolderWhileAGivenUpReadIsStillRunningIsSafe(t *testing.T) {
	// The call is given up on by the scan ending, never by this timer.
	wasTimeout := statTimeout
	statTimeout = time.Minute
	t.Cleanup(func() { statTimeout = wasTimeout })
	t.Cleanup(func() { dirReadStep.Store(nil) })

	dir := t.TempDir()
	for i := 0; i < 50; i++ {
		if err := os.WriteFile(filepath.Join(dir, "file-with-a-longish-name-"+strconv.Itoa(i)), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	for i := 0; i < 20; i++ {
		ctx, expire := expiringContext()
		d, err := newScanFS(ctx).OpenDir(dir)
		if err != nil {
			t.Fatal(err)
		}

		// The hook is called for every read in the program, including one
		// that an earlier test gave up on and that is still returning, so it
		// answers only to this reader.
		reading, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
		step := func(of *dirReader, stage string) {
			if of != d {
				return
			}
			switch stage {
			case "reading":
				close(reading)
				<-release
			case "read":
				close(finished)
			}
		}
		dirReadStep.Store(&step)

		given := make(chan error, 1)
		go func() {
			_, err := d.next(10)
			given <- err
		}()
		<-reading
		expire() // the scan is over: the call is given up on while the read runs
		if err := <-given; !errors.Is(err, errScanTooLong) {
			t.Fatalf("the call given up on returned %v, want the scan's end", err)
		}

		d.close() // the read is still running
		if _, err := d.d.Stat(); err != nil {
			t.Fatalf("the folder was closed under a read that was still running: %v", err)
		}

		close(release)
		<-finished
		// The read returned on an open handle and closed it when it was done.
		// Closing a closed file is the one call that says so the same way on
		// every system.
		if err := d.d.Close(); !errors.Is(err, fs.ErrClosed) {
			t.Fatalf("the folder was not closed once the read had returned: %v", err)
		}
	}
}
