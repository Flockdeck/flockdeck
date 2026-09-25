package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/jmwri/flockdeck/internal/creds"
	"github.com/jmwri/flockdeck/internal/jev"
	"github.com/jmwri/flockdeck/internal/store"
)

// fakeJevKey is obviously not a key, and is a fresh random marker each run, so
// that no part of it -- the whole or its tail -- can turn up in a temp path or
// any other field by chance. (A fixed "...0000" tail once matched the digits in
// macOS's /var/folders/.../T/ path, and the test called a state message a leak.)
var fakeJevKey = "FAKE-jev-" + randomMarker()

func randomMarker() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// readAllUntil reads every message up to and including one of type typ, and
// returns them raw, so a test can look for text that must not be in any.
func readAllUntil(t *testing.T, conn *websocket.Conn, typ string) (raw []string, last []byte) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		_, data, err := conn.Read(ctx)
		cancel()
		if err != nil {
			t.Fatalf("read control: %v", err)
		}
		raw = append(raw, string(data))
		var probe struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(data, &probe) == nil && probe.Type == typ {
			return raw, data
		}
	}
	t.Fatalf("timed out waiting for a %q message", typ)
	return nil, nil
}

// At the desk the key can be set, replaced and cleared; the answer says only
// whether it is set, nothing sent to the window holds it, and only keys.json
// in the state directory does.
func TestJevKeyIsSetAndClearedAtTheDesk(t *testing.T) {
	t.Setenv(jev.KeyEnv, "")
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	nextHello(t, conn)

	sendCmd(t, conn, command{Cmd: "jevKey", Kind: "get"})
	var st jevKeyMsg
	readUntil(t, conn, "jevKey", &st)
	if st.Set || st.Env {
		t.Fatalf("a fresh machine reports %+v", st)
	}

	sendCmd(t, conn, command{Cmd: "jevKey", Kind: "set", Text: "  " + fakeJevKey + " "})
	raw, last := readAllUntil(t, conn, "jevKey")
	if err := json.Unmarshal(last, &st); err != nil || !st.Set {
		t.Fatalf("after setting: %s", last)
	}
	for _, m := range raw {
		if strings.Contains(m, fakeJevKey) || strings.Contains(m, fakeJevKey[len(fakeJevKey)-12:]) {
			t.Errorf("a message to the window carries the key: %s", m)
		}
	}
	if got := creds.JevKey(); got != fakeJevKey {
		t.Errorf("the stored key is %q, want it trimmed", got)
	}
	// Setting a key turns nothing on.
	if p := store.LoadPrefs(); p.JevStatus {
		t.Error("setting the key turned on sending terminal output to TypeSafe")
	}
	if names, _ := creds.Names(); len(names) != 0 {
		t.Errorf("the key shows up as an agent's: %v", names)
	}

	// The key is in the one 0600 store and nowhere else under the state dir.
	dir, err := store.Dir()
	if err != nil {
		t.Fatal(err)
	}
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		data, _ := os.ReadFile(p)
		if has := strings.Contains(string(data), fakeJevKey); has != (filepath.Base(p) == "keys.json") {
			t.Errorf("%s holds the key: %v", p, has)
		}
		return nil
	})

	// Replacing it.
	sendCmd(t, conn, command{Cmd: "jevKey", Kind: "set", Text: "FAKE-second"})
	readAllUntil(t, conn, "jevKey")
	if creds.JevKey() != "FAKE-second" {
		t.Error("the key was not replaced")
	}

	sendCmd(t, conn, command{Cmd: "jevKey", Kind: "clear"})
	readAllUntil(t, conn, "jevKey")
	if creds.HasJevKey() {
		t.Error("the key was not cleared")
	}

	// An empty key is refused, and does not clear.
	sendCmd(t, conn, command{Cmd: "jevKey", Kind: "set", Text: "  "})
	var note noticeMsg
	readUntil(t, conn, "notice", &note)
	if !note.Error {
		t.Errorf("an empty key was accepted: %+v", note)
	}
}

// The key from the environment is reported as such, and is not the key set
// in Settings.
func TestJevKeyStateSaysWhereItWouldComeFrom(t *testing.T) {
	t.Setenv(jev.KeyEnv, "FAKE-env-key")
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	nextHello(t, conn)
	sendCmd(t, conn, command{Cmd: "jevKey", Kind: "get"})
	var st jevKeyMsg
	readUntil(t, conn, "jevKey", &st)
	if st.Set || !st.Env {
		t.Errorf("state = %+v, want the environment's key only", st)
	}
}

// A window reached through the relay can neither read, set nor clear the key.
func TestJevKeyIsRefusedThroughTheRelay(t *testing.T) {
	t.Setenv(jev.KeyEnv, "")
	srv, _ := newTestServer(t)
	if err := creds.SetJevKey(fakeJevKey); err != nil {
		t.Fatal(err)
	}
	ts := remoteServer(t, srv)
	phone, err := dialRemoteControl(ts, ts.URL)
	if err != nil {
		t.Fatalf("dial through the tunnel: %v", err)
	}
	defer phone.CloseNow()

	for _, cmd := range []command{
		{Cmd: "jevKey", Kind: "get"},
		{Cmd: "jevKey", Kind: "set", Text: "FAKE-from-phone"},
		{Cmd: "jevKey", Kind: "clear"},
	} {
		sendCmd(t, phone, cmd)
		raw, last := readAllUntil(t, phone, "notice")
		var note noticeMsg
		if err := json.Unmarshal(last, &note); err != nil || !note.Error || !strings.Contains(note.Text, "machine") {
			t.Errorf("%s through the relay was told %s", cmd.Kind, last)
		}
		for _, m := range raw {
			if strings.Contains(m, `"type":"jevKey"`) || strings.Contains(m, fakeJevKey) {
				t.Errorf("%s through the relay was answered: %s", cmd.Kind, m)
			}
		}
	}
	if got := creds.JevKey(); got != fakeJevKey {
		t.Errorf("the key after the relay's attempts is %q, want it untouched", got)
	}
}

// Nothing a window is sent as state -- the preferences, the routing view --
// holds the key.
func TestJevKeyIsNotInStateSnapshots(t *testing.T) {
	srv, _ := newTestServer(t)
	if err := creds.SetJevKey(fakeJevKey); err != nil {
		t.Fatal(err)
	}
	conn := dialControl(t, srv)
	hello := nextHello(t, conn)
	for name, v := range map[string]any{
		"hello": hello, "prefs": prefsMsg{Type: "prefs", Prefs: srv.prefs},
		"routing": routingOf(srv.ws.Catalog(), ""),
		"state":   jevKeyState(),
	} {
		data, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), fakeJevKey) {
			t.Errorf("%s carries the key: %s", name, data)
		}
	}
}
