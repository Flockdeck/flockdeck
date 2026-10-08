package server

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/jmwri/flockdeck/internal/store"
)

// handleCommandCases returns every command name handleCommand has a case for,
// read from the source. A command added to either of its switches shows up here
// whether or not anybody remembered to decide who may send it.
func handleCommandCases(t *testing.T) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "control.go", nil, 0)
	if err != nil {
		t.Fatalf("parse control.go: %v", err)
	}
	seen := map[string]bool{}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "handleCommand" {
			continue
		}
		ast.Inspect(fn, func(n ast.Node) bool {
			sw, ok := n.(*ast.SwitchStmt)
			if !ok {
				return true
			}
			// Only the switches on cmd.Cmd; a nested switch on anything else
			// has cases that are not commands.
			sel, ok := sw.Tag.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Cmd" {
				return true
			}
			for _, stmt := range sw.Body.List {
				cc, ok := stmt.(*ast.CaseClause)
				if !ok {
					continue
				}
				for _, e := range cc.List {
					lit, ok := e.(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						t.Errorf("handleCommand has a case that is not a string literal (%s); the access table cannot be checked against it", fset.Position(e.Pos()))
						continue
					}
					name := strings.Trim(lit.Value, `"`)
					seen[name] = true
				}
			}
			return true
		})
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// TestEveryCommandHasAnAccessDecision is what makes the viewer role
// default-deny in practice. A command added to handleCommand without a line in
// commandAccess fails here, so adding one forces a decision about viewers; a
// command removed from handleCommand but left in the table fails too, so the
// table cannot silently grow stale.
func TestEveryCommandHasAnAccessDecision(t *testing.T) {
	cases := handleCommandCases(t)
	if len(cases) < 150 {
		t.Fatalf("found only %d commands in handleCommand; the scan is probably wrong", len(cases))
	}
	for _, name := range cases {
		if _, ok := commandAccess[name]; !ok {
			t.Errorf("handleCommand handles %q but commandAccess has no decision for it. Add it to commandAccess in viewerrole.go: accessFull unless a viewer genuinely needs it", name)
		}
	}
	for name := range commandAccess {
		if !slices.Contains(cases, name) {
			t.Errorf("commandAccess lists %q, which handleCommand no longer handles", name)
		}
	}
}

// TestOnlyChatReadingIsOpenToViewers pins the whole opening in one place.
// Loosening the allowlist means editing this list, which is where a reviewer
// will look.
func TestOnlyChatReadingIsOpenToViewers(t *testing.T) {
	var open []string
	for name, a := range commandAccess {
		if a != accessFull {
			open = append(open, name+"="+map[cmdAccess]string{accessWatcher: "watcher", accessViewer: "viewer"}[a])
		}
	}
	sort.Strings(open)
	want := []string{
		"conversationClose=watcher", "conversationDetail=watcher", "conversationOlder=watcher",
		"conversationOpen=watcher", "conversationSearch=watcher",
	}
	if !slices.Equal(open, want) {
		t.Errorf("commands open to viewers = %v, want %v", open, want)
	}
}

// TestAllowCommandByRole checks the decision for every command and each kind
// of window, with no sockets in the way.
func TestAllowCommandByRole(t *testing.T) {
	srv, _ := newTestServer(t)
	setAccess(t, srv, "viewer-dev", "viewer", false)
	setAccess(t, srv, "watch-dev", "viewer", true)

	desk := &controlClient{}
	full := &controlClient{remote: true, device: "full-dev"}
	viewer := &controlClient{remote: true, device: "viewer-dev"}
	watcher := &controlClient{remote: true, device: "watch-dev"}
	unnamed := &controlClient{remote: true}

	names := append(handleCommandCases(t), "no-such-command", "")
	for _, name := range names {
		if !srv.allowCommand(desk, name) {
			t.Errorf("a window on this machine was refused %q", name)
		}
		if !srv.allowCommand(full, name) {
			t.Errorf("a full device was refused %q", name)
		}
		wantWatch := commandAccess[name] == accessWatcher || commandAccess[name] == accessViewer
		if got := srv.allowCommand(watcher, name); got != wantWatch {
			t.Errorf("allowCommand(watcher, %q) = %v, want %v", name, got, wantWatch)
		}
		wantViewer := commandAccess[name] == accessViewer
		if got := srv.allowCommand(viewer, name); got != wantViewer {
			t.Errorf("allowCommand(viewer, %q) = %v, want %v", name, got, wantViewer)
		}
		// A request that names no device is not trusted with anything.
		if got := srv.allowCommand(unnamed, name); got != wantViewer {
			t.Errorf("allowCommand(unnamed device, %q) = %v, want %v", name, got, wantViewer)
		}
	}
}

