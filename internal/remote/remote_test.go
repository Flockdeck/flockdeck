package remote

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/xtaci/smux"
)

// isolate points the state directory at one of this test's own.
func isolate(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)
	return dir
}

// quick makes the retrying fast enough to watch.
func quick(t *testing.T) {
	t.Helper()
	oldMin, oldMax, oldClose := backoffMin, backoffMax, closeWait
	backoffMin, backoffMax, closeWait = 10*time.Millisecond, 40*time.Millisecond, 100*time.Millisecond
	t.Cleanup(func() { backoffMin, backoffMax, closeWait = oldMin, oldMax, oldClose })
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestConfigRoundTrip(t *testing.T) {
	isolate(t)
	if c, err := Load(); c != nil || err != nil {
		t.Fatalf("Load with nothing saved = %v, %v; want nil, nil", c, err)
	}
	want := Config{Relay: "https://relay.example", HostID: "h1", AccountID: "a1", Token: "fdh_x", Name: "desk"}
	if err := want.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load()
	if err != nil || got == nil || *got != want {
		t.Fatalf("Load = %+v, %v; want %+v", got, err, want)
	}
	if err := Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if c, err := Load(); c != nil || err != nil {
		t.Errorf("Load after Clear = %v, %v; want nil, nil", c, err)
	}
	if err := Clear(); err != nil {
		t.Errorf("Clear with nothing to clear: %v", err)
	}
}

// A file that cannot be read is not the same as not being enrolled: saying
// "not enabled" would invite enrolling a second time over a live host.
func TestLoadRefusesABrokenFile(t *testing.T) {
	isolate(t)
	p, err := path()
	if err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{"{not json", `{"relay":"https://relay.example"}`} {
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if c, err := Load(); err == nil {
			t.Errorf("Load(%q) = %+v with no error", content, c)
		} else if !strings.Contains(err.Error(), filepath.Base(p)) {
			t.Errorf("Load(%q) error %q does not name the file", content, err)
		}
	}
}

func TestRelayURL(t *testing.T) {
	t.Setenv(RelayEnv, "")
	if got, err := RelayURL(""); err != nil || got != DefaultRelay {
		t.Errorf("RelayURL(\"\") = %q, %v; want the default", got, err)
	}
	t.Setenv(RelayEnv, "https://mine.example/")
	if got, err := RelayURL(""); err != nil || got != "https://mine.example" {
		t.Errorf("RelayURL from the environment = %q, %v", got, err)
	}
	if got, err := RelayURL("https://named.example"); err != nil || got != "https://named.example" {
		t.Errorf("a named relay should beat the environment: %q, %v", got, err)
	}
	// A bad address set in the environment, perhaps long ago, is refused
	// saying that is where it came from.
	t.Setenv(RelayEnv, "http://relay.example")
	if _, err := RelayURL(""); err == nil || !strings.Contains(err.Error(), RelayEnv) {
		t.Errorf("a bad relay from the environment = %v, want it to name %s", err, RelayEnv)
	}
	t.Setenv(RelayEnv, "https://mine.example/")

	for raw, ok := range map[string]bool{
		"https://relay.example":       true,
		"https://relay.example/base/": true,
		"http://127.0.0.1:8080":       true,
		"http://localhost:9":          true,
		"http://[::1]:9":              true,
		"http://relay.example":        false, // the token would cross the network in the clear
		"ftp://relay.example":         false,
		"relay.example":               true, // typed as addresses usually are
		"relay.example:8443":          true,
		"https://relay.example/?x=1":  false,
		"https://relay.example/#frag": false,
	} {
		_, err := CheckRelay(raw)
		if (err == nil) != ok {
			t.Errorf("CheckRelay(%q) error = %v, want ok=%v", raw, err, ok)
		}
	}
	if got, _ := CheckRelay("https://relay.example/base/"); got != "https://relay.example/base" {
		t.Errorf("CheckRelay kept the trailing slash: %q", got)
	}
	if got, _ := CheckRelay("relay.example.com:8443"); got != "https://relay.example.com:8443" {
		t.Errorf("CheckRelay of an address with no scheme = %q, want it taken as https://", got)
	}
	// One relay is one address however it is typed, or naming the relay this
	// machine is on would read as asking to move to another.
	for raw, want := range map[string]string{
		"https://Remote.Flockdeck.AI:443/": "https://remote.flockdeck.ai",
		"http://LOCALHOST:80":              "http://localhost",
		"https://relay.example:8443":       "https://relay.example:8443",
	} {
		if got, err := CheckRelay(raw); err != nil || got != want {
			t.Errorf("CheckRelay(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
}

// fakeRelay is enough of a relay to carry requests down a tunnel.
type fakeRelay struct {
	*httptest.Server
	token string

	mu sync.Mutex
	// refuse, when set, is the status the tunnel endpoint answers with.
	refuse int
	// closeWith, when set, closes each tunnel as soon as it is open.
	closeWith websocket.StatusCode
	connects  int
	version   string

	sessions chan *smux.Session
	calls    []string
}

func newFakeRelay(t *testing.T) *fakeRelay {
	t.Helper()
	f := &fakeRelay{token: "fdh_test", sessions: make(chan *smux.Session, 8)}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/host/connect", f.connect)
	mux.HandleFunc("/", f.api)
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	return f
}

func (f *fakeRelay) connect(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.connects++
	f.version = r.Header.Get("Flockdeck-Version")
	refuse, closeWith := f.refuse, f.closeWith
	f.mu.Unlock()
	if refuse != 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(refuse)
		_, _ = io.WriteString(w, `{"error":"this host was removed"}`)
		return
	}
	if r.Header.Get("Authorization") != "Bearer "+f.token {
		http.Error(w, `{"error":"bad token"}`, http.StatusUnauthorized)
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{Subprotocol}})
	if err != nil {
		return
	}
	if closeWith != 0 {
		_ = conn.Close(closeWith, "because the test said so")
		return
	}
	nc := websocket.NetConn(context.Background(), conn, websocket.MessageBinary)
	sess, err := smux.Client(nc, smuxConfig())
	if err != nil {
		conn.CloseNow()
		return
	}
	f.sessions <- sess
	// smux reports a failed connection through Accept rather than by closing
	// the session, so this is how the relay notices the host going.
	for {
		if _, err := sess.AcceptStream(); err != nil {
			break
		}
	}
	sess.Close()
}

func (f *fakeRelay) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.connects
}

// api answers the REST calls, recording each.
func (f *fakeRelay) api(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.calls = append(f.calls, r.Method+" "+r.URL.Path)
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path == "/api/v1/hosts" && r.Method == http.MethodPost {
		var req RegisterRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Name == "" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":"a name is required"}`)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"hostId":"h1","accountId":"a1","token":"fdh_test"}`)
		return
	}
	if r.Header.Get("Authorization") != "Bearer "+f.token {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":"unknown host"}`)
		return
	}
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/host/pairings":
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"code":"fdp_c","url":"`+f.URL+`/pair#fdp_c","expiresAt":"2030-01-01T00:10:00Z"}`)
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/host/devices":
		_, _ = io.WriteString(w, `{"devices":[{"id":"d1","name":"phone","created":"2030-01-01T00:00:00Z","lastSeen":"2030-01-01T00:05:00Z"}],`+
			`"hosts":[{"id":"h1","name":"desk","online":true,"lastSeen":"2030-01-01T00:05:00Z","self":true}]}`)
	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/v1/host/devices/"):
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/host":
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":"no such thing"}`)
	}
}

