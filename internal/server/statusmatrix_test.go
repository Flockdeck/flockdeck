package server

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/session"
)

// The tests in this file are docs/status-matrix.md's section F row that the
// server derives from a pane's status (what is pushed to a phone) and the two
// halves of row E2 as the pane view states them.

// TestStatusMatrixWhatIsPushed is row F5: a phone is pushed to about a pane
// needing its user -- waiting, or blocked -- once it has been so for the
// delay, and never about one that is working, idle, starting or exited.
func TestStatusMatrixWhatIsPushed(t *testing.T) {
	for _, c := range []struct {
		status session.Status
		pushed bool
	}{
		{session.StatusWaiting, true},
		{session.StatusBlocked, true},
		{session.StatusWorking, false},
		{session.StatusIdle, false},
		{session.StatusStarting, false},
		{session.StatusExited, false},
	} {
		t.Run(c.status.String(), func(t *testing.T) {
			srv, ws := newTestServer(t)
			enrolled(srv)
			id := firstPane(t, srv, ws)
			since, ok := ask(srv, func() time.Time {
				p := ws.Pane(id)
				p.Sess.SetStatus(c.status, "Bash")
				return p.Sess.StatusSince()
			})
			if !ok {
				t.Fatal("the workspace did not answer")
			}
			n := due(srv, since.Add(time.Hour))
			if got := n != nil; got != c.pushed {
				t.Errorf("pushed = %v (%+v), want %v", got, n, c.pushed)
			}
		})
	}
}

// TestStatusMatrixExitWithAnError is row E2 as the web UI sees it: a pane
// whose process exited non-zero is sent as failed with the exit code in its
// error, where a clean exit carries neither.
func TestStatusMatrixExitWithAnError(t *testing.T) {
	srv, ws := newTestServer(t)
	id := firstPane(t, srv, ws)
	exit := func(code int) {
		t.Helper()
		if ok, _ := ask(srv, func() bool {
			return ws.Pane(id).Sess.WriteString(fmt.Sprintf("exit %d\r", code)) == nil
		}); !ok {
			t.Fatal("could not write to the pane")
		}
		deadline := time.Now().Add(30 * time.Second)
		for {
			if done, _ := ask(srv, func() bool { return ws.Pane(id).Sess.Exited() }); done {
				return
			}
			if time.Now().After(deadline) {
				t.Fatal("the process never exited")
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	view := func() (pv paneView) {
		t.Helper()
		ask(srv, func() bool {
			pv = srv.snapshot().Panes[id]
			return true
		})
		return
	}

	exit(2)
	pv := view()
	if pv.Status != "exited" || !pv.Failed || !strings.Contains(pv.Err, "exit code 2") {
		t.Errorf("failed exit sent as status %q failed=%v err=%q", pv.Status, pv.Failed, pv.Err)
	}
}

// TestStatusMatrixCleanExitIsNotFailed is the other half of E2.
func TestStatusMatrixCleanExitIsNotFailed(t *testing.T) {
	srv, ws := newTestServer(t)
	id := firstPane(t, srv, ws)
	ask(srv, func() bool { return ws.Pane(id).Sess.WriteString("exit 0\r") == nil })
	deadline := time.Now().Add(30 * time.Second)
	for {
		if done, _ := ask(srv, func() bool { return ws.Pane(id).Sess.Exited() }); done {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the process never exited")
		}
		time.Sleep(20 * time.Millisecond)
	}
	var pv paneView
	ask(srv, func() bool {
		pv = srv.snapshot().Panes[id]
		return true
	})
	if pv.Status != "exited" || pv.Failed || pv.Err != "" {
		t.Errorf("clean exit sent as status %q failed=%v err=%q", pv.Status, pv.Failed, pv.Err)
	}
}
