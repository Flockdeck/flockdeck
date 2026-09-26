package server

import (
	"os"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/session"
)

// The tests in this file are docs/status-matrix.md's section F rows that the
// server derives from a pane's status: the outcome a fan-out or a todo step
// records when its pane closes, and what is pushed to a phone. A row the code
// gets wrong today is written as the behaviour it should have and skipped
// through knownBug.

// knownBug skips a case the matrix records as a confirmed bug, unless
// FLOCKDECK_STATUS_BUGS asks for the known bugs to be run.
func knownBug(t *testing.T, row, why string) {
	t.Helper()
	if os.Getenv("FLOCKDECK_STATUS_BUGS") == "" {
		t.Skipf("known bug, docs/status-matrix.md row %s: %s (FLOCKDECK_STATUS_BUGS=1 runs it)", row, why)
	}
}

// TestStatusMatrixOutcomeOfEachStatus is rows F3 and F4: what fanoutOutcome --
// the fan-out history's outcome, and the one a todo step's attempt records,
// ticking the step when it is "done" -- reads off each status.
func TestStatusMatrixOutcomeOfEachStatus(t *testing.T) {
	cases := []struct {
		row        string
		status     session.Status
		detail     string
		wantKind   string
		wantDetail string
		notDone    bool // only that it must not read as done
		bug        string
	}{
		{row: "F3", status: session.StatusIdle, wantKind: "done"},
		{row: "F3", status: session.StatusExited, wantKind: "done"},
		{row: "F3", status: session.StatusWaiting, detail: "AskUserQuestion", wantKind: "needs", wantDetail: "AskUserQuestion"},
		{row: "F3", status: session.StatusBlocked, detail: "Bash", wantKind: "failed", wantDetail: "A tool call was denied: Bash."},
		{row: "F3", status: session.StatusBlocked, wantKind: "failed", wantDetail: "A tool call was denied."},
		{row: "F4", status: session.StatusWorking, notDone: true},
		{row: "F4", status: session.StatusStarting, notDone: true},
	}
	for _, c := range cases {
		t.Run(c.row+"/"+c.status.String()+"/"+c.detail, func(t *testing.T) {
			if c.bug != "" {
				knownBug(t, c.row, c.bug)
			}
			srv, ws := newTestServer(t)
			id := firstPane(t, srv, ws)
			kind, detail := "", ""
			ok, _ := ask(srv, func() bool {
				p := ws.Pane(id)
				if p == nil || p.Sess == nil {
					return false
				}
				p.Sess.SetStatus(c.status, c.detail)
				kind, detail = srv.fanoutOutcome(p)
				return true
			})
			if !ok {
				t.Fatal("the pane never started")
			}
			if c.notDone {
				if kind == "done" {
					t.Errorf("outcome = %q %q, want anything but done", kind, detail)
				}
				return
			}
			if kind != c.wantKind || detail != c.wantDetail {
				t.Errorf("outcome = %q %q, want %q %q", kind, detail, c.wantKind, c.wantDetail)
			}
		})
	}
}

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
