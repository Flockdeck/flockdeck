package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/jmwri/flockdeck/internal/remote"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/store"
	"github.com/jmwri/flockdeck/internal/workspace"
)

// firstPane is the test server's first pane.
func firstPane(t *testing.T, srv *Server, ws *workspace.Workspace) string {
	t.Helper()
	id, ok := ask(srv, func() string { return ws.CurrentTab().Tree.Panes()[0] })
	if !ok {
		t.Fatal("the workspace did not answer")
	}
	return id
}

// addPane opens a tab with a pane of its own, and says the pane's id.
func addPane(t *testing.T, srv *Server, ws *workspace.Workspace, title string) string {
	t.Helper()
	id, ok := ask(srv, func() string { return ws.NewTab(session.KindShell, ws.ActiveRoot(), title).Tree.Panes()[0] })
	if !ok {
		t.Fatal("the workspace did not answer")
	}
	return id
}

// waitIn puts a pane into a wait, and says when the wait began.
func waitIn(t *testing.T, srv *Server, ws *workspace.Workspace, id string) time.Time {
	t.Helper()
	p, _ := ask(srv, func() *workspace.Pane { return ws.Pane(id) })
	p.Sess.SetStatus(session.StatusWaiting, "")
	return p.Sess.StatusSince()
}

// answer puts a pane back to work.
func answer(srv *Server, ws *workspace.Workspace, id string) {
	p, _ := ask(srv, func() *workspace.Pane { return ws.Pane(id) })
	p.Sess.SetStatus(session.StatusWorking, "")
}

// waitingPane puts the test server's first pane into a wait, and says when
// the wait began.
func waitingPane(t *testing.T, srv *Server, ws *workspace.Workspace) (string, time.Time) {
	t.Helper()
	id := firstPane(t, srv, ws)
	return id, waitIn(t, srv, ws, id)
}

func paneName(srv *Server, ws *workspace.Workspace, id string) string {
	name, _ := ask(srv, func() string { return ws.Pane(id).Name })
	return name
}

func due(srv *Server, now time.Time) *remote.Notification {
	out, _ := ask(srv, func() *remote.Notification { return srv.pushDue(now) })
	return out
}

func enrolled(srv *Server) {
	srv.SetRemote(&fakeRemote{ok: true, st: remote.Status{Name: "desk", HostID: "h1"}})
}

// A pane that has been waiting for the delay is pushed once, naming it and
// its project, opening its pane, under the machine's tag; one that waits
// again is pushed again, but not within a minute of the last push. Nothing is
// pushed for a machine that is not enrolled, or with the switch off.
func TestAWaitThatLastsIsPushedOnce(t *testing.T) {
	srv, ws := newTestServer(t)
	id, since := waitingPane(t, srv, ws)
	if n := due(srv, since.Add(time.Hour)); n != nil {
		t.Fatalf("a machine that is not enrolled was due %+v", n)
	}

	enrolled(srv)
	if n := due(srv, since.Add(29*time.Second)); n != nil {
		t.Errorf("a wait of 29 seconds was pushed: %+v", n)
	}
	n := due(srv, since.Add(30*time.Second))
	if n == nil || n.URL != "/d/h1/"+id || n.Tag != "flockdeck-h1" || !strings.HasSuffix(n.Title, " needs you") || n.Body != "On desk" {
		t.Fatalf("a wait of 30 seconds: %+v", n)
	}
	if name := paneName(srv, ws, id); !strings.HasPrefix(n.Title, name+" · ") {
		t.Errorf("the push %q does not name the pane %q and its project", n.Title, name)
	}
	if n := due(srv, since.Add(time.Hour)); n != nil {
		t.Errorf("one wait was pushed twice: %+v", n)
	}

	// Answered, and waiting again: a new wait, told once the minute since the
	// last push has passed.
	answer(srv, ws, id)
	if n := due(srv, time.Now()); n != nil {
		t.Errorf("a pane at work was pushed: %+v", n)
	}
	time.Sleep(2 * time.Millisecond) // a new wait begins at a new time
	again := waitIn(t, srv, ws, id)
	if n := due(srv, again.Add(31*time.Second)); n != nil {
		t.Errorf("a second push came within a minute of the first: %+v", n)
	}
	if n := due(srv, since.Add(30*time.Second+minPushGap)); n == nil {
		t.Error("the second wait was not pushed once the minute had passed")
	}

	ask(srv, func() bool { srv.prefs.Push.Off = true; return true })
	time.Sleep(2 * time.Millisecond)
	answer(srv, ws, id)
	waitIn(t, srv, ws, id)
	if n := due(srv, time.Now().Add(time.Hour)); n != nil {
		t.Errorf("pushed with the switch off: %+v", n)
	}
}

