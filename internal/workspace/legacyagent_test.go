package workspace

import (
	"testing"

	"github.com/google/uuid"
	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/store"
)

// TestALegacyPaneRunsItsOwnProjectsDefault covers a layout written before
// panes recorded their agent, brought back in a project that is not the one on
// screen. An empty agent means the default of the project the pane belongs to,
// and it was looked up for the project on screen instead: the pane came back as
// that project's agent, on top of a conversation another agent had been having.
func TestALegacyPaneRunsItsOwnProjectsDefault(t *testing.T) {
	isolateConfig(t)
	onScreen, other := t.TempDir(), t.TempDir()
	writeAgentsFile(t, map[string]agent.Defaults{
		onScreen: {Agent: "ghost"},
		other:    {Agent: "gocli"},
	})

	id := uuid.NewString()
	if err := store.Save(other, &store.State{Tabs: []store.Tab{{
		Focus: id,
		Root:  &store.Node{Pane: &store.Pane{ID: id, Kind: "claude", Cwd: other}},
	}}}); err != nil {
		t.Fatalf("save the other project's layout: %v", err)
	}
	if err := store.SaveSession(&store.Session{Open: []string{other}}); err != nil {
		t.Fatalf("save the session: %v", err)
	}

	ws := newTestWorkspace(t, onScreen)
	if n := ws.RestoreSession(); n != 1 {
		t.Fatalf("RestoreSession reopened %d projects, want 1", n)
	}
	p := ws.Pane(id)
	if p == nil {
		t.Fatal("the other project's pane was not restored")
	}
	if p.Err != nil {
		t.Errorf("the restored pane did not start (%v); want its own project's gocli, not ghost from the project on screen", p.Err)
	}
	if got := ws.PlanSourceFor(id).Spec.ID; got != "gocli" {
		t.Errorf("fan-out reads the pane's plan as %q's, want gocli's", got)
	}
}
