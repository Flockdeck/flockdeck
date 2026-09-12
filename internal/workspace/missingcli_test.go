package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/store"
)

// TestAPaneWhoseCLIIsMissingSaysHowToGetIt covers the message shown in place
// of a terminal when the agent a pane runs is not installed. It said what was
// wrong and stopped there, though the catalog knows where every agent comes
// from and the picker beside it says so.
func TestAPaneWhoseCLIIsMissingSaysHowToGetIt(t *testing.T) {
	isolateConfig(t)
	dir, err := store.Dir()
	if err != nil {
		t.Fatalf("state directory: %v", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"version": 1, "agents": [{"id": "ghost", "name": "Ghost", "exe": "flockdeck-no-such-cli",
		"install": "npm install -g flockdeck-no-such-cli"}]}`
	if err := os.WriteFile(filepath.Join(dir, agent.ConfigName), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	ws.NewTabWith(Choice{Kind: session.KindClaude, Agent: "ghost"}, root, "")
	p := ws.FocusedPane()
	if p == nil || p.Err == nil {
		t.Fatal("a pane whose CLI is missing reported nothing")
	}
	for _, want := range []string{"`flockdeck-no-such-cli` CLI was not found on PATH", "npm install -g flockdeck-no-such-cli"} {
		if !strings.Contains(p.Err.Error(), want) {
			t.Errorf("pane error = %q, want it to say %q", p.Err, want)
		}
	}
}
