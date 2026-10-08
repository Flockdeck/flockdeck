package server

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/workspace"
)

// The default set answers to 127.0.0.1 and localhost on any port and to
// nothing else.
func TestHostAllowDefault(t *testing.T) {
	a, err := newHostAllow("")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		host string
		want bool
	}{
		{"127.0.0.1", true},
		{"127.0.0.1:1", true},
		{"127.0.0.1:54321", true},
		{"127.0.0.1:65535", true},
		{"127.0.0.1.", true},
		{"127.0.0.1.:8080", true},
		{"localhost", true},
		{"localhost:8080", true},
		{"LOCALHOST", true},
		{"LocalHost:1", true},
		{"localhost.", true},
		{"localhost.:8080", true},
		// A name that only starts or ends with localhost is another name.
		{"localhost.evil.example", false},
		{"localhost.evil.example:8080", false},
		{"xlocalhost", false},
		{"xlocalhost:8080", false},
		{"localhost1", false},
		{"my-localhost", false},
		{"evil.example.localhost", false},
		{"localhost..", false},
		{"localhost@evil.example", false},
		{"localhost:80@evil.example", false},
		// The listener is IPv4 only, so [::1] is not on by default.
		{"[::1]", false},
		{"[::1]:8080", false},
		{"127.0.0.2", false},
		{"127.1", false},
		{"0x7f.0.0.1", false},
		{"2130706433", false},
		{"0.0.0.0", false},
		{"[::ffff:127.0.0.1]", false},
		{"[::ffff:127.0.0.1]:80", false},
		{"evil.example", false},
		{"evil.example:8080", false},
		{"127.0.0.1.evil.example", false},
		{"127.0.0.1.evil.example:80", false},
		{"evil127.0.0.1", false},
		// Not a Host at all.
		{"", false},
		{" ", false},
		{" 127.0.0.1", false},
		{"127.0.0.1 ", false},
		{"127.0.0.1:", false},
		{"127.0.0.1:0", false},
		{"127.0.0.1:65536", false},
		{"127.0.0.1:99999999", false},
		{"127.0.0.1:-1", false},
		{"127.0.0.1:+1", false},
		{"127.0.0.1:80a", false},
		{"127.0.0.1:80:80", false},
		{"127.0.0.1@evil.example", false},
		{"evil.example@127.0.0.1", false},
		{"user:pw@127.0.0.1", false},
		{"user@127.0.0.1:8080", false},
		{"127.0.0.1/", false},
		{"127.0.0.1/path", false},
		{"127.0.0.1:8080/path", false},
		{"127.0.0.1?x=1", false},
		{"127.0.0.1#frag", false},
		{`127.0.0.1\@evil.example`, false},
		{"http://127.0.0.1", false},
		{"127.0.0.1\r\nX: y", false},
		{"127.0.0.1\x00", false},
		{"127.0.0.1..", false},
		{".127.0.0.1", false},
		{"127.0.0.1,evil.example", false},
		{"*", false},
		{"127.0.0.1", true},
		{"127.0.0.é", false},
	} {
		if got := a.allows(tc.host); got != tc.want {
			t.Errorf("default set allows(%q) = %v, want %v", tc.host, got, tc.want)
		}
	}
}