func (f *fakeRelay) config() Config {
	return Config{Relay: f.URL, HostID: "h1", AccountID: "a1", Token: f.token, Name: "desk"}
}

// session waits for the next tunnel to open.
func (f *fakeRelay) session(t *testing.T) *smux.Session {
	t.Helper()
	select {
	case s := <-f.sessions:
		return s
	case <-time.After(5 * time.Second):
		t.Fatal("no tunnel arrived at the relay")
		return nil
	}
}

// get sends one HTTP request down the tunnel, as the relay does for a browser.
func get(t *testing.T, sess *smux.Session, path string) (*http.Response, string) {
	t.Helper()
	st, err := sess.OpenStream()
	if err != nil {
		t.Fatalf("open a stream: %v", err)
	}
	defer st.Close()
	req, _ := http.NewRequest(http.MethodGet, "http://relay.example"+path, nil)
	req.Header.Set("Flockdeck-Remote-Device", "d1")
	if err := req.Write(st); err != nil {
		t.Fatalf("write the request: %v", err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(st), req)
	if err != nil {
		t.Fatalf("read the response: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, string(body)
}

// The whole point: a request the relay puts on the tunnel lands on the
// handler here, with what the relay said about it intact.
func TestTunnelCarriesRequestsToTheHandler(t *testing.T) {
	quick(t)
	f := newFakeRelay(t)
	var sawHost, sawDevice atomic.Value
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawHost.Store(r.Host)
		sawDevice.Store(r.Header.Get("Flockdeck-Remote-Device"))
		_, _ = io.WriteString(w, "hello from the desk at "+r.URL.Path)
	})
	var changes atomic.Int32
	c := NewConnector(f.config(), "v1.2.3", func(l net.Listener) error { return http.Serve(l, handler) },
		func() { changes.Add(1) })
	c.Start()
	defer c.Stop()

	sess := f.session(t)
	waitFor(t, "connected", func() bool { return c.Status().State == StateConnected })
	for _, path := range []string{"/", "/assets/app.js"} {
		resp, body := get(t, sess, path)
		if resp.StatusCode != http.StatusOK || body != "hello from the desk at "+path {
			t.Errorf("GET %s down the tunnel = %d %q", path, resp.StatusCode, body)
		}
	}
	if sawHost.Load() != "relay.example" || sawDevice.Load() != "d1" {
		t.Errorf("the handler saw host %v and device %v", sawHost.Load(), sawDevice.Load())
	}
	f.mu.Lock()
	version := f.version
	f.mu.Unlock()
	if version != "v1.2.3" {
		t.Errorf("the relay was told version %q", version)
	}
	if changes.Load() == 0 {
		t.Error("nobody was told the status changed")
	}

	c.Stop()
	if st := c.Status(); st.State != StateOff {
		t.Errorf("after Stop the state is %s, want off", st.State)
	}
	select {
	case <-sess.CloseChan():
	case <-time.After(5 * time.Second):
		t.Error("stopping left the tunnel open at the relay")
	}
}

// A tunnel that drops is dialled again, and the relay sees a second one.
func TestTunnelComesBackAfterADrop(t *testing.T) {
	quick(t)
	f := newFakeRelay(t)
	c := NewConnector(f.config(), "", func(l net.Listener) error { return http.Serve(l, http.NotFoundHandler()) }, nil)
	c.Start()
	defer c.Stop()

	first := f.session(t)
	waitFor(t, "connected", func() bool { return c.Status().State == StateConnected })
	first.Close()
	f.session(t)
	waitFor(t, "connected again", func() bool { return c.Status().State == StateConnected })
	if n := f.count(); n < 2 {
		t.Errorf("the relay saw %d connections, want at least 2", n)
	}
}

// revokedSays is what a machine the relay no longer accepts is told to do:
// enrol again, by either of the ways there are.
const revokedSays = "enrol it again from Remote access… in the command palette, or with `flockdeck remote enable`"

// The relay's refusals that mean "stop": a token it no longer accepts, before
// or after the upgrade, and another connection taking this host's place. Each
// has to end the retrying, or a removed machine would hammer the relay for as
// long as it ran.
func TestTunnelStopsWhenTheRelaySaysSo(t *testing.T) {
	for _, tc := range []struct {
		name      string
		refuse    int
		closeWith websocket.StatusCode
		want      State
		// says is what the status tells the user to do about it.
		says string
	}{
		{name: "refused before the upgrade", refuse: http.StatusUnauthorized, want: StateRevoked, says: revokedSays},
		{name: "forbidden before the upgrade", refuse: http.StatusForbidden, want: StateRevoked, says: revokedSays},
		{name: "closed as revoked", closeWith: CloseRevoked, want: StateRevoked, says: revokedSays},
		{name: "closed as replaced", closeWith: CloseReplaced, want: StateReplaced, says: "restart this one"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			quick(t)
			f := newFakeRelay(t)
			f.refuse, f.closeWith = tc.refuse, tc.closeWith
			c := NewConnector(f.config(), "", func(l net.Listener) error { return http.Serve(l, http.NotFoundHandler()) }, nil)
			c.Start()
			defer c.Stop()
			waitFor(t, string(tc.want), func() bool { return c.Status().State == tc.want })
			time.Sleep(100 * time.Millisecond) // several backoffs' worth
			if n := f.count(); n != 1 {
				t.Errorf("the relay saw %d connections, want 1 and no retrying", n)
			}
			if d := c.Status().Detail; !strings.Contains(d, tc.says) {
				t.Errorf("the status says %q, which does not say what to do (%q)", d, tc.says)
			}
		})
	}
}

