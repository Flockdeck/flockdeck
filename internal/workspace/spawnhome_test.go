package workspace

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jmwri/flockdeck/internal/session"
)

// TestAHelperIsNotClaimedByAProjectAboveItsParents covers a project open on a
// directory that holds the others — the home folder, which is where a launch
// from the Start menu opens — beside the project an agent is working in. A
// worktree sits beside its repository, so it is under that outer project, and
// the helper put in one was filed under it: counted in its badge, stopped when
// it closed, and briefed as its agent, rather than as the spawning agent's.
func TestAHelperIsNotClaimedByAProjectAboveItsParents(t *testing.T) {
	isolateConfig(t)
	home := t.TempDir()
	repo := filepath.Join(home, "code", "repo")
	nested := filepath.Join(repo, "tools")
	worktree := filepath.Join(home, "code", "repo-agent-fix")
	inNested := filepath.Join(nested, "gen")
	for _, dir := range []string{repo, nested, worktree, inNested} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	ws := newTestWorkspace(t, home)
	for _, dir := range []string{repo, nested} {
		if err := ws.OpenProject(dir); err != nil {
			t.Fatalf("open %s: %v", dir, err)
		}
	}
	ws.SelectProject(repo)
	ws.NewTab(session.KindShell, repo, "lead")
	parent := ws.CurrentTab().Focus

	for _, tc := range []struct {
		name, cwd, want string
	}{
		{"a worktree beside the repository", worktree, repo},
		// A project nested inside the parent's is more particular than it, and
		// still wins.
		{"a directory inside a nested project", inNested, nested},
	} {
		child, err := ws.Spawn(parent, SpawnOptions{Task: "repair the token refresh", Cwd: tc.cwd, Kind: session.KindShell})
		if err != nil {
			t.Fatalf("%s: spawn: %v", tc.name, err)
		}
		if got := ws.RootOf(child); !sameDir(got, tc.want) {
			t.Errorf("%s: the helper belongs to %q, want %q", tc.name, got, tc.want)
		}
	}
}
