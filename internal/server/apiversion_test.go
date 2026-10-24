package server

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// nextOfType reads control messages until one of the type arrives.
func nextOfType(t *testing.T, conn *websocket.Conn, typ string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		_, data, err := conn.Read(ctx)
		cancel()
		if err != nil {
			t.Fatalf("read control waiting for %s: %v", typ, err)
		}
		var m map[string]any
		if json.Unmarshal(data, &m) == nil && m["type"] == typ {
			return m
		}
	}
	t.Fatalf("no %s message arrived", typ)
	return nil
}

// noneOfType reports whether no message of the type arrives within d.
func noneOfType(conn *websocket.Conn, typ string, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithDeadline(context.Background(), deadline)
		_, data, err := conn.Read(ctx)
		cancel()
		if err != nil {
			return true
		}
		var m map[string]any
		if json.Unmarshal(data, &m) == nil && m["type"] == typ {
			return false
		}
	}
	return true
}

func writeFrame(t *testing.T, conn *websocket.Conn, v any) {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
		t.Fatalf("write frame: %v", err)
	}
}

func TestHelloCarriesTheAPIVersionAndFeatures(t *testing.T) {
	srv, _ := newTestServer(t)
	hello := nextHello(t, dialControl(t, srv))
	if hello.API == nil {
		t.Fatal("the hello has no api block")
	}
	if hello.API.Major != APIMajor || hello.API.Minor != APIMinor {
		t.Errorf("api version = %d.%d, want %d.%d", hello.API.Major, hello.API.Minor, APIMajor, APIMinor)
	}
	for _, want := range []string{"clientHello", "unsupported", "apiMismatch"} {
		if !slices.Contains(hello.API.Caps, want) {
			t.Errorf("caps %v lack %q", hello.API.Caps, want)
		}
	}
	// A hello is made fresh each time, and what it carries is a copy.
	a := currentAPI()
	a.Caps[0] = "changed"
	if apiCaps[0] == "changed" {
		t.Error("the hello shares the server's list of caps")
	}
}

func TestClientHelloOfTheSameMajorIsNotAnswered(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	nextHello(t, conn)
	writeFrame(t, conn, clientHelloMsg{Type: "clientHello", API: apiVersion{Major: APIMajor, Minor: APIMinor + 5}, Client: "desktop-ui"})
	if !noneOfType(conn, "apiMismatch", 300*time.Millisecond) {
		t.Fatal("a newer minor of the same major was told it did not match")
	}
}

func TestClientHelloOfAnotherMajorIsToldAndStaysConnected(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	nextHello(t, conn)
	writeFrame(t, conn, clientHelloMsg{Type: "clientHello", API: apiVersion{Major: APIMajor + 1, Minor: 2}, Client: "desktop-ui"})
	m := nextOfType(t, conn, "apiMismatch")
	server, _ := m["server"].(map[string]any)
	client, _ := m["client"].(map[string]any)
	if int(server["major"].(float64)) != APIMajor || int(client["major"].(float64)) != APIMajor+1 || int(client["minor"].(float64)) != 2 {
		t.Errorf("apiMismatch = %v", m)
	}
	if s, _ := m["text"].(string); s == "" {
		t.Error("apiMismatch has no sentence")
	}
	// The socket is still in use: a command is still handled, and an unknown one still answered.
	sendCmd(t, conn, command{Cmd: "noSuchCommand"})
	nextOfType(t, conn, "unsupported")
}

func TestAnUnknownCommandIsAnsweredUnsupportedAndAKnownOneIsNot(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	nextHello(t, conn)

	sendCmd(t, conn, command{Cmd: "noSuchCommand"})
	m := nextOfType(t, conn, "unsupported")
	if m["cmd"] != "noSuchCommand" {
		t.Errorf("unsupported names %v", m["cmd"])
	}
	if s, _ := m["text"].(string); !strings.Contains(s, "noSuchCommand") {
		t.Errorf("unsupported says %q", s)
	}

	// The name is the window's own text coming back, so it is clamped.
	long := strings.Repeat("x", 500)
	sendCmd(t, conn, command{Cmd: long})
	m = nextOfType(t, conn, "unsupported")
	if got, _ := m["cmd"].(string); len([]rune(got)) > 25 || !strings.HasPrefix(got, "xxxx") {
		t.Errorf("a long name came back as %q", got)
	}
	if s, _ := m["text"].(string); len(s) > 200 {
		t.Errorf("the sentence repeats the whole name: %d characters", len(s))
	}

	// A command that is known is handled as before, and a frame with no command is left alone.
	sendCmd(t, conn, command{Cmd: "helpSeen"})
	writeFrame(t, conn, map[string]any{"hello": "there"})
	if !noneOfType(conn, "unsupported", 300*time.Millisecond) {
		t.Error("a known command or a frame with no command was answered unsupported")
	}
}

func TestClientHelloIsNotACommandAndTheNewMessagesReachTheRelay(t *testing.T) {
	if isKnownCommand("clientHello") || isKnownCommand("") {
		t.Error("clientHello is a frame, not a command")
	}
	for name := range commandAccess {
		if !isKnownCommand(name) {
			t.Errorf("%s is in commandAccess and is not known", name)
		}
	}
	for _, typ := range []string{"apiMismatch", "unsupported"} {
		data, _ := json.Marshal(map[string]any{"type": typ, "cmd": "x"})
		if got := scrubForRemote(data); got == nil {
			t.Errorf("%s is not sent to a relay window", typ)
		}
		if !viewerMessageTypes[typ] {
			t.Errorf("%s is not sent to a viewer", typ)
		}
	}
}
