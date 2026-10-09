//go:build unix

package server

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// A named pipe called *.jsonl in the recordings folder must not hold a listing:
// opening one for reading waits for a writer, and the socket's goroutine would
// wait with it.
func TestRecordingsListDoesNotBlockOnANamedPipe(t *testing.T) {
	e := newArtifactEnv(t)
	e.allow(t)
	good := writeRec(t, "shop-0a1b2c3d", "20261001T101530Z-0123abcd.jsonl", recStarted())
	fifo := filepath.Join(filepath.Dir(good), "20261002T101530Z-eeee0001.jsonl")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("cannot make a named pipe here: %v", err)
	}
	t.Cleanup(func() {
		// Lets go of anything still waiting on it if the test failed.
		if f, err := os.OpenFile(fifo, os.O_RDWR|syscall.O_NONBLOCK, 0); err == nil {
			f.Close()
		}
	})

	done := make(chan []recItem, 1)
	go func() { done <- scanRecordings(t.Context()) }()
	select {
	case got := <-done:
		if len(got) != 1 || got[0].path != good {
			t.Errorf("listed %+v, want only the regular file", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the listing blocked on a named pipe")
	}

	// And through the socket, which must still answer.
	conn, sess := e.open(t)
	got := recNames(itemsOf(t, ask2(t, conn, sess, map[string]any{"op": "list", "kind": "recordings"})))
	if len(got) != 1 || !got["20261001T101530Z-0123abcd.jsonl"] {
		t.Errorf("socket listed %v", got)
	}
}
