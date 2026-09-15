package server

import (
	"testing"

	"github.com/jmwri/flockdeck/internal/workspace"
)

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
