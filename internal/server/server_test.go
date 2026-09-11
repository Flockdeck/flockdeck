package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/jmwri/flockdeck/internal/layout"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/workspace"
)

// newTestServer starts a workspace with one shell pane behind a server.
func newTestServer(t *testing.T) (*Server, *workspace.Workspace) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)

	ws, err := workspace.New(workspace.Options{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	t.Cleanup(ws.Close)

	// Shell panes keep the test independent of whether claude is installed.
	ws.NewTab(session.KindShell, ws.ActiveRoot(), "first")

	srv, err := New(ws)
	if err != nil {
		t.Fatalf("server: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	ws.SetWake(srv.Wake)
	return srv, ws
}

func (s *Server) baseURL() string { return "http://" + s.Addr() }

// everyRoute is every path the server answers on. The token is the only thing
// standing between the agents and anything else running on the machine, so a
// route added without a check for it is the whole security model gone; listing
// them here is what makes that visible when one is.
var everyRoute = []string{
	"/",
	"/assets/app.js",
	"/assets/vendor/xterm.js",
	"/ws/control",
	"/ws/pty?id=any",
	"/help.json",
	"/health",
	"/open?path=/tmp",
	"/quit",
	"/remote/reload",
}

// TestTokenGatesEverything checks the loopback port cannot be driven by another
// local process that has not been given the token.
//
// The routes that matter most are the ones a bare GET would otherwise act on:
// /quit stops every agent in every project and /open opens a directory as one.
// Both check the token before they look at the method, which is the right way
// round and worth holding them to.
func TestTokenGatesEverything(t *testing.T) {
	srv, _ := newTestServer(t)

	// Nothing at all, a token that is not ours, one that is a prefix of ours,
	// and ours with a character changed — all of them somebody else's guess.
	real := srv.Token()
	// Changed to something it is not: a fixed replacement is the token itself
	// whenever the token already starts with it, which a hex token does one
	// time in sixteen, and then every route rightly lets it through.
	changed := "0" + real[1:]
	if real[0] == '0' {
		changed = "1" + real[1:]
	}
	for _, creds := range []struct {
		name   string
		query  string
		cookie string
	}{
		{name: "no token"},
		{name: "a token from somewhere else", query: "not-the-token"},
		{name: "a prefix of the token", query: real[:len(real)-1]},
		{name: "the token with one character changed", query: changed},
		{name: "somebody else's cookie", cookie: "not-the-token"},
		{name: "a prefix of the token as a cookie", cookie: real[:len(real)-1]},
	} {
		for _, path := range everyRoute {
			url := srv.baseURL() + path
			if creds.query != "" {
				sep := "?"
				if strings.Contains(path, "?") {
					sep = "&"
				}
				url += sep + "t=" + creds.query
			}
			req, err := http.NewRequest(http.MethodGet, url, nil)
			if err != nil {
				t.Fatalf("build request for %s: %v", path, err)
			}
			if creds.cookie != "" {
				req.AddCookie(&http.Cookie{Name: tokenCookie, Value: creds.cookie})
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("GET %s: %v", path, err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusForbidden {
				t.Errorf("GET %s with %s = %d, want 403", path, creds.name, resp.StatusCode)
			}
		}
	}

	// And the agents are still running, which is the point: /quit must not
	// have been acted on before the token was looked at.
	if roots := srv.projectRoots(t); len(roots) == 0 {
		t.Error("the workspace is gone after unauthorised requests")
	}
}

// TestIndexSetsCookieAndServesAssets covers the first load: the token in the
// URL is promoted to a cookie so the page's own requests carry it.
func TestIndexSetsCookieAndServesAssets(t *testing.T) {
	srv, _ := newTestServer(t)

	resp, err := http.Get(srv.URL())
	if err != nil {
		t.Fatalf("GET index: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("index = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "flockdeck") {
		t.Error("index does not look like the app page")
	}

	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == tokenCookie {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("index did not set the token cookie")
	}

	// The vendored terminal front end must actually be embedded.
	for _, asset := range []string{"/assets/app.js", "/assets/app.css", "/assets/vendor/xterm.js"} {
		req, _ := http.NewRequest(http.MethodGet, srv.baseURL()+asset, nil)
		req.AddCookie(cookie)
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", asset, err)
		}
		r.Body.Close()
		if r.StatusCode != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", asset, r.StatusCode)
		}
		// The page names these without a version in the path and the window
		// keeps its browser profile between runs, so a cached copy would
		// outlive the binary it shipped with.
		if cc := r.Header.Get("Cache-Control"); cc != "no-store" {
			t.Errorf("GET %s Cache-Control = %q, want no-store", asset, cc)
		}
	}
}

// dialControl opens an authorised control connection.
func dialControl(t *testing.T, srv *Server) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws://"+srv.Addr()+"/ws/control?t="+srv.Token(), nil)
	if err != nil {
		t.Fatalf("dial control: %v", err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })
	return conn
}

// nextState reads control messages until a state snapshot arrives that
// satisfies cond.
func nextState(t *testing.T, conn *websocket.Conn, cond func(stateMsg) bool) stateMsg {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		_, data, err := conn.Read(ctx)
		cancel()
		if err != nil {
			t.Fatalf("read control: %v", err)
		}
		var probe struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(data, &probe) != nil || probe.Type != "state" {
			continue
		}
		var st stateMsg
		if json.Unmarshal(data, &st) != nil {
			continue
		}
		if cond == nil || cond(st) {
			return st
		}
	}
	t.Fatal("timed out waiting for the expected state")
	return stateMsg{}
}

func sendCmd(t *testing.T, conn *websocket.Conn, c command) {
	t.Helper()
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal command: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
		t.Fatalf("write command: %v", err)
	}
}

// TestControlStateAndSplit drives the control protocol the way the window does.
func TestControlStateAndSplit(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)

	// The first message must be a usable snapshot.
	st := nextState(t, conn, nil)
	if len(st.Tabs) != 1 {
		t.Fatalf("got %d tabs, want 1", len(st.Tabs))
	}
	if st.Tabs[0].Title != "first" {
		t.Errorf("tab title = %q, want first", st.Tabs[0].Title)
	}
	if st.Tabs[0].Root == nil || st.Tabs[0].Root.Pane == "" {
		t.Fatal("tab has no pane")
	}
	first := st.Tabs[0].Root.Pane
	if _, ok := st.Panes[first]; !ok {
		t.Fatal("pane is missing from the snapshot")
	}

	// Splitting must produce a two-child split the browser can lay out.
	sendCmd(t, conn, command{Cmd: "splitPane", ID: first, Dir: "h", Kind: "shell"})
	st = nextState(t, conn, func(s stateMsg) bool {
		return len(s.Tabs) == 1 && s.Tabs[0].Root != nil && len(s.Tabs[0].Root.Children) == 2
	})
	root := st.Tabs[0].Root
	if root.Dir != "h" {
		t.Errorf("split direction = %q, want h", root.Dir)
	}
	if root.ID == "" {
		t.Error("split node has no id, so the browser cannot address it when dragging")
	}

	// Weights set from a divider drag must be reflected back.
	sendCmd(t, conn, command{Cmd: "setWeights", Node: root.ID, Weights: []float64{2, 1}})
	st = nextState(t, conn, func(s stateMsg) bool {
		r := s.Tabs[0].Root
		return r != nil && len(r.Children) == 2 && r.Children[0].Weight == 2
	})
	if got := st.Tabs[0].Root.Children[1].Weight; got != 1 {
		t.Errorf("second child weight = %v, want 1", got)
	}

	// Closing one pane collapses the split back to a single leaf.
	second := st.Tabs[0].Root.Children[1].Pane
	sendCmd(t, conn, command{Cmd: "closePane", ID: second})
	nextState(t, conn, func(s stateMsg) bool {
		return len(s.Tabs) == 1 && s.Tabs[0].Root != nil && s.Tabs[0].Root.Pane != ""
	})
}

