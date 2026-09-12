package server

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/jmwri/flockdeck/internal/remote"
)

// fakeRemote stands in for remote access, answering for a tunnel that is not
// there.
type fakeRemote struct {
	st      remote.Status
	ok      bool
	reloads atomic.Int32
	// err is what Reload fails with, for a test of an enrolment that cannot
	// be read.
	err error
}

func (f *fakeRemote) Status() (remote.Status, bool)   { return f.st, f.ok }
func (f *fakeRemote) Client() (*remote.Client, error) { return nil, remote.ErrNotEnabled }
func (f *fakeRemote) Reload() error                   { f.reloads.Add(1); return f.err }

// TestRemoteReloadSaysWhyItFailed covers `flockdeck remote enable` reaching an
// instance that cannot reread the enrolment. It used to print "500 Internal
// Server Error" and nothing else, when the instance had said why.
func TestRemoteReloadSaysWhyItFailed(t *testing.T) {
	srv, _ := newTestServer(t)
	srv.SetRemote(&fakeRemote{err: errors.New("remote.json: unexpected end of JSON input")})
	err := RequestRemoteReload(srv.BaseURL(), srv.Token())
	if err == nil || !strings.Contains(err.Error(), "unexpected end of JSON input") {
		t.Fatalf("RequestRemoteReload = %v, want the instance's reason", err)
	}
}

// remoteServer serves srv the way the tunnel does, on a listener of the test's
// own, and returns its address.
func remoteServer(t *testing.T, srv *Server) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(srv.RemoteHandler())
	t.Cleanup(ts.Close)
	return ts
}

// A request through the tunnel has no token and needs none: the relay has
// already decided the device may ask. That goes for the page, its assets and
// the help — everything the window loads.
func TestRemoteRequestsNeedNoToken(t *testing.T) {
	srv, _ := newTestServer(t)
	ts := remoteServer(t, srv)
	for _, path := range []string{"/", "/assets/app.js", "/assets/vendor/xterm.js", "/help.json"} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s through the tunnel = %d, want 200", path, resp.StatusCode)
		}
		for _, c := range resp.Cookies() {
			if strings.HasPrefix(c.Name, tokenCookie) {
				t.Errorf("GET %s through the tunnel set the local token cookie", path)
			}
		}
	}
}

// The endpoints another launch of the binary uses are not for a remote window
// at all. /quit in particular stops every agent in every project, and a
// device that can drive the window has no business reaching it by the back
// door — it has the window's own Quit, which asks first.
func TestRemoteCannotReachTheLauncherEndpoints(t *testing.T) {
	srv, _ := newTestServer(t)
	quit := make(chan struct{}, 1)
	srv.OnQuit = func() { quit <- struct{}{} }
	fake := &fakeRemote{}
	srv.SetRemote(fake)
	ts := remoteServer(t, srv)

	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/health"},
		{http.MethodPost, "/quit"},
		{http.MethodPost, "/open?path=" + queryEscape(t.TempDir())},
		{http.MethodPost, "/remote/reload"},
		// A token in the URL is no way round it: the remote side cannot have
		// the real one, and a guessed one is no better.
		{http.MethodPost, "/quit?t=not-the-token"},
	} {
		req, _ := http.NewRequest(tc.method, ts.URL+tc.path, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", tc.method, tc.path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s %s through the tunnel = %d, want 403", tc.method, tc.path, resp.StatusCode)
		}
	}
	select {
	case <-quit:
		t.Fatal("a request through the tunnel quit the instance")
	case <-time.After(100 * time.Millisecond):
	}
	if n := fake.reloads.Load(); n != 0 {
		t.Errorf("a request through the tunnel reloaded remote access %d times", n)
	}
	if roots := srv.projectRoots(t); len(roots) == 0 {
		t.Error("the projects are gone")
	}
}

// The launching binary can reload remote access; that is what `flockdeck
// remote enable` relies on to reach a running instance.
func TestRemoteReloadFromTheLauncher(t *testing.T) {
	srv, _ := newTestServer(t)
	fake := &fakeRemote{}
	srv.SetRemote(fake)
	if err := RequestRemoteReload(srv.BaseURL(), srv.Token()); err != nil {
		t.Fatalf("RequestRemoteReload: %v", err)
	}
	if n := fake.reloads.Load(); n != 1 {
		t.Errorf("reloaded %d times, want 1", n)
	}
	// A GET is not a reload, even with the token.
	resp, err := http.Get(srv.BaseURL() + "/remote/reload?t=" + srv.Token())
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET /remote/reload = %d, want 405", resp.StatusCode)
	}
}

// TestRemoteWindowsAreSentCompressed covers a window through the relay, often
// a phone on a metered link, which is sent the same few kilobytes of snapshot
// again and again with a word or two changed. It is compressed when the
// browser offers; a window on this machine gains nothing from that and is not.
func TestRemoteWindowsAreSentCompressed(t *testing.T) {
	srv, _ := newTestServer(t)
	ts := remoteServer(t, srv)
	dial := func(url, origin string) (*websocket.Conn, *http.Response) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		h := http.Header{}
		h.Set("Origin", origin)
		conn, resp, err := websocket.Dial(ctx, url,
			&websocket.DialOptions{HTTPHeader: h, CompressionMode: websocket.CompressionContextTakeover})
		if err != nil {
			t.Fatalf("dial %s: %v", url, err)
		}
		conn.SetReadLimit(16 << 20)
		t.Cleanup(func() { conn.CloseNow() })
		return conn, resp
	}

	remoteConn, resp := dial("ws"+strings.TrimPrefix(ts.URL, "http")+"/ws/control", ts.URL)
	if ext := resp.Header.Get("Sec-WebSocket-Extensions"); !strings.Contains(ext, "permessage-deflate") {
		t.Errorf("a window through the relay was not offered compression: %q", ext)
	}
	readRemoteMsg(t, remoteConn, "state")

	_, resp = dial("ws://"+srv.Addr()+"/ws/control?t="+srv.Token(), "http://"+srv.Addr())
	if ext := resp.Header.Get("Sec-WebSocket-Extensions"); ext != "" {
		t.Errorf("a window on this machine was compressed: %q", ext)
	}
}

