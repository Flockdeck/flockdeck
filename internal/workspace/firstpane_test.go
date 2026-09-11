package workspace

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/store"
)

// TestANewProjectOpensOnTheAgentItWouldRun checks what a project with no
// layout of its own is opened on when Claude is not installed. That used to
// mean a shell whatever the default agent was, so somebody running only
// another agent never got one from opening a project.
func TestANewProjectOpensOnTheAgentItWouldRun(t *testing.T) {
	isolateConfig(t)
	dir, err := store.Dir()
	if err != nil {
		t.Fatalf("state directory: %v", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("make the state directory: %v", err)
	}
	// `go` is on PATH wherever these tests run, and run with nothing after it
	// prints its usage and exits.
	body := `{"version": 1, "agents": [
		{"id": "gocli", "name": "Go", "exe": "go"},
		{"id": "ghost", "name": "Ghost", "exe": "flockdeck-no-such-cli"}
	]}`
	if err := os.WriteFile(filepath.Join(dir, agent.ConfigName), []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", agent.ConfigName, err)
	}

	for _, tc := range []struct {
		agent string
		want  bool // whether the project opens on an agent
	}{
		{"gocli", true},
		{"ghost", false},
	} {
		t.Run(tc.agent, func(t *testing.T) {
			ws := newTestWorkspace(t, t.TempDir())
			ws.claudeExe = "" // this machine has no Claude
			ws.UseAgent(tc.agent)
			if err := ws.OpenProject(t.TempDir()); err != nil {
				t.Fatalf("open: %v", err)
			}
			p := ws.FocusedPane()
			if p == nil {
				t.Fatal("the project opened with no pane")
			}
			if p.IsAgent() != tc.want {
				t.Errorf("the project opened on an agent: %v, want %v", p.IsAgent(), tc.want)
			}
		})
	}
}