// setAccess has the desk set what a device may do, the way the dialog does,
// and waits until the host has applied it.
func setAccess(t *testing.T, srv *Server, device, role string, watch bool) {
	t.Helper()
	desk := dialControl(t, srv)
	sendCmd(t, desk, command{Cmd: "setDeviceRole", ID: device, Kind: role, Watch: watch})
	want := store.DeviceAccess{Role: store.Role(role), WatchPanes: watch && role == "viewer"}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if got := srv.deviceAccess(device); got.EffectiveRole() == want.EffectiveRole() && got.WatchPanes == want.WatchPanes {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("setDeviceRole %s %s watch=%v was not applied; access is %+v", device, role, watch, srv.deviceAccess(device))
}

// collectTypes reads a socket for d and returns the type of every message that
// arrived, and the raw messages.
func collectTypes(t *testing.T, conn *websocket.Conn, d time.Duration) (types []string, raw [][]byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return types, raw
		}
		var probe struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(data, &probe)
		types = append(types, probe.Type)
		raw = append(raw, data)
	}
}

// TestAViewerIsRefusedEveryCommandItMayNotSend sends every command handleCommand
// knows, with nothing else, from a viewer's socket. Each must be answered with
// the refusal and nothing more: that it was answered at all means handleCommand
// returned before reaching the case that would have run it.
func TestAViewerIsRefusedEveryCommandItMayNotSend(t *testing.T) {
	srv, ws := newTestServer(t)
	setAccess(t, srv, "viewer-dev", "viewer", false)
	ts := remoteServer(t, srv)
	viewer, err := dialRemoteControlDevice(ts, ts.URL, "viewer-dev")
	if err != nil {
		t.Fatal(err)
	}
	defer viewer.CloseNow()

	var hello helloMsg
	readUntil(t, viewer, "hello", &hello)
	tabsBefore := len(ws.VisibleTabs())

	for _, name := range append(handleCommandCases(t), "no-such-command") {
		sendCmd(t, viewer, command{Cmd: name})
		var note noticeMsg
		readUntil(t, viewer, "notice", &note)
		if !note.Error || note.Text != viewerRefused {
			t.Errorf("%s from a viewer was answered %+v, want the refusal", name, note)
		}
	}
	if got := len(ws.VisibleTabs()); got != tabsBefore {
		t.Errorf("tabs went from %d to %d under a viewer's commands", tabsBefore, got)
	}
}

