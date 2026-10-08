package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/jmwri/flockdeck/internal/remote"
	"github.com/jmwri/flockdeck/internal/store"
)

// dialControlPage is a window on this machine that opened the way a browser
// does, with an Origin header from the server's own address.
func dialControlPage(t *testing.T, srv *Server) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	h := http.Header{}
	h.Set("Origin", "http://"+srv.Addr())
	conn, _, err := websocket.Dial(ctx, "ws://"+srv.Addr()+"/ws/control?t="+srv.Token(), &websocket.DialOptions{HTTPHeader: h})
	if err != nil {
		t.Fatalf("dial control as a page: %v", err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })
	return conn
}

// makeViewer records device as a viewer, the way the desk does.
func makeViewer(t *testing.T, srv *Server, device string) {
	t.Helper()
	ask(srv, func() bool {
		p, ok := srv.prefs.WithAccess(device, store.DeviceAccess{Role: store.RoleViewer})
		if !ok {
			t.Error("could not record the viewer")
		}
		srv.prefs = p
		srv.publishAccess()
		return true
	})
}

func auditLines(t *testing.T) string {
	t.Helper()
	dir, err := store.Dir()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, artifactAuditFile))
	return string(b)
}

// breakPrefsFile makes the preferences file something that can be neither read
// nor written, so that a change cannot be saved.
func breakPrefsFile(t *testing.T) {
	t.Helper()
	dir, err := store.Dir()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "prefs.json")
	_ = os.Remove(path)
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
}

// The three commands need a written access decision, and a viewer is refused
// all of them.
func TestArtifactCommandsAreRefusedToViewers(t *testing.T) {
	e := newArtifactEnv(t)
	makeViewer(t, e.srv, "v1")
	c := &controlClient{remote: true, device: "v1"}
	for _, name := range []string{"setRemoteArtifacts", "revokeArtifactDevice", "stopRemoteArtifacts"} {
		if got, ok := commandAccess[name]; !ok || got != accessFull {
			t.Errorf("commandAccess[%q] = %v, %v; want accessFull", name, got, ok)
		}
		if e.srv.allowCommand(c, name) {
			t.Errorf("a viewer may send %s", name)
		}
	}
	if !e.srv.allowCommand(&controlClient{}, "stopRemoteArtifacts") {
		t.Error("a window on this machine may not send stopRemoteArtifacts")
	}
}

func TestArtifactsSwitchOnAndAllowNeedAWindowThatIsAPage(t *testing.T) {
	e := newArtifactEnv(t)
	e.ver.verify("d1", remote.KeyOriginDesk, e.key)
	script := dialControl(t, e.srv) // no Origin: a program, not a window
	nextHello(t, script)

	for _, cmd := range []command{
		{Cmd: "setRemoteArtifacts", Kind: "on", Text: "recordings", Confirmed: true},
		{Cmd: "setRemoteArtifacts", Kind: "on", ID: "d1"},
	} {
		sendCmd(t, script, cmd)
		var note noticeMsg
		readUntil(t, script, "notice", &note)
		if !note.Error || !strings.Contains(note.Text, "window") {
			t.Errorf("%+v from a program was answered %+v, want a refusal", cmd, note)
		}
	}
	// Give a wrongly accepted command time to land.
	ask(e.srv, func() bool { return true })
	if got := store.LoadPrefs().RemoteArtifacts; got.KindOn("recordings") || got.HasDevice("d1") {
		t.Fatalf("a program switched artifacts on: %+v", got)
	}

	// The same commands from a page work.
	page := dialControlPage(t, e.srv)
	nextHello(t, page)
	sendCmd(t, page, command{Cmd: "setRemoteArtifacts", Kind: "on", Text: "recordings", Confirmed: true})
	waitFor(t, func() bool { return store.LoadPrefs().RemoteArtifacts.KindOn("recordings") })
	sendCmd(t, page, command{Cmd: "setRemoteArtifacts", Kind: "on", ID: "d1"})
	waitFor(t, func() bool { return store.LoadPrefs().RemoteArtifacts.HasDevice("d1") })

	// Turning things off is not limited to pages: it only ever narrows.
	sendCmd(t, script, command{Cmd: "setRemoteArtifacts", Kind: "off", ID: "d1"})
	waitFor(t, func() bool { return !store.LoadPrefs().RemoteArtifacts.HasDevice("d1") })
	sendCmd(t, script, command{Cmd: "setRemoteArtifacts", Kind: "off", Text: "recordings"})
	waitFor(t, func() bool { return !store.LoadPrefs().RemoteArtifacts.KindOn("recordings") })
}

