package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// ignoringRepo is a repository that ignores what a project usually does.
func ignoringRepo(t *testing.T) string {
	t.Helper()
	repo := commitRepo(t)
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(".env\nnode_modules/\n.idea/\nbuild/\ncache/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-m", "ignore")
	return repo
}

// madeIn makes a worktree for branch and returns it.
func madeIn(t *testing.T, repo, branch string) (*Workspace, MadeWorktree) {
	t.Helper()
	ws := newTestWorkspace(t, repo)
	made, err := ws.PrepareWorktreeNew(repo, branch)
	if err != nil {
		t.Fatal(err)
	}
	if !made.WorktreeCreated || !made.BranchCreated {
		t.Fatalf("made = %+v", made)
	}
	return ws, made
}

func mustKeep(t *testing.T, ws *Workspace, repo string, made MadeWorktree, mention string) {
	t.Helper()
	got := ws.DiscardMade(repo, made, errStart)
	if !strings.Contains(got.Error(), "was kept") || !strings.Contains(got.Error(), mention) {
		t.Errorf("DiscardMade said %v, want the worktree kept and %q named", got, mention)
	}
	if _, err := os.Stat(made.Path); err != nil {
		t.Errorf("the worktree is gone: %v", err)
	}
	if branchTip(t, repo, made.Branch) == "" {
		t.Error("the branch is gone")
	}
}

// What is ignored is not shown by git status and is deleted by a removal that is not
// forced. Each of these is something a person or a tool puts in a worktree.
func TestAWorktreeHoldingIgnoredFilesIsKeptWhole(t *testing.T) {
	isolateConfig(t)
	for name, make := range map[string]func(dir string){
		".env": func(d string) { os.WriteFile(filepath.Join(d, ".env"), []byte("TOKEN=abc\n"), 0o600) },
		"node_modules": func(d string) {
			os.MkdirAll(filepath.Join(d, "node_modules", "x"), 0o700)
			os.WriteFile(filepath.Join(d, "node_modules", "x", "i.js"), []byte("1"), 0o600)
		},
		".idea": func(d string) {
			os.MkdirAll(filepath.Join(d, ".idea"), 0o700)
			os.WriteFile(filepath.Join(d, ".idea", "w.xml"), []byte("<a/>"), 0o600)
		},
		"build": func(d string) {
			os.MkdirAll(filepath.Join(d, "build"), 0o700)
			os.WriteFile(filepath.Join(d, "build", "out.bin"), []byte("1"), 0o600)
		},
		"cache":     func(d string) { os.MkdirAll(filepath.Join(d, "cache"), 0o700) }, // an empty ignored folder
		"notes.txt": func(d string) { os.WriteFile(filepath.Join(d, "notes.txt"), []byte("n"), 0o600) },
	} {
		repo := ignoringRepo(t)
		ws, made := madeIn(t, repo, "keep-"+strings.ReplaceAll(name, ".", ""))
		make(made.Path)
		t.Run(name, func(t *testing.T) { mustKeep(t, ws, repo, made, name) })
	}
}

// A file a post-checkout hook writes into the new worktree is something nobody asked for
// and may be wanted.
func TestAFileAHookWroteKeepsTheWorktree(t *testing.T) {
	isolateConfig(t)
	repo := ignoringRepo(t)
	hook := filepath.Join(repo, ".git", "hooks", "post-checkout")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\necho SECRET=1 > .env\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ws, made := madeIn(t, repo, "hooked")
	if _, err := os.Stat(filepath.Join(made.Path, ".env")); err != nil {
		t.Skip("the hook did not run here")
	}
	mustKeep(t, ws, repo, made, ".env")
}

// Nothing but the checked-out files: removed, with the branch, and the message is true.
func TestAWorktreeWithNothingBeyondTheCheckoutIsRemovedWithItsBranch(t *testing.T) {
	isolateConfig(t)
	repo := ignoringRepo(t)
	ws, made := madeIn(t, repo, "fresh-one")
	got := ws.DiscardMade(repo, made, errStart)
	if !strings.Contains(got.Error(), "were removed") {
		t.Errorf("DiscardMade said %v", got)
	}
	if _, err := os.Stat(made.Path); err == nil {
		t.Error("the worktree is still there")
	}
	if branchTip(t, repo, "fresh-one") != "" {
		t.Error("the branch is still there")
	}
}

