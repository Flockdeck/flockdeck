package workspace

import (
	"testing"

	"github.com/jmwri/flockdeck/internal/session"
)

// TestAHelperBelongsToTheProjectOfTheAgentThatSpawnedIt covers `flockdeck
// spawn --worktree` from an agent in a project that is not on screen. The
// worktree sits beside its repository rather than inside it, so no open
// project contains it, and the helper was put down as belonging to whichever
// project the user happened to be looking at: briefed as that project's,
// counted in its badge, and stopped when it was closed.
func TestAHelperBelongsToTheProjectOfTheAgentThatSpawnedIt(t *testing.T) {
	isolateConfig(t)
	ws, first, second := twoProjects(t)

	ws.SelectProject(second)
	ws.NewTab(session.KindShell, second, "theirs")
	parent := ws.CurrentTab().Focus
	ws.SelectProject(first)

	worktree := t.TempDir() // beside the repository, under no open project
	child, err := ws.Spawn(parent, SpawnOptions{Task: "repair the token refresh", Cwd: worktree, Kind: session.KindShell})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if got := ws.RootOf(child); !sameDir(got, second) {
		t.Errorf("the helper belongs to %q, want the spawning agent's project %q", got, second)
	}
	if got := ws.Tab(ws.TabIDOf(child)); got == nil || !sameDir(got.Root, second) {
		t.Errorf("the helper's tab is not in the spawning agent's project")
	}
}