// dialRemoteControl opens the control socket through the tunnel as a page
// from origin would.
func dialRemoteControl(ts *httptest.Server, origin string) (*websocket.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	h := http.Header{}
	h.Set("Origin", origin)
	h.Set("Flockdeck-Remote-Device", "dev-1")
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/ws/control",
		&websocket.DialOptions{HTTPHeader: h})
	if err == nil {
		conn.SetReadLimit(16 << 20)
	}
	return conn, err
}

// readRemoteMsg reads control messages until one of the given type arrives.
func readRemoteMsg(t *testing.T, conn *websocket.Conn, typ string) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("waiting for %q: %v", typ, err)
		}
		var msg map[string]any
		if json.Unmarshal(data, &msg) == nil && msg["type"] == typ {
			return msg
		}
	}
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A remote window is a window like any other — it gets the state and its
// commands are carried out — but it is not one that decides whether the
// application is still being looked at.
func TestRemoteControlSocket(t *testing.T) {
	srv, _ := newTestServer(t)
	gone := make(chan struct{}, 4)
	srv.OnLastClientGone = func() { gone <- struct{}{} }
	ts := remoteServer(t, srv)

	// Through the relay, the page and the socket share the relay's address,
	// and nothing else is let in — not even a page on somebody's localhost,
	// which the local window's socket does allow.
	host := strings.TrimPrefix(ts.URL, "http://")
	_, port, _ := net.SplitHostPort(host)
	for _, origin := range []string{"https://evil.example", "http://localhost:" + port + "1"} {
		if conn, err := dialRemoteControl(ts, origin); err == nil {
			conn.CloseNow()
			t.Errorf("a page from %s opened the control socket through the tunnel", origin)
		}
	}

	conn, err := dialRemoteControl(ts, ts.URL)
	if err != nil {
		t.Fatalf("dial the control socket through the tunnel: %v", err)
	}
	defer conn.CloseNow()
	readRemoteMsg(t, conn, "hello")
	readRemoteMsg(t, conn, "state")

	waitUntil(t, "the remote window to be counted", func() bool { return srv.ClientCount() == 1 })
	if n := srv.LocalClientCount(); n != 0 {
		t.Errorf("LocalClientCount = %d with only a remote window open, want 0", n)
	}

	// With no remote access configured the dialog is told so, rather than
	// left waiting.
	if err := conn.Write(context.Background(), websocket.MessageText, []byte(`{"cmd":"remoteDevices"}`)); err != nil {
		t.Fatal(err)
	}
	msg := readRemoteMsg(t, conn, "remoteDevices")
	if msg["enabled"] != false || msg["error"] != nil {
		t.Errorf("remoteDevices with remote access off = %v, want not enabled and no error", msg)
	}
	if msg["current"] != "dev-1" {
		t.Errorf("remoteDevices did not say which device this window is on: %v", msg)
	}

	conn.Close(websocket.StatusNormalClosure, "")
	waitUntil(t, "the remote window to go", func() bool { return srv.ClientCount() == 0 })
	select {
	case <-gone:
		t.Error("a remote window closing was taken for the last window going")
	case <-time.After(200 * time.Millisecond):
	}
}

// The snapshot carries the tunnel for an enrolled machine, and nothing at all
// for one that is not — so the window shows no trace of a feature nobody has
// turned on.
func TestSnapshotCarriesRemote(t *testing.T) {
	srv, _ := newTestServer(t)
	if v := srv.remoteSnapshot(); v != nil {
		t.Errorf("remoteSnapshot with no remote access = %+v, want nil", v)
	}
	srv.SetRemote(&fakeRemote{})
	if v := srv.remoteSnapshot(); v != nil {
		t.Errorf("remoteSnapshot for a machine not enrolled = %+v, want nil", v)
	}
	srv.SetRemote(&fakeRemote{ok: true, st: remote.Status{
		State: remote.StateConnected, Relay: "https://relay.example", HostID: "h1",
	}})
	v := srv.remoteSnapshot()
	if v == nil || v.State != "connected" || v.HostID != "h1" || v.RetryAt != nil {
		t.Errorf("remoteSnapshot = %+v, want connected as h1", v)
	}
}

// ServeRemote serves until its listener goes, which is how a tunnel ending
// ends what was being served on it.
func TestServeRemoteStopsWithItsListener(t *testing.T) {
	srv, _ := newTestServer(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- srv.ServeRemote(ln) }()

	resp, err := http.Get("http://" + ln.Addr().String() + "/")
	if err != nil {
		t.Fatalf("GET through ServeRemote: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET / through ServeRemote = %d, want 200", resp.StatusCode)
	}

	ln.Close()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, net.ErrClosed) {
			t.Errorf("ServeRemote ended with %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ServeRemote kept going after its listener closed")
	}
}
