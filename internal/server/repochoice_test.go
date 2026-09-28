package server

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// A project that is the folder above its repositories used to be answered with
// an error and nothing to do about it. The windows now list the repositories
// just inside it, so that one can be picked.
func TestAFolderOfReposOffersThemToTheGitWindows(t *testing.T) {
	parent := t.TempDir()
	mk := func(rel string, isFile bool) {
		t.Helper()
		p := filepath.Join(parent, rel)
		if isFile {
			if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte("gitdir: elsewhere\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			return
		}
		if err := os.MkdirAll(p, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	mk("zeta/.git", false)
	mk("alpha/.git", false)
	mk("linked/.git", true) // a linked worktree's .git is a file
	mk("plain", false)
	mk(".hidden/.git", false)
	mk("node_modules/.git", false)

	want := []repoChoice{
		{Name: "alpha", Path: filepath.Join(parent, "alpha")},
		{Name: "linked", Path: filepath.Join(parent, "linked")},
		{Name: "zeta", Path: filepath.Join(parent, "zeta")},
	}
	if got := reposUnder(parent); !reflect.DeepEqual(got, want) {
		t.Errorf("reposUnder = %v, want %v", got, want)
	}
	if got := collectChanges(parent); got.Error == "" || !reflect.DeepEqual(got.Repos, want) {
		t.Errorf("changes = error %q, repos %v; want the error with %v", got.Error, got.Repos, want)
	}
	if got := collectWorktrees(parent); got.Error == "" || !reflect.DeepEqual(got.Repos, want) {
		t.Errorf("worktrees = error %q, repos %v; want the error with %v", got.Error, got.Repos, want)
	}
	if got := reposUnder(filepath.Join(parent, "plain")); len(got) != 0 {
		t.Errorf("a folder holding no repository offered %v", got)
	}
}

func TestTheRepoListIsBounded(t *testing.T) {
	parent := t.TempDir()
	for i := 0; i < maxRepoChoices+5; i++ {
		if err := os.MkdirAll(filepath.Join(parent, "r"+string(rune('a'+i/26))+string(rune('a'+i%26)), ".git"), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	if got := len(reposUnder(parent)); got != maxRepoChoices {
		t.Errorf("offered %d repos, want %d", got, maxRepoChoices)
	}
}
