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
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
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
	// One of the relay's secrets typed where its address goes is refused, not
	// taken for a host name, which would send it to the DNS resolver in the
	// clear, and the refusal does not repeat it.
	for _, secret := range []string{"fdh_0123456789abcdefghijkl", "fdp_0123456789abcdefghijkl"} {
		if got, err := CheckRelay(secret); err == nil || strings.Contains(err.Error(), secret) {
			t.Errorf("CheckRelay(%q) = %q, %v; want it refused without repeating it", secret, got, err)
		}
	}
	// A pairing link pasted as the relay is the wrong thing to hand, which the
	// refusal says, and its one-time code is not repeated back. The window
	// says it too, so it points at the join code, not the flag.
	if _, err := CheckRelay("https://remote.flockdeck.ai/pair#fdp_s3cretcode"); err == nil ||
		!strings.Contains(err.Error(), "pairing link") || !strings.HasSuffix(err.Error(), "give the code it prints as the join code here") ||
		strings.Contains(err.Error(), "s3cretcode") {
		t.Errorf("CheckRelay of a pairing link = %v, want it named as one, pointing at the join code, without its code", err)
	}
	// A password in the address would be saved and printed back wherever the
	// relay is named, so it is refused, and the refusal does not repeat it.
	if _, err := CheckRelay("https://someone:hunter2@relay.example"); err == nil || strings.Contains(err.Error(), "hunter2") {
		t.Errorf("CheckRelay of an address with a password = %v, want it refused without repeating the password", err)
	}
	if got, _ := CheckRelay("relay.example.com:8443"); got != "https://relay.example.com:8443" {
		t.Errorf("CheckRelay of an address with no scheme = %q, want it taken as https://", got)
	}
	// One relay is one address however it is typed, or naming the relay this
	// machine is on would read as asking to move to another.
	for raw, want := range map[string]string{
		"https://Remote.Flockdeck.AI:443/": "https://remote.flockdeck.ai",
		"https://relay.flockdeck.ai":       DefaultRelay, // its old name, as a new enrolment types it
		"http://LOCALHOST:80":              "http://localhost",
		"https://relay.example:8443":       "https://relay.example:8443",
	} {
		if got, err := CheckRelay(raw); err != nil || got != want {
			t.Errorf("CheckRelay(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
	// A machine's own address on the relay, which status shows, is not the
	// relay's, and the refusal gives the relay's.
	for raw, want := range map[string]string{
		"https://remote.flockdeck.ai/h/abcdefghijklmnop/": "the relay's own address is https://remote.flockdeck.ai",
		"https://relay.flockdeck.ai/h/abcdefghijklmnop":   "the relay's own address is " + DefaultRelay,
		"https://relay.example/base/h/abcdefghijklmnop/":  "the relay's own address is https://relay.example/base",
	} {
		if _, err := CheckRelay(raw); err == nil || !strings.HasSuffix(err.Error(), want) {
			t.Errorf("CheckRelay(%q) = %v, want it refused, ending %q", raw, err, want)
		}
	}
}

// An enrolment naming a relay off this machine without TLS is refused, not
// used: the token would cross the network in the clear. Nothing Flockdeck
// saves looks like this; only a hand edit does.
func TestLoadRefusesARelayWithoutTLS(t *testing.T) {
	isolate(t)
	for relay, ok := range map[string]bool{
		"http://relay.example":  false,
		"http://127.0.0.1:9":    true, // a relay being developed, on this machine
		"https://relay.example": true,
	} {
		if err := (&Config{Relay: relay, HostID: "h1", Token: "fdh_test"}).Save(); err != nil {
			t.Fatal(err)
		}
		c, err := Load()
		if ok != (err == nil) || (!ok && !strings.Contains(err.Error(), "without TLS")) {
			t.Errorf("Load of an enrolment with the relay %s = %+v, %v; want ok=%v", relay, c, err, ok)
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

// The relay never redirects a desktop, and a redirect is not followed, by
// the API or the tunnel: Go would carry the token across one to the same
// host even over plain HTTP.
func TestRedirectIsNotFollowedWithTheToken(t *testing.T) {
	quick(t)
	var mu sync.Mutex
	var leaked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/elsewhere" {
			mu.Lock()
			leaked = append(leaked, r.Header.Get("Authorization"))
			mu.Unlock()
			http.NotFound(w, r)
			return
		}
		http.Redirect(w, r, "/elsewhere", http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	cfg := Config{Relay: srv.URL, HostID: "h1", Token: "fdh_test"}
	if _, err := NewClient(&cfg, "v").Devices(context.Background()); err == nil || !strings.Contains(err.Error(), "redirect") {
		t.Errorf("Devices from a relay that redirects = %v, want the redirect refused", err)
	}
	c := NewConnector(cfg, "", func(l net.Listener) error { return http.Serve(l, http.NotFoundHandler()) }, nil)
	c.Start()
	defer c.Stop()
	waitFor(t, "an error", func() bool { return c.Status().State == StateError })
	if d := c.Status().Detail; !strings.Contains(d, "redirect") {
		t.Errorf("the tunnel's detail = %q, want the redirect refused", d)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(leaked) > 0 {
		t.Errorf("the token followed the redirect: %q", leaked)
	}
}

// A relay that accepts the WebSocket without agreeing to the tunnel's
// subprotocol does not speak it, and is told apart from one that does.
func TestRelayWithoutTheSubprotocolIsSaidInWords(t *testing.T) {
	quick(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
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
	waitFor(t, "an error", func() bool { return c.Status().State == StateError })
	if d := c.Status().Detail; !strings.Contains(d, "does not speak this version's tunnel ("+Subprotocol+")") {
		t.Errorf("detail = %q, want it to say the relay does not speak the tunnel", d)
	}
}

// serve stopping of its own accord, as the server's does while this Flockdeck
// shuts down, is not the relay closing the connection and is not reported as
// it.
func TestServeStoppingIsNotBlamedOnTheRelay(t *testing.T) {
	quick(t)
	f := newFakeRelay(t)
	c := NewConnector(f.config(), "", func(net.Listener) error { return errors.New("the server has shut down") }, nil)
	c.Start()
	defer c.Stop()
	waitFor(t, "an error", func() bool { return c.Status().State == StateError })
	if d := c.Status().Detail; d != "this Flockdeck stopped answering remote windows: the server has shut down" {
		t.Errorf("detail = %q, want it to say serving stopped here", d)
	}
}

// revokedSays is what a machine the relay no longer accepts is told to do:
// enrol again, by either of the ways there are.
const revokedSays = "enrol it again from Remote access… in the command palette, or with `flockdeck remote enable`"

// A tunnel that has held for a while and is then dropped, as a relay
// restarting drops every tunnel, is not shown as trouble while its first,
// prompt retry is still to come: nothing needs anybody yet.
func TestADropAfterAWhileIsNotTrouble(t *testing.T) {
	quick(t)
	old := stableAfter
	stableAfter = 50 * time.Millisecond
	t.Cleanup(func() { stableAfter = old })
	f := newFakeRelay(t)
	var mu sync.Mutex
	var seen []State
	var c *Connector
	c = NewConnector(f.config(), "", func(l net.Listener) error { return http.Serve(l, http.NotFoundHandler()) }, func() {
		mu.Lock()
		seen = append(seen, c.Status().State)
		mu.Unlock()
	})
	c.Start()
	defer c.Stop()
	first := f.session(t)
	waitFor(t, "connected", func() bool { return c.Status().State == StateConnected })
	time.Sleep(2 * stableAfter)
	mu.Lock()
	seen = nil
	mu.Unlock()
	first.Close()
	f.session(t)
	waitFor(t, "connected again", func() bool { return c.Status().State == StateConnected })
	mu.Lock()
	defer mu.Unlock()
	for _, s := range seen {
		if s == StateError {
			t.Errorf("a drop after a while showed %v on the way back", seen)
			break
		}
	}
}

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
			f.mu.Lock()
			f.refuse, f.closeWith = tc.refuse, tc.closeWith
			f.mu.Unlock()
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
	f.mu.Lock()
	f.closeWith = websocket.StatusPolicyViolation
	f.mu.Unlock()
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

// A relay whose certificate this machine does not trust, a self-hosted one
// with a certificate of its own making say, is said to be that, in words,
// in the terminal and in the window, with the verifier's reason kept.
// A relay whose name cannot be looked up is said in words, not the
// resolver's, whichever way the error arrives.
func TestLookupFailureIsSaidInWords(t *testing.T) {
	lookup := func(dns *net.DNSError) error {
		return &url.Error{Op: "Post", URL: "https://relay.example/api/v1/hosts", Err: &net.OpError{Op: "dial", Net: "tcp", Err: dns}}
	}
	for _, tc := range []struct {
		err  error
		want string
	}{
		{lookup(&net.DNSError{Err: "getaddrinfow: The requested name is valid, but no data of the requested type was found", Name: "relay.example", IsNotFound: true}),
			"relay.example could not be found; check the address, and that this machine is online"},
		{lookup(&net.DNSError{Err: "i/o timeout", Name: "relay.example", IsTimeout: true}),
			"looking up relay.example took too long; check that this machine is online"},
	} {
		if got := transportError(tc.err); got == nil || got.Error() != tc.want {
			t.Errorf("transportError(%v) = %v, want %q", tc.err, got, tc.want)
		}
	}
}

// A relay with nothing listening at its address is said in words, however
// the system spells the refusal: Windows' socket error is not Unix's.
func TestRefusedConnectionIsSaidInWords(t *testing.T) {
	const want = "nothing is answering there (the connection was refused); check the address, and that the relay is running"
	for _, errno := range []syscall.Errno{10061, syscall.ECONNREFUSED} {
		err := &url.Error{Op: "Get", URL: "https://relay.example/api/v1/host/devices",
			Err: &net.OpError{Op: "dial", Net: "tcp", Err: &os.SyscallError{Syscall: "connect", Err: errno}}}
		if got := transportError(err); got == nil || got.Error() != want {
			t.Errorf("transportError of errno %d = %v, want %q", uintptr(errno), got, want)
		}
	}
	// And a real one: an address that was listening a moment ago.
	srv := httptest.NewServer(http.NotFoundHandler())
	relay := srv.URL
	srv.Close()
	if _, err := NewClient(&Config{Relay: relay, Token: "fdh_test"}, "v").Devices(context.Background()); err == nil || !strings.HasSuffix(err.Error(), want) {
		t.Errorf("Devices from a relay with nothing listening = %v, want it to end %q", err, want)
	}
}

// A relay that answers in plain HTTP, reached as https:// (as an address
// typed without a scheme is), is said to, by enrolling and by the tunnel.
func TestPlainHTTPRelayIsSaidInWords(t *testing.T) {
	quick(t)
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	relay := "https://" + strings.TrimPrefix(srv.URL, "http://")
	const want = "it answers in plain HTTP, not HTTPS"
	if _, err := Register(context.Background(), relay, "v", RegisterRequest{Name: "desk"}); err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("enabling against a relay in plain HTTP = %v, want it to say %q", err, want)
	}
	c := NewConnector(Config{Relay: relay, HostID: "h1", Token: "fdh_test"}, "",
		func(l net.Listener) error { return http.Serve(l, http.NotFoundHandler()) }, nil)
	c.Start()
	defer c.Stop()
	waitFor(t, "an error", func() bool { return c.Status().State == StateError })
	if d := c.Status().Detail; !strings.Contains(d, want) {
		t.Errorf("the tunnel's detail = %q, want it to say %q", d, want)
	}
}

// A pairing code's expiry is given by this machine's clock, however far the
// relay's is from it, so that how long it has left is said truly.
func TestPairingExpiryIsByThisMachinesClock(t *testing.T) {
	relayNow := time.Now().Add(-30 * time.Minute).Truncate(time.Second)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Date", relayNow.UTC().Format(http.TimeFormat))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"code": "fdp_code", "url": "x", "expiresAt": relayNow.Add(10 * time.Minute)})
	}))
	defer srv.Close()
	p, err := NewClient(&Config{Relay: srv.URL, Token: "fdh_test"}, "v").Pair(context.Background(), KindDevice)
	if err != nil {
		t.Fatal(err)
	}
	if left := time.Until(p.ExpiresAt); left < 9*time.Minute || left > 11*time.Minute {
		t.Errorf("a code the relay gives 10 minutes, by a clock 30 minutes behind this one, is due here in %v", left.Round(time.Second))
	}
}

func TestUntrustedCertificateIsSaidInWords(t *testing.T) {
	quick(t)
	srv := httptest.NewTLSServer(http.NotFoundHandler())
	defer srv.Close()
	const want = "its certificate is not one this machine trusts (x509: "
	if _, err := Register(context.Background(), srv.URL, "v", RegisterRequest{Name: "desk"}); err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("enabling against an untrusted relay = %v, want it to say %q", err, want)
	}
	c := NewConnector(Config{Relay: srv.URL, HostID: "h1", Token: "fdh_test"}, "", func(l net.Listener) error { return nil }, nil)
	c.Start()
	defer c.Stop()
	var st Status
	waitFor(t, "an error", func() bool { st = c.Status(); return st.State == StateError })
	if !strings.HasPrefix(st.Detail, want) {
		t.Errorf("detail = %q, want it to begin %q", st.Detail, want)
	}
}

// One relay is one relay however it is written, and the hosted one by its old
// name, which machines enrolled in v0.2.0 keep, is the same as by its new.
func TestSameRelay(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		same bool
	}{
		{"https://relay.flockdeck.ai", DefaultRelay, true},
		{DefaultRelay, "remote.flockdeck.ai", true},
		{"https://Remote.Flockdeck.AI:443/", DefaultRelay, true},
		{"https://relay.example", DefaultRelay, false},
		{"https://relay.flockdeck.ai", "https://relay.example", false},
	} {
		if got := SameRelay(tc.a, tc.b); got != tc.same {
			t.Errorf("SameRelay(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.same)
		}
	}
}

// A device's id is not a secret; a credential typed in its place is refused
// before it travels in the request's path.
func TestRevokeRefusesACredential(t *testing.T) {
	f := newFakeRelay(t)
	c := NewClient(&Config{Relay: f.URL, Token: f.token}, "v")
	if err := c.Revoke(context.Background(), "fdd_0123456789abcdefghijkl"); err == nil || !strings.Contains(err.Error(), "not a device's id") {
		t.Errorf("Revoke of a credential = %v, want it refused", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, call := range f.calls {
		if strings.HasPrefix(call, "DELETE /api/v1/host/devices/") {
			t.Errorf("the credential was sent to the relay: %s", call)
		}
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
	// The relay forgetting it is a token it does not know, saved here, so the
	// fake relay's own fields are never written while it is serving.
	forgotten := *cfg
	forgotten.Token = "fdh_forgotten"
	if err := forgotten.Save(); err != nil {
		t.Fatal(err)
	}
	_, replaced, err = Enable(ctx, "v", EnableRequest{Relay: f.URL, Name: "desk"})
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

// A phone's pairing link given as a join code is refused before anything
// reaches the relay, saying what it is for and what a machine joins with.
func TestEnableRefusesAPairingLinkAsAJoinCode(t *testing.T) {
	isolate(t)
	f := newFakeRelay(t)
	_, _, err := Enable(context.Background(), "v", EnableRequest{Relay: f.URL, Name: "desk", Join: f.URL + "/pair#fdp_s3cretcode"})
	if err == nil || !strings.Contains(err.Error(), "pairing link") || !strings.Contains(err.Error(), "pair -desktop") {
		t.Errorf("enabling with a pairing link as the join code = %v, want it named as one", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, call := range f.calls {
		if call == "POST /api/v1/hosts" {
			t.Error("the pairing link was sent to the relay as a join code")
		}
	}
}

// An invitation given as the join code, or a join code as the invitation,
// is told so before anything reaches the relay, whose own answer would be
// that no code had been given.
func TestEnableTellsSwappedCodesApart(t *testing.T) {
	isolate(t)
	f := newFakeRelay(t)
	for _, tc := range []struct {
		req  EnableRequest
		want string
	}{
		// The window shows these too, so they name the code, not a flag.
		{EnableRequest{Relay: f.URL, Name: "desk", Join: "fdi_0123456789abcdefghijkl"}, "that is an invitation, not a join code; give it as the invitation code instead"},
		{EnableRequest{Relay: f.URL, Name: "desk", Invite: "fdp_0123456789abcdefghijkl"}, "that is a join code, not an invitation; give it as the join code instead"},
		{EnableRequest{Relay: f.URL, Name: "desk", Join: "fdh_0123456789abcdefghijkl"}, "that is a credential"},
		{EnableRequest{Relay: f.URL, Name: "desk", Invite: "fdd_0123456789abcdefghijkl"}, "that is a credential"},
		{EnableRequest{Relay: f.URL, Name: "fdp_0123456789abcdefghijkl"}, "not a name"},
	} {
		if _, _, err := Enable(context.Background(), "v", tc.req); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("Enable(%+v) = %v, want %q", tc.req, err, tc.want)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, call := range f.calls {
		if call == "POST /api/v1/hosts" {
			t.Error("a code given under the wrong name was sent to the relay")
		}
	}
}

// Trying again from the window does not wait out the backoff: a relay that
// has come back is reached at once.
func TestReconnectDoesNotWaitOutTheBackoff(t *testing.T) {
	isolate(t)
	oldMin, oldMax := backoffMin, backoffMax
	backoffMin, backoffMax = time.Minute, time.Minute
	t.Cleanup(func() { backoffMin, backoffMax = oldMin, oldMax })
	f := newFakeRelay(t)
	f.mu.Lock()
	f.refuse = http.StatusServiceUnavailable
	f.mu.Unlock()
	cfg := f.config()
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	m := NewManager("", func(l net.Listener) error { return http.Serve(l, http.NotFoundHandler()) }, nil)
	if err := m.Reload(); err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	waitFor(t, "an error", func() bool { s, _ := m.Status(); return s.State == StateError })
	f.mu.Lock()
	f.refuse = 0
	f.mu.Unlock()
	if err := m.Reconnect(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "connected", func() bool { s, _ := m.Status(); return s.State == StateConnected })
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
// The name saved is the one the relay keeps, and so the one every device
// lists: without control characters, and no longer than the relay's limit.
func TestEnableSavesTheNameTheRelayKeeps(t *testing.T) {
	isolate(t)
	f := newFakeRelay(t)
	long := strings.Repeat("é", maxName) + "-and-more"
	cfg, _, err := Enable(context.Background(), "v", EnableRequest{Relay: f.URL, Name: " desk\tone\x07 "})
	if err != nil || cfg.Name != "deskone" {
		t.Errorf("Enable saved the name %q (%v), want %q", cfg.Name, err, "deskone")
	}
	if err := Clear(); err != nil {
		t.Fatal(err)
	}
	cfg, _, err = Enable(context.Background(), "v", EnableRequest{Relay: f.URL, Name: long})
	if want := strings.Repeat("é", maxName); err != nil || cfg.Name != want {
		t.Errorf("Enable saved a long name as %q (%v), want it cut to %d characters", cfg.Name, err, maxName)
	}
}

// A machine with no name given and no host name to be had is saved under the
// name the relay gives it, which is the one every device lists.
func TestEnableWithNoNameToBeHad(t *testing.T) {
	isolate(t)
	old := hostname
	hostname = func() (string, error) { return "", errors.New("no host name") }
	t.Cleanup(func() { hostname = old })
	f := newFakeRelay(t)
	cfg, _, err := Enable(context.Background(), "v", EnableRequest{Relay: f.URL})
	if err != nil || cfg.Name != "Desktop" {
		t.Errorf("Enable with no name to be had saved %+v, %v; want the relay's own name for it, Desktop", cfg, err)
	}
}

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

// The relay closes the tunnel as revoked the moment it is told a machine is
// leaving. Disabling from the window must not show that — the relay no
// longer accepting this machine — to somebody who has just switched it off,
// and a disable the relay refuses must leave the tunnel as it was.
func TestManagerDisableDoesNotShowRevoked(t *testing.T) {
	isolate(t)
	quick(t)
	var mu sync.Mutex
	var tunnel *websocket.Conn
	refuse := true
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/host/connect", func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{Subprotocol}})
		if err != nil {
			return
		}
		mu.Lock()
		tunnel = conn
		mu.Unlock()
		sess, err := smux.Client(websocket.NetConn(context.Background(), conn, websocket.MessageBinary), smuxConfig())
		if err != nil {
			return
		}
		for {
			if _, err := sess.AcceptStream(); err != nil {
				return
			}
		}
	})
	mux.HandleFunc("POST /api/v1/hosts", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"hostId":"h1","accountId":"a1","token":"fdh_test"}`)
	})
	mux.HandleFunc("DELETE /api/v1/host", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		c, no := tunnel, refuse
		mu.Unlock()
		if no {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"error":"the relay could not unregister this desktop"}`)
			return
		}
		// What the relay does: kick the tunnel as revoked, then answer.
		if c != nil {
			go c.Close(CloseRevoked, "this desktop was unregistered")
			time.Sleep(20 * time.Millisecond)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	var seen []State
	var m *Manager
	m = NewManager("v", func(l net.Listener) error { return http.Serve(l, http.NotFoundHandler()) }, func() {
		if st, ok := m.Status(); ok {
			mu.Lock()
			seen = append(seen, st.State)
			mu.Unlock()
		}
	})
	defer m.Close()
	if _, err := m.Enable(context.Background(), EnableRequest{Relay: srv.URL, Name: "desk"}); err != nil {
		t.Fatal(err)
	}
	connected := func() bool { st, _ := m.Status(); return st.State == StateConnected }
	waitFor(t, "connected", connected)

	var untold *RelayUntoldError
	if _, err := m.Disable(context.Background(), false); !errors.As(err, &untold) {
		t.Fatalf("a disable the relay refused = %v, want a RelayUntoldError", err)
	}
	waitFor(t, "the tunnel back after a refused disable", connected)

	mu.Lock()
	refuse, seen = false, nil
	mu.Unlock()
	if _, err := m.Disable(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	for _, s := range seen {
		if s == StateRevoked {
			t.Errorf("disabling showed the window %v on the way", seen)
			break
		}
	}
}

// A disable that cannot even read the enrolment leaves the tunnel alone:
// had it been closed, Reload could not open it again, for the same reason.
func TestManagerDisableOfAnUnreadableEnrolmentLeavesTheTunnel(t *testing.T) {
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
	p, err := path()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Disable(context.Background(), false); err == nil {
		t.Fatal("disabling with an unreadable enrolment succeeded")
	}
	time.Sleep(50 * time.Millisecond)
	if st, _ := m.Status(); st.State != StateConnected {
		t.Errorf("after a refused disable the tunnel is %s, want it left as it was", st.State)
	}
}

// Remote access coming on is shown as connecting, never, for an instant, as
// off: the window is told as soon as the tunnel is started.
func TestEnablingIsNeverShownAsOff(t *testing.T) {
	quick(t)
	f := newFakeRelay(t)
	var mu sync.Mutex
	var seen []State
	var cfg *Config
	var m *Manager
	m = NewManager("v", func(l net.Listener) error { return http.Serve(l, http.NotFoundHandler()) }, func() {
		if st, ok := m.Status(); ok {
			mu.Lock()
			seen = append(seen, st.State)
			mu.Unlock()
		}
	})
	m.load = func() (*Config, error) { return cfg, nil }
	defer m.Close()
	c := f.config()
	cfg = &c
	if err := m.Reload(); err != nil {
		t.Fatal(err)
	}
	f.session(t)
	waitFor(t, "connected", func() bool { st, _ := m.Status(); return st.State == StateConnected })
	mu.Lock()
	defer mu.Unlock()
	for _, s := range seen {
		if s == StateOff {
			t.Errorf("remote access coming on was shown as %v", seen)
			break
		}
	}
}

// Moving to another relay while a tunnel is up is shown as connecting to the
// new one, never as the new one not connected before it has been tried.
func TestChangingRelayIsNeverShownAsOff(t *testing.T) {
	quick(t)
	f1, f2 := newFakeRelay(t), newFakeRelay(t)
	var mu sync.Mutex
	var seen []State
	var cfg *Config
	var m *Manager
	m = NewManager("v", func(l net.Listener) error { return http.Serve(l, http.NotFoundHandler()) }, func() {
		if st, ok := m.Status(); ok {
			mu.Lock()
			seen = append(seen, st.State)
			mu.Unlock()
		}
	})
	m.load = func() (*Config, error) { return cfg, nil }
	defer m.Close()
	c1 := f1.config()
	cfg = &c1
	if err := m.Reload(); err != nil {
		t.Fatal(err)
	}
	f1.session(t)
	waitFor(t, "connected to the first relay", func() bool { st, _ := m.Status(); return st.State == StateConnected })
	mu.Lock()
	seen = nil
	mu.Unlock()
	c2 := f2.config()
	cfg = &c2
	if err := m.Reload(); err != nil {
		t.Fatal(err)
	}
	f2.session(t)
	waitFor(t, "connected to the second relay", func() bool {
		st, _ := m.Status()
		return st.State == StateConnected && st.Relay == f2.URL
	})
	mu.Lock()
	defer mu.Unlock()
	for _, s := range seen {
		if s == StateOff {
			t.Errorf("moving to another relay was shown as %v", seen)
			break
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
