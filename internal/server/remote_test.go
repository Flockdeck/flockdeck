package server

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/jmwri/flockdeck/internal/e2e"
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
	// enabled and disabled are what the dialog asked for; enableErr is what
	// enrolling is refused with, and untoldErr why the relay cannot be told
	// of a disable.
	enabled   []remote.EnableRequest
	disabled  []bool
	enableErr error
	untoldErr error
	// renamed is each name this machine was given, and renameErr what
	// renaming it is refused with.
	renamed   []string
	renameErr error
	// moved is each move the dialog asked for; moveErr is what moving is
	// refused with, and moveUntold why the old relay could not be told.
	moved      []remote.EnableRequest
	moveErr    error
	moveUntold error
	// e2eCapable is what E2ECapable answers, keyed by device id; a device
	// not in the map is not capable, the same as a real Manager's answer for
	// one that has not registered a key.
	e2eCapable map[string]bool
}

// E2ECapable and E2ERespond stand in for a real Manager's end-to-end
// handshake: no test in this file drives a real handshake through a fake
// remote, so E2ECapable simply reports what the test set up and E2ERespond
// is never expected to be called when it did not.
func (f *fakeRemote) E2ECapable(_ context.Context, deviceID string, _ remote.KeyOrigin) bool {
	return f.e2eCapable[deviceID]
}

func (f *fakeRemote) E2ERespond(_ context.Context, _ string, _ remote.KeyOrigin, _ []byte) (*e2e.Session, []byte, error) {
	return nil, nil, remote.ErrNoE2EKey
}

// E2EPublicKey stands in for this host's own end-to-end public key: no test
// in this file reads it for its value, so an empty string -- the same as a
// host with no identity yet -- is enough.
func (f *fakeRemote) E2EPublicKey() string { return "" }

func (f *fakeRemote) Move(_ context.Context, req remote.EnableRequest) (error, error) {
	f.moved = append(f.moved, req)
	if f.moveErr != nil {
		return nil, f.moveErr
	}
	return f.moveUntold, nil
}

func (f *fakeRemote) Rename(_ context.Context, name string) error {
	f.renamed = append(f.renamed, name)
	return f.renameErr
}

func (f *fakeRemote) Status() (remote.Status, bool)   { return f.st, f.ok }
func (f *fakeRemote) Client() (*remote.Client, error) { return nil, remote.ErrNotEnabled }
func (f *fakeRemote) Reload() error                   { f.reloads.Add(1); return f.err }
func (f *fakeRemote) Reconnect() error                { return f.err }

func (f *fakeRemote) Enable(_ context.Context, req remote.EnableRequest) (bool, error) {
	f.enabled = append(f.enabled, req)
	return false, f.enableErr
}

func (f *fakeRemote) Disable(_ context.Context, force bool) (error, error) {
	f.disabled = append(f.disabled, force)
	if f.untoldErr != nil && !force {
		return nil, &remote.RelayUntoldError{Err: f.untoldErr}
	}
	return f.untoldErr, nil
}