func TestArtifactsStopEndpointKeepsWhatEachDeviceMayDo(t *testing.T) {
	e := newArtifactEnv(t)
	e.allow(t)
	makeViewer(t, e.srv, "v1")
	// The file never held the viewer record, as if it had been deleted.
	savePrefs(t, func(p *store.Prefs) { p.Devices = nil })
	if e.srv.deviceRole("v1") != store.RoleViewer {
		t.Fatal("setup: v1 is not a viewer")
	}

	// The published copy has recordings on, as it would after a save.
	ask(e.srv, func() bool {
		e.srv.prefs.RemoteArtifacts = store.LoadPrefs().RemoteArtifacts
		e.srv.publishAccess()
		return true
	})
	if !e.srv.access.Load().prefs.RemoteArtifacts.KindOn("recordings") {
		t.Fatal("setup: recordings are not on in the published copy")
	}
	savePrefs(t, func(p *store.Prefs) { p.RemoteArtifacts.AllOff() })
	if err := RequestArtifactsStop("http://"+e.srv.Addr(), e.srv.Token()); err != nil {
		t.Fatal(err)
	}
	if got := e.srv.deviceRole("v1"); got != store.RoleViewer {
		t.Errorf("after the stop v1 is %q: the stop reread the file and lost the record in memory", got)
	}
	// And the published copy, which the connection goroutines read, follows.
	pub := e.srv.access.Load().prefs
	if pub.RemoteArtifacts.KindOn("recordings") {
		t.Error("the published preferences still have recordings on")
	}
	if pub.AccessFor("v1").EffectiveRole() != store.RoleViewer {
		t.Error("the published preferences lost the viewer record")
	}
}

func TestArtifactsFailedSaveIsNotReportedAsDone(t *testing.T) {
	e := newArtifactEnv(t)
	e.ver.verify("d1", remote.KeyOriginDesk, e.key)
	page := dialControlPage(t, e.srv)
	nextHello(t, page)
	breakPrefsFile(t)

	for _, c := range []struct {
		cmd   command
		event string
	}{
		{command{Cmd: "setRemoteArtifacts", Kind: "on", Text: "recordings", Confirmed: true}, "kind-on"},
		{command{Cmd: "setRemoteArtifacts", Kind: "on", ID: "d1"}, "device-allow"},
		{command{Cmd: "revokeArtifactDevice", ID: "d1"}, "device-revoke"},
		{command{Cmd: "setRemoteArtifacts", Kind: "off", Text: "recordings"}, "kind-off"},
	} {
		sendCmd(t, page, c.cmd)
		var n noticeMsg
		readUntil(t, page, "notice", &n)
		if !n.Error || !strings.Contains(n.Text, "Could not save") {
			t.Errorf("%s: first notice %+v, want the save failure", c.cmd.Cmd, n)
		}
		waitFor(t, func() bool { return strings.Contains(auditLines(t), `"`+c.event+`-failed"`) })
		for _, line := range strings.Split(auditLines(t), "\n") {
			if strings.Contains(line, `"event":"`+c.event+`"`) {
				t.Errorf("%s was written to the audit log although nothing was saved: %s", c.event, line)
			}
		}
	}
}

func TestArtifactsSuccessfulChangesAreReportedAndAudited(t *testing.T) {
	e := newArtifactEnv(t)
	page := dialControlPage(t, e.srv)
	nextHello(t, page)
	sendCmd(t, page, command{Cmd: "setRemoteArtifacts", Kind: "on", Text: "recordings", Confirmed: true})
	var n noticeMsg
	readUntil(t, page, "notice", &n)
	if n.Error || !strings.Contains(n.Text, "can now view") {
		t.Errorf("notice = %+v", n)
	}
	waitFor(t, func() bool { return strings.Contains(auditLines(t), `"event":"kind-on"`) })
	if strings.Contains(auditLines(t), "-failed") {
		t.Error("a failure was logged for a change that was saved")
	}
}

func TestArtifactsSocketsCloseOnlyOnceTheFileSaysOff(t *testing.T) {
	e := newArtifactEnv(t)
	e.allow(t)
	page := dialControlPage(t, e.srv)
	nextHello(t, page)
	conn, sess := e.open(t)
	ask2(t, conn, sess, map[string]any{"op": "hello"})
	sendCmd(t, page, command{Cmd: "setRemoteArtifacts", Kind: "off", Text: "recordings"})
	if code, reason := closedWith(t, conn); code != artifactCloseRefused || reason != artifactReasonDisabled {
		t.Fatalf("closed with %d %q", code, reason)
	}
	// By the time the socket is closed the file already says so, so a
	// reconnect cannot read the old setting.
	if store.LoadPrefs().RemoteArtifacts.KindOn("recordings") {
		t.Error("the socket was closed before the preferences were saved")
	}
}