// Agents that stop together are told of in one push that counts them and
// names them, and opens the machine's list rather than one of them. An agent
// that began waiting just before is counted in it, and is not told of again
// when its own delay passes.
func TestAgentsWaitingTogetherArePushedAsOne(t *testing.T) {
	srv, ws := newTestServer(t)
	enrolled(srv)
	a := firstPane(t, srv, ws)
	b := addPane(t, srv, ws, "second")
	c := addPane(t, srv, ws, "third")
	since := waitIn(t, srv, ws, a)
	waitIn(t, srv, ws, b)
	time.Sleep(5 * time.Millisecond)
	late := waitIn(t, srv, ws, c)

	n := due(srv, since.Add(30*time.Second))
	if n == nil || n.Title != "3 agents need you" || n.URL != "/d/h1" || n.Tag != "flockdeck-h1" {
		t.Fatalf("three agents waiting: %+v", n)
	}
	for _, id := range []string{a, b, c} {
		if name := paneName(srv, ws, id); !strings.Contains(n.Body, name) {
			t.Errorf("the push %q does not name %q", n.Body, name)
		}
	}
	if !strings.HasPrefix(n.Body, "On desk: ") {
		t.Errorf("the push does not say which machine: %q", n.Body)
	}
	if n := due(srv, late.Add(30*time.Second+minPushGap)); n != nil {
		t.Errorf("an agent counted in the push was told of again: %+v", n)
	}
}

// A pane somebody is using from their phone -- its terminal open there, typed
// into or tapped -- is one they can see waiting, and is not pushed to the phone
// as well. Once they have left it a while, and it is still waiting, it is.
func TestAPaneUsedFromAPhoneIsNotPushed(t *testing.T) {
	srv, ws := newTestServer(t)
	enrolled(srv)
	id, since := waitingPane(t, srv, ws)

	ts := remoteServer(t, srv)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	h := http.Header{}
	h.Set("Origin", ts.URL)
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/ws/pty?id="+id, &websocket.DialOptions{HTTPHeader: h})
	if err != nil {
		t.Fatalf("open the pane through the relay: %v", err)
	}
	waitFor(t, func() bool { return relayUse.since(id, since) })
	if n := due(srv, since.Add(30*time.Second)); n != nil {
		t.Errorf("a pane open on the phone was pushed to it: %+v", n)
	}
	conn.Close(websocket.StatusNormalClosure, "")
	if n := due(srv, since.Add(31*time.Second)); n != nil {
		t.Errorf("a pane left on the phone a moment ago was pushed to it: %+v", n)
	}
	if n := due(srv, time.Now().Add(phoneLook+time.Second)); n == nil {
		t.Error("a pane left on the phone two minutes ago, still waiting, was not pushed")
	}
}