// What the variable adds: bare hosts match on any port, host:port matches one.
func TestHostAllowList(t *testing.T) {
	a, err := newHostAllow(" flockdeck.example.com , Svc.NS.svc.,flockdeck:8080,[::1], [2001:DB8::1]:9000 ,,10.0.0.5, flockdeck:8080 ,flockdeck.example.com")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		host string
		want bool
	}{
		// A bare host takes every port, and no port, and any case.
		{"flockdeck.example.com", true},
		{"flockdeck.example.com:80", true},
		{"flockdeck.example.com:8443", true},
		{"FLOCKDECK.Example.COM", true},
		{"flockdeck.example.com.", true},
		{"flockdeck.example.com.:8443", true},
		{"svc.ns.svc", true},
		{"SVC.NS.SVC:3000", true},
		// A host:port entry takes that port only.
		{"flockdeck:8080", true},
		{"FLOCKDECK:8080", true},
		{"flockdeck.:8080", true},
		{"flockdeck:8081", false},
		{"flockdeck:80", false},
		{"flockdeck", false},
		// Names that merely contain an entry, or sit above or below it, are
		// other names.
		{"example.com", false},
		{"www.flockdeck.example.com", false},
		{"flockdeck.example.com.evil.example", false},
		{"xflockdeck.example.com", false},
		{"flockdeck.example.org", false},
		{"svc.ns.svc.cluster.local", false},
		{"svc", false},
		// IPv6 in brackets, in any spelling of the same address.
		{"[::1]", true},
		{"[::1]:8080", true},
		{"[0:0:0:0:0:0:0:1]", true},
		{"[0000:0000:0000:0000:0000:0000:0000:0001]:5", true},
		{"[::01]", true},
		{"::1", false},
		{"[::1", false},
		{"[::1]x", false},
		{"[::1]:", false},
		{"[::1%eth0]", false},
		{"[::2]", false},
		{"[2001:db8::1]:9000", true},
		{"[2001:DB8::1]:9000", true},
		{"[2001:db8:0::1]:9000", true},
		{"[2001:db8::1]:9001", false},
		{"[2001:db8::1]", false},
		{"[127.0.0.1]", false},
		// An IPv4 entry.
		{"10.0.0.5", true},
		{"10.0.0.5:3000", true},
		{"10.0.0.6", false},
		// The default is still there.
		{"127.0.0.1", true},
		{"127.0.0.1:1234", true},
		{"localhost", true},
		{"localhost:3000", true},
		{"[::1]:3000", true},
	} {
		if got := a.allows(tc.host); got != tc.want {
			t.Errorf("allows(%q) = %v, want %v", tc.host, got, tc.want)
		}
	}
}

// A port-only entry does not widen to every port, and an IPv6 host:port entry
// is not read as a bare host.
func TestHostAllowExactEntriesStayExact(t *testing.T) {
	a, err := newHostAllow("only.example:8080,[::1]:9000")
	if err != nil {
		t.Fatal(err)
	}
	for host, want := range map[string]bool{
		"only.example:8080": true,
		"only.example":      false,
		"only.example:1":    false,
		"[::1]:9000":        true,
		"[::1]":             false,
		"[::1]:9001":        false,
	} {
		if got := a.allows(host); got != want {
			t.Errorf("allows(%q) = %v, want %v", host, got, want)
		}
	}
}

// Entries that are not a host or host:port are refused at start-up, with the
// variable's name and the entry in the message.
func TestHostAllowRejectsBadEntries(t *testing.T) {
	for _, bad := range []string{
		"*",
		"*.example.com",
		"example.*",
		"http://example.com",
		"https://example.com:8443",
		"example.com/",
		"example.com/path",
		"user@example.com",
		"example.com:",
		"example.com:0",
		"example.com:65536",
		"example.com:http",
		"example.com:80:80",
		"::1",
		"[::1",
		"[::1]:x",
		"[::1%lo]",
		"[example.com]",
		"exa mple.com",
		"example..com",
		".example.com",
		"example.com..",
		"exämple.com",
		"good.example,bad host",
		"good.example,*",
	} {
		_, err := newHostAllow(bad)
		if err == nil {
			t.Errorf("newHostAllow(%q) = nil error, want one", bad)
			continue
		}
		if !strings.Contains(err.Error(), AllowedHostsEnv) {
			t.Errorf("newHostAllow(%q) error %q does not name %s", bad, err, AllowedHostsEnv)
		}
	}
	// Blank pieces are skipped, not errors.
	for _, ok := range []string{"", " ", ",", " , ,", "a.example,", ",a.example"} {
		if _, err := newHostAllow(ok); err != nil {
			t.Errorf("newHostAllow(%q) = %v, want none", ok, err)
		}
	}
}

// A long or hostile value is not echoed whole.
func TestCleanHost(t *testing.T) {
	if got := cleanHost("evil\r\nSet-Cookie: x\x00\xff.example"); strings.ContainsAny(got, "\r\n\x00\xff") {
		t.Errorf("cleanHost left a control or non-ASCII byte in %q", got)
	}
	long := strings.Repeat("a", 5000)
	if got := cleanHost(long); len(got) > 70 {
		t.Errorf("cleanHost kept %d bytes of a long value", len(got))
	}
	if got := cleanHost("Example.com:80"); got != "Example.com:80" {
		t.Errorf("cleanHost changed a plain value to %q", got)
	}
}

