package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/store"
)

// blockSaves makes every save of the list of open projects fail, as a full
// disk or a state folder that cannot be written to would: a directory stands
// where the file goes.
func blockSaves(t *testing.T) {
	t.Helper()
	dir, err := store.Dir()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "session.json")
	_ = os.Remove(path)
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
}

// Quit and Restart saved the layout first and ignored whether that worked, so
// with the disk full or the state folder read-only everything that had changed
// since the last save was lost without a word. The first is turned down, with
// the reason, in the window that asked. Asking again goes ahead: flockdeck has
// still to be quittable on a disk that stays full.
func TestAQuitOrRestartThatCannotSaveTheLayoutSaysSoFirst(t *testing.T) {
	for _, cmd := range []string{"quit", "restart"} {
		t.Run(cmd, func(t *testing.T) {
			srv, _ := newTestServer(t)
			asked := make(chan struct{}, 2)
			if cmd == "quit" {
				srv.OnQuit = func() { asked <- struct{}{} }
			} else {
				srv.OnRestart = func() { asked <- struct{}{} }
			}
			blockSaves(t)
			conn := dialControl(t, srv)
			nextState(t, conn, nil)

			sendCmd(t, conn, command{Cmd: cmd})
			var note noticeMsg
			readUntil(t, conn, "notice", &note)
			if !note.Error || !strings.Contains(note.Text, "could not be saved") || !strings.Contains(note.Text, "again") {
				t.Errorf("%s with a save that failed was answered %+v, want the reason and how to go ahead anyway", cmd, note)
			}
			select {
			case <-asked:
				t.Fatalf("flockdeck was asked to %s although the layout could not be saved", cmd)
			case <-time.After(300 * time.Millisecond):
			}

			sendCmd(t, conn, command{Cmd: cmd})
			select {
			case <-asked:
			case <-time.After(5 * time.Second):
				t.Fatalf("asked a second time, having been told, flockdeck still did not %s", cmd)
			}
		})
	}
}

// Detach closed the window as soon as it was told it had detached, taking any
// word of a save that failed with it. The detach still happens -- the agents
// run on, and the save is tried again -- but the window stays, and says so.
func TestADetachThatCannotSaveTheLayoutSaysSo(t *testing.T) {
	srv, _ := newTestServer(t)
	blockSaves(t)
	conn := dialControl(t, srv)
	nextState(t, conn, nil)

	sendCmd(t, conn, command{Cmd: "detach"})
	var note noticeMsg
	readUntil(t, conn, "notice", &note)
	if !note.Error || !strings.Contains(note.Text, "could not be saved") {
		t.Errorf("detach with a save that failed was answered %+v, want the failure told", note)
	}
	if !srv.Detached() {
		t.Error("the instance did not detach")
	}
}
