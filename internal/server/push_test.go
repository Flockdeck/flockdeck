package server

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/remote"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/store"
	"github.com/jmwri/flockdeck/internal/workspace"
)

// waitingPane puts the test server's first pane into a wait, and says when
// the wait began.
func waitingPane(t *testing.T, srv *Server, ws *workspace.Workspace) (string, time.Time) {
	t.Helper()
	id, ok := ask(srv, func() string { return ws.CurrentTab().Tree.Panes()[0] })
	if !ok {
		t.Fatal("the workspace did not answer")
	}
	p, _ := ask(srv, func() *workspace.Pane { return ws.Pane(id) })
	p.Sess.SetStatus(session.StatusWaiting, "")
	return id, p.Sess.StatusSince()
}

func due(srv *Server, now time.Time) []remote.Notification {
	out, _ := ask(srv, func() []remote.Notification { return srv.pushesDue(now) })
	return out
}

// A pane that has been waiting for the delay is pushed once, naming it and
// its project; one that waits again is pushed again. Nothing is pushed for a
// machine that is not enrolled, or with the switch off.
func TestAWaitThatLastsIsPushedOnce(t *testing.T) {
	srv, ws := newTestServer(t)
	id, since := waitingPane(t, srv, ws)
	if n := due(srv, since.Add(time.Hour)); len(n) != 0 {
		t.Fatalf("a machine that is not enrolled was due %+v", n)
	}

	srv.SetRemote(&fakeRemote{ok: true, st: remote.Status{Name: "desk", HostID: "h1"}})
	if n := due(srv, since.Add(29*time.Second)); len(n) != 0 {
		t.Errorf("a wait of 29 seconds was pushed: %+v", n)
	}
	n := due(srv, since.Add(30*time.Second))
	if len(n) != 1 || n[0].PaneID != id || !strings.HasSuffix(n[0].Title, " needs you") || n[0].Body != "On desk" {
		t.Fatalf("a wait of 30 seconds: %+v", n)
	}
	name, _ := ask(srv, func() string { return ws.Pane(id).Name })
	if !strings.HasPrefix(n[0].Title, name+" · ") {
		t.Errorf("the push %q does not name the pane %q and its project", n[0].Title, name)
	}
	if n := due(srv, since.Add(time.Hour)); len(n) != 0 {
		t.Errorf("one wait was pushed twice: %+v", n)
	}

	// Answered, and waiting again: a new wait.
	p, _ := ask(srv, func() *workspace.Pane { return ws.Pane(id) })
	p.Sess.SetStatus(session.StatusWorking, "")
	if n := due(srv, time.Now()); len(n) != 0 {
		t.Errorf("a pane at work was pushed: %+v", n)
	}
	time.Sleep(2 * time.Millisecond) // a new wait begins at a new time
	p.Sess.SetStatus(session.StatusWaiting, "")
	again := p.Sess.StatusSince()
	if n := due(srv, again.Add(31*time.Second)); len(n) != 1 {
		t.Errorf("the second wait was not pushed: %+v", n)
	}

	srv.setPushOff(true)
	time.Sleep(2 * time.Millisecond)
	p.Sess.SetStatus(session.StatusWorking, "")
	p.Sess.SetStatus(session.StatusWaiting, "")
	if n := due(srv, time.Now().Add(time.Hour)); len(n) != 0 {
		t.Errorf("pushed with the switch off: %+v", n)
	}
}

// The delay is a setting, within bounds, and sent anonymously a push says only
// which machine an agent is on.
func TestThePushSettingsShapeWhatIsSent(t *testing.T) {
	srv, ws := newTestServer(t)
	srv.SetRemote(&fakeRemote{ok: true, st: remote.Status{Name: "desk", HostID: "h1"}})
	conn := dialControl(t, srv)
	nextHello(t, conn)

	sendCmd(t, conn, command{Cmd: "pushDelay", Size: 120})
	nextPrefs(t, conn, func(p store.Prefs) bool { return p.Push.DelaySeconds == 120 })
	sendCmd(t, conn, command{Cmd: "pushDelay", Size: 1}) // below the bounds, and refused
	sendCmd(t, conn, command{Cmd: "pushAnonymous", Kind: "on"})
	got := nextPrefs(t, conn, func(p store.Prefs) bool { return p.Push.Anonymous })
	if got.Push.DelaySeconds != 120 {
		t.Errorf("a delay of a second was taken: %+v", got.Push)
	}
	if !store.LoadPrefs().Push.Anonymous {
		t.Error("sending anonymously did not reach the disk")
	}

	_, since := waitingPane(t, srv, ws)
	if n := due(srv, since.Add(time.Minute)); len(n) != 0 {
		t.Errorf("pushed before the delay of two minutes: %+v", n)
	}
	n := due(srv, since.Add(2*time.Minute))
	if len(n) != 1 || n[0].Title != "An agent on desk needs you" || n[0].Body != "" {
		t.Errorf("an anonymous push: %+v", n)
	}

	sendCmd(t, conn, command{Cmd: "pushDelay", Size: 30})
	nextPrefs(t, conn, func(p store.Prefs) bool { return p.Push.DelaySeconds == 0 })
	sendCmd(t, conn, command{Cmd: "pushNotify", Kind: "off"})
	nextPrefs(t, conn, func(p store.Prefs) bool { return p.Push.Off })
}

// A due wait is sent to the relay off the workspace goroutine, and a refusal
// is kept for Settings to say, until one goes through.
func TestAPushIsSentAndItsFailureShown(t *testing.T) {
	srv, ws := newTestServer(t)
	srv.SetRemote(&fakeRemote{ok: true, st: remote.Status{Name: "desk", HostID: "h1"}})
	sent := make(chan remote.Notification, 4)
	refusal := errors.New("the relay said: this relay does not send push notifications")
	fail := true
	ask(srv, func() bool {
		srv.push.send = func(_ context.Context, n remote.Notification) error {
			sent <- n
			if fail {
				return refusal
			}
			return nil
		}
		return true
	})
	_, since := waitingPane(t, srv, ws)
	ask(srv, func() bool { srv.watchWaits(since.Add(time.Minute)); return true })
	select {
	case n := <-sent:
		if n.Body != "On desk" {
			t.Errorf("sent %+v", n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("nothing was sent")
	}
	waitFor(t, func() bool { v := srv.remoteSnapshot(); return v != nil && v.PushError == refusal.Error() })
}

// waitFor waits for cond, for a few seconds at most.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("it never happened")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
