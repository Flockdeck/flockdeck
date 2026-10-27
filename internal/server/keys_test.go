package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/creds"
	"github.com/jmwri/flockdeck/internal/store"
)

// TestUnreadableKeyStoreIsReported covers a keys.json that no longer parses --
// a hand edit with a trailing comma, say. Every stored key then reads as not
// set, and the dialog looked exactly like one on a machine with no keys.
func TestUnreadableKeyStoreIsReported(t *testing.T) {
	srv, _ := newTestServer(t)
	dir, err := store.Dir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "keys.json"), []byte(`{"anthropic": "sk-",`), 0o600); err != nil {
		t.Fatal(err)
	}
	conn := dialControl(t, srv)
	sendCmd(t, conn, command{Cmd: "keys"})
	var note noticeMsg
	readUntil(t, conn, "notice", &note)
	if !note.Error || !strings.Contains(note.Text, "keys.json") {
		t.Fatalf("an unreadable key store was answered %+v, want an error naming it", note)
	}
}

// TestKeysDialogIsAnswered covers the API keys dialog, which asked a server
// that had no answer for it and sat on "Loading…" for good -- leaving the
// command line as the only way to give an API agent its key. The built-in agents
// that take a key are hidden for now, so the dialog starts empty; a key stored
// for one is listed on a row of its own, where it can be cleared.
func TestKeysDialogIsAnswered(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)

	sendCmd(t, conn, command{Cmd: "keys"})
	var msg keysMsg
	readUntil(t, conn, "keys", &msg)
	if len(msg.Items) != 0 {
		t.Fatalf("the dialog was offered %+v, though every agent that takes a key is hidden", msg.Items)
	}
	const agentID = "gemini-acp"

	find := func(m keysMsg) creds.Status {
		for _, st := range m.Items {
			if st.Agent == agentID {
				return st
			}
		}
		t.Fatalf("%s is missing from %+v", agentID, m.Items)
		return creds.Status{}
	}

	sendCmd(t, conn, command{Cmd: "keySet", ID: agentID, Text: "sk-test-not-a-real-key"})
	readUntil(t, conn, "keys", &msg)
	if st := find(msg); !st.Set || st.Source != creds.SourceStore || st.Name == "" {
		t.Fatalf("after setting a key the dialog shows %+v, want it stored, on the agent's own row", st)
	}

	sendCmd(t, conn, command{Cmd: "keyClear", ID: agentID})
	readUntil(t, conn, "keys", &msg)
	for _, st := range msg.Items {
		if st.Agent == agentID {
			t.Fatalf("after clearing the key the dialog shows %+v", st)
		}
	}
}

// A key is saved only for an agent in the catalog that takes one. One stored
// under any other id is read by nothing, and the dialog would say it was saved.
func TestAKeyForAnAgentThatTakesNoneIsRefused(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	for _, id := range []string{"no-such-agent", "opencode-acp", "claude", "", "@typesafe"} {
		sendCmd(t, conn, command{Cmd: "keySet", ID: id, Text: "sk-test-not-a-real-key"})
		var note noticeMsg
		readUntil(t, conn, "notice", &note)
		if !note.Error || note.Text != "That agent does not take a key" {
			t.Errorf("a key for %q was answered %+v", id, note)
		}
	}
	if names, _ := creds.Names(); len(names) != 0 {
		t.Errorf("the store holds %v after refused saves", names)
	}
}

// A hidden agent that takes a key gets a row once it has a stored key, so the
// key can be changed or cleared; without one it has none.
func TestAHiddenKeyedAgentIsListedWhenItHasAKey(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	sendCmd(t, conn, command{Cmd: "keys"})
	var msg keysMsg
	readUntil(t, conn, "keys", &msg)
	if len(msg.Items) != 0 {
		t.Fatalf("rows without a key: %+v", msg.Items)
	}
	if err := creds.Set("codex-acp", "sk-test-not-a-real-key"); err != nil {
		t.Fatal(err)
	}
	sendCmd(t, conn, command{Cmd: "keys"})
	readUntil(t, conn, "keys", &msg)
	if len(msg.Items) != 1 || msg.Items[0].Agent != "codex-acp" || msg.Items[0].Name != "Codex (structured)" ||
		!msg.Items[0].Set || !msg.Items[0].Stored {
		t.Fatalf("rows = %+v, want codex-acp on its own row with its name", msg.Items)
	}
}

// A key that cannot be an environment value is refused with a fixed text and
// is not saved.
func TestAnUnusableKeyIsRefusedInTheDialog(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	for _, key := range []string{"sk-line1\nsk-line2", "sk-\x00nul", strings.Repeat("k", 5000)} {
		sendCmd(t, conn, command{Cmd: "keySet", ID: "gemini-acp", Text: key})
		var note noticeMsg
		readUntil(t, conn, "notice", &note)
		if !note.Error || note.Text != "That key can't be used" {
			t.Errorf("answered %+v", note)
		}
	}
	if names, _ := creds.Names(); len(names) != 0 {
		t.Errorf("the store holds %v", names)
	}
}
