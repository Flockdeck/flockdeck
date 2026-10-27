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
// command line as the only way to give an API agent its key. No catalog agent
// takes a key now, so the dialog starts empty and a key stored for an id the
// catalog does not have is listed on a row of its own, where it can be cleared.
func TestKeysDialogIsAnswered(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)

	sendCmd(t, conn, command{Cmd: "keys"})
	var msg keysMsg
	readUntil(t, conn, "keys", &msg)
	if len(msg.Items) != 0 {
		t.Fatalf("the dialog was offered %+v, though no catalog agent takes a key", msg.Items)
	}
	const agentID = "stand-in"

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
	if st := find(msg); !st.Set || st.Source != creds.SourceStore {
		t.Fatalf("after setting a key the dialog shows %+v, want it stored", st)
	}

	sendCmd(t, conn, command{Cmd: "keyClear", ID: agentID})
	readUntil(t, conn, "keys", &msg)
	for _, st := range msg.Items {
		if st.Agent == agentID {
			t.Fatalf("after clearing the key the dialog shows %+v", st)
		}
	}
}
