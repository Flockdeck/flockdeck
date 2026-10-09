//go:build unix

package record

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// mkfifo makes a named pipe and arranges for anything blocked opening it to be
// let go when the test ends, so that a failing test does not leave a goroutine
// stuck for the rest of the run.
func mkfifo(t *testing.T, path string) {
	t.Helper()
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skipf("cannot make a named pipe here: %v", err)
	}
	t.Cleanup(func() {
		if f, err := os.OpenFile(path, os.O_RDWR|syscall.O_NONBLOCK, 0); err == nil {
			f.Close()
		}
	})
}

func finishesWithin(t *testing.T, d time.Duration, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { defer close(done); fn() }()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("%s blocked for %v", what, d)
	}
}

// A named pipe called *.jsonl would hold List until something wrote to it.
func TestListDoesNotBlockOnANamedPipe(t *testing.T) {
	dir, folder := recFolder(t)
	mkfifo(t, filepath.Join(folder, "20261001T101530Z-00000009.jsonl"))
	good := filepath.Join(folder, "20261001T101530Z-00000002.jsonl")
	if err := os.WriteFile(good, []byte(startLine(2)), 0o600); err != nil {
		t.Fatal(err)
	}
	var got []Info
	var err error
	finishesWithin(t, 5*time.Second, "List", func() { got, err = List(dir) })
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Path != good {
		t.Errorf("List = %+v, want only the regular file", got)
	}
}

// If a regular file is replaced by a pipe between the check and the open, the
// open still must not wait.
func TestOpenNoBlockDoesNotWaitOnANamedPipe(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.jsonl")
	mkfifo(t, p)
	finishesWithin(t, 5*time.Second, "openNoBlock", func() {
		f, err := openNoBlock(p)
		if err != nil {
			return
		}
		defer f.Close()
		if fi, err := f.Stat(); err == nil && fi.Mode().IsRegular() {
			t.Error("a named pipe reported itself as regular")
		}
	})
}

func TestReadFirstLineRefusesANamedPipe(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.jsonl")
	mkfifo(t, p)
	finishesWithin(t, 5*time.Second, "readFirstLine", func() {
		if _, err := readFirstLine(p); err == nil {
			t.Error("readFirstLine read a named pipe")
		}
	})
}
