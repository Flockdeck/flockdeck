package server

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestProbeCountsTheWindowsOnThisMachine covers a second launch deciding
// whether it needs to open a window at all. Each launch used to open another,
// so bringing back a window lost behind others added a duplicate every time.
// A window through the relay is somebody elsewhere and is not counted.
func TestProbeCountsTheWindowsOnThisMachine(t *testing.T) {
	srv, _ := newTestServer(t)
	ts := remoteServer(t, srv)
	remote, err := dialRemoteControl(ts, ts.URL)
	if err != nil {
		t.Fatalf("dial through the tunnel: %v", err)
	}
	defer remote.CloseNow()
	readRemoteMsg(t, remote, "state")

	if h, err := Probe(srv.BaseURL(), srv.Token()); err != nil || h.Windows != 0 {
		t.Fatalf("with only a relayed window open, probe = %+v, %v; want 0 windows", h, err)
	}
	nextState(t, dialControl(t, srv), nil)
	if h, err := Probe(srv.BaseURL(), srv.Token()); err != nil || h.Windows != 1 {
		t.Fatalf("with a window open on this machine, probe = %+v, %v; want 1 window", h, err)
	}
}

// TestRequestOpenSaysWhyItFailed covers `flockdeck -C` handing the running
// instance a project it will not open. The instance says why, and the launch
// used to print "open project: 400 Bad Request" in place of it.
func TestRequestOpenSaysWhyItFailed(t *testing.T) {
	srv, ws := newTestServer(t)
	missing := filepath.Join(t.TempDir(), "gone")

	why := make(chan error, 1)
	srv.do(func() { why <- ws.OpenProject(missing) })
	want := <-why
	if want == nil {
		t.Fatal("the workspace opened a directory that is not there")
	}

	err := RequestOpen(srv.BaseURL(), srv.Token(), missing)
	if err == nil || !strings.Contains(err.Error(), want.Error()) {
		t.Fatalf("RequestOpen = %v, want it to say %q", err, want)
	}
}

