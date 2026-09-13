package gitx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestAddNewBranchTakesBackAWorktreeGivenUpOnPartWay covers `git worktree add
// -b` killed at its deadline. It had commandTimeout's twenty seconds, which a
// large repository's checkout outlasts, and what it left was a registered
// worktree with the new branch checked out in it -- so the branch could not be
// deleted, and both stayed behind.
//
// A post-checkout hook that holds git until the test lets it go stands in for
// a checkout too big to finish: git runs it once the worktree is registered
// and the branch checked out there, which is the state a killed checkout
// leaves. The hook moves out of the worktree first, so that on Windows it does
// not hold the folder open.
func TestAddNewBranchTakesBackAWorktreeGivenUpOnPartWay(t *testing.T) {
	repo := newRepo(t)
	scratch := t.TempDir()
	hold := filepath.Join(scratch, "hold")
	done := filepath.Join(scratch, "done")
	if err := os.WriteFile(hold, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	hook := "#!/bin/sh\n" +
		"cd / || exit 0\n" +
		"exec >/dev/null 2>&1\n" +
		"while [ -e '" + filepath.ToSlash(hold) + "' ]; do sleep 0.1; done\n" +
		": > '" + filepath.ToSlash(done) + "'\n"
	hooks := filepath.Join(repo, ".git", "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hooks, "post-checkout"), []byte(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	// Let the hook go, and wait for it to finish, before the directories it
	// runs from are deleted.
	t.Cleanup(func() {
		os.Remove(hold)
		for end := time.Now().Add(10 * time.Second); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
			if _, err := os.Stat(done); err == nil {
				return
			}
		}
	})

	defer func(d time.Duration) { worktreeTimeout = d }(worktreeTimeout)
	worktreeTimeout = 3 * time.Second
	path := filepath.Join(t.TempDir(), "wt")

	start := time.Now()
	err := AddNewBranch(repo, path, "agent/slow")
	took := time.Since(start)
	if err == nil {
		t.Fatal("a worktree whose checkout never finished was reported made")
	}
	if !errors.Is(err, context.DeadlineExceeded) || took >= commandTimeout {
		t.Errorf("gave up after %s with %v; want it given up at worktreeTimeout, the worktree's own deadline", took.Round(time.Second), err)
	}
	if BranchExists(repo, "agent/slow") {
		t.Error("the branch of the worktree given up on was left behind")
	}
	wts, lerr := List(repo)
	if lerr != nil {
		t.Fatal(lerr)
	}
	for _, wt := range wts {
		if samePath(wt.Path, path) {
			t.Errorf("the worktree given up on is still registered: %+v", wt)
		}
	}
}
