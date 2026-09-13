//go:build windows

package creds

import (
	"os"
	"syscall"
	"testing"
	"time"
)

// hold is how long the other process keeps the store in these tests: longer
// than a first attempt, well inside the state directory's retry budget.
const hold = 400 * time.Millisecond

// A key saved while something else has the store open -- a pane reading it
// from a process of its own, the picker's probe, the virus scanner -- is
// saved once the hold is gone, rather than failing "Access is denied": Windows
// refuses to replace a file anything has open, and storeMu reaches none of
// them.
func TestAKeySavedWhileTheStoreIsOpenElsewhereIsSaved(t *testing.T) {
	isolateConfig(t)
	if err := Set("openai", "sk-test-first"); err != nil {
		t.Fatal(err)
	}
	held, err := os.Open(storePath(t))
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(hold)
		held.Close()
	}()

	start := time.Now()
	if err := Set("anthropic", "sk-test-second"); err != nil {
		t.Fatalf("the save was lost to a %v hold on the store: %v", hold, err)
	}
	if waited := time.Since(start); waited < hold {
		t.Errorf("the save reported success after %v, but the store was held for %v", waited, hold)
	}
	if !Has("openai") || !Has("anthropic") {
		t.Error("the store does not hold both keys")
	}
}

// A pane started while another process is replacing the store waits the
// replacement out and finds its key, rather than reading a refused open as
// no key at all.
func TestAKeyReadWhileTheStoreIsBeingReplacedIsFound(t *testing.T) {
	isolateConfig(t)
	if err := Set("openai", "sk-test"); err != nil {
		t.Fatal(err)
	}
	holdExclusively(t, storePath(t), hold)
	if !Has("openai") {
		t.Errorf("the key read as missing while another process held the store for %v", hold)
	}
}

// holdExclusively opens path allowing nobody else in, as a save part-way
// through replacing the file does, and lets go after d. Every other open made
// meanwhile is refused with a sharing violation.
func holdExclusively(t *testing.T, path string, d time.Duration) {
	t.Helper()
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	h, err := syscall.CreateFile(p, syscall.GENERIC_READ, 0, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatalf("hold %s: %v", path, err)
	}
	released := make(chan struct{})
	go func() {
		time.Sleep(d)
		syscall.CloseHandle(h)
		close(released)
	}()
	t.Cleanup(func() { <-released })
}