// TestAViewerIsSentNothingOfTheDesks covers the other direction. The control
// socket is not end-to-end encrypted, so what the host puts on it for a viewer
// is what the relay can read.
func TestAViewerIsSentNothingOfTheDesks(t *testing.T) {
	srv, ws := newTestServer(t)
	setAccess(t, srv, "viewer-dev", "viewer", false)
	ts := remoteServer(t, srv)
	viewer, err := dialRemoteControlDevice(ts, ts.URL, "viewer-dev")
	if err != nil {
		t.Fatal(err)
	}
	defer viewer.CloseNow()

	// Things that reach every window: a notice, a preferences change, a key
	// table change, a change of state.
	srv.notifyAll("desk-only notice: /secret/place", false)
	desk := dialControl(t, srv)
	sendCmd(t, desk, command{Cmd: "theme", Text: "light"})
	sendCmd(t, desk, command{Cmd: "newTab"})
	srv.do(func() { srv.broadcastPrefs(); srv.broadcastKeys() })
	var root string
	srv.do(func() {
		root = ws.ActiveRoot()
		ws.NewTab(0, root, "a-private-tab-name")
	})
	srv.Wake()

	types, raw := collectTypes(t, viewer, 2*time.Second)
	for i, typ := range types {
		switch typ {
		case "hello", "state":
		default:
			t.Errorf("a viewer was sent a %q message: %s", typ, raw[i])
		}
	}
	all := string(slices.Concat(raw...))
	for _, secret := range []string{"a-private-tab-name", "/secret/place", root} {
		if strings.Contains(all, secret) {
			t.Errorf("a viewer was sent %q", secret)
		}
	}
	for i, typ := range types {
		if typ != "state" {
			continue
		}
		var st stateMsg
		if err := json.Unmarshal(raw[i], &st); err != nil {
			t.Fatal(err)
		}
		if len(st.Tabs) != 0 || len(st.Panes) != 0 || len(st.Projects) != 0 || st.Root != "" || st.Remote != nil || len(st.Agents.Items) != 0 {
			t.Errorf("a viewer was sent a snapshot with content: %s", raw[i])
		}
		if st.CanStartAgent || st.CanMutePane || st.CanAutoReview {
			t.Errorf("a viewer was told it can act: %s", raw[i])
		}
	}
	// Activity on the desk does not reach it either, not even as repeats.
	if n := countOf(types, "state"); n > 1 {
		t.Errorf("a viewer was sent %d snapshots, want at most the first (the rest would show when the agents are busy)", n)
	}
}

func countOf(types []string, want string) int {
	n := 0
	for _, t := range types {
		if t == want {
			n++
		}
	}
	return n
}

// TestAViewerAllowedToWatchSeesPanesButNotTheDesk covers the "may also watch
// panes" switch.
func TestAViewerAllowedToWatchSeesPanesButNotTheDesk(t *testing.T) {
	srv, ws := newTestServer(t)
	setAccess(t, srv, "watch-dev", "viewer", true)
	ts := remoteServer(t, srv)
	watcher, err := dialRemoteControlDevice(ts, ts.URL, "watch-dev")
	if err != nil {
		t.Fatal(err)
	}
	defer watcher.CloseNow()

	var hello helloMsg
	readUntil(t, watcher, "hello", &hello)
	if hello.Role != "viewer" || !hello.WatchPanes {
		t.Errorf("hello says role %q watch %v, want a viewer allowed to watch", hello.Role, hello.WatchPanes)
	}
	st := nextState(t, watcher, nil)
	if len(st.Panes) == 0 || len(st.Tabs) == 0 {
		t.Fatalf("a viewer allowed to watch was sent no panes: %+v", st)
	}
	if st.Remote != nil || len(st.Agents.Items) != 0 || st.CanStartAgent || st.CanMutePane || st.CanAutoReview {
		t.Errorf("a watcher was sent more than the panes: %+v", st)
	}
	_ = ws

	sendCmd(t, watcher, command{Cmd: "sendPrompt", Text: "rm -rf /"})
	var note noticeMsg
	readUntil(t, watcher, "notice", &note)
	if note.Text != viewerRefused {
		t.Errorf("sendPrompt from a watcher was answered %+v", note)
	}
}

// TestTheRecordOfRolesIsNeverSentToAWindow keeps the list of which devices are
// limited, and how, off every socket.
func TestTheRecordOfRolesIsNeverSentToAWindow(t *testing.T) {
	srv, _ := newTestServer(t)
	setAccess(t, srv, "limited-device-id", "viewer", true)
	ts := remoteServer(t, srv)
	phone, err := dialRemoteControlDevice(ts, ts.URL, "some-full-device")
	if err != nil {
		t.Fatal(err)
	}
	defer phone.CloseNow()
	desk := dialControl(t, srv)
	srv.do(func() { srv.broadcastPrefs() })

	for name, conn := range map[string]*websocket.Conn{"phone": phone, "desk": desk} {
		_, raw := collectTypes(t, conn, 1500*time.Millisecond)
		if len(raw) == 0 {
			t.Fatalf("%s was sent nothing", name)
		}
		for _, m := range raw {
			if strings.Contains(string(m), "limited-device-id") || strings.Contains(string(m), `"devices"`) {
				t.Errorf("%s was sent the record of device roles: %.200s", name, m)
			}
		}
	}
}

