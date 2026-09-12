package workspace

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jmwri/flockdeck/internal/layout"
	"github.com/jmwri/flockdeck/internal/session"
)

// TestAnAgentPutIntoAWorktreeStaysInItsProject covers the worktree panel's
// "start an agent here", which splits the focused pane into another checkout
// of the project on screen. With a project open on a directory above that one
// — the home folder, which is where a launch from the Start menu opens — the
// worktree beside the repository is inside it, and the new agent was filed
// under the home folder rather than the project whose worktree it is.
func TestAnAgentPutIntoAWorktreeStaysInItsProject(t *testing.T) {
	isolateConfig(t)
	home := t.TempDir()
	repo := filepath.Join(home, "code", "repo")
	worktree := filepath.Join(home, "code", "repo-agent-fix")
	for _, dir := range []string{repo, worktree} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	ws := newTestWorkspace(t, home)
	if err := ws.OpenProject(repo); err != nil {
		t.Fatalf("open %s: %v", repo, err)
	}
	ws.NewTab(session.KindShell, repo, "lead")

	ws.SplitPaneInWith(layout.Horizontal, Choice{Kind: session.KindShell}, worktree)
	p := ws.FocusedPane()
	if p == nil || !sameDir(p.Cwd, worktree) {
		t.Fatalf("the split did not open in the worktree: %+v", p)
	}
	if !sameDir(p.Root, repo) {
		t.Errorf("the agent split into the worktree belongs to %q, want the project it is a worktree of, %q", p.Root, repo)
	}

	// The panel's other two buttons open a tab on the worktree instead.
	ws.NewTabWith(Choice{Kind: session.KindShell}, worktree, "fix")
	if p := ws.FocusedPane(); p == nil || !sameDir(p.Root, repo) {
		t.Errorf("the pane in a tab opened on the worktree belongs to %q, want %q", p.Root, repo)
	}
}
