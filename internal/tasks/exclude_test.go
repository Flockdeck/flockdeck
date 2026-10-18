package tasks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git", "info"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestCreatingTheBoardExcludesItsDirectoryFromGit(t *testing.T) {
	repo := gitRepo(t)
	exclude := filepath.Join(repo, ".git", "info", "exclude")
	if err := os.WriteFile(exclude, []byte("# user rules\n*.log"), 0o644); err != nil {
		t.Fatal(err)
	}
	b, _ := testBoardIn(t, repo)
	mustAdd(t, b, mgr, "first")
	got := readFile(t, exclude)
	if !strings.HasPrefix(got, "# user rules\n*.log\n") || strings.Count(got, ".flockdeck/") != 1 {
		t.Errorf("exclude = %q", got)
	}
	// Written again and again, the line is there once. So it is when the file is
	// made anew by a board that was opened later.
	mustAdd(t, b, mgr, "second")
	if err := os.Remove(b.Path); err != nil {
		t.Fatal(err)
	}
	again, _ := testBoardIn(t, repo)
	mustAdd(t, again, mgr, "third")
	if got := readFile(t, exclude); strings.Count(got, ".flockdeck/") != 1 {
		t.Errorf("exclude = %q", got)
	}
	// The tracked ignore file is never touched or made.
	if _, err := os.Stat(filepath.Join(repo, ".gitignore")); err == nil {
		t.Error(".gitignore was created")
	}
}

func TestAnExcludeFileThatDoesNotExistYetIsCreated(t *testing.T) {
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	b, _ := testBoardIn(t, repo)
	mustAdd(t, b, mgr, "x")
	got := readFile(t, filepath.Join(repo, ".git", "info", "exclude"))
	if !strings.Contains(got, "\n.flockdeck/\n") {
		t.Errorf("exclude = %q", got)
	}
}

func TestAnExistingExcludeLineIsKept(t *testing.T) {
	repo := gitRepo(t)
	exclude := filepath.Join(repo, ".git", "info", "exclude")
	for _, line := range []string{".flockdeck/", "/.flockdeck/", ".flockdeck", "  .flockdeck/  "} {
		if err := os.WriteFile(exclude, []byte("a\n"+line+"\nb\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		changed, err := EnsureExcluded(repo)
		if err != nil || changed {
			t.Errorf("line %q: changed = %v, err = %v", line, changed, err)
		}
		if got := readFile(t, exclude); got != "a\n"+line+"\nb\n" {
			t.Errorf("line %q: exclude was rewritten: %q", line, got)
		}
	}
}

func TestADirectoryThatIsNotARepositoryIsLeftAlone(t *testing.T) {
	dir := t.TempDir()
	b, logs := testBoardIn(t, dir)
	mustAdd(t, b, mgr, "no git here")
	if _, err := os.Stat(b.Path); err != nil {
		t.Errorf("the board was not written: %v", err)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() != ".flockdeck" {
			t.Errorf("unexpected %s in a directory that is not a repository", e.Name())
		}
	}
	if len(*logs) != 0 {
		t.Errorf("log = %v", *logs)
	}
	if changed, err := EnsureExcluded(dir); changed || err != nil {
		t.Errorf("EnsureExcluded = %v, %v", changed, err)
	}
	if got := MainCheckout(dir); got != dir {
		t.Errorf("MainCheckout = %q", got)
	}
}

func TestAnExcludeFileThatCannotBeWrittenIsLoggedAndTheBoardStillWorks(t *testing.T) {
	repo := gitRepo(t)
	// A directory where the file should be makes it unwritable on every system.
	if err := os.Mkdir(filepath.Join(repo, ".git", "info", "exclude"), 0o755); err != nil {
		t.Fatal(err)
	}
	b, logs := testBoardIn(t, repo)
	if task := mustAdd(t, b, mgr, "still works"); task.ID != "T1" {
		t.Errorf("task = %+v", task)
	}
	if len(*logs) != 1 || !strings.Contains((*logs)[0], "exclude") {
		t.Errorf("log = %v", *logs)
	}
}

// linkedWorktree lays out what `git worktree add` leaves: a checkout whose .git is
// a file naming a directory under the main repository's .git, which has a
// commondir file pointing back.
func linkedWorktree(t *testing.T) (main, linked string) {
	t.Helper()
	main = gitRepo(t)
	admin := filepath.Join(main, ".git", "worktrees", "feat")
	if err := os.MkdirAll(admin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(admin, "commondir"), []byte("../..\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	linked = t.TempDir()
	if err := os.WriteFile(filepath.Join(linked, ".git"), []byte("gitdir: "+admin+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return main, linked
}

func TestALinkedWorktreeUsesTheMainCheckoutsBoardAndExcludeFile(t *testing.T) {
	main, linked := linkedWorktree(t)
	if got := MainCheckout(linked); filepath.Clean(got) != filepath.Clean(main) {
		t.Fatalf("MainCheckout(linked) = %q, want %q", got, main)
	}
	b, _ := testBoardIn(t, linked)
	if want := filepath.Join(main, ".flockdeck", "team", "tasks.json"); b.Path != want {
		t.Errorf("board path = %q, want %q", b.Path, want)
	}
	mustAdd(t, b, mgr, "from the worktree")
	if _, err := os.Stat(filepath.Join(linked, ".flockdeck")); err == nil {
		t.Error("a board was made in the linked worktree")
	}
	if got := readFile(t, filepath.Join(main, ".git", "info", "exclude")); !strings.Contains(got, ".flockdeck/") {
		t.Errorf("exclude = %q", got)
	}
	// Two boards opened from the main checkout and its worktree share a file.
	other, _ := testBoardIn(t, main)
	if list, _, _ := other.List(Filter{}); len(list) != 1 {
		t.Errorf("the main checkout sees %d tasks, want 1", len(list))
	}
}

func TestAGitFileThatIsNotAWorktreePointerIsNotARepository(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("not a pointer"), 0o644); err != nil {
		t.Fatal(err)
	}
	if changed, err := EnsureExcluded(dir); changed || err != nil {
		t.Errorf("EnsureExcluded = %v, %v", changed, err)
	}
	if got := MainCheckout(dir); got != dir {
		t.Errorf("MainCheckout = %q", got)
	}
}
