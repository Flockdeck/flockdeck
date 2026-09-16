package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/jmwri/flockdeck/internal/help"
	"github.com/jmwri/flockdeck/internal/keybindings"
)

// nextKeyTable reads control messages until a keyTable push satisfies cond.
func nextKeyTable(t *testing.T, conn *websocket.Conn, cond func([]keyView) bool) []keyView {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		_, data, err := conn.Read(ctx)
		cancel()
		if err != nil {
			t.Fatalf("read control: %v", err)
		}
		var msg keyTableMsg
		if json.Unmarshal(data, &msg) != nil || msg.Type != "keyTable" {
			continue
		}
		if cond == nil || cond(msg.Keys) {
			return msg.Keys
		}
	}
	t.Fatal("timed out waiting for the expected key table")
	return nil
}

func findKey(keys []keyView, id string) (keyView, bool) {
	for _, k := range keys {
		if k.ID == id {
			return k, true
		}
	}
	return keyView{}, false
}

// A remap reaches every window's key table -- the palette, the tooltips and
// the keydown handling all read it, never a binding of their own -- and is
// kept on disk under keybindings.json.
func TestRemappingAKeybindingBroadcastsAndPersists(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	nextHello(t, conn)

	def, ok := help.Lookup("closePane")
	if !ok {
		t.Fatal("closePane missing from help.Keys")
	}

	sendCmd(t, conn, command{Cmd: "setKeybinding", ID: "closePane", Text: "Ctrl+Alt+W"})
	keys := nextKeyTable(t, conn, func(ks []keyView) bool {
		k, ok := findKey(ks, "closePane")
		return ok && k.Keys == "Ctrl+Alt+W"
	})
	k, _ := findKey(keys, "closePane")
	if !k.Overridden {
		t.Errorf("closePane came back Overridden=false after being remapped: %+v", k)
	}

	f, err := keybindings.Load()
	if err != nil {
		t.Fatalf("keybindings.Load: %v", err)
	}
	if f.Overrides["closePane"] != "Ctrl+Alt+W" {
		t.Errorf("the remap did not reach disk: %+v", f.Overrides)
	}

	sendCmd(t, conn, command{Cmd: "resetKeybinding", ID: "closePane"})
	keys = nextKeyTable(t, conn, func(ks []keyView) bool {
		k, ok := findKey(ks, "closePane")
		return ok && k.Keys == def.Keys
	})
	k, _ = findKey(keys, "closePane")
	if k.Overridden {
		t.Errorf("closePane still reads Overridden after being reset: %+v", k)
	}
}

// A binding that collides with another action's is refused rather than
// silently taking it: two actions can never share a chord.
func TestSetKeybindingRefusesAConflictOverTheWire(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	nextHello(t, conn)

	worktrees, ok := help.Lookup("worktrees")
	if !ok {
		t.Fatal("worktrees missing from help.Keys")
	}
	changes, ok := help.Lookup("changes")
	if !ok {
		t.Fatal("changes missing from help.Keys")
	}

	sendCmd(t, conn, command{Cmd: "setKeybinding", ID: "changes", Text: worktrees.Keys})
	var note noticeMsg
	readUntil(t, conn, "notice", &note)
	if note.Text == "" {
		t.Fatal("no notice text came back for a conflicting remap")
	}

	f, err := keybindings.Load()
	if err != nil {
		t.Fatalf("keybindings.Load: %v", err)
	}
	if _, has := f.Overrides["changes"]; has {
		t.Error("a refused remap was saved anyway")
	}
	if changes.Keys == worktrees.Keys {
		t.Fatal("test fixture assumption broken: changes and worktrees already share a chord")
	}
}

// Reset all puts every action back to its built-in binding in one go.
func TestResetKeybindingsPutsEverythingBack(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	nextHello(t, conn)

	sendCmd(t, conn, command{Cmd: "setKeybinding", ID: "closePane", Text: "Ctrl+Alt+W"})
	nextKeyTable(t, conn, func(ks []keyView) bool {
		k, ok := findKey(ks, "closePane")
		return ok && k.Overridden
	})

	sendCmd(t, conn, command{Cmd: "resetKeybindings"})
	keys := nextKeyTable(t, conn, func(ks []keyView) bool {
		k, ok := findKey(ks, "closePane")
		return ok && !k.Overridden
	})
	for _, k := range keys {
		if k.Overridden {
			t.Errorf("%s still reads Overridden after resetKeybindings: %+v", k.ID, k)
		}
	}
	f, err := keybindings.Load()
	if err != nil {
		t.Fatalf("keybindings.Load: %v", err)
	}
	if len(f.Overrides) != 0 {
		t.Errorf("overrides left on disk after resetKeybindings: %+v", f.Overrides)
	}
}