// TestTheDialogTurnsRemoteAccessOnAndOff covers enrolling and leaving from the
// Remote access dialog rather than a terminal: what the form was filled in
// with reaches remote access as it was typed, a refusal comes back for the
// window to show, and a relay that cannot be told of a disable is not taken
// for one that was, until the window says to forget it anyway.
func TestTheDialogTurnsRemoteAccessOnAndOff(t *testing.T) {
	srv, _ := newTestServer(t)
	fake := &fakeRemote{enableErr: errors.New("the join code is not valid")}
	srv.SetRemote(fake)
	conn := dialControl(t, srv)
	// Each answer is read into an empty one: the fields an answer leaves out
	// are its own, not whatever the last answer said.
	type outcome struct {
		Type, Action, Error, Warning string
		Untold                       bool
	}
	var out outcome
	read := func() {
		t.Helper()
		out = outcome{}
		readUntil(t, conn, "remoteOutcome", &out)
	}

	sendCmd(t, conn, command{Cmd: "remoteEnable", Relay: "relay.example", Name: "desk", Join: "fdj_x"})
	read()
	if out.Action != "enable" || out.Error != "the join code is not valid" {
		t.Errorf("a refused enrolment was answered %+v", out)
	}
	if len(fake.enabled) != 1 || fake.enabled[0] != (remote.EnableRequest{Relay: "relay.example", Name: "desk", Join: "fdj_x"}) {
		t.Errorf("remote access was asked to enrol with %+v", fake.enabled)
	}

	fake.untoldErr = errors.New("dial tcp: no route to host")
	sendCmd(t, conn, command{Cmd: "remoteDisable"})
	read()
	if !out.Untold || out.Error == "" {
		t.Errorf("a disable the relay never heard of was answered %+v, want it offered again", out)
	}
	sendCmd(t, conn, command{Cmd: "remoteDisable", Force: true})
	read()
	if out.Untold || out.Error != "" || !strings.Contains(out.Warning, "no route to host") {
		t.Errorf("forgetting the enrolment anyway was answered %+v, want a warning saying why", out)
	}
	if len(fake.disabled) != 2 || fake.disabled[0] || !fake.disabled[1] {
		t.Errorf("remote access was asked to disable with force %v", fake.disabled)
	}
}

// TestTheDialogMovesToAnotherRelay covers moving this machine from the Remote
// access dialog: what the form was filled in with reaches remote access as it
// was typed, a refusal comes back for the window to show under the form, and
// an old relay that could not be told is a warning on a move that went ahead,
// with every device told to pair again.
func TestTheDialogMovesToAnotherRelay(t *testing.T) {
	srv, _ := newTestServer(t)
	fake := &fakeRemote{moveErr: errors.New("this relay needs an invite code")}
	srv.SetRemote(fake)
	conn := dialControl(t, srv)
	type outcome struct {
		Type, Action, Error, Warning string
	}
	var out outcome
	read := func() {
		t.Helper()
		out = outcome{}
		readUntil(t, conn, "remoteOutcome", &out)
	}

	sendCmd(t, conn, command{Cmd: "remoteMove", Relay: "relay.company.example", Invite: "fdi_x"})
	read()
	if out.Action != "move" || out.Error != "this relay needs an invite code" {
		t.Errorf("a refused move was answered %+v", out)
	}
	if len(fake.moved) != 1 || fake.moved[0] != (remote.EnableRequest{Relay: "relay.company.example", Invite: "fdi_x"}) {
		t.Errorf("remote access was asked to move with %+v", fake.moved)
	}

	fake.moveErr, fake.moveUntold = nil, errors.New("dial tcp: no route to host")
	sendCmd(t, conn, command{Cmd: "remoteMove", Relay: "relay.company.example", Invite: "fdi_y"})
	var note noticeMsg
	readUntil(t, conn, "notice", &note)
	if note.Error || !strings.Contains(note.Text, "pair each device again") {
		t.Errorf("a move was announced as %+v, want every device told to pair again", note)
	}
	read()
	if out.Error != "" || !strings.Contains(out.Warning, "no route to host") || !strings.Contains(out.Warning, "old relay") {
		t.Errorf("a move the old relay never heard of was answered %+v, want a warning saying why", out)
	}
}