func TestArtifactsASocketIsCutEvenWhenTheOffCannotBeSaved(t *testing.T) {
	e := newArtifactEnv(t)
	e.allow(t)
	page := dialControlPage(t, e.srv)
	nextHello(t, page)
	conn, sess := e.open(t)
	ask2(t, conn, sess, map[string]any{"op": "hello"})
	breakPrefsFile(t)
	sendCmd(t, page, command{Cmd: "setRemoteArtifacts", Kind: "off", Text: "recordings"})
	if code, _ := closedWith(t, conn); code != artifactCloseRefused {
		t.Errorf("closed with %d", code)
	}
}

// A viewer the desk allowed to read artifacts is told so: the "artifacts"
// message is not held back with the rest of the desk's messages.
func TestAViewerOnTheAllowlistIsToldArtifactsAreAvailable(t *testing.T) {
	e := newArtifactEnv(t)
	makeViewer(t, e.srv, "d1")
	c, err := dialRemoteControlDevice(e.ts, e.ts.URL, "d1")
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	h := readRemoteMsg(t, c, "hello")
	if h["role"] != "viewer" {
		t.Fatalf("setup: hello = %v", h)
	}
	if _, has := h["artifacts"]; has {
		t.Error("artifacts offered while everything is off")
	}

	e.allow(t)
	ask(e.srv, func() bool {
		e.srv.prefs.RemoteArtifacts = store.LoadPrefs().RemoteArtifacts
		e.srv.broadcastPrefs()
		return true
	})
	m := readRemoteMsg(t, c, "artifacts")
	if m["available"] != true {
		t.Errorf("artifacts = %v, want available", m)
	}

	// A viewer that connects after is told in hello.
	c2, err := dialRemoteControlDevice(e.ts, e.ts.URL, "d1")
	if err != nil {
		t.Fatal(err)
	}
	defer c2.CloseNow()
	h2 := readRemoteMsg(t, c2, "hello")
	if got, _ := h2["artifacts"].(map[string]any); got["v"] != float64(1) {
		t.Errorf("hello.artifacts = %v", h2["artifacts"])
	}
	if p, _ := h2["prefs"].(map[string]any); p != nil {
		if _, has := p["remoteArtifacts"]; has {
			t.Error("a viewer was sent the artifact settings")
		}
		if _, has := p["devices"]; has {
			t.Error("a viewer was sent the device list")
		}
	}

	savePrefs(t, func(p *store.Prefs) { p.RemoteArtifacts.AllOff() })
	ask(e.srv, func() bool {
		e.srv.prefs.RemoteArtifacts = store.LoadPrefs().RemoteArtifacts
		e.srv.broadcastPrefs()
		return true
	})
	if m := readRemoteMsg(t, c, "artifacts"); m["available"] != false {
		t.Errorf("artifacts = %v, want gone", m)
	}
}

func TestViewersAreStillSentNothingElse(t *testing.T) {
	for _, typ := range []string{"prefs", "notice", "approval", "remoteDevices"} {
		if viewerMessageTypes[typ] {
			t.Errorf("a viewer is sent %q", typ)
		}
	}
	if !viewerMessageTypes["artifacts"] || !viewerMessageTypes["hello"] {
		t.Error("a viewer is not sent hello and artifacts")
	}
}

func TestArtifactsARequestRunsUnderADeadline(t *testing.T) {
	e := newArtifactEnv(t)
	e.allow(t)
	var sawDeadline atomic.Bool
	e.srv.artifacts.setTimings(func(tm *artifactTimings) {
		tm.request = 200 * time.Millisecond
		tm.hook = func(ctx context.Context, op string) {
			_, ok := ctx.Deadline()
			sawDeadline.Store(ok)
			if op == "list" {
				<-ctx.Done() // a request that never finishes
			}
		}
	})
	conn, sess := e.open(t)
	start := time.Now()
	r := ask2(t, conn, sess, map[string]any{"op": "list", "kind": "recordings"})
	if r["code"] != artifactErrTimeout {
		t.Errorf("reply = %v, want a timeout", r)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("the request took %v", took)
	}
	if !sawDeadline.Load() {
		t.Error("the request ran without a deadline")
	}
	// The socket is still good for the next, quick, request.
	if r := ask2(t, conn, sess, map[string]any{"op": "hello"}); r["op"] != "hello" {
		t.Errorf("after the timeout: %v", r)
	}
}

func TestArtifactsAHandshakeThatNeverFinishesIsDropped(t *testing.T) {
	e := newArtifactEnv(t)
	e.allow(t)
	e.srv.artifacts.setTimings(func(tm *artifactTimings) { tm.handshake = 200 * time.Millisecond })
	conn, _, err := e.rawDial(t, e.header("d1"))
	if err != nil {
		t.Fatal(err)
	}
	// A read that runs out of time drops the connection without a close frame,
	// so all that can be seen is that it ends, and when.
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, _, err := conn.Read(ctx); err == nil || ctx.Err() != nil {
		t.Fatalf("the socket was still open after %v: %v", time.Since(start), err)
	}
	if took := time.Since(start); took > 3*time.Second {
		t.Errorf("took %v", took)
	}
}

