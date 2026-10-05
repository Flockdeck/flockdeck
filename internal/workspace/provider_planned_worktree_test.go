package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

// A worktree that is planned and not made yet, under a folder that is a repository of its
// own (a home folder kept as dotfiles), is judged as it will be when it is made: by itself
// and the checkout it is cut from, not by the folders above it.
func TestAPlannedWorktreeUnderAnotherRepositoryIsJudgedAsItWillBeWhenMade(t *testing.T) {
	isolateEnv(t)
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", "settings.local.json"), []byte(`{"env": {"ANTHROPIC_BASE_URL": "https://gw.example"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	planned := filepath.Join(home, "worktrees", "001-fix")
	asked, _ := BatonProviderDetail(claudeSpec, planned)
	// Made: a folder with a .git file of its own, as git worktree add makes.
	if err := os.MkdirAll(planned, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(planned, ".git"), []byte("gitdir: elsewhere\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	started, _ := BatonProviderDetail(claudeSpec, planned)
	if asked != started {
		t.Errorf("the company was %q when asked and %q when the helper started", asked, started)
	}
}

// git show with a plain name takes a tag before a branch of the same name; the branch is
// what the helper will have checked out.
func TestBranchSettingsAreReadFromTheBranchAndNotATagOfTheSameName(t *testing.T) {
	isolateConfig(t)
	repo := commitRepo(t)
	ws := newTestWorkspace(t, repo)
	write := func(body string) {
		if err := os.MkdirAll(filepath.Join(repo, ".claude"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo, ".claude", "settings.json"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		gitIn(t, repo, "add", ".")
		gitIn(t, repo, "commit", "-m", "settings")
	}
	write(`{"env": {"A": "tag"}}`)
	gitIn(t, repo, "tag", "same")
	write(`{"env": {"A": "branch"}}`)
	gitIn(t, repo, "branch", "same")
	got, _ := ws.BranchSettings(repo, "same")
	if len(got) != 1 || string(got[0]) != `{"env": {"A": "branch"}}` {
		t.Errorf("BranchSettings read %q", got)
	}
}
