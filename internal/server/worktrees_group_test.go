package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jmwri/flockdeck/internal/workspace"
)

// bareGitRepo makes a real git repository with one commit, for a test that
// wants to run collectGroupWorktrees against actual git rather than a
// stubbed readWorktrees.
func bareGitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo := t.TempDir()
	for _, args := range [][]string{
		{"init", "--initial-branch=main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
		{"config", "commit.gpgsign", "false"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git %v: %v: %s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("hi\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-m", "initial"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git %v: %v: %s", args, err, out)
		}
	}
	return repo
}

// TestCollectGroupWorktreesTagsRowsOnlyWhenSpanningRepos checks the panel's
// fan-out: a project of one repo runs collectWorktrees exactly as it always
// has, through the same readWorktrees hook, and tags nothing; a project
// spanning more than one repo merges every member's listing into one, each
// row tagged with the repo it came from.
func TestCollectGroupWorktreesTagsRowsOnlyWhenSpanningRepos(t *testing.T) {
	was := readWorktrees
	t.Cleanup(func() { readWorktrees = was })
	readWorktrees = func(root string) worktreesMsg {
		return worktreesMsg{
			Type:        "worktrees",
			Root:        root,
			DefaultBase: "main",
			Items:       []worktreeView{{Path: root + "/wt", Label: "wt"}},
		}
	}

	single := collectGroupWorktrees([]workspace.RepoSummary{{Root: "/code/api", Name: "api"}})
	if len(single.Items) != 1 || single.Items[0].Repo != "" {
		t.Fatalf("single-repo listing = %+v, want one untagged row", single.Items)
	}
	if single.Root != "/code/api" {
		t.Errorf("root = %q, want /code/api", single.Root)
	}

	multi := collectGroupWorktrees([]workspace.RepoSummary{
		{Root: "/code/api", Name: "api"},
		{Root: "/code/web", Name: "web"},
	})
	if len(multi.Items) != 2 {
		t.Fatalf("items = %d, want one from each repo", len(multi.Items))
	}
	seen := map[string]string{}
	for _, it := range multi.Items {
		seen[it.RepoRoot] = it.Repo
	}
	if seen["/code/api"] != "api" || seen["/code/web"] != "web" {
		t.Errorf("rows tagged %+v, want each row to name its own repo", seen)
	}
}

// TestCollectGroupWorktreesTagsBranchesOnlyWhenSpanningRepos checks that
// "branches without a worktree" and the base-branch suggestions get the same
// per-repo tagging as the worktree rows themselves. Without it, two repos in
// one project offered their branches as a single merged list with no way to
// tell one repo's branch from another's, or which repo checking one out
// would run git in.
func TestCollectGroupWorktreesTagsBranchesOnlyWhenSpanningRepos(t *testing.T) {
	was := readWorktrees
	t.Cleanup(func() { readWorktrees = was })
	readWorktrees = func(root string) worktreesMsg {
		return worktreesMsg{
			Type:     "worktrees",
			Root:     root,
			Branches: []branchView{{Name: "main"}},
		}
	}

	single := collectGroupWorktrees([]workspace.RepoSummary{{Root: "/code/api", Name: "api"}})
	if len(single.Branches) != 1 || single.Branches[0].Repo != "" {
		t.Fatalf("single-repo branches = %+v, want one untagged branch", single.Branches)
	}

	multi := collectGroupWorktrees([]workspace.RepoSummary{
		{Root: "/code/api", Name: "api"},
		{Root: "/code/web", Name: "web"},
	})
	if len(multi.Branches) != 2 {
		t.Fatalf("branches = %d, want one from each repo", len(multi.Branches))
	}
	seen := map[string]string{}
	for _, b := range multi.Branches {
		seen[b.RepoRoot] = b.Repo
	}
	if seen["/code/api"] != "api" || seen["/code/web"] != "web" {
		t.Errorf("branches tagged %+v, want each to name its own repo", seen)
	}
}

// TestCollectGroupWorktreesKeepsWorkingWhenOneRepoFails checks that one
// member's git failing does not blank the whole listing -- the other
// member's worktrees are still worth showing.
func TestCollectGroupWorktreesKeepsWorkingWhenOneRepoFails(t *testing.T) {
	was := readWorktrees
	t.Cleanup(func() { readWorktrees = was })
	readWorktrees = func(root string) worktreesMsg {
		if root == "/code/broken" {
			return worktreesMsg{Type: "worktrees", Root: root, Error: "not a repository"}
		}
		return worktreesMsg{Type: "worktrees", Root: root, Items: []worktreeView{{Path: root}}}
	}

	msg := collectGroupWorktrees([]workspace.RepoSummary{
		{Root: "/code/broken", Name: "broken"},
		{Root: "/code/ok", Name: "ok"},
	})
	if msg.Error != "" {
		t.Errorf("error = %q, want none: the other repo still answered", msg.Error)
	}
	if len(msg.Items) != 1 || msg.Items[0].RepoRoot != "/code/ok" {
		t.Fatalf("items = %+v, want the one repo that answered", msg.Items)
	}
}

// TestCollectGroupWorktreesShowsAGenuineNonGitMemberAsSuch runs the real
// path rather than a stub: a project grouping an actual git repository with
// an actual plain folder still lists the repository's own worktree, the
// plain folder contributing nothing to the listing rather than sinking it
// or being reported as though it had a worktree at all -- "shows the
// non-git member's row, or its absence, sensibly" is the absence.
func TestCollectGroupWorktreesShowsAGenuineNonGitMemberAsSuch(t *testing.T) {
	repo := bareGitRepo(t)
	plain := t.TempDir()

	msg := collectGroupWorktrees([]workspace.RepoSummary{
		{Root: repo, Name: "api"},
		{Root: plain, Name: "docs"},
	})
	if msg.Error != "" {
		t.Errorf("error = %q, want none: the git member still answered", msg.Error)
	}
	if len(msg.Items) != 1 || msg.Items[0].RepoRoot != repo {
		t.Fatalf("items = %+v, want only the one member that is a repository", msg.Items)
	}
	if msg.Items[0].Repo != "api" {
		t.Errorf("repo = %q, want api", msg.Items[0].Repo)
	}
}
