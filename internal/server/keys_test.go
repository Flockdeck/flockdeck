package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/creds"
	"github.com/jmwri/flockdeck/internal/store"
)

// TestSavedKeyOffersTheAgentAtOnce covers the picker after a key is saved. An
// API agent can be started exactly when it has a key, and the picker greys out
// the ones that cannot -- so saving one has to be what un-greys it, not the
// cache of the last answer running out a few seconds later.
func TestSavedKeyOffersTheAgentAtOnce(t *testing.T) {
	srv, ws := newTestServer(t)
	spec, ok := ws.Catalog().Find("anthropic")
	if !ok {
		t.Skip("no built-in anthropic agent")
	}
	for _, v := range creds.Env(spec) {
		t.Setenv(v, "")
	}
	agent.Refresh() // an earlier test's answer is not this machine's

	conn := dialControl(t, srv)
	r := readControl(conn)
	available := func(s stateMsg) bool {
		for _, it := range s.Agents.Items {
			if it.ID == spec.ID {
				return it.Available
			}
		}
		return false
	}
	if st, ok := r.stateWithin(10*time.Second, nil); !ok || available(st) {
		t.Fatalf("before a key is set the agent reads available=%v (got state: %v)", available(st), ok)
	}

	sendCmd(t, conn, command{Cmd: "keySet", ID: spec.ID, Text: "sk-test-not-a-real-key"})
	if _, ok := r.stateWithin(3*time.Second, available); !ok {
		t.Fatal("the agent was not offered within three seconds of its key being saved")
	}
}

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
// command line as the only way to give an API agent its key.
func TestKeysDialogIsAnswered(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)

	sendCmd(t, conn, command{Cmd: "keys"})
	var msg keysMsg
	readUntil(t, conn, "keys", &msg)
	if len(msg.Items) == 0 {
		t.Fatal("the dialog was offered no agents, though the built-ins include API agents")
	}
	agentID := msg.Items[0].Agent
	// A key already in this machine's environment would win over a stored one.
	for _, v := range msg.Items[0].Vars {
		t.Setenv(v, "")
	}

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
	if st := find(msg); st.Set {
		t.Fatalf("after clearing the key the dialog shows %+v", st)
	}
}

// TestAKeySavedUnderAnExportedOneSaysSo covers Replace… on a key that comes
// from flockdeck's environment. The environment wins, so the key typed in is
// kept but not used while the variable is set, and "saved the key" alone read
// as though it now was.
func TestAKeySavedUnderAnExportedOneSaysSo(t *testing.T) {
	srv, ws := newTestServer(t)
	spec, ok := ws.Catalog().Find("anthropic")
	if !ok || len(spec.API.KeyEnv) == 0 {
		t.Skip("no built-in anthropic agent with a key variable")
	}
	for _, v := range spec.API.KeyEnv {
		t.Setenv(v, "")
	}
	exported := spec.API.KeyEnv[0]
	t.Setenv(exported, "sk-from-the-environment")

	conn := dialControl(t, srv)
	sendCmd(t, conn, command{Cmd: "keySet", ID: spec.ID, Text: "sk-typed-into-the-dialog"})
	var note noticeMsg
	readUntil(t, conn, "notice", &note)
	if note.Error || !strings.Contains(note.Text, exported) {
		t.Fatalf("saving a key under an exported one said %+v; want it to name %s, which is what is used", note, exported)
	}
}
