package server

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/channel"
	"github.com/jmwri/flockdeck/internal/store"
)

// startChannelFor puts a local channel in front of srv, minting links the way
// the instance does.
func startChannelFor(t *testing.T, srv *Server) *channel.Channel {
	t.Helper()
	c, err := channel.Start(channel.Config{
		StateDir: stateTempDir(t),
		PID:      os.Getpid(),
		Started:  time.Now(),
		Window:   srv.WindowURL,
		Logf:     t.Logf,
	})
	if err != nil {
		t.Fatalf("channel: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func channelWindow(t *testing.T, c *channel.Channel) string {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, "http://channel/window", nil)
	resp, err := channel.Client(c.Path()).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("window = %d: %s", resp.StatusCode, body)
	}
	return string(body)
}

// The link the channel gives is the one the HTTP server's own window request
// gives: used once, and then it opens nothing.
func TestChannelWindowLinkIsSingleUse(t *testing.T) {
	srv, _ := newTestServer(t)
	c := startChannelFor(t, srv)
	link := channelWindow(t, c)
	if strings.Contains(link, srv.Token()) || !strings.HasPrefix(link, srv.BaseURL()+"/?"+linkParam+"=") {
		t.Fatalf("link = %q", strings.ReplaceAll(link, srv.Token(), "<token>"))
	}
	if code := getStatus(t, browserClient(t), link); code != http.StatusOK {
		t.Fatalf("first load = %d, want 200", code)
	}
	if code := getStatus(t, browserClient(t), link); code != http.StatusForbidden {
		t.Fatalf("second load = %d, want 403", code)
	}
	// Every request mints a different link.
	if a, b := channelWindow(t, c), channelWindow(t, c); a == b {
		t.Fatal("two requests got the same link")
	}
}

func TestChannelWindowLinkRunsOut(t *testing.T) {
	srv, _ := newTestServer(t)
	c := startChannelFor(t, srv)

	// A life already over.
	srv.linkLife = -time.Second
	if code := getStatus(t, browserClient(t), channelWindow(t, c)); code != http.StatusForbidden {
		t.Errorf("a link past its life = %d, want 403", code)
	}

	// And one that is waited out.
	srv.linkLife = 50 * time.Millisecond
	link := channelWindow(t, c)
	time.Sleep(200 * time.Millisecond)
	if code := getStatus(t, browserClient(t), link); code != http.StatusForbidden {
		t.Errorf("a link left unused past its life = %d, want 403", code)
	}

	// The default life is the same minute as ever.
	if windowLinkLife != time.Minute {
		t.Errorf("windowLinkLife = %v, want one minute", windowLinkLife)
	}
}

// What the loopback server answers on each route, with and without the token,
// as it was before the channel existed. Adding the channel changes none of it.
// Statuses come from a GET.
var tcpGolden = map[string]string{
	"/":                       "403 200",
	"/assets/app.js":          "403 200",
	"/assets/vendor/xterm.js": "403 200",
	"/ws/control":             "403 426",
	"/ws/pty?id=any":          "403 404",
	"/help.json":              "403 200",
	"/health":                 "403 200",
	"/open?path=/tmp":         "403 405",
	"/quit":                   "403 405",
	"/window":                 "403 405",
	"/remote/reload":          "403 405",
	"/identify":               "404 404",
	"/channel":                "404 404",
}

func TestTCPBehaviourIsUnchangedByTheChannel(t *testing.T) {
	srv, _ := newTestServer(t)
	startChannelFor(t, srv)

	var got []string
	for _, path := range append(append([]string{}, everyRoute...), "/identify", "/channel") {
		sep := "?"
		if strings.Contains(path, "?") {
			sep = "&"
		}
		bare := getStatus(t, http.DefaultClient, srv.baseURL()+path)
		with := getStatus(t, http.DefaultClient, srv.baseURL()+path+sep+"t="+srv.Token())
		got = append(got, fmt.Sprintf("%q: \"%d %d\",", path, bare, with))
		if want := tcpGolden[path]; want != fmt.Sprintf("%d %d", bare, with) {
			t.Errorf("%s: %d %d, golden %q", path, bare, with, want)
		}
	}
	t.Log("\n" + strings.Join(got, "\n"))
}

// instance.json is written by the store and the channel adds nothing to it.
// The bytes are fixed here, field order and layout included.
func TestInstanceJSONIsByteForByteUnchanged(t *testing.T) {
	state := stateTempDir(t)
	t.Setenv("APPDATA", state)
	t.Setenv("XDG_CONFIG_HOME", state)
	t.Setenv("HOME", state)
	goTelemetryOff(t)

	started := time.Date(2026, 10, 8, 9, 30, 0, 0, time.UTC)
	if err := store.SaveInstance(&store.Instance{PID: 4242, URL: "http://127.0.0.1:5555", Token: "tok", Started: started}); err != nil {
		t.Fatal(err)
	}
	dir, err := store.Dir()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "instance.json")
	const golden = `{
  "pid": 4242,
  "url": "http://127.0.0.1:5555",
  "token": "tok",
  "started": "2026-10-08T09:30:00Z"
}`
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, []byte(golden)) {
		t.Fatalf("instance.json = %q, golden %q", before, golden)
	}

	// A channel started in the same state directory leaves it alone.
	c, err := channel.Start(channel.Config{
		StateDir: dir, PID: 4242, Started: started, Window: func() string { return "x" }, Logf: t.Logf,
	})
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(after, before) {
		t.Fatalf("instance.json changed: %q %v", after, err)
	}
	c.Close()
	after, err = os.ReadFile(path)
	if err != nil || !bytes.Equal(after, before) {
		t.Fatalf("instance.json changed on close: %q %v", after, err)
	}
	inst, err := store.LoadInstance()
	if err != nil || inst == nil || inst.Token != "tok" {
		t.Fatalf("LoadInstance = %+v, %v", inst, err)
	}
}
