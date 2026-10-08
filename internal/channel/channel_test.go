package channel

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// start makes a channel in a fresh state directory. change may adjust the
// config first.
func start(t *testing.T, change func(*Config)) *Channel {
	t.Helper()
	cfg := Config{
		StateDir: t.TempDir(),
		PID:      os.Getpid(),
		Started:  time.Now(),
		Version:  "1.2.3",
		Window:   func() string { return "http://127.0.0.1:1/?w=abc" },
		Logf:     t.Logf,
	}
	if change != nil {
		change(&cfg)
	}
	c, err := Start(cfg)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func post(t *testing.T, c *Channel, path, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "http://channel"+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := Client(c.Path()).Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(data)
}

func TestIdentifyRoundTrip(t *testing.T) {
	started := time.Now().Round(0)
	c := start(t, func(cfg *Config) { cfg.Started = started })
	code, body := post(t, c, "/identify", "")
	if code != 200 {
		t.Fatalf("status %d: %s", code, body)
	}
	var id Identity
	if err := json.Unmarshal([]byte(body), &id); err != nil {
		t.Fatal(err)
	}
	if id.App != "flockdeck" || id.PID != os.Getpid() || id.Version != "1.2.3" || !id.Started.Equal(started) {
		t.Fatalf("identify = %+v", id)
	}
}

func TestWindowVerbReturnsTheLinkTheConfigMakes(t *testing.T) {
	n := 0
	c := start(t, func(cfg *Config) {
		cfg.Window = func() string { n++; return "http://127.0.0.1:1/?w=link" + string(rune('0'+n)) }
	})
	_, a := post(t, c, "/window", "")
	_, b := post(t, c, "/window", "")
	if a != "http://127.0.0.1:1/?w=link1" || b != "http://127.0.0.1:1/?w=link2" {
		t.Fatalf("got %q then %q", a, b)
	}
}

// The channel has two verbs and nothing else: not the control socket, not a
// terminal socket, not the HTTP server's own routes, and not the same verbs by
// another method.
func TestNoVerbBeyondIdentifyAndWindow(t *testing.T) {
	c := start(t, nil)
	for _, p := range []string{
		"/", "/ws/control", "/ws/pty", "/health", "/open", "/quit", "/restart",
		"/remote/reload", "/helpers/start", "/helpers/stop", "/helpers/open",
		"/help.json", "/assets/", "/identify/", "/window/x", "/spawn", "/hook",
	} {
		if code, _ := post(t, c, p, ""); code != http.StatusNotFound {
			t.Errorf("POST %s = %d, want 404", p, code)
		}
	}
	for _, p := range []string{"/identify", "/window"} {
		req, _ := http.NewRequest(http.MethodGet, "http://channel"+p, nil)
		resp, err := Client(c.Path()).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("GET %s = %d, want 405", p, resp.StatusCode)
		}
	}
	// A WebSocket upgrade for the control socket is a 404 like any other.
	req, _ := http.NewRequest(http.MethodGet, "http://channel/ws/control", nil)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	resp, err := Client(c.Path()).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("websocket upgrade = %d, want 404", resp.StatusCode)
	}
}

