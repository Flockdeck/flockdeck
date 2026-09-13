package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/jmwri/flockdeck/internal/help"
	"github.com/jmwri/flockdeck/internal/store"
)

// The help pages are behind the same token as everything else, and come back
// rendered rather than as Markdown for the front end to parse.
func TestHelpIsServedToAuthorisedWindows(t *testing.T) {
	srv, _ := newTestServer(t)

	resp, err := http.Get(srv.baseURL() + "/help.json")
	if err != nil {
		t.Fatalf("GET /help.json: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("/help.json without a token = %d, want 403", resp.StatusCode)
	}

	resp, err = http.Get(srv.baseURL() + "/help.json?t=" + srv.Token())
	if err != nil {
		t.Fatalf("GET /help.json with a token: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/help.json = %d, want 200", resp.StatusCode)
	}
	// Through the relay this address outlives the binary that answered it, so
	// a cached copy would be the last version's pages.
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("/help.json Cache-Control = %q, want no-store", cc)
	}

	var body struct {
		Pages []help.Page `json:"pages"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode help: %v", err)
	}
	if len(body.Pages) != len(help.Slugs()) {
		t.Fatalf("got %d pages, want %d", len(body.Pages), len(help.Slugs()))
	}
	for _, p := range body.Pages {
		if p.Title == "" || p.HTML == "" || p.Text == "" {
			t.Errorf("%s came back incomplete: %+v", p.Slug, p)
		}
	}
}

// BenchmarkHelp measures answering /help.json, which a window asks for once
// per page load and the first one opens unasked.
func BenchmarkHelp(b *testing.B) {
	if _, err := help.Pages(); err != nil {
		b.Fatal(err)
	}
	s := &Server{token: "t"}
	req := httptest.NewRequest(http.MethodGet, "/help.json?t=t", nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.handleHelp(httptest.NewRecorder(), req)
	}
}

// nextHello reads control messages until the opening hello arrives.
func nextHello(t *testing.T, conn *websocket.Conn) helloMsg {
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
		if json.Unmarshal(data, &probe) != nil || probe.Type != "hello" {
			continue
		}
		var hello helloMsg
		if err := json.Unmarshal(data, &hello); err != nil {
			t.Fatalf("decode hello: %v", err)
		}
		return hello
	}
	t.Fatal("timed out waiting for the hello")
	return helloMsg{}
}

// The palette is built from the key table, so a window that never received it
// would have no commands at all. It has to arrive unprompted, on connect.
func TestHelloCarriesTheKeyTable(t *testing.T) {
	srv, _ := newTestServer(t)
	hello := nextHello(t, dialControl(t, srv))

	if len(hello.Keys) != len(help.Keys) {
		t.Fatalf("got %d actions, want %d", len(hello.Keys), len(help.Keys))
	}
	if hello.Keys[0].ID == "" || hello.Keys[0].Label == "" {
		t.Errorf("the key table arrived without ids or labels: %+v", hello.Keys[0])
	}
	if hello.Prefs.HelpSeen {
		t.Error("a fresh install should not report the help as already seen")
	}
}

// The first-run welcome and the inline hints are remembered on this side,
// because the front end has nowhere durable to keep them: the port changes
// every run, so the browser treats each run as a different origin.
func TestPrefsArePersistedAndBroadcast(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	nextHello(t, conn)

	sendCmd(t, conn, command{Cmd: "helpSeen"})
	sendCmd(t, conn, command{Cmd: "dismissTip", ID: "welcome"})

	got := nextPrefs(t, conn, func(p store.Prefs) bool {
		return p.HelpSeen && p.Dismissed("welcome")
	})
	if !got.HelpSeen || !got.Dismissed("welcome") {
		t.Fatalf("prefs were not applied: %+v", got)
	}

	if saved := store.LoadPrefs(); !saved.HelpSeen || !saved.Dismissed("welcome") {
		t.Errorf("prefs did not reach the disk: %+v", saved)
	}
}

// nextPrefs reads control messages until a prefs push satisfies cond.
func nextPrefs(t *testing.T, conn *websocket.Conn, cond func(store.Prefs) bool) store.Prefs {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		_, data, err := conn.Read(ctx)
		cancel()
		if err != nil {
			t.Fatalf("read control: %v", err)
		}
		var msg prefsMsg
		if json.Unmarshal(data, &msg) != nil || msg.Type != "prefs" {
			continue
		}
		if cond == nil || cond(msg.Prefs) {
			return msg.Prefs
		}
	}
	t.Fatal("timed out waiting for the expected prefs")
	return store.Prefs{}
}