// A submodule's folder is empty in a fresh worktree and does not count; one somebody
// filled does.
func TestASubmoduleFolderIsEmptyInAFreshWorktreeAndKeepsItWhenFilled(t *testing.T) {
	isolateConfig(t)
	sub := commitRepo(t)
	repo := commitRepo(t)
	gitIn(t, repo, "-c", "protocol.file.allow=always", "submodule", "add", sub, "sub")
	gitIn(t, repo, "commit", "-m", "submodule")

	ws, made := madeIn(t, repo, "with-sub")
	if _, err := os.Stat(filepath.Join(made.Path, "sub")); err != nil {
		t.Skip("the submodule folder was not made")
	}
	got := ws.DiscardMade(repo, made, errStart)
	if !strings.Contains(got.Error(), "were removed") {
		t.Errorf("a fresh worktree with an empty submodule folder: %v", got)
	}
	ws2, made2 := madeIn(t, repo, "with-sub-filled")
	if err := os.MkdirAll(filepath.Join(made2.Path, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(made2.Path, "sub", "f.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	mustKeep(t, ws2, repo, made2, "sub/")
}

// More than five extras: five are named and the rest counted.
func TestAKeptWorktreeNamesFiveFilesAndCountsTheRest(t *testing.T) {
	isolateConfig(t)
	repo := ignoringRepo(t)
	ws, made := madeIn(t, repo, "many")
	for _, n := range []string{"a", "b", "c", "d", "e", "f", "g"} {
		if err := os.WriteFile(filepath.Join(made.Path, n+".log"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got := ws.DiscardMade(repo, made, errStart).Error()
	if !strings.Contains(got, "and 2 more") {
		t.Errorf("DiscardMade said %q", got)
	}
}

// The branch moves between the look at it and the delete. A delete that is told the commit
// leaves it; a plain delete would take the commit made meanwhile.
func TestABranchThatMovesBetweenTheLookAndTheDeleteIsKept(t *testing.T) {
	isolateConfig(t)
	repo := ignoringRepo(t)
	ws, made := madeIn(t, repo, "races")
	var moved string
	old := beforeBranchDelete
	beforeBranchDelete = func(repo string, m MadeWorktree) {
		tree := gitIn(t, repo, "rev-parse", "HEAD^{tree}")
		moved = gitIn(t, repo, "commit-tree", tree, "-p", m.Start, "-m", "made by another process")
		gitIn(t, repo, "update-ref", "refs/heads/"+m.Branch, moved)
	}
	t.Cleanup(func() { beforeBranchDelete = old })
	got := ws.DiscardMade(repo, made, errStart)
	if !strings.Contains(got.Error(), "was kept") {
		t.Errorf("DiscardMade said %v", got)
	}
	if branchTip(t, repo, "races") != moved || moved == "" {
		t.Errorf("the branch is at %q, want the commit made meanwhile %q", branchTip(t, repo, "races"), moved)
	}
}

// A tracked file that was edited while git was told not to look for changes reads as clean to
// git status, and the worktree is kept all the same.
func TestAWorktreeWithAnAssumeUnchangedFileThatWasEditedIsKept(t *testing.T) {
	isolateConfig(t)
	repo := ignoringRepo(t)
	ws, made := madeIn(t, repo, "hidden-edit")
	gitIn(t, made.Path, "update-index", "--assume-unchanged", "f.txt")
	if err := os.WriteFile(filepath.Join(made.Path, "f.txt"), []byte("edited\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mustKeep(t, ws, repo, made, "f.txt")
}

// When the files of the worktree cannot be looked at, it is kept and the message says so, and
// has no placeholder in it for a check that went well.
func TestAWorktreeWhoseFilesCannotBeListedIsKeptAndSaysSo(t *testing.T) {
	isolateConfig(t)
	repo := ignoringRepo(t)
	ws, made := madeIn(t, repo, "walk-fails")
	old := extrasOf
	extrasOf = func(string, int) ([]string, int, error) { return nil, 0, errors.New("access denied") }
	t.Cleanup(func() { extrasOf = old })
	got := ws.DiscardMade(repo, made, errStart).Error()
	if !strings.Contains(got, "was kept") || !strings.Contains(got, "access denied") || strings.Contains(got, "<nil>") {
		t.Errorf("DiscardMade said %q", got)
	}
	if _, err := os.Stat(made.Path); err != nil {
		t.Errorf("the worktree is gone: %v", err)
	}
}

// A tracked folder that cannot be read is the same: the walk fails, and the worktree is
// kept with the reason said, never removed and never taken for clean.
func TestAWorktreeWithAnUnreadableTrackedFolderIsKeptAndSaysSo(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a folder cannot be made unreadable with chmod on Windows")
	}
	isolateConfig(t)
	repo := ignoringRepo(t)
	if err := os.MkdirAll(filepath.Join(repo, "pkg"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "pkg", "a.txt"), []byte("a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-m", "add pkg")
	ws, made := madeIn(t, repo, "denied-tracked")
	dir := filepath.Join(made.Path, "pkg")
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	if _, err := os.ReadDir(dir); err == nil {
		t.Skip("reads are not denied here (running as root?)")
	}
	got := ws.DiscardMade(repo, made, errStart).Error()
	if !strings.Contains(got, "was kept") || !strings.Contains(got, "could not be checked") {
		t.Errorf("DiscardMade said %q", got)
	}
	if _, err := os.Stat(made.Path); err != nil {
		t.Errorf("the worktree is gone: %v", err)
	}
	if branchTip(t, repo, made.Branch) == "" {
		t.Error("the branch is gone")
	}
}

// Any file that git is told not to look at keeps the worktree: assume-unchanged, skip-worktree,
// both; skip-worktree is expected only where a sparse checkout is set.
func TestAWorktreeWithAFileGitIsToldNotToLookAtIsKept(t *testing.T) {
	isolateConfig(t)
	for name, flag := range map[string]string{"assume-unchanged": "--assume-unchanged", "skip-worktree": "--skip-worktree"} {
		repo := ignoringRepo(t)
		ws, made := madeIn(t, repo, "hide-"+strings.ReplaceAll(name, "-", ""))
		gitIn(t, made.Path, "update-index", flag, "f.txt")
		if err := os.WriteFile(filepath.Join(made.Path, "f.txt"), []byte("edited\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Run(name, func(t *testing.T) { mustKeep(t, ws, repo, made, "f.txt") })
	}
	// both flags at once give a lower case tag as well
	repo := ignoringRepo(t)
	ws, made := madeIn(t, repo, "both-flags")
	gitIn(t, made.Path, "update-index", "--assume-unchanged", "--skip-worktree", "f.txt")
	mustKeep(t, ws, repo, made, "f.txt")
}

// In a sparse checkout S is how files left out are marked, and is not a reason to keep.
func TestSkipWorktreeIsExpectedInASparseCheckout(t *testing.T) {
	isolateConfig(t)
	repo := ignoringRepo(t)
	_, made := madeIn(t, repo, "sparse-one")
	gitIn(t, repo, "config", "core.sparseCheckout", "true")
	gitIn(t, made.Path, "update-index", "--skip-worktree", "f.txt")
	if names, err := gitxAssume(made.Path); err != nil || len(names) != 0 {
		t.Errorf("sparse checkout: %v %v", names, err)
	}
	gitIn(t, repo, "config", "core.sparseCheckout", "false")
	if names, err := gitxAssume(made.Path); err != nil || len(names) != 1 {
		t.Errorf("not sparse: %v %v", names, err)
	}
}

// When git cannot say which files it is told not to look at, the worktree is kept, and the
// message says why.
func TestAWorktreeWhoseHiddenFilesCannotBeListedIsKeptAndSaysSo(t *testing.T) {
	isolateConfig(t)
	repo := ignoringRepo(t)
	ws, made := madeIn(t, repo, "hidden-fails")
	old := hiddenOf
	hiddenOf = func(string, int) ([]string, error) { return nil, errors.New("ls-files refused") }
	t.Cleanup(func() { hiddenOf = old })
	got := ws.DiscardMade(repo, made, errStart).Error()
	if !strings.Contains(got, "was kept") || !strings.Contains(got, "ls-files refused") {
		t.Errorf("DiscardMade said %q", got)
	}
}

func gitxAssume(dir string) ([]string, error) { return hiddenOf(dir, 5) }