// TestARoleIsSetOnlyAtTheDesk: a full device reached through the relay cannot
// free a viewer, or limit another device, from where it is.
func TestARoleIsSetOnlyAtTheDesk(t *testing.T) {
	srv, _ := newTestServer(t)
	ts := remoteServer(t, srv)
	phone, err := dialRemoteControlDevice(ts, ts.URL, "full-dev")
	if err != nil {
		t.Fatal(err)
	}
	defer phone.CloseNow()

	sendCmd(t, phone, command{Cmd: "setDeviceRole", ID: "other-dev", Kind: "viewer"})
	var note noticeMsg
	readUntil(t, phone, "notice", &note)
	if !note.Error || !strings.Contains(note.Text, "machine") {
		t.Errorf("setDeviceRole through the relay was answered %+v, want it refused", note)
	}
	sendCmd(t, phone, command{Cmd: "setDeviceRole", ID: "full-dev", Kind: "viewer"})
	readUntil(t, phone, "notice", &note)
	time.Sleep(200 * time.Millisecond)
	if got := srv.deviceRole("other-dev"); got != store.RoleFull {
		t.Errorf("a device through the relay limited another device: %q", got)
	}
	if got := srv.deviceRole("full-dev"); got != store.RoleFull {
		t.Errorf("a device through the relay limited itself: %q", got)
	}
	if p, err := store.ReadPrefs(); err != nil || len(p.Devices) != 0 {
		t.Errorf("saved preferences after the refusals = %+v, %v", p.Devices, err)
	}

	// A viewer cannot free itself.
	setAccess(t, srv, "viewer-dev", "viewer", false)
	viewer, err := dialRemoteControlDevice(ts, ts.URL, "viewer-dev")
	if err != nil {
		t.Fatal(err)
	}
	defer viewer.CloseNow()
	sendCmd(t, viewer, command{Cmd: "setDeviceRole", ID: "viewer-dev", Kind: "full"})
	readUntil(t, viewer, "notice", &note)
	time.Sleep(200 * time.Millisecond)
	if got := srv.deviceRole("viewer-dev"); got != store.RoleViewer {
		t.Errorf("a viewer made itself %q", got)
	}
}

func TestTheDeskSetsAndClearsARole(t *testing.T) {
	srv, _ := newTestServer(t)
	desk := dialControl(t, srv)

	for _, bad := range []command{
		{Cmd: "setDeviceRole", ID: "d", Kind: "admin"},
		{Cmd: "setDeviceRole", ID: "d", Kind: ""},
		{Cmd: "setDeviceRole", ID: "", Kind: "viewer"},
		{Cmd: "setDeviceRole", ID: "../../etc", Kind: "viewer"},
		{Cmd: "setDeviceRole", ID: strings.Repeat("a", 129), Kind: "viewer"},
	} {
		sendCmd(t, desk, bad)
		var note noticeMsg
		readUntil(t, desk, "notice", &note)
		if !note.Error {
			t.Errorf("%+v was answered %+v, want an error", bad, note)
		}
	}
	if p, _ := store.ReadPrefs(); len(p.Devices) != 0 {
		t.Errorf("a refused request was saved: %+v", p.Devices)
	}

	setAccess(t, srv, "d", "viewer", true)
	if p, _ := store.ReadPrefs(); p.AccessFor("d").EffectiveRole() != store.RoleViewer || !p.AccessFor("d").WatchPanes {
		t.Errorf("saved access for d = %+v", p.AccessFor("d"))
	}
	// Watch means nothing for a full device and is not kept.
	setAccess(t, srv, "d", "full", true)
	if p, _ := store.ReadPrefs(); len(p.Devices) != 0 {
		t.Errorf("a full device left a record: %+v", p.Devices)
	}
	if got := srv.deviceAccess("d"); got.EffectiveRole() != store.RoleFull || got.WatchPanes {
		t.Errorf("access for d after making it full = %+v", got)
	}
}