// TestTheDialogRenamesThisMachine covers renaming from the Remote access
// dialog: this machine through remote access, so that the name is saved and
// shown here too, trimmed as the relay keeps it; a name that could not be one,
// or a device not named, is refused before anything is asked.
func TestTheDialogRenamesThisMachine(t *testing.T) {
	srv, _ := newTestServer(t)
	fake := &fakeRemote{}
	srv.SetRemote(fake)
	conn := dialControl(t, srv)
	var note noticeMsg
	read := func() {
		t.Helper()
		note = noticeMsg{}
		readUntil(t, conn, "notice", &note)
	}

	sendCmd(t, conn, command{Cmd: "remoteRename", Kind: "host", Name: "  Work PC "})
	read()
	if note.Error || note.Text != "this machine is now called “Work PC”" {
		t.Errorf("renaming this machine was answered %+v", note)
	}
	if len(fake.renamed) != 1 || fake.renamed[0] != "Work PC" {
		t.Errorf("remote access was asked to rename this machine to %q", fake.renamed)
	}
	for _, name := range []string{" ", "fdp_code"} {
		sendCmd(t, conn, command{Cmd: "remoteRename", Kind: "host", Name: name})
		read()
		if !note.Error || !strings.HasPrefix(note.Text, "could not rename it: ") {
			t.Errorf("renaming this machine to %q was answered %+v, want it refused", name, note)
		}
	}
	sendCmd(t, conn, command{Cmd: "remoteRename", Kind: "device", Name: "phone"})
	read()
	if !note.Error || note.Text != "no device was named" {
		t.Errorf("renaming a device without its id was answered %+v", note)
	}
	if len(fake.renamed) != 1 {
		t.Errorf("a refused rename reached remote access: %q", fake.renamed)
	}
}

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

// A window reached through the relay has no cookie of this server's own to
// ride on -- the tunnel is what authorised it -- so frame-ancestors 'self'
// would say nothing legitimately framed it, which is true only by accident:
// unlike the local window, nothing here is a defence against a page on
// another local port. The policy says outright that nothing may frame it.
func TestRemoteIndexRefusesFraming(t *testing.T) {
	srv, _ := newTestServer(t)
	ts := remoteServer(t, srv)
	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("GET index through the tunnel: %v", err)
	}
	resp.Body.Close()
	if csp := resp.Header.Get("Content-Security-Policy"); csp != "frame-ancestors 'none'" {
		t.Errorf("index through the tunnel Content-Security-Policy = %q, want frame-ancestors 'none'", csp)
	}
}

// A phone reaches the front end only through the tunnel, and keeps a browser
// profile of its own between one desktop version and the next. Locally that is
// covered by TestIndexSetsCookieAndServesAssets; this is the same guarantee
// for the path a phone actually takes, so an upgrade cannot leave a relay
// window running an older page or script than the desktop it is talking to.
func TestRemoteAssetsAreNotCached(t *testing.T) {
	srv, _ := newTestServer(t)
	ts := remoteServer(t, srv)
	for _, path := range []string{"/", "/assets/app.js", "/assets/app.css", "/assets/vendor/xterm.js", "/assets/icon-32.png"} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s through the tunnel = %d, want 200", path, resp.StatusCode)
		}
		if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
			t.Errorf("GET %s through the tunnel Cache-Control = %q, want no-store", path, cc)
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

// TestRemoteTerminalsAreSentCompressed covers the terminal sockets of a window
// through the relay, one per pane, whose output deflates to about a third. A
// window on this machine is sent it as it is.
func TestRemoteTerminalsAreSentCompressed(t *testing.T) {
	srv, ws := newTestServer(t)
	ts := remoteServer(t, srv)
	pane, _ := ask(srv, func() string { return ws.CurrentTab().Focus })
	dial := func(url, origin string) *http.Response {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		h := http.Header{}
		h.Set("Origin", origin)
		conn, resp, err := websocket.Dial(ctx, url,
			&websocket.DialOptions{HTTPHeader: h, CompressionMode: websocket.CompressionNoContextTakeover})
		if err != nil {
			t.Fatalf("dial %s: %v", url, err)
		}
		t.Cleanup(func() { conn.CloseNow() })
		return resp
	}

	resp := dial("ws"+strings.TrimPrefix(ts.URL, "http")+"/ws/pty?id="+pane, ts.URL)
	if ext := resp.Header.Get("Sec-WebSocket-Extensions"); !strings.Contains(ext, "permessage-deflate") {
		t.Errorf("a terminal through the relay was not offered compression: %q", ext)
	}
	resp = dial("ws://"+srv.Addr()+"/ws/pty?t="+srv.Token()+"&id="+pane, "http://"+srv.Addr())
	if ext := resp.Header.Get("Sec-WebSocket-Extensions"); ext != "" {
		t.Errorf("a terminal on this machine was compressed: %q", ext)
	}
}