// TestPTYStreamRoundTrip is the important one: a keystroke sent over the
// terminal socket must reach the process and its output must come back.
func TestPTYStreamRoundTrip(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	st := nextState(t, conn, nil)
	paneID := st.Tabs[0].Root.Pane

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pty, _, err := websocket.Dial(ctx,
		"ws://"+srv.Addr()+"/ws/pty?t="+srv.Token()+"&id="+paneID, nil)
	if err != nil {
		t.Fatalf("dial pty: %v", err)
	}
	defer pty.CloseNow()
	pty.SetReadLimit(8 << 20)

	// Report a size, as the browser does once it has measured the pane.
	if err := pty.Write(ctx, websocket.MessageText, []byte(`{"resize":{"cols":100,"rows":30}}`)); err != nil {
		t.Fatalf("resize: %v", err)
	}
	// Then type, as a keystroke would arrive.
	if err := pty.Write(ctx, websocket.MessageBinary, []byte("echo gui_marker_ok\r")); err != nil {
		t.Fatalf("write input: %v", err)
	}

	var seen strings.Builder
	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		readCtx, c := context.WithTimeout(context.Background(), 25*time.Second)
		_, data, err := pty.Read(readCtx)
		c()
		if err != nil {
			t.Fatalf("read pty: %v", err)
		}
		seen.Write(data)
		if strings.Contains(seen.String(), "gui_marker_ok") {
			break
		}
	}
	if !strings.Contains(seen.String(), "gui_marker_ok") {
		t.Fatalf("did not see the typed command echoed back; saw:\n%s", seen.String())
	}

	// The size the browser reported must have reached the PTY.
	st = nextState(t, conn, func(s stateMsg) bool {
		p, ok := s.Panes[paneID]
		return ok && p.Cols == 100 && p.Rows == 30
	})
	if p := st.Panes[paneID]; p.Cols != 100 || p.Rows != 30 {
		t.Errorf("pane size = %dx%d, want 100x30", p.Cols, p.Rows)
	}
}

// TestUnknownPaneIsRejected covers the terminal endpoint's error path.
func TestUnknownPaneIsRejected(t *testing.T) {
	srv, _ := newTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, resp, err := websocket.Dial(ctx, "ws://"+srv.Addr()+"/ws/pty?t="+srv.Token()+"&id=nope", nil)
	if err == nil {
		t.Fatal("expected the dial to fail for an unknown pane")
	}
	if resp != nil && resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

// TestNewTabAndSelect covers tab management from the window.
func TestNewTabAndSelect(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	nextState(t, conn, nil)

	sendCmd(t, conn, command{Cmd: "newTab", Kind: "shell", Text: "second"})
	st := nextState(t, conn, func(s stateMsg) bool { return len(s.Tabs) == 2 })
	if st.ActiveTab != st.Tabs[1].ID {
		t.Errorf("active tab = %q, want the new one %q", st.ActiveTab, st.Tabs[1].ID)
	}
	if st.Tabs[1].Title != "second" {
		t.Errorf("new tab title = %q, want second", st.Tabs[1].Title)
	}

	firstID, secondID := st.Tabs[0].ID, st.Tabs[1].ID
	sendCmd(t, conn, command{Cmd: "selectTab", ID: firstID})
	nextState(t, conn, func(s stateMsg) bool { return s.ActiveTab == firstID })

	sendCmd(t, conn, command{Cmd: "closeTab", ID: secondID})
	nextState(t, conn, func(s stateMsg) bool { return len(s.Tabs) == 1 })
}

// TestStaleTokenInURLKeepsWorkingCookie covers reloading a bookmarked URL from
// an earlier run: the request is still authorised by the cookie the window
// already holds, and that cookie must survive rather than being replaced by
// the dead token from the address bar.
func TestStaleTokenInURLKeepsWorkingCookie(t *testing.T) {
	srv, _ := newTestServer(t)

	req, _ := http.NewRequest(http.MethodGet, srv.baseURL()+"/?t=stale-token-from-a-previous-run", nil)
	req.AddCookie(&http.Cookie{Name: tokenCookie, Value: srv.Token()})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET index: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("index = %d, want 200", resp.StatusCode)
	}
	for _, c := range resp.Cookies() {
		if c.Name == tokenCookie && c.Value != srv.Token() {
			t.Fatalf("index replaced the working cookie with %q", c.Value)
		}
	}
}

