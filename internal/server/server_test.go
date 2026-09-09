package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/jmwri/perch/internal/session"
	"github.com/jmwri/perch/internal/workspace"
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

// TestTokenGatesEverything checks the loopback port cannot be driven by another
// local process that has not been given the token.
func TestTokenGatesEverything(t *testing.T) {
	srv, _ := newTestServer(t)

	for _, path := range []string{"/", "/assets/app.js", "/ws/control"} {
		resp, err := http.Get(srv.baseURL() + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("GET %s without a token = %d, want 403", path, resp.StatusCode)
		}
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
	if !strings.Contains(string(body), "perch") {
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