// Only the first refusal in an interval is written, and it carries the count
// of those skipped.
func TestHostRefusalsAreRateLimited(t *testing.T) {
	var h hostRefusals
	now := time.Now()
	line, ok := h.note(now, "a.example")
	if !ok || !strings.Contains(line, "a.example") || !strings.Contains(line, AllowedHostsEnv) {
		t.Fatalf("first refusal = %q, %v; want a line naming the host and %s", line, ok, AllowedHostsEnv)
	}
	for i := 0; i < 1000; i++ {
		if _, ok := h.note(now.Add(time.Duration(i)*time.Millisecond), "b.example"); ok {
			t.Fatalf("refusal %d inside the interval was written", i)
		}
	}
	line, ok = h.note(now.Add(hostRefusalLogEvery+time.Second), "c.example")
	if !ok || !strings.Contains(line, "c.example") || !strings.Contains(line, "1000 more") {
		t.Fatalf("refusal after the interval = %q, %v; want a line with the 1000 skipped", line, ok)
	}
}

// getWithHost fetches path from srv with the Host header set to host.
func getWithHost(t *testing.T, srv *Server, path, host string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, srv.baseURL()+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = host
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s with Host %q: %v", path, host, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp, string(body)
}

// upgradeWithHost sends a WebSocket handshake for path to srv with the given
// Host and, when not empty, Origin, and returns the response.
func upgradeWithHost(t *testing.T, srv *Server, addr, path, host, origin string) *http.Response {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	var b strings.Builder
	b.WriteString("GET " + path + " HTTP/1.1\r\n")
	b.WriteString("Host: " + host + "\r\n")
	b.WriteString("Connection: Upgrade\r\nUpgrade: websocket\r\n")
	b.WriteString("Sec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n")
	if origin != "" {
		b.WriteString("Origin: " + origin + "\r\n")
	}
	b.WriteString("\r\n")
	if _, err := conn.Write([]byte(b.String())); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("read the answer to the upgrade with Host %q: %v", host, err)
	}
	return resp
}

// A request whose Host is not one the server answers to is refused, with the
// right token and without it, on every route, and the refusal says how to
// change that.
func TestForeignHostIsRefusedOnEveryRoute(t *testing.T) {
	srv, _ := newTestServer(t)
	for _, host := range []string{
		"evil.example",
		"evil.example:" + portOf(srv),
		"localhost.evil.example:" + portOf(srv),
		"xlocalhost",
		"127.0.0.1.evil.example:" + portOf(srv),
		"127.0.0.1@evil.example",
		"127.0.0.1/x",
		"[::1]:" + portOf(srv),
	} {
		for _, path := range everyRoute {
			for _, creds := range []string{"", "t=" + srv.Token()} {
				p := path
				if creds != "" {
					if strings.Contains(p, "?") {
						p += "&" + creds
					} else {
						p += "?" + creds
					}
				}
				resp, body := getWithHost(t, srv, p, host)
				if resp.StatusCode != http.StatusForbidden {
					t.Errorf("GET %s with Host %q = %d, want 403", p, host, resp.StatusCode)
					continue
				}
				if !strings.Contains(body, AllowedHostsEnv) {
					t.Errorf("the 403 for Host %q does not name %s: %q", host, AllowedHostsEnv, body)
				}
				if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
					t.Errorf("the 403 for Host %q has Content-Type %q, want text/plain", host, ct)
				}
				if len(resp.Cookies()) != 0 {
					t.Errorf("the 403 for Host %q set a cookie", host)
				}
			}
		}
	}
}

func portOf(srv *Server) string {
	_, port, _ := net.SplitHostPort(srv.Addr())
	return port
}

// The names the server is meant to answer to still work: the page, and /health
// with the loopback Host the container's healthcheck uses.
func TestLoopbackHostIsServed(t *testing.T) {
	srv, _ := newTestServer(t)
	for _, host := range []string{srv.Addr(), "127.0.0.1", "127.0.0.1:1", "127.0.0.1.:" + portOf(srv), "localhost:" + portOf(srv), "LOCALHOST", "localhost."} {
		resp, _ := getWithHost(t, srv, "/?t="+srv.Token(), host)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET / with Host %q = %d, want 200", host, resp.StatusCode)
		}
		resp, _ = getWithHost(t, srv, "/health?t="+srv.Token(), host)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET /health with Host %q = %d, want 200", host, resp.StatusCode)
		}
	}
}

// A request with no Host header at all (HTTP/1.0) has an empty Host and is
// refused.
func TestMissingHostIsRefused(t *testing.T) {
	srv, _ := newTestServer(t)
	conn, err := net.DialTimeout("tcp", srv.Addr(), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := conn.Write([]byte("GET /health?t=" + srv.Token() + " HTTP/1.0\r\n\r\n")); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("a request with no Host = %d, want 403", resp.StatusCode)
	}
}