// TestCloseEndsControlConnections covers shutdown of a hijacked connection:
// http.Shutdown leaves websockets alone, so the server has to close them
// itself or the window is left holding a socket nothing is listening on.
func TestCloseEndsControlConnections(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	nextState(t, conn, nil)

	if err := srv.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		if _, _, err := conn.Read(ctx); err != nil {
			break
		}
	}
	if ctx.Err() != nil {
		t.Fatal("the control connection outlived the server")
	}
	// The server side unwinds independently, so give it a moment to notice.
	for deadline := time.Now().Add(5 * time.Second); srv.ClientCount() != 0; {
		if time.Now().After(deadline) {
			t.Fatalf("client count = %d after close, want 0", srv.ClientCount())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestRenameTabRejectsBlankAndClampsLongTitles covers what a window can
// actually send: the rename prompt only checks that something was typed.
func TestRenameTabRejectsBlankAndClampsLongTitles(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	st := nextState(t, conn, nil)
	id := st.Tabs[0].ID

	sendCmd(t, conn, command{Cmd: "renameTab", ID: id, Text: "   \t "})
	sendCmd(t, conn, command{Cmd: "renameTab", ID: id, Text: "  renamed   by   hand  "})
	st = nextState(t, conn, func(s stateMsg) bool {
		return len(s.Tabs) == 1 && s.Tabs[0].Title != "first"
	})
	if got := st.Tabs[0].Title; got != "renamed by hand" {
		t.Errorf("title = %q, want %q", got, "renamed by hand")
	}

	sendCmd(t, conn, command{Cmd: "renameTab", ID: id, Text: strings.Repeat("x", 200)})
	st = nextState(t, conn, func(s stateMsg) bool {
		return len(s.Tabs) == 1 && s.Tabs[0].Title != "renamed by hand"
	})
	if n := len([]rune(st.Tabs[0].Title)); n > 41 {
		t.Errorf("title kept %d runes, want it clamped", n)
	}
}

// TestSnapshotSendsArraysWhenEmpty covers the state a project is left in when
// its last tab is closed. The window walks tabs and projects without checking
// them, so an empty list has to arrive as [] rather than null.
func TestSnapshotSendsArraysWhenEmpty(t *testing.T) {
	srv, ws := newTestServer(t)

	done := make(chan []byte, 1)
	srv.do(func() {
		for len(ws.Tabs) > 0 {
			ws.CloseTab(ws.Tabs[0].ID)
		}
		data, err := json.Marshal(srv.snapshot())
		if err != nil {
			t.Error(err)
		}
		done <- data
	})
	data := string(<-done)

	if !strings.Contains(data, `"tabs":[]`) {
		t.Errorf("snapshot with no tabs does not send an empty array: %s", data)
	}
	if strings.Contains(data, `"projects":null`) {
		t.Errorf("snapshot sent a null project list: %s", data)
	}
}

// TestStalePaneCommandDoesNotHitTheWrongPane covers a click on a pane that has
// already gone. These commands act on whichever pane has focus after being
// pointed at the named one, so failing to point at it must stop the command
// rather than let it close or restart whatever was focused instead.
func TestStalePaneCommandDoesNotHitTheWrongPane(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	st := nextState(t, conn, nil)
	survivor := st.Tabs[0].Root.Pane

	sendCmd(t, conn, command{Cmd: "closePane", ID: "a-pane-that-has-gone"})

	var note noticeMsg
	readUntil(t, conn, "notice", &note)
	if !note.Error {
		t.Errorf("expected an error notice, got %+v", note)
	}

	// The pane that did have focus must still be there.
	sendCmd(t, conn, command{Cmd: "focusPane", ID: survivor})
	st = nextState(t, conn, func(s stateMsg) bool { return s.Tabs[0].Focus == survivor })
	if _, ok := st.Panes[survivor]; !ok {
		t.Fatal("the focused pane was closed by a command aimed at another one")
	}
}

// controlReader reads a control connection in the background. A test that
// wants to prove a message did *not* arrive cannot simply read with a short
// deadline: cancelling a read closes the websocket, so the connection would be
// gone before the test could check anything else on it. Reading continuously
// and letting the test wait on a channel keeps the connection intact.
type controlReader struct{ msgs chan []byte }

func readControl(conn *websocket.Conn) *controlReader {
	r := &controlReader{msgs: make(chan []byte, 256)}
	go func() {
		defer close(r.msgs)
		for {
			_, data, err := conn.Read(context.Background())
			if err != nil {
				return
			}
			r.msgs <- data
		}
	}()
	return r
}

// stateWithin waits up to d for a state snapshot satisfying cond.
func (r *controlReader) stateWithin(d time.Duration, cond func(stateMsg) bool) (stateMsg, bool) {
	deadline := time.After(d)
	for {
		select {
		case data, ok := <-r.msgs:
			if !ok {
				return stateMsg{}, false
			}
			var probe struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(data, &probe) != nil || probe.Type != "state" {
				continue
			}
			var st stateMsg
			if json.Unmarshal(data, &st) != nil {
				continue
			}
			if cond == nil || cond(st) {
				return st, true
			}
		case <-deadline:
			return stateMsg{}, false
		}
	}
}

// settle waits for the opening flurry to stop: the first snapshot, the shell's
// own start-up chatter and the git refresh a new window asks for all land in
// the first second or so.
func (r *controlReader) settle(t *testing.T) {
	t.Helper()
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); {
		if _, ok := r.stateWithin(800*time.Millisecond, nil); !ok {
			return
		}
	}
	t.Fatal("the control connection never went quiet")
}

// TestUnchangedStateIsNotResent covers the cost of a talkative agent. Every
// chunk of output a session produces wakes the server, but almost none of them
// change anything the snapshot carries, and a window that is handed the state
// it already holds parses it and re-renders the whole interface for nothing.
func TestUnchangedStateIsNotResent(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	r := readControl(conn)
	r.settle(t)

	// Twenty wakes at roughly the rate an agent streaming output produces
	// them, with nothing behind them that a window would draw differently.
	for i := 0; i < 20; i++ {
		srv.Wake()
		time.Sleep(10 * time.Millisecond)
	}
	if st, ok := r.stateWithin(time.Second, nil); ok {
		t.Fatalf("an unchanged snapshot was broadcast again: %+v", st)
	}

	// A real change must still get through, or the window would freeze.
	sendCmd(t, conn, command{Cmd: "newTab", Kind: "shell", Text: "second"})
	if _, ok := r.stateWithin(10*time.Second, func(s stateMsg) bool {
		return len(s.Tabs) == 2 && s.Tabs[1].Title == "second"
	}); !ok {
		t.Fatal("a real change was not broadcast")
	}
}

// TestSnapshotsSupersedeRatherThanQueue covers a window that has stopped
// draining its socket. The queue behind it fills, and what is dropped once it
// is full is the newest message — so the window is left showing state that has
// since changed, with nothing to put it right now that an unchanged snapshot
// is no longer re-sent. A snapshot supersedes whichever one is still waiting
// instead of queueing behind the backlog, and does not spend the queue that
// the notices need, since those do not supersede each other.
func TestSnapshotsSupersedeRatherThanQueue(t *testing.T) {
	c := &controlClient{out: make(chan []byte, 2), ready: make(chan struct{}, 1)}

	c.send([]byte("notice one"))
	c.send([]byte("notice two"))
	c.send([]byte("notice three")) // the queue is full; this one is lost

	c.sendState([]byte("state one"))
	c.sendState([]byte("state two"))

	if got := string(c.takeState()); got != "state two" {
		t.Errorf("waiting snapshot = %q, want the newest one", got)
	}
	if got := c.takeState(); got != nil {
		t.Errorf("a snapshot was taken twice: %q", got)
	}

	// The notices already queued are still there to be written.
	for _, want := range []string{"notice one", "notice two"} {
		select {
		case got := <-c.out:
			if string(got) != want {
				t.Errorf("queued message = %q, want %q", got, want)
			}
		default:
			t.Errorf("%q was pushed out of the queue by a snapshot", want)
		}
	}
}

// benchServer starts a workspace of shell panes behind a server, which is as
// close as a benchmark can get to a window full of agents without needing the
// claude CLI. The panes carry the git summaries a real one would.
func benchServer(b *testing.B, panes int) *Server {
	b.Helper()
	dir := b.TempDir()
	b.Setenv("APPDATA", dir)
	b.Setenv("XDG_CONFIG_HOME", dir)
	b.Setenv("HOME", dir)

	ws, err := workspace.New(workspace.Options{Root: b.TempDir()})
	if err != nil {
		b.Fatalf("workspace: %v", err)
	}
	b.Cleanup(ws.Close)
	ws.NewTab(session.KindShell, ws.ActiveRoot(), "bench")
	for i := 1; i < panes; i++ {
		ws.SplitPaneIn(layout.Horizontal, session.KindShell, "")
	}
	srv, err := New(ws)
	if err != nil {
		b.Fatalf("server: %v", err)
	}
	b.Cleanup(func() { _ = srv.Close() })
	return srv
}

var snapshotSink stateMsg

// BenchmarkSnapshot measures the cost paid on every wake. A session calls back
// on every chunk of output it produces, so this runs at the debounce ceiling
// whenever the agents are talking, and it runs on the goroutine that owns the
// workspace and dispatches every command from every window.
func BenchmarkSnapshot(b *testing.B) {
	srv := benchServer(b, 12)
	done := make(chan struct{})
	srv.do(func() {
		defer close(done)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			snapshotSink = srv.snapshot()
		}
		b.StopTimer()
	})
	<-done
}

