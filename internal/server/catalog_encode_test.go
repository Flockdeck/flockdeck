package server

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/jmwri/flockdeck/internal/agent"
)

// TestTheCatalogIsSentAsItWasBuilt pins what keeping the catalog's encoding
// must not change: the bytes a snapshot carries for it are the ones encoding
// the catalog afresh would give, and a snapshot decodes to the catalog built.
func TestTheCatalogIsSentAsItWasBuilt(t *testing.T) {
	stateDir(t)
	built := buildCatalog(agent.Load(), "")
	type plain agentCatalog
	fresh, err := json.Marshal(plain(built))
	if err != nil {
		t.Fatal(err)
	}
	kept, err := json.Marshal(built.encoded())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(kept, fresh) {
		t.Fatalf("the kept encoding differs from a fresh one:\n%s\n%s", kept, fresh)
	}

	srv, _ := newTestServer(t)
	msg, ok := ask(srv, srv.snapshot)
	if !ok {
		t.Fatal("no snapshot")
	}
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	var back stateMsg
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if len(back.Agents.Items) == 0 || back.Agents.Items[0].ID != msg.Agents.Items[0].ID || back.Agents.Default != msg.Agents.Default {
		t.Fatalf("the snapshot's catalog reads back as %+v, want %+v", back.Agents, msg.Agents)
	}
}
