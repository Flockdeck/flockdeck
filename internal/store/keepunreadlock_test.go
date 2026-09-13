package store

import (
	"errors"
	"os"
	"testing"
	"time"
)

// TestAFileIsNotMovedAsideWhileASaveOfItIsPartWayThrough checks moving a file
// that could not be read out of the way waits for a save of that file this
// process has already begun.
//
// Without it the move raced the save's rename. On Windows a rename of a file
// another rename is replacing is refused, so one of the two waited out the
// whole retry budget, and on a slow machine could lose the race altogether.
func TestAFileIsNotMovedAsideWhileASaveOfItIsPartWayThrough(t *testing.T) {
	isolateConfig(t)
	TakeKept() // whatever earlier tests in this process left behind
	if err := Save("/repo/a", &State{Tabs: []Tab{{Title: "alpha"}}}); err != nil {
		t.Fatalf("save: %v", err)
	}
	p, err := path("/repo/a")
	if err != nil {
		t.Fatal(err)
	}
	noteRead(p, errors.New("the device is not ready"))

	// A save of the same file, part-way through: it holds the file's lock.
	mu := writeLock(p)
	mu.Lock()
	unlocked := false
	defer func() {
		if !unlocked {
			mu.Unlock()
		}
	}()

	done := make(chan error, 1)
	go func() { done <- keepUnread(p, layoutWhat("/repo/a")) }()
	select {
	case err := <-done:
		t.Fatalf("the file was moved aside (%v) while a save of it was part-way through", err)
	case <-time.After(100 * time.Millisecond):
	}
	if _, err := os.Stat(p + unreadSuffix); err == nil {
		t.Fatal("the file was moved aside while a save of it was part-way through")
	}

	mu.Unlock()
	unlocked = true
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("keep: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the file was never moved aside once the save had finished")
	}
	if _, err := os.Stat(p + unreadSuffix); err != nil {
		t.Errorf("the file was not moved aside once the save had finished: %v", err)
	}
}