// The relay's reason for closing the tunnel — "update Flockdeck", say — is
// what the window shows, as the relay put it.
func TestTunnelShowsTheRelaysReason(t *testing.T) {
	quick(t)
	f := newFakeRelay(t)
	f.closeWith = websocket.StatusPolicyViolation
	c := NewConnector(f.config(), "", func(l net.Listener) error { return http.Serve(l, http.NotFoundHandler()) }, nil)
	c.Start()
	defer c.Stop()
	waitFor(t, "an error", func() bool { return c.Status().State == StateError })
	if got, want := c.Status().Detail, "the relay closed the connection: because the test said so"; got != want {
		t.Errorf("detail = %q, want %q", got, want)
	}
}

// A relay that stops answering, which is what a dropped network or a laptop
// asleep looks like from here, is found out by the keepalive, and the window
// is told so in those words.
func TestTunnelNoticesASilentRelay(t *testing.T) {
	quick(t)
	oldEvery, oldTimeout := keepAlive, keepAliveTimeout
	keepAlive, keepAliveTimeout = 20*time.Millisecond, 100*time.Millisecond
	t.Cleanup(func() { keepAlive, keepAliveTimeout = oldEvery, oldTimeout })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{Subprotocol}})
		if err != nil {
			return
		}
		defer conn.CloseNow()
		// Take what the desktop sends, and say nothing back.
		for {
			if _, _, err := conn.Read(r.Context()); err != nil {
				return
			}
		}
	}))
	defer srv.Close()
	c := NewConnector(Config{Relay: srv.URL, HostID: "h1", Token: "fdh_test"}, "",
		func(l net.Listener) error { return http.Serve(l, http.NotFoundHandler()) }, nil)
	c.Start()
	defer c.Stop()
	var st Status
	waitFor(t, "the silence to be noticed", func() bool { st = c.Status(); return st.State == StateError })
	if want := "the relay stopped answering"; st.Detail != want {
		t.Errorf("detail = %q, want %q", st.Detail, want)
	}
}