// A pane used at the desk is not a pane used from a phone.
func TestAPaneUsedAtTheDeskIsStillPushed(t *testing.T) {
	srv, ws := newTestServer(t)
	enrolled(srv)
	id, since := waitingPane(t, srv, ws)
	conn := dialPTY(t, srv, id)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"focus":true}`)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if relayUse.since(id, since) {
		t.Error("a window at the desk was taken for a phone")
	}
	if n := due(srv, since.Add(30*time.Second)); n == nil {
		t.Error("a pane used at the desk was not pushed")
	}
}

// setIdle replaces the server's idle source with a fixed answer, on the
// workspace goroutine, where deskInUse reads it, and forgets the answer
// deskInUse kept from the one before.
func setIdle(srv *Server, d time.Duration, locked, ok bool) {
	ask(srv, func() bool {
		srv.push.idleSince = func() (time.Duration, bool, bool) { return d, locked, ok }
		srv.push.idleMu.Lock()
		srv.push.idleAsk = time.Time{}
		srv.push.idleMu.Unlock()
		return true
	})
}

// The OS is asked again only once its last answer could have changed: an
// idle time of 90 seconds cannot reach two minutes in under 30, and an OS
// with no answer is asked again after a minute. On Linux and macOS each
// asking runs a command, and an agent can wait on somebody at the desk for
// hours.
func TestTheOSIsNotAskedAgainBeforeItsAnswerCouldChange(t *testing.T) {
	srv, _ := newTestServer(t)
	var asked atomic.Int32
	answer := func(d time.Duration, ok bool) {
		ask(srv, func() bool {
			srv.push.idleSince = func() (time.Duration, bool, bool) { asked.Add(1); return d, false, ok }
			srv.push.idleMu.Lock()
			srv.push.idleAsk = time.Time{}
			srv.push.idleMu.Unlock()
			return true
		})
		asked.Store(0)
	}

	t0 := time.Now()
	answer(90*time.Second, true)
	for _, at := range []time.Duration{0, 10 * time.Second, 29 * time.Second} {
		if !srv.deskInUse(t0.Add(at)) {
			t.Fatalf("idle 90 seconds at %v was taken for away", at)
		}
	}
	if n := asked.Load(); n != 1 {
		t.Errorf("the OS was asked %d times in the 30 seconds its answer held, not once", n)
	}
	srv.deskInUse(t0.Add(31 * time.Second))
	if n := asked.Load(); n != 2 {
		t.Errorf("the OS was not asked again once its answer could have changed: %d", n)
	}

	answer(0, false)
	srv.deskInUse(t0)
	srv.deskInUse(t0.Add(59 * time.Second))
	if n := asked.Load(); n != 1 {
		t.Errorf("an OS with no answer was asked %d times inside a minute, not once", n)
	}
	srv.deskInUse(t0.Add(61 * time.Second))
	if n := asked.Load(); n != 2 {
		t.Errorf("an OS with no answer was not asked again after a minute: %d", n)
	}
}

// An idle time the OS can answer decides who is at the desk, with no window
// open here at all: under two minutes the phone is not told, and once two
// minutes have passed, it is.
func TestOSIdleDecidesWhoIsAtTheDesk(t *testing.T) {
	srv, ws := newTestServer(t)
	enrolled(srv)
	_, since := waitingPane(t, srv, ws)

	setIdle(srv, 90*time.Second, false, true)
	if n := due(srv, since.Add(30*time.Second)); n != nil {
		t.Errorf("pushed with the machine idle 90 seconds, under the two minutes: %+v", n)
	}

	setIdle(srv, deskLook+time.Second, false, true)
	if n := due(srv, since.Add(30*time.Second+minPushGap)); n == nil {
		t.Error("not pushed once the machine had been idle two minutes, with no window focused at all")
	}
}

// A locked screen is always away, whatever the idle time -- even one so
// short it would otherwise mean somebody was just at the keyboard.
func TestALockedScreenIsAlwaysAway(t *testing.T) {
	srv, ws := newTestServer(t)
	enrolled(srv)
	_, since := waitingPane(t, srv, ws)
	setIdle(srv, 0, true, true)
	if n := due(srv, since.Add(30*time.Second)); n == nil {
		t.Error("not pushed while the screen was locked")
	}
}

// A window here saying it is "front" no longer buys the desk anything: what
// decides is the OS's own idle time, and three minutes of nobody touching
// the keyboard or mouse anywhere on the machine is a push due, whatever any
// one window last reported of its own focus.
func TestAFocusedWindowDoesNotBlockAPushTheOSSaysIsDue(t *testing.T) {
	srv, ws := newTestServer(t)
	enrolled(srv)
	conn := dialControl(t, srv)
	nextHello(t, conn)
	_, since := waitingPane(t, srv, ws)

	// A window reporting itself "front" -- the old, discarded signal -- is
	// sent here to show it changes nothing: the machine has not been touched
	// for three minutes, by this reading, and the push goes ahead despite it.
	sendCmd(t, conn, command{Cmd: "presence", Kind: "front"})
	setIdle(srv, 3*time.Minute, false, true)
	if n := due(srv, since.Add(30*time.Second)); n == nil {
		t.Error("a stale 'front' report held back a push the OS idle time said was due")
	}
}

// Where the OS cannot say how idle the machine is, a Flockdeck window here
// falls back to reporting real input of its own -- a keystroke, a click, a
// scroll, the pointer moving -- and only that counts: coming to the front by
// itself does not. A window reached through the relay is not this desk.
func TestFallsBackToWindowInputWhenTheOSCannotAnswer(t *testing.T) {
	srv, ws := newTestServer(t)
	enrolled(srv)
	setIdle(srv, 0, false, false)
	conn := dialControl(t, srv)
	nextHello(t, conn)

	// A window through the relay reporting input is not this desk. Checked
	// before anything is reported from the desk itself, so its report is the
	// only one in play.
	ts := remoteServer(t, srv)
	remote, err := dialRemoteControl(ts, ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer remote.CloseNow()
	sendCmd(t, remote, command{Cmd: "presence", Kind: "used"})
	time.Sleep(100 * time.Millisecond)
	if srv.deskInUse(time.Now()) {
		t.Error("a window through the relay was taken for the desk")
	}

	id, since := waitingPane(t, srv, ws)
	if n := due(srv, since.Add(30*time.Second)); n == nil {
		t.Error("not pushed with a window open here that had reported no input at all")
	}

	// Put behind something first, so the next wait is a fresh one to push.
	answer(srv, ws, id)
	time.Sleep(2 * time.Millisecond)
	again := waitIn(t, srv, ws, id)
	sendCmd(t, conn, command{Cmd: "presence", Kind: "used"})
	waitFor(t, func() bool { return srv.deskInUse(time.Now()) })
	if n := due(srv, again.Add(30*time.Second)); n != nil {
		t.Errorf("pushed despite a keydown reported at this desk moments ago: %+v", n)
	}
	if n := due(srv, time.Now().Add(deskLook+time.Second)); n == nil {
		t.Error("not pushed once the input reported here had aged past deskLook")
	}
}

// The delay is a setting, within bounds, and sent anonymously a push says only
// how many agents are waiting, and on which machine.
func TestThePushSettingsShapeWhatIsSent(t *testing.T) {
	srv, ws := newTestServer(t)
	enrolled(srv)
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

	id, since := waitingPane(t, srv, ws)
	if n := due(srv, since.Add(time.Minute)); n != nil {
		t.Errorf("pushed before the delay of two minutes: %+v", n)
	}
	n := due(srv, since.Add(2*time.Minute))
	if n == nil || n.Title != "An agent on desk needs you" || n.Body != "" || n.URL != "/d/h1/"+id {
		t.Errorf("an anonymous push: %+v", n)
	}
	other := addPane(t, srv, ws, "second")
	later := waitIn(t, srv, ws, other)
	n = due(srv, later.Add(2*time.Minute+minPushGap))
	if n == nil || n.Title != "2 agents on desk need you" || n.Body != "" || strings.Contains(n.Title, paneName(srv, ws, other)) {
		t.Errorf("an anonymous push about two: %+v", n)
	}

	sendCmd(t, conn, command{Cmd: "pushDelay", Size: 30})
	nextPrefs(t, conn, func(p store.Prefs) bool { return p.Push.DelaySeconds == 0 })
	sendCmd(t, conn, command{Cmd: "pushNotify", Kind: "off"})
	nextPrefs(t, conn, func(p store.Prefs) bool { return p.Push.Off })
}

// A due push is sent to the relay off the workspace goroutine, and a refusal
// is kept for Settings to say.
func TestAPushIsSentAndItsFailureShown(t *testing.T) {
	srv, ws := newTestServer(t)
	enrolled(srv)
	sent := make(chan remote.Notification, 4)
	refusal := errors.New("the relay said: this relay does not send push notifications")
	ask(srv, func() bool {
		srv.push.send = func(_ context.Context, n remote.Notification) error {
			sent <- n
			return refusal
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

// A pane muted from the phone is left out of the push entirely, while a
// waiting pane that is not muted is still told of, by name, as though the
// muted one did not exist.
func TestAMutedPaneIsLeftOutOfThePush(t *testing.T) {
	srv, ws := newTestServer(t)
	enrolled(srv)
	a := firstPane(t, srv, ws)
	b := addPane(t, srv, ws, "second")
	waitIn(t, srv, ws, a)
	// Timed from the later of the two waits, b's: on a fine-grained clock b
	// goes waiting a little after a, so "30 s after a" is not yet 30 s after
	// b and nothing would be due. Windows' coarser clock gave both the same
	// instant and hid this.
	since := waitIn(t, srv, ws, b)
	if muted, ok := ask(srv, func() bool { return ws.SetPaneMuted(a, true) }); !ok || !muted {
		t.Fatal("muting a pane that exists was refused")
	}

	n := due(srv, since.Add(30*time.Second))
	if n == nil {
		t.Fatal("the unmuted pane's wait was not pushed")
	}
	// Naming one pane, not counting two, is what shows the muted pane was left
	// out rather than merely folded into a count: with both counted this would
	// have been "2 agents need you", opening the machine's list rather than
	// pane b's own pane.
	if n.URL != "/d/h1/"+b {
		t.Errorf("the push opened %q, want the unmuted pane %q alone: %+v", n.URL, b, n)
	}
	if name := paneName(srv, ws, b); !strings.HasPrefix(n.Title, name+" · ") || !strings.HasSuffix(n.Title, " needs you") {
		t.Errorf("the push %q does not name the unmuted pane %q alone", n.Title, name)
	}
}

// Unmuted, a pane's wait is told of exactly as any other's, once the delay
// and the gap since the last push have passed.
func TestUnmutingAPaneBringsItsWaitBack(t *testing.T) {
	srv, ws := newTestServer(t)
	enrolled(srv)
	id, since := waitingPane(t, srv, ws)
	ask(srv, func() bool { return ws.SetPaneMuted(id, true) })
	if n := due(srv, since.Add(30*time.Second)); n != nil {
		t.Fatalf("a muted pane's wait was pushed: %+v", n)
	}

	ask(srv, func() bool { return ws.SetPaneMuted(id, false) })
	n := due(srv, since.Add(30*time.Second))
	if n == nil || n.URL != "/d/h1/"+id {
		t.Fatalf("unmuting did not bring the wait back: %+v", n)
	}
}

// Closing a pane leaves nothing behind that remembers it was muted: there is
// no pane left to mute or unmute.
func TestClosingAPaneForgetsItWasMuted(t *testing.T) {
	srv, ws := newTestServer(t)
	id := addPane(t, srv, ws, "second")
	if muted, ok := ask(srv, func() bool { return ws.SetPaneMuted(id, true) }); !ok || !muted {
		t.Fatal("muting the pane was refused")
	}
	ask(srv, func() bool { return ws.ClosePaneByID(id) })
	if found, ok := ask(srv, func() bool { return ws.SetPaneMuted(id, true) }); !ok || found {
		t.Error("a closed pane could still be muted, as though it were still remembered")
	}
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