// TestAnUnnamedRemoteRequestIsAViewer: the relay always names the device. A
// request that does not is not one the desk could have given access to.
func TestAnUnnamedRemoteRequestIsAViewer(t *testing.T) {
	srv, _ := newTestServer(t)
	ts := remoteServer(t, srv)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	h := http.Header{}
	h.Set("Origin", ts.URL)
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/ws/control", &websocket.DialOptions{HTTPHeader: h})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	var hello helloMsg
	readUntil(t, conn, "hello", &hello)
	if hello.Role != "viewer" || hello.WatchPanes {
		t.Errorf("hello for a request with no device = role %q watch %v, want a viewer", hello.Role, hello.WatchPanes)
	}
	if got := srv.deviceRole(""); got != store.RoleViewer {
		t.Errorf("deviceRole of no device = %q", got)
	}
	if got := srv.deviceRole("never-seen-before"); got != store.RoleFull {
		t.Errorf("deviceRole of a device with no record = %q, want full", got)
	}
}

func dialRemotePTYResult(ts *httptest.Server, paneID, device string) (*websocket.Conn, *http.Response, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	h := http.Header{}
	h.Set("Origin", ts.URL)
	if device != "" {
		h.Set("Flockdeck-Remote-Device", device)
	}
	return websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/ws/pty?id="+paneID, &websocket.DialOptions{HTTPHeader: h})
}

// TestAViewersTerminalSocketIsRefused covers /ws/pty, which carries keystrokes.
func TestAViewersTerminalSocketIsRefused(t *testing.T) {
	srv, _ := newTestServer(t)
	paneID := nextState(t, dialControl(t, srv), nil).Tabs[0].Root.Pane
	setAccess(t, srv, "viewer-dev", "viewer", false)
	ts := remoteServer(t, srv)

	for _, tc := range []struct{ name, device, pane string }{
		{"viewer", "viewer-dev", paneID},
		{"viewer, pane that does not exist", "viewer-dev", "no-such-pane"},
		{"no device named", "", paneID},
	} {
		conn, resp, err := dialRemotePTYResult(ts, tc.pane, tc.device)
		if err == nil {
			conn.CloseNow()
			t.Errorf("%s: the terminal socket opened", tc.name)
			continue
		}
		if resp == nil || resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s: dial answered %v, want 403 (the same for a pane that exists and one that does not)", tc.name, resp)
		}
	}

	conn, _, err := dialRemotePTYResult(ts, paneID, "full-dev")
	if err != nil {
		t.Fatalf("a full device could not open the terminal: %v", err)
	}
	conn.CloseNow()
}

// TestAWatcherReadsTheTerminalAndTypesNothing: output reaches it; its
// keystrokes, size and focus do not reach the pane.
func TestAWatcherReadsTheTerminalAndTypesNothing(t *testing.T) {
	srv, _ := newTestServer(t)
	paneID := nextState(t, dialControl(t, srv), nil).Tabs[0].Root.Pane
	setAccess(t, srv, "watch-dev", "viewer", true)
	ts := remoteServer(t, srv)

	watcher := dialRemotePTYDevice(t, ts, paneID, "watch-dev")
	deskTerm := dialPTY(t, srv, paneID)
	cols, rows := paneSize(t, srv, paneID)

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	// The watcher "types" first, then the desk types a command with a marker
	// of its own. The shell runs them in the order written, so once the desk's
	// marker has come back to the watcher the watcher's line would have too.
	if err := watcher.Write(ctx, websocket.MessageBinary, []byte("echo WATCHERTYPED\r")); err != nil {
		t.Fatal(err)
	}
	sendResize(t, watcher, cols+17, rows+9)
	sendFocus(t, watcher)

	stop := keepTyping(ctx, deskTerm, "echo DESKMARK\r")
	defer stop()
	var seen strings.Builder
	for !strings.Contains(seen.String(), "DESKMARK") {
		_, data, err := watcher.Read(ctx)
		if err != nil {
			t.Fatalf("watcher read: %v\nsaw: %s", err, seen.String())
		}
		seen.Write(data)
	}
	if strings.Contains(seen.String(), "WATCHERTYPED") {
		t.Errorf("a watcher's keystrokes reached the pane:\n%s", seen.String())
	}
	if c2, r2 := paneSize(t, srv, paneID); c2 != cols || r2 != rows {
		t.Errorf("a watcher resized the pane from %dx%d to %dx%d", cols, rows, c2, r2)
	}
}