// A relay that cannot be reached is retried, and the status says when.
func TestUnreachableRelayIsRetried(t *testing.T) {
	quick(t)
	f := newFakeRelay(t)
	cfg := f.config()
	f.Close()
	c := NewConnector(cfg, "", func(l net.Listener) error { return nil }, nil)
	c.Start()
	defer c.Stop()
	waitFor(t, "an error", func() bool { return c.Status().State == StateError })
	st := c.Status()
	if st.RetryAt.IsZero() || st.Detail == "" || strings.Contains(st.Detail, cfg.Relay) {
		t.Errorf("status = %+v, want why the relay could not be reached, without naming it again, and a retry time", st)
	}
}

// The window says "Cannot reach <relay>:" and then this, so a relay that
// never answers, or a network that answers in its place, is said in words.
func TestUnreachableRelaySaysWhy(t *testing.T) {
	quick(t)
	old := dialTimeout
	dialTimeout = 100 * time.Millisecond
	t.Cleanup(func() { dialTimeout = old })
	release := make(chan struct{})
	hang := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-release }))
	defer hang.Close()
	defer close(release)
	portal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "<html>Sign in to use this Wi-Fi</html>")
	}))
	defer portal.Close()
	for relay, want := range map[string]string{
		hang.URL:   "no answer within 100ms",
		portal.URL: "it answered 200 OK rather than opening the tunnel",
	} {
		c := NewConnector(Config{Relay: relay, HostID: "h1", Token: "fdh_test"}, "", func(l net.Listener) error { return nil }, nil)
		c.Start()
		waitFor(t, "an error", func() bool { return c.Status().State == StateError })
		if got := c.Status().Detail; got != want {
			t.Errorf("detail = %q, want %q", got, want)
		}
		c.Stop()
	}
}

