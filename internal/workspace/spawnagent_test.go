package workspace

import (
	"testing"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/session"
)

// TestAHelperIsCheckedForTheAgentItWillRun covers a helper asked for no agent
// in particular, spawned by an agent in a project that is not on screen. It
// runs its own project's default agent, but whether that could start was
// asked of the project on screen's default instead: the spawn was refused
// over an agent the helper was never going to run.
func TestAHelperIsCheckedForTheAgentItWillRun(t *testing.T) {
	isolateConfig(t)
	first, second := t.TempDir(), t.TempDir()
	writeAgentsFile(t, map[string]agent.Defaults{
		first:  {Agent: "ghost"}, // not installed
		second: {Agent: "gocli"},
	})

	ws := newTestWorkspace(t, first)
	ws.NewTab(session.KindShell, first, "mine")
	if err := ws.OpenProject(second); err != nil {
		t.Fatalf("open second: %v", err)
	}
	ws.NewTab(session.KindShell, second, "theirs")
	parent := ws.CurrentTab().Focus
	ws.SelectProject(first)

	child, err := ws.Spawn(parent, SpawnOptions{Task: "reticulate the splines", Kind: session.KindClaude})
	if err != nil {
		t.Fatalf("the spawn was refused over the project on screen's agent: %v", err)
	}
	if p := ws.Pane(child); p.Agent != "gocli" {
		t.Errorf("the helper runs %q, want its own project's gocli", p.Agent)
	}
}
