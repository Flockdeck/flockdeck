package server

import (
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/gitx"
	"github.com/jmwri/flockdeck/internal/store"
	"github.com/jmwri/flockdeck/internal/workspace"
)

// Whether the header may name a pane whose work would conflict is chosen in
// Settings, is off until then, and is kept.
func TestConflictRadarIsOffByDefaultAndKept(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	if hello := nextHello(t, conn); hello.Prefs.ConflictRadar {
		t.Fatal("the conflict radar starts on")
	}

	sendCmd(t, conn, command{Cmd: "conflictRadar", Kind: "on"})
	nextPrefs(t, conn, func(p store.Prefs) bool { return p.ConflictRadar })
	if !store.LoadPrefs().ConflictRadar {
		t.Error("the setting did not reach disk")
	}

	sendCmd(t, conn, command{Cmd: "conflictRadar", Kind: "off"})
	nextPrefs(t, conn, func(p store.Prefs) bool { return !p.ConflictRadar })
	if store.LoadPrefs().ConflictRadar {
		t.Error("turning the setting off did not reach disk")
	}

	// Anything but an explicit "on" is off.
	sendCmd(t, conn, command{Cmd: "conflictRadar", Kind: "on"})
	nextPrefs(t, conn, func(p store.Prefs) bool { return p.ConflictRadar })
	sendCmd(t, conn, command{Cmd: "conflictRadar"})
	nextPrefs(t, conn, func(p store.Prefs) bool { return !p.ConflictRadar })
}

// A pane's conflicts reach the window as the other pane's name, branch and the
// files, and a pane that has closed since is not named.
func TestPaneConflictsReachTheStatePush(t *testing.T) {
	srv, ws := newTestServer(t)
	a := firstPane(t, srv, ws)
	b := addPane(t, srv, ws, "second")

	many := make([]string, maxConflictPaths+5)
	for i := range many {
		many[i] = fmt.Sprintf("f%02d.go", i)
	}
	snap, ok := ask(srv, func() stateMsg {
		pa, pb := ws.Pane(a), ws.Pane(b)
		pb.Name, pb.Branch = "billing", "feature/billing"
		pa.Conflicts = []workspace.PaneConflict{
			{With: b, Paths: many},
			{With: "closed-since", Paths: []string{"x.go"}},
		}
		return srv.snapshot()
	})
	if !ok {
		t.Fatal("the workspace did not answer")
	}
	got := snap.Panes[a].Conflicts
	if len(got) != 1 {
		t.Fatalf("conflicts = %+v, want one, the pane still open", got)
	}
	c := got[0]
	if c.ID != b || c.Name != "billing" || c.Branch != "feature/billing" {
		t.Errorf("named %+v, want pane %s, billing, feature/billing", c, b)
	}
	if !reflect.DeepEqual(c.Paths, many[:maxConflictPaths]) || c.More != 5 {
		t.Errorf("paths = %d listed with %d more, want %d and 5", len(c.Paths), c.More, maxConflictPaths)
	}
	if len(snap.Panes[b].Conflicts) != 0 {
		t.Errorf("a pane nothing was recorded for was given %+v", snap.Panes[b].Conflicts)
	}
}

// A window reached through the relay may switch it. Turning it on sends no
// terminal output anywhere, unlike the Jev switch; what it does cause is that
// pane names, branches and file paths of predicted conflicts ride the state push
// to every window, this one included.
func TestConflictRadarCanBeSwitchedFromARemoteWindow(t *testing.T) {
	srv, _ := newTestServer(t)
	ts := remoteServer(t, srv)
	phone, err := dialRemoteControl(ts, ts.URL)
	if err != nil {
		t.Fatalf("dial through the tunnel: %v", err)
	}
	defer phone.CloseNow()
	sendCmd(t, phone, command{Cmd: "conflictRadar", Kind: "on"})
	nextPrefs(t, phone, func(p store.Prefs) bool { return p.ConflictRadar })
	if !store.LoadPrefs().ConflictRadar {
		t.Error("the setting did not reach disk")
	}
}

// The window is told why the radar cannot be turned on, when it cannot, so that
// Settings can say so beside the switch.
func TestHelloSaysWhyTheRadarIsUnavailable(t *testing.T) {
	srv, _ := newTestServer(t)
	// The version of git is read in the background; wait for it before a window
	// connects, so that the hello is built after.
	deadline := time.Now().Add(10 * time.Second)
	var ok bool
	var why string
	for {
		var known bool
		if ok, why, known = gitx.MergeTreeSupport(); known {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the version of git was not read")
		}
		time.Sleep(10 * time.Millisecond)
	}
	hello := nextHello(t, dialControl(t, srv))
	if ok && hello.RadarUnavailable != "" {
		t.Errorf("hello says the radar is unavailable (%q) on a git that can do it", hello.RadarUnavailable)
	}
	if !ok && hello.RadarUnavailable != why {
		t.Errorf("hello says %q, want %q", hello.RadarUnavailable, why)
	}
}