func TestArtifactsOpeningSocketsIsRateLimitedPerDevice(t *testing.T) {
	e := newArtifactEnv(t)
	e.allow(t)
	for i := 0; i < artifactConnectsPerMinute; i++ {
		conn, _, err := e.rawDial(t, e.header("d1"))
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.CloseNow()
		waitFor(t, func() bool {
			e.srv.artifacts.mu.Lock()
			defer e.srv.artifacts.mu.Unlock()
			return len(e.srv.artifacts.socks) == 0
		})
	}
	conn, _, err := e.rawDial(t, e.header("d1"))
	if err != nil {
		t.Fatal(err)
	}
	if code, reason := closedWith(t, conn); code != artifactCloseBusy || reason != artifactErrBusy {
		t.Errorf("the socket past the limit closed with %d %q", code, reason)
	}
	if !strings.Contains(auditLines(t), "connect-rate") {
		t.Errorf("the refusal for rate was not recorded: %s", auditLines(t))
	}
}

func TestArtifactsUnchangedSettingsAreNotReportedAsChanges(t *testing.T) {
	e := newArtifactEnv(t)
	e.ver.verify("d1", remote.KeyOriginDesk, e.key)
	page := dialControlPage(t, e.srv)
	nextHello(t, page)
	for _, cmd := range []command{
		{Cmd: "setRemoteArtifacts", Kind: "off", Text: "recordings"},
		{Cmd: "revokeArtifactDevice", ID: "d1"},
	} {
		sendCmd(t, page, cmd)
		var n noticeMsg
		readUntil(t, page, "notice", &n)
		if n.Error || !strings.Contains(n.Text, "Nothing changed") && !strings.Contains(n.Text, "not on the allowlist") {
			t.Errorf("%s: notice %+v, want a note that nothing changed", cmd.Cmd, n)
		}
	}
	// Allow once, then again.
	sendCmd(t, page, command{Cmd: "setRemoteArtifacts", Kind: "on", ID: "d1"})
	var n noticeMsg
	readUntil(t, page, "notice", &n)
	sendCmd(t, page, command{Cmd: "setRemoteArtifacts", Kind: "on", ID: "d1"})
	readUntil(t, page, "notice", &n)
	if !strings.Contains(n.Text, "already allowed") {
		t.Errorf("second allow: %+v", n)
	}
	ask(e.srv, func() bool { return true })
	log := auditLines(t)
	for _, ev := range []string{`"event":"kind-off"`, `"event":"device-revoke"`} {
		if strings.Contains(log, ev) {
			t.Errorf("audit log has %s for a change that did not happen", ev)
		}
	}
	if c := strings.Count(log, `"event":"device-allow"`); c != 1 {
		t.Errorf("device-allow written %d times, want 1", c)
	}
}

func TestArtifactsStopEndpointIsNotReachableThroughTheRelay(t *testing.T) {
	e := newArtifactEnv(t)
	e.allow(t)
	conn, sess := e.open(t)
	ask2(t, conn, sess, map[string]any{"op": "hello"})
	resp, err := http.Post(e.ts.URL+"/remote/artifacts/stop?t="+e.srv.Token(), "text/plain", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("through the tunnel with the token = %d, want 403", resp.StatusCode)
	}
	if r := ask2(t, conn, sess, map[string]any{"op": "hello"}); r["op"] != "hello" {
		t.Errorf("the socket was closed by a request through the relay: %v", r)
	}
}

func TestArtifactsAFirstRequestThatNeverComesIsDropped(t *testing.T) {
	e := newArtifactEnv(t)
	e.allow(t)
	e.srv.artifacts.setTimings(func(tm *artifactTimings) { tm.first = 200 * time.Millisecond })
	conn, _ := e.open(t)
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, _, err := conn.Read(ctx); err == nil || ctx.Err() != nil {
		t.Fatalf("still open after %v: %v", time.Since(start), err)
	}
}

// The key is looked up again after the handshake, and that lookup gets its own
// time: a handshake that outlasts the lookup bound must not make it fail.
func TestArtifactsASlowHandshakeStillPassesTheKeyRecheck(t *testing.T) {
	e := newArtifactEnv(t)
	e.allow(t)
	e.srv.artifacts.setTimings(func(tm *artifactTimings) {
		tm.roster = 150 * time.Millisecond
		tm.handshake = 5 * time.Second
	})
	conn, _, err := e.rawDial(t, e.header("d1"))
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(400 * time.Millisecond) // longer than the roster bound
	sess := e.session(t, conn)
	if r := ask2(t, conn, sess, map[string]any{"op": "hello"}); r["op"] != "hello" {
		t.Fatalf("a slow handshake was refused: %v", r)
	}
}