var encodeSink []byte

// BenchmarkSnapshotEncode measures the whole per-wake cost: the snapshot plus
// the encoding that decides whether anything actually changed.
func BenchmarkSnapshotEncode(b *testing.B) {
	srv := benchServer(b, 12)
	done := make(chan struct{})
	srv.do(func() {
		defer close(done)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			encodeSink, _ = json.Marshal(srv.snapshot())
		}
		b.StopTimer()
	})
	<-done
	b.Logf("snapshot encodes to %d bytes", len(encodeSink))
}

// TestChangesReachTheWindowPromptly covers the latency a person actually sees.
// Splitting, closing, zooming and switching tabs all show up only when the next
// state arrives, so holding every change for the rate-limit interval puts that
// interval on the end of every one of them. Only changes that follow a recent
// broadcast should have to wait.
func TestChangesReachTheWindowPromptly(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	r := readControl(conn)
	r.settle(t)
	id := srv.firstTabID(t)

	// Timings on a loaded machine are noisy, so several are taken and the best
	// one is judged: under a plain delay not one of them could beat the
	// interval, however quiet the machine happened to be.
	best := time.Hour
	for i := 0; i < 8; i++ {
		title := fmt.Sprintf("prompt %d", i)
		start := time.Now()
		sendCmd(t, conn, command{Cmd: "renameTab", ID: id, Text: title})
		if _, ok := r.stateWithin(10*time.Second, func(s stateMsg) bool {
			return len(s.Tabs) == 1 && s.Tabs[0].Title == title
		}); !ok {
			t.Fatalf("the rename to %q was never broadcast", title)
		}
		if d := time.Since(start); d < best {
			best = d
		}
		// Long enough that the next change starts from a quiet interval.
		time.Sleep(2 * stateInterval)
	}
	if best >= stateInterval {
		t.Errorf("the quickest of eight changes took %v, which is the whole %v interval: changes are being delayed rather than rate limited", best, stateInterval)
	}
	t.Logf("quickest change to reach the window: %v", best)

	// The interval must still cap the rate, or a talkative agent would have
	// the window rebuilding itself on every chunk of output it produces.
	const burst = 40
	var last string
	for i := 0; i < burst; i++ {
		last = fmt.Sprintf("burst %d", i)
		sendCmd(t, conn, command{Cmd: "renameTab", ID: id, Text: last})
	}
	sent := 0
	for {
		st, ok := r.stateWithin(10*time.Second, nil)
		if !ok {
			t.Fatalf("the last of %d changes was never broadcast", burst)
		}
		sent++
		if len(st.Tabs) == 1 && st.Tabs[0].Title == last {
			break
		}
	}
	if sent > burst/2 {
		t.Errorf("%d of %d changes were broadcast separately; they should be coalesced", sent, burst)
	}
	t.Logf("%d changes were coalesced into %d broadcasts", burst, sent)
}