// A pane in a fan-out of a dozen could be told of eleven others; it is told of
// five and how many more there were.
func TestPaneConflictsAreCappedPerPane(t *testing.T) {
	srv, ws := newTestServer(t)
	a := firstPane(t, srv, ws)
	var pairs []workspace.PaneConflict
	for i := 0; i < maxConflictPanes+3; i++ {
		id := addPane(t, srv, ws, fmt.Sprintf("other %d", i))
		pairs = append(pairs, workspace.PaneConflict{With: id, Paths: []string{"x.go"}})
	}
	snap, ok := ask(srv, func() stateMsg {
		ws.Pane(a).Conflicts = pairs
		return srv.snapshot()
	})
	if !ok {
		t.Fatal("the workspace did not answer")
	}
	if got := snap.Panes[a]; len(got.Conflicts) != maxConflictPanes || got.ConflictsMore != 3 {
		t.Errorf("%d conflicts listed with %d more, want %d and 3", len(got.Conflicts), got.ConflictsMore, maxConflictPanes)
	}
}

// A window that connects before the version of git has been read is told it is
// being checked, and is sent what it found: the switch would otherwise stay
// enabled on a git that cannot run the radar.
func TestAWindowConnectedBeforeTheProbeIsToldWhatItFound(t *testing.T) {
	release := make(chan struct{})
	const why = "it needs git 2.38 or newer, and this is git 2.34"
	var resolved atomic.Bool
	oldSupport, oldWatch := radarSupport, radarWatch
	t.Cleanup(func() { radarSupport, radarWatch = oldSupport, oldWatch })
	radarSupport = func() (bool, string, bool) {
		if resolved.Load() {
			return false, why, true
		}
		return false, "", false
	}
	radarWatch = func(since uint64, done <-chan struct{}) (bool, string, uint64, bool, bool) {
		select {
		case <-release:
			resolved.Store(true)
			return false, why, 1, true, true
		case <-done:
			return false, "", since, false, false
		}
	}
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	hello := nextHello(t, conn)
	if !hello.RadarChecking || hello.RadarUnavailable != "" {
		t.Fatalf("hello = checking %v, unavailable %q, want it being checked", hello.RadarChecking, hello.RadarUnavailable)
	}
	close(release)
	var m radarSupportMsg
	readUntil(t, conn, "radarSupport", &m)
	if m.Unavailable != why {
		t.Errorf("the window was told %q, want %q", m.Unavailable, why)
	}
	// A window connecting after is told in its hello.
	later := nextHello(t, dialControl(t, srv))
	if later.RadarChecking || later.RadarUnavailable != why {
		t.Errorf("a later hello = checking %v, unavailable %q", later.RadarChecking, later.RadarUnavailable)
	}
}

// A probe that failed, and mended when it was asked again, is announced: a window
// told "could not read git's version" is told it can run after all, and a window
// that connects afterwards is told what is true now.
func TestARetriedProbeIsAnnouncedAndReadByLaterHellos(t *testing.T) {
	type answer struct {
		ok    bool
		why   string
		final bool
	}
	steps := make(chan answer)
	var mu sync.Mutex
	current := answer{}
	known := false
	oldSupport, oldWatch := radarSupport, radarWatch
	t.Cleanup(func() { radarSupport, radarWatch = oldSupport, oldWatch })
	radarSupport = func() (bool, string, bool) {
		mu.Lock()
		defer mu.Unlock()
		return current.ok, current.why, known
	}
	radarWatch = func(since uint64, done <-chan struct{}) (bool, string, uint64, bool, bool) {
		select {
		case a := <-steps:
			mu.Lock()
			current, known = a, true
			mu.Unlock()
			return a.ok, a.why, since + 1, a.final, true
		case <-done:
			return false, "", since, false, false
		}
	}
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	nextHello(t, conn)

	steps <- answer{false, "could not read the version of git: boom", false}
	var first radarSupportMsg
	readUntil(t, conn, "radarSupport", &first)
	if first.Unavailable == "" {
		t.Fatal("the failed probe was not announced")
	}
	// A window opened now is told the failure in its hello.
	if h := nextHello(t, dialControl(t, srv)); h.RadarUnavailable != first.Unavailable {
		t.Errorf("hello says %q, want %q", h.RadarUnavailable, first.Unavailable)
	}

	steps <- answer{true, "", true}
	var second radarSupportMsg
	readUntil(t, conn, "radarSupport", &second)
	if second.Unavailable != "" {
		t.Errorf("the mended probe was announced as %q, want available", second.Unavailable)
	}
	if h := nextHello(t, dialControl(t, srv)); h.RadarUnavailable != "" || h.RadarChecking {
		t.Errorf("a hello after the retry says unavailable %q, checking %v", h.RadarUnavailable, h.RadarChecking)
	}
}
