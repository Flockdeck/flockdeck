package server

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/agent"
)

// TestDefaultsSavedAtOnceDoNotLoseEachOther covers two windows setting a
// default agent at the same moment. Each save reads agents.json, changes its
// own entry and writes the whole file back, so side by side one wrote back a
// copy without the other's change -- after both windows had been told theirs
// was saved. A save now waits for any other to finish.
func TestDefaultsSavedAtOnceDoNotLoseEachOther(t *testing.T) {
	srv, ws := newTestServer(t)
	if _, ok := ws.Catalog().Find("anthropic"); !ok {
		t.Skip("no built-in anthropic agent")
	}
	path, err := agent.ConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	saved := func() string {
		f, err := agent.ReadConfig(filepath.Dir(path))
		if err != nil {
			t.Fatal(err)
		}
		return f.Defaults.Agent
	}

	// Another save, part way through.
	defaultWrites.Lock()
	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.applyAgentDefault(&controlClient{out: make(chan []byte, 8)},
			command{Cmd: "setAgentDefault", Agent: "anthropic", Target: "all"})
	}()
	time.Sleep(300 * time.Millisecond)
	written := saved() == "anthropic"
	defaultWrites.Unlock()
	if written {
		t.Fatal("a default was written while another save had the file")
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the save never finished")
	}
	if saved() != "anthropic" {
		t.Fatal("the default was not saved")
	}
}
