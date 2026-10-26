package gitx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeIn(t *testing.T, dir, name, text string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func commitIn(t *testing.T, dir, name, text, msg string) string {
	t.Helper()
	writeIn(t, dir, name, text)
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-q", "-m", msg)
	return strings.TrimSpace(gitRun(t, dir, "rev-parse", "HEAD"))
}

func TestMergeIntoFastForwardsTheExactCommitAndNotTheBranchName(t *testing.T) {
	dir := newRepo(t)
	gitRun(t, dir, "checkout", "-q", "-b", "feat")
	approved := commitIn(t, dir, "a.txt", "a\n", "a")
	// The branch moves after the commit was approved.
	commitIn(t, dir, "b.txt", "b\n", "b")
	gitRun(t, dir, "checkout", "-q", "main")

	tip, err := MergeInto(context.Background(), dir, "main", approved, "merge")
	if err != nil || tip != approved {
		t.Fatalf("tip = %q, %v; want the approved commit %s", tip, err, approved)
	}
	if _, err := os.Stat(filepath.Join(dir, "b.txt")); err == nil {
		t.Error("a commit that came after the approved one was merged")
	}
	if got := strings.TrimSpace(gitRun(t, dir, "rev-parse", "main")); got != approved {
		t.Errorf("main = %s", got)
	}
}

func TestMergeIntoMakesAMergeCommitWhenTheBaseMovedAndNothingConflicts(t *testing.T) {
	dir := newRepo(t)
	gitRun(t, dir, "checkout", "-q", "-b", "feat")
	feat := commitIn(t, dir, "a.txt", "a\n", "a")
	gitRun(t, dir, "checkout", "-q", "main")
	commitIn(t, dir, "m.txt", "m\n", "m")

	tip, err := MergeInto(context.Background(), dir, "main", feat, "Merge feat")
	if err != nil || tip == feat {
		t.Fatalf("tip = %q, %v", tip, err)
	}
	if parents := strings.Fields(gitRun(t, dir, "rev-list", "--parents", "-n", "1", tip)); len(parents) != 3 {
		t.Errorf("not a merge commit: %v", parents)
	}
	for _, f := range []string{"a.txt", "m.txt"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("%s missing after the merge", f)
		}
	}
}

func TestMergeIntoRefusesAConflictAndLeavesTheCheckoutAsItWas(t *testing.T) {
	dir := newRepo(t)
	gitRun(t, dir, "checkout", "-q", "-b", "feat")
	feat := commitIn(t, dir, "README.md", "from feat\n", "feat")
	gitRun(t, dir, "checkout", "-q", "main")
	main := commitIn(t, dir, "README.md", "from main\n", "main")

	_, err := MergeInto(context.Background(), dir, "main", feat, "merge")
	if !errors.Is(err, ErrNotFastForward) {
		t.Fatalf("err = %v", err)
	}
	if got := strings.TrimSpace(gitRun(t, dir, "rev-parse", "HEAD")); got != main {
		t.Errorf("main moved to %s", got)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git", "MERGE_HEAD")); err == nil {
		t.Error("a merge was left in progress")
	}
	if out := gitRun(t, dir, "status", "--porcelain"); strings.TrimSpace(out) != "" {
		t.Errorf("the checkout is not clean: %s", out)
	}
}

func TestMergeIntoRefusesADirtyOrOtherCheckoutAndNeverForces(t *testing.T) {
	dir := newRepo(t)
	gitRun(t, dir, "checkout", "-q", "-b", "feat")
	feat := commitIn(t, dir, "a.txt", "a\n", "a")
	gitRun(t, dir, "checkout", "-q", "main")

	// A tracked file with an edit.
	writeIn(t, dir, "README.md", "edited\n")
	if _, err := MergeInto(context.Background(), dir, "main", feat, "m"); !errors.Is(err, ErrNotClean) {
		t.Errorf("dirty: %v", err)
	}
	if got := strings.TrimSpace(gitRun(t, dir, "show", "HEAD:README.md")); got != "hello" {
		t.Errorf("README.md = %q", got)
	}
	gitRun(t, dir, "checkout", "-q", "--", "README.md")

	// A merge in progress.
	writeIn(t, filepath.Join(dir, ".git"), "MERGE_HEAD", feat+"\n")
	if _, err := MergeInto(context.Background(), dir, "main", feat, "m"); !errors.Is(err, ErrNotClean) {
		t.Errorf("mid-merge: %v", err)
	}
	if err := os.Remove(filepath.Join(dir, ".git", "MERGE_HEAD")); err != nil {
		t.Fatal(err)
	}

	// The checkout is on another branch.
	gitRun(t, dir, "checkout", "-q", "feat")
	if _, err := MergeInto(context.Background(), dir, "main", feat, "m"); !errors.Is(err, ErrWrongBranch) {
		t.Errorf("wrong branch: %v", err)
	}
	gitRun(t, dir, "checkout", "-q", "main")

	// A name that is not a commit is refused, not passed to git as an option.
	if _, err := MergeInto(context.Background(), dir, "main", "--abort", "m"); err == nil {
		t.Error("an option was taken for a commit")
	}
	if _, err := MergeInto(context.Background(), dir, "main", strings.Repeat("0", 40), "m"); err == nil {
		t.Error("a commit that does not exist was merged")
	}
}

func TestTheHelpersAnswerAboutAncestryCountsAndConflicts(t *testing.T) {
	dir := newRepo(t)
	base := strings.TrimSpace(gitRun(t, dir, "rev-parse", "HEAD"))
	gitRun(t, dir, "checkout", "-q", "-b", "feat")
	feat := commitIn(t, dir, "README.md", "from feat\n", "feat")
	gitRun(t, dir, "checkout", "-q", "main")
	main := commitIn(t, dir, "README.md", "from main\n", "main")
	ctx := context.Background()

	if ok, err := IsAncestor(ctx, dir, base, feat); err != nil || !ok {
		t.Errorf("base is an ancestor of feat: %v, %v", ok, err)
	}
	if ok, err := IsAncestor(ctx, dir, feat, main); err != nil || ok {
		t.Errorf("feat is not an ancestor of main: %v, %v", ok, err)
	}
	if n, err := CountBetween(ctx, dir, base, feat); err != nil || n != 1 {
		t.Errorf("count = %d, %v", n, err)
	}
	if clean, known := MergesCleanly(ctx, dir, main, feat); known && clean {
		t.Error("a conflict was reported as clean")
	}
	if path, ok := CheckoutOf(ctx, dir, "main"); !ok || !samePath(path, dir) {
		t.Errorf("checkout of main = %q, %v", path, ok)
	}
	if _, ok := CheckoutOf(ctx, dir, "feat"); ok {
		t.Error("feat is not checked out anywhere")
	}
}
