package server

import "testing"

// TestFanoutTrustKeepsItsProjectsDefault covers a fan-out that carries folder
// trust over. Whether a row's agent asks about trust was decided on the
// fan-out's own goroutine, and a row on the run's default names no agent -- so
// the lookup resolved the default from the active project there, racing every
// project switch, and a switch landing in between decided trust against the
// other project's agent. The default has to be the one read when the run began.
func TestFanoutTrustKeepsItsProjectsDefault(t *testing.T) {
	srv, ws := newTestServer(t)
	first := srv.activeRoot()
	second := t.TempDir()
	if _, ok := ws.Catalog().Find("anthropic"); !ok {
		t.Skip("no built-in anthropic agent to give the second project")
	}
	if err := setAgentDefault(second, agentChoice{Agent: "anthropic"}); err != nil {
		t.Fatal(err)
	}
	ws.ReloadAgents()

	// What the fan-out reads as it begins, on the workspace goroutine, with
	// the first project active.
	def, _ := ask(srv, func() string {
		if err := ws.OpenProject(second); err != nil {
			t.Error(err)
		}
		ws.SelectProject(first)
		_, def := ws.Agents()
		return def
	})
	if def == "anthropic" {
		// The test arranged the difference itself, so a skip here would pass
		// it unrun whenever that arrangement broke.
		t.Fatal("the first project's default could not be told from the second's")
	}
	// The project switches before the trust step runs.
	ask(srv, func() bool { ws.SelectProject(second); return true })

	spec, err := srv.specOrDefault(def)("")
	if err != nil && spec.ID == "" {
		t.Fatalf("looking up the run's default: %v", err)
	}
	if spec.ID != def {
		t.Fatalf("a row on the run's default was looked up as %q, the default of the project switched to; want %q, the run's own", spec.ID, def)
	}
}