// firstTabID reads the id of the first visible tab.
func (s *Server) firstTabID(t *testing.T) string {
	t.Helper()
	done := make(chan string, 1)
	s.do(func() {
		if tabs := s.ws.VisibleTabs(); len(tabs) > 0 {
			done <- tabs[0].ID
			return
		}
		done <- ""
	})
	select {
	case id := <-done:
		if id == "" {
			t.Fatal("no visible tabs")
		}
		return id
	case <-time.After(10 * time.Second):
		t.Fatal("timed out reading the tab list")
		return ""
	}
}

// TestAPanicDoesNotTakeTheAgentsDown covers the worst outcome a bad command
// can have. Every window command is applied on one goroutine, and a panic in
// any goroutine ends the process — which here means killing every agent
// running under it. The interface has to survive it and say so.
func TestAPanicDoesNotTakeTheAgentsDown(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	r := readControl(conn)
	r.settle(t)
	id := srv.firstTabID(t)

	srv.do(func() { panic("a command reached something that had gone") })

	// The workspace goroutine must still be there to serve what follows.
	sendCmd(t, conn, command{Cmd: "renameTab", ID: id, Text: "still here"})
	if _, ok := r.stateWithin(10*time.Second, func(s stateMsg) bool {
		return len(s.Tabs) == 1 && s.Tabs[0].Title == "still here"
	}); !ok {
		t.Fatal("the workspace stopped answering after a panic")
	}

	// And a panic while handling a command is contained the same way.
	srv.guard("in a test", func() { panic("and again") })
	sendCmd(t, conn, command{Cmd: "renameTab", ID: id, Text: "and still here"})
	if _, ok := r.stateWithin(10*time.Second, func(s stateMsg) bool {
		return len(s.Tabs) == 1 && s.Tabs[0].Title == "and still here"
	}); !ok {
		t.Fatal("the workspace stopped answering after a second panic")
	}
}

// TestAPanicTellsTheWindow covers the report: a click that quietly did nothing
// is worse than one that says what went wrong.
func TestAPanicTellsTheWindow(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	nextState(t, conn, nil)

	srv.do(func() { panic("the tab was not there") })

	var note noticeMsg
	readUntil(t, conn, "notice", &note)
	if !note.Error {
		t.Errorf("notice = %+v, want it marked as an error", note)
	}
	if !strings.Contains(note.Text, "the tab was not there") {
		t.Errorf("notice = %q, want it to carry what went wrong", note.Text)
	}
}

// projectRoots reads the open project list.
func (s *Server) projectRoots(t *testing.T) []string {
	t.Helper()
	done := make(chan []string, 1)
	s.do(func() {
		var roots []string
		for _, p := range s.ws.Projects() {
			roots = append(roots, p.Root)
		}
		done <- roots
	})
	select {
	case roots := <-done:
		return roots
	case <-time.After(10 * time.Second):
		t.Fatal("timed out reading the project list")
		return nil
	}
}

// TestOpenProjectNeedsAFullPath covers what a path the window did not fill in
// would otherwise mean. Anything that is not absolute is resolved against the
// directory flockdeck itself was launched from, and an empty one resolves to that
// directory exactly — so it would open as a project, take over the tab bar and
// have an agent started in it, none of which anybody asked for.
func TestOpenProjectNeedsAFullPath(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	nextState(t, conn, nil)
	before := srv.projectRoots(t)

	for _, path := range []string{"", ".", filepath.Join("relative", "dir")} {
		sendCmd(t, conn, command{Cmd: "openProject", Path: path})
		var note noticeMsg
		readUntil(t, conn, "notice", &note)
		if !note.Error {
			t.Errorf("openProject %q was accepted: %+v", path, note)
		}
	}

	if after := srv.projectRoots(t); len(after) != len(before) {
		t.Errorf("open projects went from %v to %v", before, after)
	}
}