func TestRequestSizeLimits(t *testing.T) {
	c := start(t, nil)
	if code, _ := post(t, c, "/identify", strings.Repeat("x", MaxBodyBytes)); code != 200 {
		t.Errorf("a body at the limit = %d, want 200", code)
	}
	if code, _ := post(t, c, "/identify", strings.Repeat("x", MaxBodyBytes+1)); code != http.StatusRequestEntityTooLarge {
		t.Errorf("a body over the limit = %d, want 413", code)
	}
	if code, _ := post(t, c, "/window", strings.Repeat("x", 1<<20)); code != http.StatusRequestEntityTooLarge {
		t.Errorf("a 1 MiB body = %d, want 413", code)
	}

	conn, err := Dial(c.Path(), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	io.WriteString(conn, "POST /identify HTTP/1.1\r\nHost: channel\r\nX-Pad: "+strings.Repeat("a", 64<<10)+"\r\n\r\n")
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err == nil {
		resp.Body.Close()
		if resp.StatusCode != http.StatusRequestHeaderFieldsTooLarge {
			t.Errorf("64 KiB of header = %d, want 431", resp.StatusCode)
		}
	}
	// A refusal that is just a closed connection is as good.
}

func TestConnectionLimit(t *testing.T) {
	c := start(t, nil)
	var held []net.Conn
	defer func() {
		for _, h := range held {
			h.Close()
		}
	}()
	for i := 0; i < MaxConns; i++ {
		conn, err := Dial(c.Path(), 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		held = append(held, conn)
	}
	// All places are taken, so the next connection is closed on arrival.
	extra, err := Dial(c.Path(), 5*time.Second)
	if err == nil {
		defer extra.Close()
		_ = extra.SetReadDeadline(time.Now().Add(5 * time.Second))
		io.WriteString(extra, "POST /identify HTTP/1.1\r\nHost: channel\r\nContent-Length: 0\r\n\r\n")
		if _, rerr := http.ReadResponse(bufio.NewReader(extra), nil); rerr == nil {
			t.Fatal("a connection over the limit was served")
		}
	}
	// Freeing a place lets the next one in.
	held[0].Close()
	held = held[1:]
	deadline := time.Now().Add(5 * time.Second)
	for {
		code, _ := post(t, c, "/identify", "")
		if code == 200 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no place freed after a connection closed")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A connection whose peer is not this user is closed before a byte is read.
func TestPeerOfAnotherUserCannotConnect(t *testing.T) {
	var asked sync.WaitGroup
	asked.Add(1)
	var once sync.Once
	c := start(t, func(cfg *Config) {
		cfg.Seams.Self = "alice"
		cfg.Seams.ReadPeer = func(net.Conn) (Peer, error) {
			once.Do(asked.Done)
			return Peer{PID: 99, Owner: "mallory"}, nil
		}
	})
	req, _ := http.NewRequest(http.MethodPost, "http://channel/window", nil)
	if resp, err := Client(c.Path()).Do(req); err == nil {
		resp.Body.Close()
		t.Fatalf("another user's request was answered: %d", resp.StatusCode)
	}
	asked.Wait()
	if c.Denied() == 0 {
		t.Fatal("the refusal was not counted")
	}
}

func TestPeerLookupFailureRefuses(t *testing.T) {
	c := start(t, func(cfg *Config) {
		cfg.Seams.ReadPeer = func(net.Conn) (Peer, error) { return Peer{}, errors.New("no credentials") }
	})
	req, _ := http.NewRequest(http.MethodPost, "http://channel/identify", nil)
	if resp, err := Client(c.Path()).Do(req); err == nil {
		resp.Body.Close()
		t.Fatalf("answered although the peer could not be identified: %d", resp.StatusCode)
	}
}

func TestPeerOfThisUserIsServed(t *testing.T) {
	var got Peer
	c := start(t, func(cfg *Config) {
		cfg.Seams.Self = "alice"
		cfg.Seams.ReadPeer = func(net.Conn) (Peer, error) {
			got = Peer{PID: 7, Owner: "alice"}
			return got, nil
		}
	})
	if code, _ := post(t, c, "/identify", ""); code != 200 {
		t.Fatalf("status %d", code)
	}
}

// The operating system's own lookup, not a seam: this process connects to
// itself and is found to be this user.
func TestRealPeerLookupSeesThisProcess(t *testing.T) {
	self, err := selfOwner()
	if err != nil {
		t.Fatal(err)
	}
	var got Peer
	var gerr error
	c := start(t, func(cfg *Config) {
		cfg.Seams.ReadPeer = func(conn net.Conn) (Peer, error) {
			got, gerr = readPeer(conn)
			return got, gerr
		}
	})
	if code, _ := post(t, c, "/identify", ""); code != 200 {
		t.Fatalf("status %d", code)
	}
	if gerr != nil {
		t.Fatalf("readPeer: %v", gerr)
	}
	if got.Owner != self {
		t.Errorf("owner = %q, want %q", got.Owner, self)
	}
	if runtime.GOOS != "darwin" && got.PID != os.Getpid() {
		t.Errorf("pid = %d, want %d", got.PID, os.Getpid())
	}
}

func goroutines() int {
	runtime.GC()
	return runtime.NumGoroutine()
}

// Clients that vanish mid-request, and a channel that is closed with
// connections open, leave no goroutine behind.
func TestNoGoroutinesLeftAfterAbruptClose(t *testing.T) {
	before := goroutines()
	c, err := Start(Config{
		StateDir: t.TempDir(), PID: os.Getpid(), Started: time.Now(),
		Window: func() string { return "x" },
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		conn, err := Dial(c.Path(), 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		switch i % 3 {
		case 0: // half a request, then gone
			io.WriteString(conn, "POST /identify HTTP/1.1\r\nHost: channel\r\nContent-Le")
		case 1: // a whole request, never reads the answer
			io.WriteString(conn, "POST /window HTTP/1.1\r\nHost: channel\r\nContent-Length: 0\r\n\r\n")
		}
		conn.Close()
	}
	// Two more stay open while the channel is closed under them.
	open1, _ := Dial(c.Path(), 5*time.Second)
	open2, _ := Dial(c.Path(), 5*time.Second)
	io.WriteString(open2, "POST /identify HTTP/1.1\r\nHost: ch")
	c.Close()
	if open1 != nil {
		open1.Close()
	}
	if open2 != nil {
		open2.Close()
	}

	deadline := time.Now().Add(5 * time.Second)
	for goroutines() > before {
		if time.Now().After(deadline) {
			buf := make([]byte, 1<<16)
			t.Fatalf("goroutines: %d before, %d after\n%s", before, runtime.NumGoroutine(), buf[:runtime.Stack(buf, true)])
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestCloseIsIdempotentAndRemovesNothingElse(t *testing.T) {
	c := start(t, nil)
	if err := c.Close(); err != nil {
		t.Logf("first close: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
	if _, err := Dial(c.Path(), time.Second); err == nil {
		t.Fatal("still answering after Close")
	}
}

func TestIDIsEightHexDigitsAndPerInstance(t *testing.T) {
	at := time.Unix(1700000000, 5)
	a, b := ID(10, at), ID(11, at)
	if len(a) != 8 || a == b || a != ID(10, at) || a == ID(10, at.Add(1)) {
		t.Fatalf("ID = %q, %q", a, b)
	}
}

func TestStartNeedsAWindowVerb(t *testing.T) {
	if _, err := Start(Config{StateDir: t.TempDir()}); err == nil {
		t.Fatal("started without a window verb")
	}
}