// A message far bigger than any smux frame is a relay gone wrong, and the
// tunnel is dropped and dialled again rather than read to the end.
func TestTunnelDropsAnOversizedMessage(t *testing.T) {
	quick(t)
	var connects atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connects.Add(1)
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{Subprotocol}})
		if err != nil {
			return
		}
		defer conn.CloseNow()
		// Two megabytes of smux keepalives: frames that are harmless one by
		// one, in a single message twice the size the tunnel allows.
		nop := "\x02\x03\x00\x00\x00\x00\x00\x00"
		_ = conn.Write(r.Context(), websocket.MessageBinary, []byte(strings.Repeat(nop, 2<<20/len(nop))))
		for {
			if _, _, err := conn.Read(r.Context()); err != nil {
				return
			}
		}
	}))
	defer srv.Close()
	c := NewConnector(Config{Relay: srv.URL, HostID: "h1", Token: "fdh_test"}, "",
		func(l net.Listener) error { return http.Serve(l, http.NotFoundHandler()) }, nil)
	c.Start()
	defer c.Stop()
	waitFor(t, "the tunnel to be dropped and dialled again", func() bool { return connects.Load() >= 2 })
}

// Only the relay can revoke this machine. A 403 page from a proxy or a
// firewall in front of it is a reason to try again, not to stop for good.
func TestOnlyTheRelayRevokes(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want bool
	}{
		{decodeError(http.StatusUnauthorized, []byte(`{"error":"this desktop is no longer registered with the relay"}`)), true},
		{decodeError(http.StatusForbidden, []byte(`{"error":"this host was removed"}`)), true},
		{why(websocket.CloseError{Code: CloseRevoked}), true},
		{decodeError(http.StatusForbidden, []byte(`<html><body>Access denied</body></html>`)), false},
		{decodeError(http.StatusUnauthorized, nil), false},
		{decodeError(http.StatusInternalServerError, []byte(`{"error":"the database is not answering"}`)), false},
	} {
		if got := IsRevoked(tc.err); got != tc.want {
			t.Errorf("IsRevoked(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}

	quick(t)
	var connects atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connects.Add(1)
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, "<html><body>Access denied</body></html>")
	}))
	defer proxy.Close()
	c := NewConnector(Config{Relay: proxy.URL, HostID: "h1", Token: "fdh_test"}, "",
		func(l net.Listener) error { return http.Serve(l, http.NotFoundHandler()) }, nil)
	c.Start()
	defer c.Stop()
	waitFor(t, "the tunnel to be tried again", func() bool { return connects.Load() >= 3 })
	if st := c.Status(); st.State == StateRevoked {
		t.Errorf("a proxy's 403 page stopped the tunnel as revoked: %+v", st)
	}
}

// A relay that takes a request and never answers is said to have given no
// answer, in those words, in the terminal and in the window alike.
func TestClientSaysWhenTheRelayDoesNotAnswer(t *testing.T) {
	old := requestTimeout
	requestTimeout = 100 * time.Millisecond
	t.Cleanup(func() { requestTimeout = old })
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-release }))
	defer srv.Close()
	defer close(release)
	_, err := NewClient(&Config{Relay: srv.URL, Token: "fdh_test"}, "").Devices(context.Background())
	if err == nil || !strings.HasSuffix(err.Error(), ": no answer within 100ms") {
		t.Errorf("Devices from a relay that never answers = %v", err)
	}
}