// TestRemoteDetachLeavesTheDeskAlone covers Detach pressed on a phone. The
// phone's window closing never stopped anything, so there is nothing for it to
// detach -- and setting the instance detached would stop the window on the
// desk from quitting when it is closed, without anyone at the desk knowing.
func TestRemoteDetachLeavesTheDeskAlone(t *testing.T) {
	srv, _ := newTestServer(t)
	ts := remoteServer(t, srv)

	remoteConn, err := dialRemoteControl(ts, ts.URL)
	if err != nil {
		t.Fatalf("dial through the tunnel: %v", err)
	}
	defer remoteConn.CloseNow()
	sendCmd(t, remoteConn, command{Cmd: "detach"})
	// Told why rather than told it has been detached: a phone's tab cannot
	// close itself, and one that believes it is closing stops reconnecting.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for {
		_, data, err := remoteConn.Read(ctx)
		if err != nil {
			t.Fatalf("waiting for the answer to detach: %v", err)
		}
		var msg noticeMsg
		if json.Unmarshal(data, &msg) != nil {
			continue
		}
		if msg.Type == "detached" {
			t.Fatal("a window through the relay was told it had been detached")
		}
		if msg.Type == "notice" {
			if msg.Error {
				t.Errorf("detach through the relay was answered with an error: %q", msg.Text)
			}
			break
		}
	}
	if srv.Detached() {
		t.Error("a window through the relay detached the instance on the desk")
	}

	local := dialControl(t, srv)
	sendCmd(t, local, command{Cmd: "detach"})
	readUntil(t, local, "detached", &struct{}{})
	if !srv.Detached() {
		t.Error("the window on the desk could not detach")
	}
}

// TestRemoteAssetsAreSentCompressed covers the front end fetched through the
// relay, which is fetched again on every page load: a megabyte of script, most
// of it text that compresses to a fraction. A window on this machine is sent it
// as it is.
func TestRemoteAssetsAreSentCompressed(t *testing.T) {
	srv, _ := newTestServer(t)
	ts := remoteServer(t, srv)
	// A client that leaves the body as it came, as a proxy in the middle does.
	client := &http.Client{Transport: &http.Transport{DisableCompression: true}}
	get := func(url string) (*http.Response, []byte) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, url, nil)
		req.Header.Set("Accept-Encoding", "gzip, deflate, br")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", url, err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp, body
	}

	resp, packed := get(ts.URL + "/assets/app.js")
	if resp.Header.Get("Content-Encoding") != "gzip" {
		t.Fatalf("app.js through the relay came as %q, want gzip", resp.Header.Get("Content-Encoding"))
	}
	zr, err := gzip.NewReader(bytes.NewReader(packed))
	if err != nil {
		t.Fatal(err)
	}
	plain, _ := io.ReadAll(zr)

	resp, local := get(srv.baseURL() + "/assets/app.js?t=" + srv.Token())
	if enc := resp.Header.Get("Content-Encoding"); enc != "" {
		t.Errorf("app.js on this machine came as %q", enc)
	}
	if !bytes.Equal(plain, local) {
		t.Fatalf("the compressed app.js is not the file: %d bytes against %d", len(plain), len(local))
	}
	if 2*len(packed) > len(plain) {
		t.Errorf("app.js compressed to %d of %d bytes", len(packed), len(plain))
	}

	// The help pages, which the first window on a phone opens unasked.
	if resp, _ := get(ts.URL + "/help.json"); resp.Header.Get("Content-Encoding") != "gzip" {
		t.Errorf("help.json through the relay came as %q, want gzip", resp.Header.Get("Content-Encoding"))
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
