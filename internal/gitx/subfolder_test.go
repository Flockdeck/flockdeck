package gitx

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDefaultWorktreePathOfASubfolderIsBesideTheRepository covers a project
// opened at a folder inside a repository -- one service of a monorepo. The
// worktree panel asks for a new worktree's place with the project's folder,
// and "beside it" was then inside the repository: a nested checkout the main
// one listed as untracked, and that its next Commit from the panel would add
// as an embedded repository.
func TestDefaultWorktreePathOfASubfolderIsBesideTheRepository(t *testing.T) {
	t.Parallel()
	repo := newRepo(t)
	sub := filepath.Join(repo, "services", "api")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	got := DefaultWorktreePath(sub, "feature")
	if want := DefaultWorktreePath(repo, "feature"); got != want {
		t.Errorf("suggested %q for a subfolder, want %q beside the repository", got, want)
	}
	if rel, err := filepath.Rel(repo, got); err == nil && filepath.IsLocal(rel) {
		t.Errorf("suggested %q, inside the repository %q", got, repo)
	}
}