// The case the Origin check does not cover. A page that reached this port by
// another name sends that name as Host, and as the host of its Origin, so the
// WebSocket library, which compares the two, would accept it. The Host check
// refuses it before the upgrade, on both sockets and even with the token.
func TestSocketsRefuseAForeignHostWhoseOriginMatches(t *testing.T) {
	srv, _ := newTestServer(t)
	for _, tc := range []struct {
		path string
	}{
		{"/ws/control?t=" + srv.Token()},
		{"/ws/pty?id=any&t=" + srv.Token()},
	} {
		host := "evil.example:" + portOf(srv)
		resp := upgradeWithHost(t, srv, srv.Addr(), tc.path, host, "http://"+host)
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("upgrade %s with Host and Origin %q = %d, want 403", tc.path, host, resp.StatusCode)
		}
		// With no Origin the library would accept it; the Host check does not.
		resp = upgradeWithHost(t, srv, srv.Addr(), tc.path, host, "")
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("upgrade %s with Host %q and no Origin = %d, want 403", tc.path, host, resp.StatusCode)
		}
	}
}

// The window's own handshake still goes through: the loopback Host, with an
// Origin that matches it.
func TestControlSocketUpgradesOnTheLoopbackHost(t *testing.T) {
	srv, _ := newTestServer(t)
	host := srv.Addr()
	resp := upgradeWithHost(t, srv, srv.Addr(), "/ws/control?t="+srv.Token(), host, "http://"+host)
	resp.Body.Close()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Errorf("upgrade on Host %q = %d, want 101", host, resp.StatusCode)
	}
	// The ordinary client the other tests use, which sends the same.
	conn := dialControl(t, srv)
	_ = conn
}

// Names added through the variable are answered, and only those.
func TestAllowedHostsFromTheEnvironment(t *testing.T) {
	t.Setenv(AllowedHostsEnv, "Flockdeck.Example.com, svc:8080")
	srv, _ := newTestServer(t)
	port := portOf(srv)
	for host, want := range map[string]int{
		"flockdeck.example.com:" + port: http.StatusOK,
		"FLOCKDECK.example.com":         http.StatusOK,
		"svc:8080":                      http.StatusOK,
		"svc:8081":                      http.StatusForbidden,
		"svc":                           http.StatusForbidden,
		"localhost:" + port:             http.StatusOK,
		"evil.example":                  http.StatusForbidden,
		"127.0.0.1:" + port:             http.StatusOK,
	} {
		resp, _ := getWithHost(t, srv, "/?t="+srv.Token(), host)
		if resp.StatusCode != want {
			t.Errorf("GET / with Host %q = %d, want %d", host, resp.StatusCode, want)
		}
	}
	// A page on an added name can open the socket, since its Origin is its Host.
	host := "flockdeck.example.com"
	resp := upgradeWithHost(t, srv, srv.Addr(), "/ws/control?t="+srv.Token(), host, "http://"+host)
	resp.Body.Close()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Errorf("upgrade on the added Host %q = %d, want 101", host, resp.StatusCode)
	}
	// And the token is still needed on it.
	resp = upgradeWithHost(t, srv, srv.Addr(), "/ws/control", host, "http://"+host)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("upgrade on the added Host %q without the token = %d, want 403", host, resp.StatusCode)
	}
}

// A mistake in the variable stops the server from starting, and says which
// variable.
func TestBadAllowedHostsStopsTheServer(t *testing.T) {
	dir := stateTempDir(t)
	t.Setenv("APPDATA", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)
	goTelemetryOff(t)
	_ = t.TempDir()
	ws, err := workspace.New(workspace.Options{Root: stateTempDir(t)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ws.Close)
	t.Setenv(AllowedHostsEnv, "*.example.com")
	srv, err := New(ws)
	if err == nil {
		srv.Close()
		t.Fatal("New accepted a wildcard in " + AllowedHostsEnv)
	}
	if !strings.Contains(err.Error(), AllowedHostsEnv) || !strings.Contains(err.Error(), "*.example.com") {
		t.Errorf("New error %q does not name the variable and the entry", err)
	}
}

// A window reached through the relay is not held to the loopback names. Its
// Host is whatever the tunnel forwards, it is let in by the relay's own check,
// and it still works with a Host that the loopback listener would refuse.
func TestRemoteWindowIsNotHeldToTheLoopbackHosts(t *testing.T) {
	srv, _ := newTestServer(t)
	ts := remoteServer(t, srv)
	for _, host := range []string{"relay.example", "relay.example:443", "localhost", "evil.example"} {
		req, err := http.NewRequest(http.MethodGet, ts.URL+"/", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Host = host
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET / through the tunnel with Host %q = %d, want 200", host, resp.StatusCode)
		}
	}
	// The same page on the loopback port with that Host is refused.
	resp, _ := getWithHost(t, srv, "/?t="+srv.Token(), "relay.example")
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("GET / on the loopback port with Host relay.example = %d, want 403", resp.StatusCode)
	}
	// And its socket opens with a Host that is not loopback.
	tsAddr := strings.TrimPrefix(ts.URL, "http://")
	up := upgradeWithHost(t, srv, tsAddr, "/ws/control", "relay.example", "")
	up.Body.Close()
	if up.StatusCode != http.StatusSwitchingProtocols {
		t.Errorf("upgrade through the tunnel with Host relay.example = %d, want 101", up.StatusCode)
	}
}