// TestChangingARoleEndsTheDevicesSockets: a device already connected as full
// does not stay full after the desk limits it.
func TestChangingARoleEndsTheDevicesSockets(t *testing.T) {
	srv, _ := newTestServer(t)
	paneID := nextState(t, dialControl(t, srv), nil).Tabs[0].Root.Pane
	ts := remoteServer(t, srv)

	ctl, err := dialRemoteControlDevice(ts, ts.URL, "dev-x")
	if err != nil {
		t.Fatal(err)
	}
	defer ctl.CloseNow()
	var hello helloMsg
	readUntil(t, ctl, "hello", &hello)
	if hello.Role != "" {
		t.Fatalf("a full device's hello says role %q", hello.Role)
	}
	term := dialRemotePTYDevice(t, ts, paneID, "dev-x")
	other, err := dialRemoteControlDevice(ts, ts.URL, "dev-y")
	if err != nil {
		t.Fatal(err)
	}
	defer other.CloseNow()

	setAccess(t, srv, "dev-x", "viewer", false)

	for name, conn := range map[string]*websocket.Conn{"control": ctl, "terminal": term} {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		for {
			if _, _, err := conn.Read(ctx); err != nil {
				if ctx.Err() != nil {
					t.Errorf("the %s socket of a device made a viewer was still open", name)
				}
				break
			}
		}
		cancel()
	}

	// Only that device's. And it comes back as a viewer.
	{
		ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
		for {
			_, _, err := other.Read(ctx)
			if err != nil {
				if ctx.Err() == nil {
					t.Errorf("another device's socket was ended too: %v", err)
				}
				break
			}
		}
		cancel()
	}
	again, err := dialRemoteControlDevice(ts, ts.URL, "dev-x")
	if err != nil {
		t.Fatal(err)
	}
	defer again.CloseNow()
	readUntil(t, again, "hello", &hello)
	if hello.Role != "viewer" {
		t.Errorf("hello after the change says role %q, want viewer", hello.Role)
	}

	// And back: the viewer's socket is ended so it reconnects as full.
	setAccess(t, srv, "dev-x", "full", false)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for {
		if _, _, err := again.Read(ctx); err != nil {
			if ctx.Err() != nil {
				t.Error("a viewer's socket stayed open after it was made full")
			}
			break
		}
	}
	back, err := dialRemoteControlDevice(ts, ts.URL, "dev-x")
	if err != nil {
		t.Fatal(err)
	}
	defer back.CloseNow()
	hello = helloMsg{}
	readUntil(t, back, "hello", &hello)
	if hello.Role != "" {
		t.Errorf("hello after being made full again says role %q", hello.Role)
	}
}

// TestADamagedRecordKeepsEveryDeviceAViewerThroughLaterSaves: the record being
// lost must not be undone by the next unrelated preference being saved.
func TestADamagedRecordKeepsEveryDeviceAViewerThroughLaterSaves(t *testing.T) {
	srv, _ := newTestServer(t)
	ask(srv, func() bool {
		srv.prefs.KeepAccessUnknown()
		srv.publishAccess()
		return true
	})
	if got := srv.deviceRole("a-device"); got != store.RoleViewer {
		t.Fatalf("with the record lost a device is %q, want viewer", got)
	}
	desk := dialControl(t, srv)
	sendCmd(t, desk, command{Cmd: "theme", Text: "light"})
	var prefs prefsMsg
	readUntil(t, desk, "prefs", &prefs)
	if got := srv.deviceRole("a-device"); got != store.RoleViewer {
		t.Errorf("after an unrelated preference was saved a device is %q, want it still a viewer", got)
	}
}