// TestProbeIdentifiesTheInstance covers what a second launch uses to decide
// whether to attach.
func TestProbeIdentifiesTheInstance(t *testing.T) {
	srv, _ := newTestServer(t)
	Version = "test-version"

	h, err := Probe(srv.BaseURL(), srv.Token())
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if h.App != "flockdeck" {
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

// TestRequestOpenAddsAProject covers `flockdeck -C dir` attaching to a
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

// TestQuitEndpointStopsTheApplication covers `flockdeck -quit`. Stopping is what
// the real OnQuit does, and the request does not report success until it has
// happened, so the stand-in has to do it too.
func TestQuitEndpointStopsTheApplication(t *testing.T) {
	srv, _ := newTestServer(t)
	stopped := make(chan struct{})
	srv.OnQuit = func() { close(stopped); _ = srv.Close() }

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
	defer func(g, r time.Duration) { busyGrace, busyRetry = g, r }(busyGrace, busyRetry)
	busyGrace, busyRetry = 600*time.Millisecond, 100*time.Millisecond

	srv, _ := newTestServer(t)

	// Occupy the workspace goroutine for longer than a probe will wait.
	release := make(chan struct{})
	defer close(release)
	srv.do(func() { <-release })

	start := time.Now()
	if _, err := Probe(srv.BaseURL(), srv.Token()); err == nil {
		t.Fatal("expected the probe to fail while the workspace is stuck")
	}
	// The probe waits a busy instance out rather than writing it off, so the
	// bound is that grace and not a single request.
	if elapsed := time.Since(start); elapsed > busyGrace+probeTimeout {
		t.Errorf("probe took %v, want an answer within %v", elapsed, busyGrace+probeTimeout)
	}
}

// TestProbeWaitsOutABusyInstance is the other side of it. Opening a project
// and starting the agents in it runs on the workspace goroutine, so an
// instance can easily be unable to answer for a second while it does exactly
// what a previous launch asked of it. Writing it off then is the expensive
// mistake: the record is cleared and a second set of agents is started
// alongside the first, in a second window, with no way back to one.
func TestProbeWaitsOutABusyInstance(t *testing.T) {
	srv, _ := newTestServer(t)

	release := make(chan struct{})
	srv.do(func() { <-release })
	go func() {
		time.Sleep(healthTimeout + 500*time.Millisecond)
		close(release)
	}()

	h, err := Probe(srv.BaseURL(), srv.Token())
	if err != nil {
		t.Fatalf("probe gave up on a busy instance: %v", err)
	}
	if !h.Ready || h.Projects != 1 {
		t.Errorf("probe reported ready=%v projects=%d, want true and 1", h.Ready, h.Projects)
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

// TestQuitAnswersBeforeItActs covers `flockdeck -quit`. Acting on the request ends
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

// TestActingEndpointsRefuseTheCookieAlone covers the way into these endpoints
// that a page actually has.
//
// The token is held in a cookie; cookies are scoped to a host and ignore the
// port, and a different port is the same site, so a page served from anything
// else on 127.0.0.1 has this instance's token attached to a request it makes
// here whatever SameSite says. Nothing else stands in the way of a plain
// cross-origin POST, and one of these stops every agent the user is running.
// The token has to be in the request itself, which such a page cannot know.
func TestActingEndpointsRefuseTheCookieAlone(t *testing.T) {
	srv, ws := newTestServer(t)
	srv.OnQuit = func() { t.Error("a page was allowed to stop the application") }

	before := len(ws.Projects())
	posts := []string{
		srv.BaseURL() + "/quit",
		srv.BaseURL() + "/open?path=" + queryEscape(t.TempDir()),
	}
	for _, url := range posts {
		resp := sendWithCookie(t, http.MethodPost, url, srv)
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("POST %s with only the cookie = %d, want 403", url, resp.StatusCode)
		}
	}
	if n := len(ws.Projects()); n != before {
		t.Errorf("projects = %d, want %d — a page opened one", n, before)
	}

	// Reporting on the instance is not for a page either: the reply names the
	// process and what it has open.
	resp := sendWithCookie(t, http.MethodGet, srv.BaseURL()+"/health", srv)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("GET /health with only the cookie = %d, want 403", resp.StatusCode)
	}

	// The launching binary carries the token in the URL and must still work.
	if _, err := Probe(srv.BaseURL(), srv.Token()); err != nil {
		t.Errorf("the launching binary was refused: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
}

// sendWithCookie makes the request a page in a browser would: no token of its
// own, and the cookie attached for it.
func sendWithCookie(t *testing.T, method, url string, srv *Server) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.AddCookie(&http.Cookie{Name: srv.cookieName(), Value: srv.Token()})
	req.Header.Set("Origin", "http://127.0.0.1:9999")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	resp.Body.Close()
	return resp
}

// TestProbeSurvivesABackedUpWorkspace is the case the busy-instance grace did
// not reach on its own. Opening a project holds the workspace goroutine and
// fills the queue in front of it, and while that queue is full nothing can
// hand work over at all -- including the health endpoint, whose whole job is
// to answer within a moment. Without an answer the launch has nothing to wait
// for: it declares the record stale and starts a rival set of agents.
func TestProbeSurvivesABackedUpWorkspace(t *testing.T) {
	srv, _ := newTestServer(t)

	release := make(chan struct{})
	var backlog sync.WaitGroup
	defer backlog.Wait()
	stop := sync.OnceFunc(func() { close(release) })
	defer stop()

	srv.do(func() { <-release })
	for range cap(srv.cmds) * 2 {
		backlog.Add(1)
		go func() { defer backlog.Done(); srv.do(func() {}) }()
	}
	deadline := time.Now().Add(10 * time.Second)
	for len(srv.cmds) < cap(srv.cmds) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if len(srv.cmds) < cap(srv.cmds) {
		t.Fatalf("could not fill the workspace queue: %d of %d", len(srv.cmds), cap(srv.cmds))
	}

	// Held for longer than one probe waits, and freed well inside the grace a
	// probe gives a busy instance.
	go func() {
		time.Sleep(probeTimeout + 500*time.Millisecond)
		stop()
	}()

	if _, err := Probe(srv.BaseURL(), srv.Token()); err != nil {
		t.Errorf("probe wrote off an instance that was only busy: %v", err)
	}
}

// TestOpenIsAcceptedWhileTheWorkspaceIsSlow covers `flockdeck -C dir` against an
// instance that is busy. Opening a project can only fail on the directory, and
// the launch has already checked that; taking longer than the wait is not a
// failure, and reporting one refused to show a window onto a project that was
// about to open anyway.
func TestOpenIsAcceptedWhileTheWorkspaceIsSlow(t *testing.T) {
	defer func(d time.Duration) { openTimeout = d }(openTimeout)
	openTimeout = 300 * time.Millisecond

	srv, ws := newTestServer(t)
	other := t.TempDir()

	release := make(chan struct{})
	stop := sync.OnceFunc(func() { close(release) })
	defer stop()
	srv.do(func() { <-release })

	// Queued behind the occupied goroutine, so no answer arrives in time.
	if err := RequestOpen(srv.BaseURL(), srv.Token(), other); err != nil {
		t.Fatalf("open was reported as a failure: %v", err)
	}

	stop()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if len(ws.Projects()) == 2 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("projects = %d, want the accepted one to have opened", len(ws.Projects()))
}

// TestOpenSaysSoWhenItCannotBeTakenAtAll separates the two ways a busy
// instance can be busy. A request that never reached the workspace has not
// been accepted, and telling the launch it has would have it open a window
// onto a project that is never going to appear.
func TestOpenSaysSoWhenItCannotBeTakenAtAll(t *testing.T) {
	defer func(d time.Duration) { openTimeout = d }(openTimeout)
	openTimeout = 300 * time.Millisecond

	srv, ws := newTestServer(t)
	before := len(ws.Projects())

	release := make(chan struct{})
	var backlog sync.WaitGroup
	defer backlog.Wait()
	defer close(release)
	srv.do(func() { <-release })
	for range cap(srv.cmds) * 2 {
		backlog.Add(1)
		go func() { defer backlog.Done(); srv.do(func() {}) }()
	}
	deadline := time.Now().Add(10 * time.Second)
	for len(srv.cmds) < cap(srv.cmds) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if len(srv.cmds) < cap(srv.cmds) {
		t.Fatalf("could not fill the workspace queue: %d of %d", len(srv.cmds), cap(srv.cmds))
	}

	start := time.Now()
	if err := RequestOpen(srv.BaseURL(), srv.Token(), t.TempDir()); err == nil {
		t.Error("expected an instance too busy to take the request to say so")
	}
	// The answer has to come from the instance, inside its own budget, rather
	// than from the launch giving up on its side with nothing to report.
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("the launch waited %v for an answer, want one within the instance's budget", elapsed)
	}
	if n := len(ws.Projects()); n != before {
		t.Errorf("projects = %d, want %d", n, before)
	}
}