// TestTheKeyTableArrivesBeforeTheFirstState covers the order a window is set up
// in. The palette and the first-run hints are drawn from the key table and the
// preferences, which is why they are sent before the state; a snapshot that
// overtakes them renders hints with their keyboard shortcut missing until the
// hello lands. State travels in a slot of its own rather than the queue, so
// nothing but the write order keeps that promise.
func TestTheKeyTableArrivesBeforeTheFirstState(t *testing.T) {
	srv, _ := newTestServer(t)

	const rounds = 60
	early := 0
	for i := 0; i < rounds; i++ {
		conn := dialControl(t, srv)
		for {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			_, data, err := conn.Read(ctx)
			cancel()
			if err != nil {
				t.Fatalf("read control: %v", err)
			}
			var probe struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(data, &probe) != nil {
				continue
			}
			if probe.Type == "state" {
				early++
			}
			if probe.Type == "state" || probe.Type == "hello" {
				break
			}
		}
		_ = conn.CloseNow()
	}
	if early > 0 {
		t.Errorf("the state overtook the key table on %d of %d connections", early, rounds)
	}
}

// TestMalformedCommandsDoNotStopTheServer fires the whole shape of the command
// surface at the socket with ids that name nothing, targets that are their own
// source, oversized text and directions that do not exist.
//
// Everything here is reachable: a window renders from a snapshot that is
// already out of date by the time it is clicked, so any id it sends may name
// something another window closed a moment ago, and the socket is driven by
// other things than the page — the ctl and dump tools speak it too. What the
// dispatch must not do is stop serving, whatever it is handed.
func TestMalformedCommandsDoNotStopTheServer(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	// Some of the answers are as long as what was sent; the browser has no
	// such limit, so this is the test's own connection being realistic.
	conn.SetReadLimit(8 << 20)
	r := readControl(conn)
	r.settle(t)

	tab := srv.firstTabID(t)
	pane := srv.firstPaneID(t)
	big := strings.Repeat("q", 100000)

	for _, cmd := range []command{
		{Cmd: "movePane", ID: pane, Target: pane, Edge: "left"},
		{Cmd: "movePane", ID: "gone", Target: pane, Edge: "nowhere"},
		{Cmd: "swapPanes", ID: pane, Target: pane},
		{Cmd: "movePaneToTab", ID: pane, Target: tab},
		{Cmd: "movePaneToNewTab", ID: "gone"},
		{Cmd: "movePaneDir", Dir: "sideways"},
		{Cmd: "mergeTab", ID: tab, Target: tab, Dir: "h"},
		{Cmd: "moveTab", ID: tab, Target: tab},
		{Cmd: "mergeAllTabs", ID: "gone", Dir: "h"},
		{Cmd: "setWeights", Node: "gone", Weights: []float64{1, 2, 3}},
		{Cmd: "setWeights", Node: tab, Weights: make([]float64, 5000)},
		{Cmd: "resize", ID: pane, Cols: -5, Rows: -5},
		{Cmd: "resize", ID: pane, Cols: 1 << 30, Rows: 1 << 30},
		{Cmd: "renameTab", ID: tab, Text: "a\x00b\nc\rd\te"},
		{Cmd: "renameTab", ID: tab, Text: big},
		{Cmd: "renameTab", ID: big, Text: "x"},
		{Cmd: "selectTab", ID: big},
		{Cmd: "closeTab", ID: big},
		{Cmd: "focusPane", ID: big},
		{Cmd: "splitPane", ID: "gone", Dir: "h", Kind: "shell"},
		{Cmd: "closePane", ID: "gone"},
		{Cmd: "restartPane", ID: "gone"},
		{Cmd: "toggleZoom", ID: "gone"},
		{Cmd: "toggleBroadcastMember", ID: "gone"},
		{Cmd: "openProject", Path: "\x00"},
		{Cmd: "closeProject", Root: ""},
		{Cmd: "selectProject", Root: big},
		{Cmd: "forgetRecent", Root: ""},
		{Cmd: "revealPane", Root: big, Node: big, ID: big},
		{Cmd: "changes", Path: big},
		{Cmd: "conversations", Path: big},
		{Cmd: "fanoutPreview", ID: "gone"},
		{Cmd: "sendPrompt", Text: ""},
		{Cmd: strings.Repeat("z", 5000)},
		{Cmd: ""},
	} {
		sendCmd(t, conn, cmd)
	}
	// Not JSON at all, and JSON that is not a command.
	writeRaw(t, conn, []byte("{"))
	writeRaw(t, conn, []byte("[1,2,3]"))
	writeRaw(t, conn, []byte(`{"cmd":123}`))

	sendCmd(t, conn, command{Cmd: "renameTab", ID: tab, Text: "still serving"})
	if _, ok := r.stateWithin(20*time.Second, func(s stateMsg) bool {
		for _, tb := range s.Tabs {
			if tb.Title == "still serving" {
				return true
			}
		}
		return false
	}); !ok {
		t.Fatal("the server stopped answering after the malformed commands")
	}
}

// TestWindowsComingAndGoingDoNotLeak covers reconnection, which the page does
// by itself whenever the socket drops. A window that is not taken off the
// client list keeps the count above zero for ever, so the application never
// learns that its last window has gone and never shuts down.
func TestWindowsComingAndGoingDoNotLeak(t *testing.T) {
	srv, _ := newTestServer(t)
	keep := dialControl(t, srv)
	r := readControl(keep)
	r.settle(t)
	tab := srv.firstTabID(t)

	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 6; j++ {
				c := dialControl(t, srv)
				// Gone again before it has read a byte of what it was sent.
				sendCmd(t, c, command{Cmd: "nextTab"})
				_ = c.CloseNow()
			}
		}()
	}
	// Broadcasts running the whole time, so some of them are aimed at windows
	// that go away between being listed and being written to.
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				srv.Wake()
				time.Sleep(time.Millisecond)
			}
		}()
	}
	wg.Wait()

	for deadline := time.Now().Add(20 * time.Second); srv.ClientCount() != 1; {
		if time.Now().After(deadline) {
			t.Fatalf("client count = %d, want the one window still open", srv.ClientCount())
		}
		time.Sleep(20 * time.Millisecond)
	}

	sendCmd(t, keep, command{Cmd: "renameTab", ID: tab, Text: "still serving"})
	if _, ok := r.stateWithin(20*time.Second, func(s stateMsg) bool {
		for _, tb := range s.Tabs {
			if tb.Title == "still serving" {
				return true
			}
		}
		return false
	}); !ok {
		t.Fatal("the window that stayed stopped being served")
	}
}