// A 404 not in the relay's own words is, most likely, an address that is not
// a relay's, and both the command line and the window ask whether it is.
func TestNotARelayIsAskedAbout(t *testing.T) {
	quick(t)
	site := httptest.NewServer(http.NotFoundHandler())
	defer site.Close()
	_, err := Register(context.Background(), site.URL, "v", RegisterRequest{Name: "desk"})
	if err == nil || !strings.HasSuffix(err.Error(), "is that a Flockdeck relay's address?") {
		t.Errorf("Register with a website = %v", err)
	}
	c := NewConnector(Config{Relay: site.URL, HostID: "h1", Token: "fdh_test"}, "", func(l net.Listener) error { return nil }, nil)
	c.Start()
	defer c.Stop()
	var st Status
	waitFor(t, "an error", func() bool { st = c.Status(); return st.State == StateError })
	if !strings.HasSuffix(st.Detail, "is that a Flockdeck relay's address?") {
		t.Errorf("detail = %q, want it to ask whether this is a relay", st.Detail)
	}
	// The relay's own 404, with its own words, is not second-guessed.
	if msg := decodeError(http.StatusNotFound, []byte(`{"error":"there is no such device"}`)).Error(); strings.Contains(msg, "relay's address") {
		t.Errorf("the relay's own 404 = %q", msg)
	}
}

func TestClientCalls(t *testing.T) {
	f := newFakeRelay(t)
	ctx := context.Background()

	reg, err := Register(ctx, f.URL, "v", RegisterRequest{Name: "desk"})
	if err != nil || reg.HostID != "h1" || reg.Token != "fdh_test" {
		t.Fatalf("Register = %+v, %v", reg, err)
	}
	_, err = Register(ctx, f.URL, "v", RegisterRequest{})
	var api *APIError
	if !errors.As(err, &api) || api.Status != http.StatusBadRequest || api.Message != "a name is required" {
		t.Errorf("a refused registration = %v, want the relay's own words", err)
	}

	c := NewClient(&Config{Relay: f.URL, Token: f.token}, "v")
	p, err := c.Pair(ctx, KindDevice)
	if err != nil || p.Code != "fdp_c" || !strings.HasSuffix(p.URL, "/pair#fdp_c") || p.ExpiresAt.IsZero() {
		t.Errorf("Pair = %+v, %v", p, err)
	}
	r, err := c.Devices(ctx)
	if err != nil || len(r.Devices) != 1 || r.Devices[0].Name != "phone" || len(r.Hosts) != 1 || !r.Hosts[0].Self {
		t.Errorf("Devices = %+v, %v", r, err)
	}
	if err := c.Revoke(ctx, "d1"); err != nil {
		t.Errorf("Revoke: %v", err)
	}
	if err := c.Unregister(ctx); err != nil {
		t.Errorf("Unregister: %v", err)
	}
	f.mu.Lock()
	calls := strings.Join(f.calls, "\n")
	f.mu.Unlock()
	for _, want := range []string{"DELETE /api/v1/host/devices/d1", "DELETE /api/v1/host"} {
		if !strings.Contains(calls, want) {
			t.Errorf("the relay never saw %s; it saw:\n%s", want, calls)
		}
	}

	stale := NewClient(&Config{Relay: f.URL, Token: "fdh_revoked"}, "v")
	if _, err := stale.Devices(ctx); !IsRevoked(err) {
		t.Errorf("a token the relay does not know gave %v, want it reported as revoked", err)
	}
}

