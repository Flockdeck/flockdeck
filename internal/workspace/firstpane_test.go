package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/layout"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/store"
)

// writeAgentsFile puts an agents.json where the catalog reads it, in a state
// directory isolateConfig must already have pointed somewhere of the test's
// own. Among the agents is `go` under the id gocli: it is on PATH wherever
// these tests run, and run with nothing after it prints its usage and exits.
func writeAgentsFile(t *testing.T, projects map[string]agent.Defaults) {
	t.Helper()
	dir, err := store.Dir()
	if err != nil {
		t.Fatalf("state directory: %v", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("make the state directory: %v", err)
	}
	body, err := json.Marshal(map[string]any{
		"version": 1,
		"agents": []map[string]string{
			{"id": "gocli", "name": "Go", "exe": "go"},
			{"id": "ghost", "name": "Ghost", "exe": "flockdeck-no-such-cli"},
		},
		"projects": projects,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, agent.ConfigName), body, 0o600); err != nil {
		t.Fatalf("write %s: %v", agent.ConfigName, err)
	}
}

// TestANewProjectOpensOnTheAgentItWouldRun checks what a project with no
// layout of its own is opened on when Claude is not installed. That used to
// mean a shell whatever the default agent was, so somebody running only
// another agent never got one from opening a project.
func TestANewProjectOpensOnTheAgentItWouldRun(t *testing.T) {
	isolateConfig(t)
	writeAgentsFile(t, nil)

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

// TestAOneKeystrokePaneRunsItsProjectsDefault covers the default a project is
// given in the picker. The agent it named was looked up for the project on
// screen and the model it named was never looked up at all, so a split asked
// for nothing got the agent's own default model — and a split into another
// project got this one's agent.
func TestAOneKeystrokePaneRunsItsProjectsDefault(t *testing.T) {
	isolateConfig(t)
	first, second := t.TempDir(), t.TempDir()
	writeAgentsFile(t, map[string]agent.Defaults{
		first:  {Agent: "gocli", Model: "m1"},
		second: {Agent: "gocli", Model: "m2"},
	})

	ws := newTestWorkspace(t, first)
	ws.NewTab(session.KindClaude, first, "")
	if p := ws.FocusedPane(); p.Agent != "gocli" || p.Model != "m1" {
		t.Errorf("new tab runs %q · %q, want the project's gocli · m1", p.Agent, p.Model)
	}

	if err := ws.OpenProject(second); err != nil {
		t.Fatalf("open second: %v", err)
	}
	ws.SelectProject(first)
	ws.SplitPaneInProject(layout.Horizontal, session.KindClaude, second)
	if p := ws.FocusedPane(); p.Agent != "gocli" || p.Model != "m2" {
		t.Errorf("split into the second project runs %q · %q, want its gocli · m2", p.Agent, p.Model)
	}
}
