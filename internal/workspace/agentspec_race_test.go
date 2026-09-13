package workspace

import (
	"fmt"
	"testing"
)

// TestAgentSpecByIDReadsNothingAProjectSwitchWrites covers the lookups made off
// the workspace goroutine: the fan-out dialog asking whether each agent can be
// started, and a fan-out asking which of its rows' agents ask about folder
// trust. Both named a real agent, but AgentSpec read the active project for
// every id, and a project switch writes it on the workspace goroutine -- a data
// race, which `go test -race` reports here.
func TestAgentSpecByIDReadsNothingAProjectSwitchWrites(t *testing.T) {
	isolateConfig(t)
	w := &Workspace{activeRoot: "/one"}
	visible := w.agents().Visible()
	if len(visible) == 0 {
		t.Skip("the catalog has no agents")
	}
	id := visible[0].ID

	// The workspace goroutine, switching projects.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			w.activeRoot = fmt.Sprintf("/project-%d", i)
		}
	}()
	for i := 0; i < 200; i++ {
		if spec, _ := w.AgentSpecByID(id); spec.ID != id {
			t.Fatalf("looking up %q found %q", id, spec.ID)
		}
	}
	<-done
}