// Reloading brings the tunnel in line with the enrolment: started, left alone
// when nothing has changed, and stopped when the enrolment goes.
func TestManagerReload(t *testing.T) {
	quick(t)
	f := newFakeRelay(t)
	var cfg *Config
	m := NewManager("v", func(l net.Listener) error { return http.Serve(l, http.NotFoundHandler()) }, nil)
	m.load = func() (*Config, error) { return cfg, nil }
	defer m.Close()

	if err := m.Reload(); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Status(); ok {
		t.Error("a machine with no enrolment reports a tunnel")
	}
	if _, err := m.Client(); !errors.Is(err, ErrNotEnabled) {
		t.Errorf("Client with no enrolment = %v, want ErrNotEnabled", err)
	}
	// It is read in the window and in a terminal, and names the way from each.
	if msg := ErrNotEnabled.Error(); !strings.Contains(msg, "Remote access… in the command palette") || !strings.Contains(msg, "flockdeck remote enable") {
		t.Errorf("ErrNotEnabled = %q, want it to name both ways to turn remote access on", msg)
	}

	c := f.config()
	cfg = &c
	if err := m.Reload(); err != nil {
		t.Fatal(err)
	}
	f.session(t)
	waitFor(t, "connected", func() bool { st, _ := m.Status(); return st.State == StateConnected })

	first := m.conn
	if err := m.Reload(); err != nil {
		t.Fatal(err)
	}
	if m.conn != first {
		t.Error("reloading an unchanged enrolment replaced a working tunnel")
	}
	if n := f.count(); n != 1 {
		t.Errorf("the relay saw %d connections, want 1", n)
	}

	cfg = nil
	if err := m.Reload(); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Status(); ok {
		t.Error("the tunnel outlived the enrolment")
	}

	m.load = func() (*Config, error) { return nil, errors.New("broken") }
	if err := m.Reload(); err == nil {
		t.Error("a broken enrolment was reloaded without a word")
	}
}

// Enabling and disabling, which the command line and the window both do
// through these.
func TestEnableAndDisable(t *testing.T) {
	isolate(t)
	f := newFakeRelay(t)
	ctx := context.Background()

	cfg, replaced, err := Enable(ctx, "v", EnableRequest{Relay: f.URL, Name: " desk "})
	if err != nil || replaced || cfg.HostID != "h1" || cfg.Name != "desk" || cfg.Relay != f.URL {
		t.Fatalf("Enable = %+v, %v, %v", cfg, replaced, err)
	}
	if saved, err := Load(); err != nil || saved == nil || *saved != *cfg {
		t.Errorf("Enable saved %+v, %v; want %+v", saved, err, cfg)
	}
	var already *AlreadyEnabledError
	if _, _, err := Enable(ctx, "v", EnableRequest{Relay: f.URL, Name: "again"}); !errors.As(err, &already) || already.Err != nil {
		t.Errorf("enabling over a live enrolment = %v, want it refused", err)
	}

	// An enrolment the relay has forgotten is replaced, and the caller told.
	f.token = "fdh_forgotten"
	_, replaced, err = Enable(ctx, "v", EnableRequest{Relay: f.URL, Name: "desk"})
	f.token = "fdh_test"
	if err != nil || !replaced {
		t.Errorf("enabling over a forgotten enrolment = %v, replaced %v", err, replaced)
	}

	if had, untold, err := Disable(ctx, "v", false); !had || untold != nil || err != nil {
		t.Errorf("Disable = %v, %v, %v", had, untold, err)
	}
	if c, _ := Load(); c != nil {
		t.Error("Disable left the enrolment behind")
	}
	if had, _, err := Disable(ctx, "v", false); had || err != nil {
		t.Errorf("Disable with nothing enrolled = %v, %v", had, err)
	}

	// A relay that cannot be told keeps the enrolment, unless forced.
	if _, _, err := Enable(ctx, "v", EnableRequest{Relay: f.URL, Name: "desk"}); err != nil {
		t.Fatal(err)
	}
	f.Close()
	var untoldErr *RelayUntoldError
	if _, _, err := Disable(ctx, "v", false); !errors.As(err, &untoldErr) {
		t.Errorf("Disable with the relay gone = %v, want a RelayUntoldError", err)
	}
	if c, _ := Load(); c == nil {
		t.Fatal("a Disable that could not tell the relay forgot the enrolment")
	}
	if had, untold, err := Disable(ctx, "v", true); !had || untold == nil || err != nil {
		t.Errorf("forced Disable = %v, %v, %v; want it done, saying why the relay was not told", had, untold, err)
	}
	if c, _ := Load(); c != nil {
		t.Error("a forced Disable left the enrolment behind")
	}
}

