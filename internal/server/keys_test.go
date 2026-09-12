package server

import (
	"testing"

	"github.com/jmwri/flockdeck/internal/creds"
)

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
