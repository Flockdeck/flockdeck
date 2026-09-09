package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestProbeIdentifiesTheInstance covers what a second launch uses to decide
// whether to attach.
func TestProbeIdentifiesTheInstance(t *testing.T) {
	srv, _ := newTestServer(t)
	Version = "test-version"

	h, err := Probe(srv.BaseURL(), srv.Token())
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if h.App != "perch" {
		t.Errorf("app = %q", h.App)
	}
	if h.Version != "test-version" {
		t.Errorf("version = %q, want test-version", h.Version)
	}
	if h.Projects != 1 {
		t.Errorf("projects = %d, want 1", h.Projects)
	}
	if h.PID == 0 {
		t.Error("expected a process id")
	}
}

// TestProbeRejectsTheWrongToken keeps another local process from discovering or
// driving the instance.
func TestProbeRejectsTheWrongToken(t *testing.T) {
	srv, _ := newTestServer(t)
	if _, err := Probe(srv.BaseURL(), "not-the-token"); err == nil {
		t.Error("expected a probe with the wrong token to fail")
	}
}

// TestProbeFailsWhenNothingIsListening is the stale-record case: a process that
// crashed leaves its record behind.
func TestProbeFailsWhenNothingIsListening(t *testing.T) {
	// Port 1 is not going to be serving this.
	if _, err := Probe("http://127.0.0.1:1", "token"); err == nil {
		t.Error("expected a probe against a dead address to fail")
	}
}

// TestRequestOpenAddsAProject covers `perch -C dir` attaching to a
// running instance and handing it the directory.
func TestRequestOpenAddsAProject(t *testing.T) {
	srv, ws := newTestServer(t)
	other := t.TempDir()

	if err := RequestOpen(srv.BaseURL(), srv.Token(), other); err != nil {
		t.Fatalf("request open: %v", err)
	}
	if got := len(ws.Projects()); got != 2 {
		t.Fatalf("projects = %d, want 2", got)
	}
	if ws.ActiveRoot() != other {
		t.Errorf("active root = %q, want the newly opened %q", ws.ActiveRoot(), other)
	}
}

// TestRequestOpenRejectsBadPaths covers the error path.
func TestRequestOpenRejectsBadPaths(t *testing.T) {
	srv, _ := newTestServer(t)
	if err := RequestOpen(srv.BaseURL(), srv.Token(), t.TempDir()+"/missing"); err == nil {
		t.Error("expected opening a missing directory to fail")
	}
	if err := RequestOpen(srv.BaseURL(), "wrong-token", t.TempDir()); err == nil {
		t.Error("expected a bad token to be rejected")
	}
}

// TestQuitEndpointStopsTheApplication covers `perch -quit`.
func TestQuitEndpointStopsTheApplication(t *testing.T) {
	srv, _ := newTestServer(t)
	stopped := make(chan struct{})
	srv.OnQuit = func() { close(stopped) }

	if err := RequestQuit(srv.BaseURL(), srv.Token()); err != nil {
		t.Fatalf("request quit: %v", err)
	}
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("quit was not acted on")
	}
}

// TestQuitNeedsTheToken stops any local process from killing the agents.
func TestQuitNeedsTheToken(t *testing.T) {
	srv, _ := newTestServer(t)
	srv.OnQuit = func() { t.Error("quit should not have been acted on") }

	resp, err := http.Post(srv.BaseURL()+"/quit?t=nope", "", nil)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
	time.Sleep(200 * time.Millisecond)
}

// TestDetachKeepsTheApplicationAlive covers the flag that lets agents outlive
// the window.
func TestDetachKeepsTheApplicationAlive(t *testing.T) {
	srv, _ := newTestServer(t)
	if srv.Detached() {
		t.Fatal("a fresh server should not be detached")
	}
	srv.Detach()
	if !srv.Detached() {
		t.Error("Detach did not take effect")
	}
	srv.Attach()
	if srv.Detached() {
		t.Error("Attach should reverse a detach")
	}
}

// TestDetachCommandFromTheWindow covers the palette action.
func TestDetachCommandFromTheWindow(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	nextState(t, conn, nil)

	sendCmd(t, conn, command{Cmd: "detach"})

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if srv.Detached() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Error("the detach command did not take effect")
}

// TestProbeFailsWhileTheWorkspaceIsStuck covers the case that matters to a
// second launch: the port answers, but the instance behind it cannot open
// anything. The probe has to come back as a failure so the launch starts its
// own instance rather than handing its directory to a wedged one.
func TestProbeFailsWhileTheWorkspaceIsStuck(t *testing.T) {
	srv, _ := newTestServer(t)

	// Occupy the workspace goroutine for longer than a probe will wait.
	release := make(chan struct{})
	defer close(release)
	srv.do(func() { <-release })

	start := time.Now()
	if _, err := Probe(srv.BaseURL(), srv.Token()); err == nil {
		t.Fatal("expected the probe to fail while the workspace is stuck")
	}
	if elapsed := time.Since(start); elapsed > probeTimeout {
		t.Errorf("probe took %v, want an answer within %v", elapsed, probeTimeout)
	}
}

// TestActingEndpointsNeedAPost covers the two endpoints that do something
// rather than report something. A GET carrying the token — a link followed by
// accident, an address filled in from history — must not open a project or
// stop the agents.
func TestActingEndpointsNeedAPost(t *testing.T) {
	srv, ws := newTestServer(t)
	srv.OnQuit = func() { t.Error("a GET should not have stopped the application") }

	before := len(ws.Projects())
	urls := []string{
		srv.BaseURL() + "/quit?t=" + srv.Token(),
		srv.BaseURL() + "/open?t=" + srv.Token() + "&path=" + queryEscape(t.TempDir()),
	}
	for _, url := range urls {
		resp, err := http.Get(url)
		if err != nil {
			t.Fatalf("GET %s: %v", url, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("GET %s = %d, want 405", url, resp.StatusCode)
		}
		if allow := resp.Header.Get("Allow"); allow != http.MethodPost {
			t.Errorf("GET %s Allow = %q, want POST", url, allow)
		}
	}
	if n := len(ws.Projects()); n != before {
		t.Errorf("projects = %d, want %d — a GET opened one", n, before)
	}
}

// TestQuitAnswersBeforeItActs covers `perch -quit`. Acting on the request ends
// the process, so the reply has to have left the connection first; otherwise
// the command reports a failure for a shutdown that worked.
func TestQuitAnswersBeforeItActs(t *testing.T) {
	srv, _ := newTestServer(t)

	rec := httptest.NewRecorder()
	flushed := make(chan bool, 1)
	srv.OnQuit = func() { flushed <- rec.Flushed }

	srv.handleQuit(rec, httptest.NewRequest(http.MethodPost, "/quit?t="+srv.Token(), nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	select {
	case ok := <-flushed:
		if !ok {
			t.Error("the shutdown started before the reply was flushed")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("quit was not acted on")
	}
}