// firstPaneID reads the id of a pane on the first visible tab.
func (s *Server) firstPaneID(t *testing.T) string {
	t.Helper()
	done := make(chan string, 1)
	s.do(func() {
		if tabs := s.ws.VisibleTabs(); len(tabs) > 0 {
			if panes := tabs[0].Tree.Panes(); len(panes) > 0 {
				done <- panes[0]
				return
			}
		}
		done <- ""
	})
	select {
	case id := <-done:
		if id == "" {
			t.Fatal("no panes")
		}
		return id
	case <-time.After(10 * time.Second):
		t.Fatal("timed out reading the pane list")
		return ""
	}
}

// writeRaw sends bytes the command decoder is not expected to understand.
func writeRaw(t *testing.T, conn *websocket.Conn, data []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
		t.Fatalf("write raw: %v", err)
	}
}

// TestAChangeAskedForBeatsTheChatter covers the case the interval was hurting
// most. With several agents talking, the last broadcast is never long ago, so
// a single rate limit put its full wait on the end of every split, close and
// tab switch — the interface felt slowest exactly when there was most going on.
func TestAChangeAskedForBeatsTheChatter(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	r := readControl(conn)
	r.settle(t)
	id := srv.firstTabID(t)

	// Sessions waking the server at the rate a few busy agents would.
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			srv.Wake()
			time.Sleep(2 * time.Millisecond)
		}
	}()

	// Latency under a plain rate limit is spread evenly across the interval,
	// so the best of a few runs proves nothing and the worst is at the mercy
	// of a busy machine. The middle one is the honest measure.
	const rounds = 15
	var took []time.Duration
	for i := 0; i < rounds; i++ {
		title := fmt.Sprintf("asked %d", i)
		start := time.Now()
		sendCmd(t, conn, command{Cmd: "renameTab", ID: id, Text: title})
		if _, ok := r.stateWithin(10*time.Second, func(s stateMsg) bool {
			return len(s.Tabs) == 1 && s.Tabs[0].Title == title
		}); !ok {
			t.Fatalf("the rename to %q was never broadcast", title)
		}
		took = append(took, time.Since(start))
		// A gap that is not a multiple of the interval, so the changes do not
		// fall into step with the chatter's broadcasts and land at the same
		// point in the wait every time.
		time.Sleep(time.Duration(60+rand.Intn(90)) * time.Millisecond)
	}
	sort.Slice(took, func(a, b int) bool { return took[a] < took[b] })
	median := took[len(took)/2]
	if median >= stateInterval/4 {
		t.Errorf("the middle of %d changes took %v while the agents talked; the chatter's %v interval is being charged to it", rounds, median, stateInterval)
	}
	t.Logf("median change reaching the window while agents talked: %v (worst %v)", median, took[len(took)-1])
}

