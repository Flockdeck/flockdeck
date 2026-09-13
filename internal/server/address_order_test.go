package server

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/agent"
)

// TestAnAddressWaitsForAnotherSave covers the picker's address field saved
// while a default or a routing setting is being saved from another window.
// Each reads agents.json, changes its own part and writes the whole file back.
// The default and the routing wait for one another, but the address did not,
// so side by side one of them wrote back a copy without the other's change --
// after both windows had been told theirs was saved.
func TestAnAddressWaitsForAnotherSave(t *testing.T) {
	srv, _ := newTestServer(t)
	path, err := agent.ConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	const address = "http://127.0.0.1:11434/v1"
	saved := func() bool {
		data, err := os.ReadFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			return false
		}
		if err != nil {
			t.Fatal(err)
		}
		return strings.Contains(string(data), address)
	}

	// Another save, part way through.
	defaultWrites.Lock()
	c := &controlClient{out: make(chan []byte, 8)}
	srv.setAgentAddress(c, agent.OpenAICompatibleID, address)
	time.Sleep(300 * time.Millisecond)
	written := saved()
	defaultWrites.Unlock()
	if written {
		t.Fatal("an address was written while another save had the file")
	}

	deadline := time.After(5 * time.Second)
	for {
		select {
		case raw := <-c.out:
			var msg agentAddressMsg
			if json.Unmarshal(raw, &msg) != nil || msg.Type != "agentAddress" {
				continue
			}
			if msg.Error != "" {
				t.Fatalf("the address was refused: %s", msg.Error)
			}
			if !saved() {
				t.Fatal("the address was answered as saved but is not in agents.json")
			}
			return
		case <-deadline:
			t.Fatal("the save never finished")
		}
	}
}