// The window enables and disables through the manager, which brings the
// tunnel up and down to match.
func TestManagerEnablesAndDisables(t *testing.T) {
	isolate(t)
	quick(t)
	f := newFakeRelay(t)
	m := NewManager("v", func(l net.Listener) error { return http.Serve(l, http.NotFoundHandler()) }, nil)
	defer m.Close()
	if _, err := m.Enable(context.Background(), EnableRequest{Relay: f.URL, Name: "desk"}); err != nil {
		t.Fatal(err)
	}
	f.session(t)
	waitFor(t, "connected", func() bool { st, _ := m.Status(); return st.State == StateConnected })
	if _, err := m.Client(); err != nil {
		t.Errorf("Client after Enable = %v", err)
	}
	if _, err := m.Disable(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Status(); ok {
		t.Error("the tunnel outlived disabling")
	}
}

// Two requests to enable at once, a double click say, enrol the machine once:
// the second finds the first's enrolment rather than orphaning it.
func TestManagerEnablesOnceAtATime(t *testing.T) {
	isolate(t)
	quick(t)
	f := newFakeRelay(t)
	m := NewManager("v", func(l net.Listener) error { return http.Serve(l, http.NotFoundHandler()) }, nil)
	defer m.Close()
	errs := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := m.Enable(context.Background(), EnableRequest{Relay: f.URL, Name: "desk"})
			errs <- err
		}()
	}
	refused := 0
	for range 2 {
		var already *AlreadyEnabledError
		if err := <-errs; errors.As(err, &already) {
			refused++
		} else if err != nil {
			t.Errorf("Enable: %v", err)
		}
	}
	f.mu.Lock()
	registered := strings.Count(strings.Join(f.calls, "\n"), "POST /api/v1/hosts")
	f.mu.Unlock()
	if registered != 1 || refused != 1 {
		t.Errorf("the relay saw %d registrations and %d of 2 enables were refused; want 1 and 1", registered, refused)
	}
}

// A machine nobody names is called by its host name, without the ".local" a
// Mac adds for its own network, which is not part of what anybody calls it.
func TestHostName(t *testing.T) {
	for host, want := range map[string]string{
		"Jims-MacBook-Pro.local": "Jims-MacBook-Pro",
		"Jims-MacBook-Pro.LOCAL": "Jims-MacBook-Pro",
		"DESKTOP-4F2K9LQ":        "DESKTOP-4F2K9LQ",
		"build.local.example":    "build.local.example",
		"workstation":            "workstation",
	} {
		if got := hostName(host); got != want {
			t.Errorf("hostName(%q) = %q, want %q", host, got, want)
		}
	}
}

func TestQRSVG(t *testing.T) {
	svg, err := QRSVG("https://relay.example/pair#fdp_0123456789abcdefghijkl")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(svg, "<svg") || !strings.HasSuffix(svg, "</svg>") || !strings.Contains(svg, `fill="#fff"`) {
		t.Errorf("QRSVG does not look like a QR code on white: %.120s…", svg)
	}
}

func TestQRTerminalIsSquare(t *testing.T) {
	out, err := QRTerminal("https://relay.example/pair#fdp_0123456789abcdefghijkl")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	width := len([]rune(lines[0]))
	for i, l := range lines {
		if n := len([]rune(l)); n != width {
			t.Fatalf("line %d is %d wide, want %d", i, n, width)
		}
	}
	// Two modules to a line, so a square code is about half as many lines as
	// it is wide.
	if got := len(lines); got != (width+1)/2 {
		t.Errorf("%d lines for a code %d wide, want %d", got, width, (width+1)/2)
	}
	// The margin is light all the way round, which is what a reader looks for.
	if !strings.HasPrefix(lines[0], "██") || strings.ContainsRune(lines[0], ' ') {
		t.Errorf("the top edge is not solid quiet zone: %q", lines[0])
	}
}