// TestAWindowOpenedLaterIsNotToldStaleNews covers the two halves of skipping
// work while no window is open. Nothing is built or sent for nobody — but a
// change made in that time is then absent from what was last broadcast, so if
// the state wanders away and comes back to it, the comparison that suppresses
// an unchanged snapshot would suppress one a newly opened window has never
// seen.
func TestAWindowOpenedLaterIsNotToldStaleNews(t *testing.T) {
	srv, _ := newTestServer(t)

	first := dialControl(t, srv)
	r1 := readControl(first)
	r1.settle(t)
	id := srv.firstTabID(t)
	original := ""
	if st, ok := r1.stateWithin(time.Second, nil); ok {
		original = st.Tabs[0].Title
	}
	if original == "" {
		original = "first"
	}

	// The window goes.
	_ = first.CloseNow()
	for deadline := time.Now().Add(10 * time.Second); srv.ClientCount() != 0; {
		if time.Now().After(deadline) {
			t.Fatal("the window was never taken off the client list")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// The workspace moves on with nobody watching.
	renameTab(t, srv, id, "while nobody was looking")
	time.Sleep(4 * stateInterval)

	// A window opens and is given the state as it now stands.
	second := dialControl(t, srv)
	r2 := readControl(second)
	if _, ok := r2.stateWithin(20*time.Second, func(s stateMsg) bool {
		return len(s.Tabs) == 1 && s.Tabs[0].Title == "while nobody was looking"
	}); !ok {
		t.Fatal("the newly opened window was not given the current state")
	}

	// And the state goes back to exactly what was last broadcast, before this
	// window existed. It has never been sent that, so it has to be now.
	renameTab(t, srv, id, original)
	if _, ok := r2.stateWithin(20*time.Second, func(s stateMsg) bool {
		return len(s.Tabs) == 1 && s.Tabs[0].Title == original
	}); !ok {
		t.Fatalf("the window was never told the title went back to %q", original)
	}
}

// renameTab renames a tab from outside any window, as something other than the
// interface would.
func renameTab(t *testing.T, s *Server, id, title string) {
	t.Helper()
	done := make(chan struct{})
	s.do(func() {
		defer close(done)
		if tab := s.ws.Tab(id); tab != nil {
			tab.Title = title
			tab.AutoTitle = false
		}
	})
	<-done
	s.Wake()
}

// TestEncodeNodeCollectsExactlyThePaneIds pins the equivalence the snapshot now
// relies on. The pane ids used to come from a second walk of the tree and are
// now gathered on the way through the first, so they have to be the same ids in
// the same order — including the skipping of a leaf that holds no pane, which a
// split left half built can produce.
func TestEncodeNodeCollectsExactlyThePaneIds(t *testing.T) {
	inner := layout.NewSplit(layout.Vertical)
	inner.Children = []*layout.Node{
		layout.NewLeaf("b"),
		layout.NewLeaf(""), // a leaf with no pane in it
		layout.NewLeaf("c"),
	}
	root := layout.NewSplit(layout.Horizontal)
	root.Children = []*layout.Node{layout.NewLeaf("a"), inner, layout.NewLeaf("d")}

	var got []string
	view := encodeNode(root, &got)

	want := root.Panes()
	if len(got) != len(want) {
		t.Fatalf("collected %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("collected %v, want %v", got, want)
		}
	}

	// A weight of zero comes back from a layout written before weights were
	// recorded; sent on as zero it would lay the pane out with no width at all.
	if view.Children[0].Weight != 1 {
		t.Errorf("leaf weight = %v, want the default 1", view.Children[0].Weight)
	}
	if view.Dir != "h" || view.Children[1].Dir != "v" {
		t.Errorf("split directions = %q and %q, want h and v", view.Dir, view.Children[1].Dir)
	}
}

// TestABadWeightCannotSilenceTheWindows covers the one value in the snapshot
// that can stop it being a snapshot at all. Weights are floats, and the layout
// divides and multiplies them when splits collapse into one another, so a set
// far enough apart can arrive at an infinity or a NaN. encoding/json refuses to
// write either, and a state message that will not encode is not sent — not this
// one, and not any after it, since the same number is still there. Every window
// would stop being updated for good, without a word.
func TestABadWeightCannotSilenceTheWindows(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	r := readControl(conn)
	r.settle(t)
	id := srv.firstTabID(t)

	for _, bad := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		done := make(chan struct{})
		srv.do(func() {
			defer close(done)
			if tab := srv.ws.Tab(id); tab != nil {
				tab.Tree.Weight = bad
			}
		})
		<-done

		title := fmt.Sprintf("after %v", bad)
		sendCmd(t, conn, command{Cmd: "renameTab", ID: id, Text: title})
		st, ok := r.stateWithin(10*time.Second, func(s stateMsg) bool {
			return len(s.Tabs) == 1 && s.Tabs[0].Title == title
		})
		if !ok {
			t.Fatalf("a weight of %v stopped the windows being updated", bad)
		}
		if w := st.Tabs[0].Root.Weight; w != 1 {
			t.Errorf("weight of %v was sent on as %v, want the default 1", bad, w)
		}
	}
}

// TestChatterIsHeldToTheInterval covers the cap the interval exists for. Each
// state message costs the window a parse and a full redraw of its chrome, on
// the same thread that draws the terminals, so what the agents change between
// themselves has to be rationed however fast they change it.
func TestChatterIsHeldToTheInterval(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	r := readControl(conn)
	r.settle(t)
	id := srv.firstTabID(t)

	// A change every few milliseconds, each one different, arriving the way a
	// session's own output does rather than as something a person asked for.
	stop := make(chan struct{})
	go func() {
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			renameTab(t, srv, id, fmt.Sprintf("chatter %d", i))
			time.Sleep(3 * time.Millisecond)
		}
	}()

	const window = 2 * time.Second
	deadline := time.Now().Add(window)
	sent := 0
	for time.Now().Before(deadline) {
		if _, ok := r.stateWithin(time.Until(deadline), nil); ok {
			sent++
		}
	}
	close(stop)

	// A little over the cap allows for the wait being measured from the start
	// of one broadcast rather than the end.
	most := int(window/stateInterval) + 2
	if sent > most {
		t.Errorf("%d broadcasts in %v, which is more than the %v interval allows (%d)", sent, window, stateInterval, most)
	}
	t.Logf("%d broadcasts in %v", sent, window)
}

// TestNewTabTitleIsClampedLikeARename covers the one place a title reached the
// tab bar uncleaned. The rename path has always trimmed and bounded what it was
// given; the create path did not, and it is fed branch names — the worktree
// panel opens a tab named after one, and a branch named out of a ticket title
// is long enough to push every other tab off the bar.
func TestNewTabTitleIsClampedLikeARename(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	r := readControl(conn)
	r.settle(t)

	long := "feature/" + strings.Repeat("a-very-long-branch-name-", 12)
	sendCmd(t, conn, command{Cmd: "newTab", Kind: "shell", Text: long})
	st, ok := r.stateWithin(20*time.Second, func(s stateMsg) bool { return len(s.Tabs) == 2 })
	if !ok {
		t.Fatal("the tab was never created")
	}
	if n := len([]rune(st.Tabs[1].Title)); n > 41 {
		t.Errorf("new tab kept %d runes of its title, want it clamped like a rename", n)
	}

	// Whitespace is collapsed, as a rename does.
	sendCmd(t, conn, command{Cmd: "newTab", Kind: "shell", Text: "  two   words  "})
	st, ok = r.stateWithin(20*time.Second, func(s stateMsg) bool { return len(s.Tabs) == 3 })
	if !ok {
		t.Fatal("the second tab was never created")
	}
	if got := st.Tabs[2].Title; got != "two words" {
		t.Errorf("new tab title = %q, want %q", got, "two words")
	}

	// And a tab asked for with no title still names itself.
	sendCmd(t, conn, command{Cmd: "newTab", Kind: "shell"})
	st, ok = r.stateWithin(20*time.Second, func(s stateMsg) bool { return len(s.Tabs) == 4 })
	if !ok {
		t.Fatal("the third tab was never created")
	}
	if st.Tabs[3].Title == "" {
		t.Error("a tab created with no title was left without one")
	}
}