// The list the Helm chart builds for a release called flockdeck in namespace
// tools, with a Service on port 8080, covers every way a client reaches the
// Service, and a port-forward to a local port of the user's choosing.
func TestHostAllowChartList(t *testing.T) {
	a, err := newHostAllow("flockdeck,flockdeck.tools,flockdeck.tools.svc,flockdeck.tools.svc.cluster.local,flockdeck.example.com")
	if err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{
		"flockdeck:8080", "flockdeck.tools:8080", "flockdeck.tools.svc:8080",
		"flockdeck.tools.svc.cluster.local:8080", "flockdeck.tools.svc.cluster.local",
		"flockdeck.tools.svc.cluster.local.:8080", "localhost:9000", "127.0.0.1:9000",
		"flockdeck.example.com", "flockdeck.example.com:443",
	} {
		if !a.allows(host) {
			t.Errorf("chart list does not allow %q", host)
		}
	}
	for _, host := range []string{"flockdeck.other.svc:8080", "other.tools.svc", "evil.example"} {
		if a.allows(host) {
			t.Errorf("chart list allows %q", host)
		}
	}
}

// The refusal page does not repeat the Host header's bytes as they came. Go's
// own server rejects most bad ones first, so this drives the handler directly.
func TestRefusalDoesNotEchoControlBytes(t *testing.T) {
	a, _ := newHostAllow("")
	s := &Server{hosts: a}
	called := false
	h := s.guardHosts(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	req, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1/", nil)
	req.Host = "evil\r\nSet-Cookie: a=b\x00\xff" + strings.Repeat("x", 500)
	rec := &recorder{header: http.Header{}}
	h.ServeHTTP(rec, req)
	if called {
		t.Fatal("the handler behind the guard was called")
	}
	if rec.code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.code)
	}
	if strings.ContainsAny(rec.body.String(), "\x00\xff") || strings.Contains(rec.body.String(), "\r") {
		t.Errorf("the refusal repeats raw bytes from the Host header: %q", rec.body.String())
	}
	if rec.body.Len() > 600 {
		t.Errorf("the refusal is %d bytes; a long Host header should be cut", rec.body.Len())
	}
	if rec.header.Get("X-Content-Type-Options") != "nosniff" {
		t.Error("the refusal does not set X-Content-Type-Options: nosniff")
	}
}

type recorder struct {
	header http.Header
	body   strings.Builder
	code   int
}

func (r *recorder) Header() http.Header         { return r.header }
func (r *recorder) WriteHeader(c int)           { r.code = c }
func (r *recorder) Write(p []byte) (int, error) { return r.body.Write(p) }

// Names the server does not answer to by default are refused on both sockets
// with a 403 before the upgrade: [::1] (the listener is IPv4 only), a host name
// and a LAN address, each with an Origin equal to its Host. Each must come
// back as an answer, not as a failed connection.
func TestSocketsRefuseNamesOutsideTheDefault(t *testing.T) {
	srv, _ := newTestServer(t)
	for _, host := range []string{
		"[::1]:" + portOf(srv),
		"[::1]",
		"flockdeck.example.com:" + portOf(srv),
		"192.168.1.20:" + portOf(srv),
		"localhost.evil.example:" + portOf(srv),
	} {
		for _, path := range []string{"/ws/control?t=" + srv.Token(), "/ws/pty?id=any&t=" + srv.Token()} {
			resp := upgradeWithHost(t, srv, srv.Addr(), path, host, "http://"+host)
			resp.Body.Close()
			if resp.StatusCode != http.StatusForbidden {
				t.Errorf("upgrade %s with Host %q = %d, want 403", path, host, resp.StatusCode)
			}
		}
	}
}
