package gitx

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

func sortedPaths(p []string) string {
	got := append([]string{}, p...)
	sort.Strings(got)
	return strings.Join(got, ",")
}

// snapOf makes a scratch and snapshots dir in it, for a test that looks at one.
func snapOf(t *testing.T, repo, dir string, dirty bool) (Snap, *Scratch, error) {
	t.Helper()
	common, err := CommonDir(repo)
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewScratch(common)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx := context.Background()
	snap, err := Snapshot(ctx, dir, s, BaseOf(ctx, repo), dirty)
	return snap, s, err
}

func realIndex(t *testing.T, dir string) string {
	t.Helper()
	p := strings.TrimSpace(gitRun(t, dir, "rev-parse", "--git-path", "index"))
	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}
	return p
}

// core.splitIndex makes git write sharedindex.<id> into the git directory
// whenever it writes an index, and the fallback for an unreadable index writes
// one. The scratch commands are told not to, so the git directory is as it was.
func TestSnapshotWritesNothingIntoAGitDirWithASplitIndex(t *testing.T) {
	t.Parallel()
	for _, torn := range []bool{false, true} {
		repo, wt := radarRepo(t, "wa")
		gitRun(t, wt["wa"], "config", "core.splitIndex", "true")
		editLine(t, wt["wa"], "a.txt", 2, "edited")
		write(t, wt["wa"], "fresh.txt", "new\n")
		gitRun(t, wt["wa"], "update-index", "--split-index")
		gitDir := strings.TrimSpace(gitRun(t, wt["wa"], "rev-parse", "--git-dir"))
		shared, _ := filepath.Glob(filepath.Join(gitDir, "sharedindex.*"))
		if len(shared) == 0 {
			t.Skip("this git did not make a split index")
		}
		if torn {
			full, err := os.ReadFile(realIndex(t, wt["wa"]))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(realIndex(t, wt["wa"]), full[:len(full)/2], 0o600); err != nil {
				t.Fatal(err)
			}
		}
		before := gitDirState(t, filepath.Join(repo, ".git"))
		snap, _, err := snapOf(t, repo, wt["wa"], true)
		if err != nil {
			t.Fatalf("torn=%v: %v", torn, err)
		}
		if got := sortedPaths(snap.Paths); got != "a.txt,fresh.txt" {
			t.Errorf("torn=%v: paths = %s, want a.txt,fresh.txt", torn, got)
		}
		if after := gitDirState(t, filepath.Join(repo, ".git")); before != after {
			t.Errorf("torn=%v: the git directory changed:\n%s", torn, lineDiff(before, after))
		}
	}
}

// index.skipHash writes an index whose checksum is zeros. It is copied and read
// rather than sent to the fallback every refresh.
func TestAnIndexWithSkipHashIsSeeded(t *testing.T) {
	t.Parallel()
	repo, wt := radarRepo(t, "wa")
	gitRun(t, wt["wa"], "config", "index.skipHash", "true")
	editLine(t, wt["wa"], "a.txt", 2, "edited")
	gitRun(t, wt["wa"], "add", "a.txt")
	data, err := os.ReadFile(realIndex(t, wt["wa"]))
	if err != nil {
		t.Fatal(err)
	}
	if !indexIsWhole(data) {
		t.Fatal("an index written with index.skipHash is not taken as whole")
	}
	common, _ := CommonDir(repo)
	s, err := NewScratch(common)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if seeded, _ := seedIndex(context.Background(), wt["wa"], s.envFor(s.index(wt["wa"])), s.index(wt["wa"])); !seeded {
		t.Error("the index was not seeded")
	}
	// And a copy that is cut short is still refused where there is a checksum.
	if indexIsWhole(data[:len(data)-30]) && !strings.HasSuffix(string(data[:len(data)-30]), strings.Repeat("\x00", 20)) {
		t.Error("a cut index was taken as whole")
	}
}

// The copy keeps the real index's modification time. A copy stamped now would
// pass an edit made in the same instant as the index was written as unchanged.
func TestSeededIndexKeepsTheRealOnesTime(t *testing.T) {
	t.Parallel()
	_, wt := radarRepo(t, "wa")
	real := realIndex(t, wt["wa"])
	old := time.Now().Add(-48 * time.Hour).Truncate(time.Second)
	if err := os.Chtimes(real, old, old); err != nil {
		t.Fatal(err)
	}
	to := filepath.Join(t.TempDir(), "idx")
	if err := copyIndex(real, to); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(to)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(old) {
		t.Errorf("the copy's time is %v, want the original's %v", info.ModTime(), old)
	}
}

// Seeding a sparse checkout keeps its skip-worktree entries, so the directories
// that were never checked out are not read as deleted. An unreadable index in
// one is not rebuilt from HEAD, which would read them so: it is an error, and
// the radar keeps what it knew.
func TestSnapshotOfASparseCheckout(t *testing.T) {
	t.Parallel()
	repo, wt := radarRepo(t, "wa")
	if err := os.MkdirAll(filepath.Join(repo, "keep"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "skip"), 0o700); err != nil {
		t.Fatal(err)
	}
	write(t, repo, "keep/x.txt", "x\n")
	write(t, repo, "skip/y.txt", "y\n")
	gitRun(t, repo, "add", "-A")
	gitRun(t, repo, "commit", "-m", "dirs")
	gitRun(t, wt["wa"], "merge", "-q", "main")
	if out, err := runGit(wt["wa"], "sparse-checkout", "set", "--cone", "keep"); err != nil {
		t.Skipf("sparse-checkout is not available: %v %s", err, out)
	}
	if _, err := os.Stat(filepath.Join(wt["wa"], "skip", "y.txt")); err == nil {
		t.Skip("the sparse checkout left the skipped directory on disk")
	}
	write(t, wt["wa"], "keep/x.txt", "edited\n")

	snap, _, err := snapOf(t, repo, wt["wa"], true)
	if err != nil {
		t.Fatal(err)
	}
	if got := sortedPaths(snap.Paths); got != "keep/x.txt" {
		t.Errorf("paths = %s, want only keep/x.txt: the skipped directory was read as deleted", got)
	}

	full, _ := os.ReadFile(realIndex(t, wt["wa"]))
	if err := os.WriteFile(realIndex(t, wt["wa"]), full[:len(full)/2], 0o600); err != nil {
		t.Fatal(err)
	}
	if snap, _, err := snapOf(t, repo, wt["wa"], true); err == nil {
		t.Errorf("a sparse checkout with an unreadable index was snapshotted from HEAD: %v", snap.Paths)
	}
}

func runGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// A checkout in the middle of a merge, cherry-pick or revert, or with files
// unmerged by a stash pop, has conflict markers in its files and is not
// snapshotted.
func TestSnapshotRefusesACheckoutInTheMiddleOfAnOperation(t *testing.T) {
	t.Parallel()
	for _, how := range []string{"merge", "cherry-pick", "revert", "stash pop"} {
		repo, wt := radarRepo(t, "wa", "wb")
		editLine(t, wt["wb"], "a.txt", 5, "from b")
		commitAll(t, wt["wb"], "b")
		dir := wt["wa"]
		switch how {
		case "merge":
			editLine(t, dir, "a.txt", 5, "from a")
			commitAll(t, dir, "a")
			_, _ = runGit(dir, "merge", "wb")
		case "cherry-pick":
			editLine(t, dir, "a.txt", 5, "from a")
			commitAll(t, dir, "a")
			_, _ = runGit(dir, "cherry-pick", "wb")
		case "revert":
			// Reverting the commit b made, after a has built on it.
			gitRun(t, dir, "merge", "-q", "wb")
			editLine(t, dir, "a.txt", 5, "built on b")
			commitAll(t, dir, "a builds on it")
			_, _ = runGit(dir, "revert", "--no-edit", "wb~0")
		case "stash pop":
			editLine(t, dir, "a.txt", 5, "stashed")
			gitRun(t, dir, "stash")
			editLine(t, dir, "a.txt", 5, "committed")
			commitAll(t, dir, "a")
			_, _ = runGit(dir, "stash", "pop")
		}
		if out, _ := runGit(dir, "ls-files", "--unmerged"); strings.TrimSpace(out) == "" {
			t.Logf("%s: no unmerged files were made, so there is nothing to check", how)
			continue
		}
		_, _, err := snapOf(t, repo, dir, true)
		if !errors.Is(err, ErrMidOperation) {
			t.Errorf("%s: err = %v, want ErrMidOperation", how, err)
		}
	}
}

// merge-tree names a file that conflicts with a directory as foo~<commit>.
func TestDirectoryAgainstFileConflicts(t *testing.T) {
	t.Parallel()
	repo, wt := radarRepo(t, "wa", "wb")
	write(t, wt["wa"], "foo", "a file\n")
	commitAll(t, wt["wa"], "a adds file foo")
	if err := os.Mkdir(filepath.Join(wt["wb"], "foo"), 0o700); err != nil {
		t.Fatal(err)
	}
	write(t, wt["wb"], "foo/bar", "in a directory\n")

	paths, conflict := predicted(t, repo, wt["wa"], wt["wb"], false, true)
	if !conflict || len(paths) != 1 || !strings.HasPrefix(paths[0], "foo") {
		t.Errorf("conflict = %v, paths = %v, want a conflict naming foo", conflict, paths)
	}
}

// Two panes rename one file to two names.
func TestRenameAgainstRenameConflicts(t *testing.T) {
	t.Parallel()
	repo, wt := radarRepo(t, "wa", "wb")
	gitRun(t, wt["wa"], "mv", "b.txt", "one.txt")
	commitAll(t, wt["wa"], "a renames")
	gitRun(t, wt["wb"], "mv", "b.txt", "two.txt")
	commitAll(t, wt["wb"], "b renames")

	paths, conflict := predicted(t, repo, wt["wa"], wt["wb"], false, false)
	if !conflict || len(paths) == 0 {
		t.Errorf("conflict = %v, paths = %v, want a rename/rename conflict", conflict, paths)
	}
}

// One pane moves a directory and the other edits a file in it: git carries the
// edit across, and both panes name the file, so the pair reaches merge-tree.
func TestDirectoryRenameAgainstEditInItIsClean(t *testing.T) {
	t.Parallel()
	repo, wt := radarRepo(t, "wa", "wb")
	if err := os.MkdirAll(filepath.Join(wt["wa"], "old"), 0o700); err != nil {
		t.Fatal(err)
	}
	write(t, wt["wa"], "old/x.txt", "1\n2\n3\n4\n5\n")
	commitAll(t, wt["wa"], "a adds old/")
	gitRun(t, wt["wb"], "merge", "-q", "wa")
	gitRun(t, wt["wa"], "mv", "old", "new")
	commitAll(t, wt["wa"], "a renames the directory")
	editLine(t, wt["wb"], "old/x.txt", 3, "edited by b")

	paths, conflict := predicted(t, repo, wt["wa"], wt["wb"], false, true)
	if conflict {
		t.Errorf("a directory rename against an edit in it conflicted: %v", paths)
	}
}

func TestPathsWithSpacesAndNonASCIINamesSurvive(t *testing.T) {
	t.Parallel()
	repo, wt := radarRepo(t, "wa", "wb")
	name := "my notes é.txt"
	write(t, wt["wa"], name, "from a\n")
	write(t, wt["wb"], name, "from b\n")
	paths, conflict := predicted(t, repo, wt["wa"], wt["wb"], true, true)
	if !conflict || len(paths) != 1 || paths[0] != name {
		t.Errorf("conflict = %v, paths = %q, want %q", conflict, paths, name)
	}
}

// merge-tree exits 1 for a commit it cannot find as well as for a conflict, and
// something else for a directory that is no repository. Neither is a conflict.
func TestMergeTreeErrorsAreNotConflicts(t *testing.T) {
	t.Parallel()
	repo, wt := radarRepo(t, "wa")
	common, _ := CommonDir(repo)
	s, err := NewScratch(common)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	head := strings.TrimSpace(gitRun(t, wt["wa"], "rev-parse", "HEAD"))
	if _, conflict, err := MergeTree(context.Background(), repo, s, head, "no-such-commit"); err == nil || conflict {
		t.Errorf("a commit that is not there: conflict = %v, err = %v, want an error", conflict, err)
	}
	if _, conflict, err := MergeTree(context.Background(), t.TempDir(), s, head, head); err == nil || conflict {
		t.Errorf("a directory that is no repository: conflict = %v, err = %v, want an error", conflict, err)
	}
	if !startsWithTreeID(strings.Repeat("a", 40)+"\x00x\x00") || startsWithTreeID("fatal: nope\n") || startsWithTreeID(strings.Repeat("g", 40)+"\x00") {
		t.Error("startsWithTreeID does not tell a tree id from other output")
	}
}

// A repository using SHA-256 has a 32 byte checksum on its index, 64 digit ids.
func TestSnapshotsInASHA256Repository(t *testing.T) {
	t.Parallel()
	if !Available() {
		t.Skip("git is not installed")
	}
	if ok, why := CheckMergeTree(); !ok {
		t.Skip(why)
	}
	repo := t.TempDir()
	if out, err := runGit(repo, "init", "-q", "--initial-branch=main", "--object-format=sha256"); err != nil {
		t.Skipf("no SHA-256 repositories here: %v %s", err, out)
	}
	for _, kv := range [][2]string{{"user.email", "t@example.com"}, {"user.name", "T"}, {"commit.gpgsign", "false"}} {
		gitRun(t, repo, "config", kv[0], kv[1])
	}
	write(t, repo, "a.txt", "1\n2\n3\n4\n5\n6\n7\n8\n")
	gitRun(t, repo, "add", "-A")
	gitRun(t, repo, "commit", "-q", "-m", "base")
	trees := map[string]string{}
	for _, name := range []string{"wa", "wb"} {
		dir := filepath.Join(t.TempDir(), name)
		if err := AddNewBranch(repo, dir, name); err != nil {
			t.Skipf("no worktrees in a SHA-256 repository: %v", err)
		}
		trees[name] = dir
	}
	editLine(t, trees["wa"], "a.txt", 4, "A")
	editLine(t, trees["wb"], "a.txt", 5, "B")
	commitAll(t, trees["wb"], "b")

	common, _ := CommonDir(repo)
	s, err := NewScratch(common)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	base := BaseOf(ctx, repo)
	sa, err := Snapshot(ctx, trees["wa"], s, base, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(sa.Commit) != 64 {
		t.Errorf("snapshot id %q is not a SHA-256 id", sa.Commit)
	}
	if seeded, _ := seedIndex(ctx, trees["wa"], s.envFor(s.index(trees["wa"])), s.index(trees["wa"])); !seeded {
		t.Error("a SHA-256 index was not seeded")
	}
	sb, err := Snapshot(ctx, trees["wb"], s, base, false)
	if err != nil {
		t.Fatal(err)
	}
	_, conflict, err := MergeTree(ctx, repo, s, sa.Commit, sb.Commit)
	if err != nil || !conflict {
		t.Errorf("conflict = %v, err = %v, want a conflict", conflict, err)
	}
}

// Failed probes are not believed for ever, and a version that was read is.
func TestTheVersionProbeRetriesAFailureButNotAnAnswer(t *testing.T) {
	oldVersion, oldNow := gitVersion, versionNow
	t.Cleanup(func() {
		gitVersion, versionNow = oldVersion, oldNow
		versionState.mu.Lock()
		versionState.known, versionState.probing, versionState.ok, versionState.why = false, false, false, ""
		versionState.mu.Unlock()
	})
	if !Available() {
		t.Skip("git is not installed")
	}
	now := time.Now()
	versionNow = func() time.Time { return now }

	gitVersion = func() (string, error) { return "", errors.New("boom") }
	probeVersion()
	if ok, why, known := MergeTreeSupport(); ok || !known || !strings.Contains(why, "boom") {
		t.Fatalf("after a failed probe: %v %q %v", ok, why, known)
	}

	// A minute on, the failure is not believed: the next call asks again, in the
	// background, and says what it knew meanwhile.
	gitVersion = func() (string, error) { return "git version 2.43.0\n", nil }
	now = now.Add(2 * versionRetry)
	MergeTreeSupport()
	deadline := time.Now().Add(5 * time.Second)
	for {
		ok, _, known := MergeTreeSupport()
		if ok && known {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the probe was not made again after the failure expired")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// An answer is kept: an old git stays old, with no more probes.
	probes := 0
	gitVersion = func() (string, error) { probes++; return "git version 2.30.0\n", nil }
	probeVersion()
	now = now.Add(24 * time.Hour)
	for i := 0; i < 3; i++ {
		if ok, why, known := MergeTreeSupport(); ok || !known || !strings.Contains(why, "2.38") {
			t.Fatalf("an old git: %v %q %v", ok, why, known)
		}
	}
	if probes != 1 {
		t.Errorf("git was asked %d times, want once", probes)
	}
}

// Scratch directories left by a run that was killed are swept once they are old,
// and a recent one, which may be another instance's, is left.
func TestSweepScratchRemovesOnlyOldDirectories(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	old := filepath.Join(root, scratchPrefix+"old")
	recent := filepath.Join(root, scratchPrefix+"recent")
	other := filepath.Join(root, "something-else")
	for _, d := range []string{old, recent, other} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	past := time.Now().Add(-3 * time.Hour)
	for _, d := range []string{old, other} {
		if err := os.Chtimes(d, past, past); err != nil {
			t.Fatal(err)
		}
	}
	sweepScratch(root, time.Hour)
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Error("an old scratch directory was left")
	}
	for _, d := range []string{recent, other} {
		if _, err := os.Stat(d); err != nil {
			t.Errorf("%s was removed: %v", d, err)
		}
	}
}

// A call without a scratch directory, or after it was closed, is an error, and
// not a nil dereference in whichever goroutine made it.
func TestNoScratchIsAnErrorNotAPanic(t *testing.T) {
	t.Parallel()
	repo, wt := radarRepo(t, "wa")
	ctx := context.Background()
	if _, _, err := MergeTree(ctx, repo, nil, "a", "b"); err == nil {
		t.Error("MergeTree with no scratch returned no error")
	}
	if _, err := Snapshot(ctx, wt["wa"], nil, "", true); err == nil {
		t.Error("Snapshot with no scratch returned no error")
	}
	common, _ := CommonDir(repo)
	s, err := NewScratch(common)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	if _, _, err := MergeTree(ctx, repo, s, "a", "b"); err == nil {
		t.Error("MergeTree with a closed scratch returned no error")
	}
}

// Only exit status 1 from merge-base is "no common history". A base git cannot
// find is an error, which the radar reads as unknown and not as nothing to
// compare, which it would then remember.
func TestSnapshotMergeBaseErrorsAreNotEmpty(t *testing.T) {
	t.Parallel()
	repo, wt := radarRepo(t, "wa")
	editLine(t, wt["wa"], "a.txt", 2, "edited")
	common, _ := CommonDir(repo)
	s, err := NewScratch(common)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()

	missing := strings.Repeat("0", 40)
	if snap, err := Snapshot(ctx, wt["wa"], s, missing, false); err == nil {
		t.Errorf("a base that is not there: %+v, want an error", snap)
	}

	// A commit with no history in common is the one case that is "nothing".
	orphan := strings.TrimSpace(gitRun(t, repo, "commit-tree", "4b825dc642cb6eb9a060e54bf8d69288fbee4904", "-m", "unrelated"))
	if snap, err := Snapshot(ctx, wt["wa"], s, orphan, false); err != nil || !snap.Empty {
		t.Errorf("a base with no common history: %+v, %v, want Empty", snap, err)
	}
}

func TestResolveBaseSaysNoBaseAndCouldNotSayApart(t *testing.T) {
	t.Parallel()
	repo, _ := radarRepo(t)
	ctx := context.Background()
	if id, err := ResolveBase(ctx, repo); err != nil || len(id) != 40 {
		t.Errorf("ResolveBase = %q, %v, want the commit", id, err)
	}
	fresh := t.TempDir()
	gitRun(t, fresh, "init", "-q", "--initial-branch=main")
	if id, err := ResolveBase(ctx, fresh); !errors.Is(err, ErrNoBase) || id != "" {
		t.Errorf("a repository with no commits: %q, %v, want ErrNoBase", id, err)
	}
	if id, err := ResolveBase(ctx, filepath.Join(fresh, "no", "such", "dir")); err == nil {
		t.Errorf("a directory that is not there: %q, want an error", id)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if id, err := ResolveBase(cancelled, repo); err == nil {
		t.Errorf("a cancelled context: %q, want an error", id)
	}
}

// A directory renamed whole by one pane while another adds a file to the old
// one: git moves the file or stops on it ("file location"), and no path is in
// both sets. The old directory is in the renaming pane's set, so they meet.
func TestAFileAddedToARenamedDirectoryMeets(t *testing.T) {
	t.Parallel()
	repo, _ := radarRepo(t)
	if err := os.MkdirAll(filepath.Join(repo, "d", "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	write(t, repo, "d/x.txt", "1\n2\n3\n")
	write(t, repo, "d/sub/y.txt", "1\n2\n3\n")
	commitAll(t, repo, "main adds d/")
	wt := map[string]string{}
	for _, name := range []string{"wa", "wb"} {
		dir := filepath.Join(t.TempDir(), name)
		if err := AddNewBranch(repo, dir, name); err != nil {
			t.Fatal(err)
		}
		wt[name] = dir
	}
	gitRun(t, wt["wa"], "mv", "d", "e")
	commitAll(t, wt["wa"], "a renames d/ to e/")
	write(t, wt["wb"], "d/new.txt", "added by b\n")
	write(t, wt["wb"], "d/sub/new2.txt", "added by b\n")

	ctx := context.Background()
	sa, s, err := snapOf(t, repo, wt["wa"], false)
	if err != nil {
		t.Fatal(err)
	}
	sb, err := Snapshot(ctx, wt["wb"], s, BaseOf(ctx, repo), true)
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, p := range sa.Dirs {
		have[p] = true
	}
	if !have["d"] || !have["d/sub"] {
		t.Errorf("a's directories %v lack the old directories d and d/sub (paths %v)", sa.Dirs, sa.Paths)
	}
	// A path under a directory in the other set is what the radar's filter looks for.
	under := false
	for _, p := range sb.Paths {
		for d := range have {
			if strings.HasPrefix(p, d+"/") {
				under = true
			}
		}
	}
	if !under {
		t.Errorf("b's paths %v are under none of a's directories %v", sb.Paths, sa.Dirs)
	}
	_, conflict, err := MergeTree(ctx, repo, s, sa.Commit, sb.Commit)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("git says conflict = %v", conflict)
}

// merge-tree names each of these as an exit it reached from a state the
// snapshot has to refuse: a merge finished by hand and not committed, a rebase
// stopped with nothing unmerged, and a merge that stopped before committing.
// Their index has no unmerged entry, so only the git directory says so.
func TestSnapshotRefusesWhatOnlyTheGitDirectoryShows(t *testing.T) {
	t.Parallel()
	for _, how := range []string{"resolved merge", "merge --no-commit", "rebase stopped"} {
		repo, wt := radarRepo(t, "wa", "wb")
		dir := wt["wa"]
		editLine(t, wt["wb"], "a.txt", 5, "from b")
		commitAll(t, wt["wb"], "b")
		switch how {
		case "resolved merge":
			editLine(t, dir, "a.txt", 5, "from a")
			commitAll(t, dir, "a")
			_, _ = runGit(dir, "merge", "wb")
			editLine(t, dir, "a.txt", 5, "resolved")
			gitRun(t, dir, "add", "a.txt")
		case "merge --no-commit":
			editLine(t, dir, "a.txt", 20, "from a")
			commitAll(t, dir, "a")
			gitRun(t, dir, "merge", "--no-commit", "--no-ff", "wb")
		case "rebase stopped":
			write(t, dir, "one.txt", "1\n")
			commitAll(t, dir, "one")
			write(t, dir, "two.txt", "2\n")
			commitAll(t, dir, "two")
			cmd := exec.Command("git", "rebase", "-i", "main")
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "GIT_SEQUENCE_EDITOR=sed -i 1s/pick/edit/")
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Logf("%s: rebase said %v %s", how, err, out)
			}
		}
		if out, _ := runGit(dir, "ls-files", "--unmerged"); strings.TrimSpace(out) != "" {
			t.Fatalf("%s: setup left unmerged files, so the index would catch it: %s", how, out)
		}
		gitDir := strings.TrimSpace(gitRun(t, dir, "rev-parse", "--git-dir"))
		if midOperation(gitDir) == "" {
			t.Fatalf("%s: setup left no operation in progress", how)
		}
		_, _, err := snapOf(t, repo, dir, true)
		if !errors.Is(err, ErrMidOperation) {
			t.Errorf("%s: err = %v, want ErrMidOperation", how, err)
		}
	}
}

// The user's own GIT_CONFIG_COUNT is kept: ours are numbered after theirs.
func TestScratchConfigDoesNotReplaceTheUsersOwn(t *testing.T) {
	t.Setenv("GIT_CONFIG_COUNT", "2")
	s, err := NewScratch(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	env := strings.Join(s.env, "\n")
	for _, want := range []string{"GIT_CONFIG_COUNT=5", "GIT_CONFIG_KEY_2=", "GIT_CONFIG_KEY_3=", "GIT_CONFIG_KEY_4="} {
		if !strings.Contains(env, want) {
			t.Errorf("env lacks %s:\n%s", want, env)
		}
	}
	if strings.Contains(env, "GIT_CONFIG_KEY_0=") || strings.Contains(env, "GIT_CONFIG_KEY_1=") {
		t.Error("the scratch overwrote entries 0 or 1, which are the user's")
	}
}

// A watcher is told when a failed probe, asked again after its minute, comes out
// differently: the retry is what makes the answer change, and without the watcher
// polling, nothing would ask.
func TestWatchMergeTreeSupportFollowsARetry(t *testing.T) {
	oldVersion, oldNow := gitVersion, versionNow
	t.Cleanup(func() {
		gitVersion, versionNow = oldVersion, oldNow
		versionState.mu.Lock()
		versionState.known, versionState.probing, versionState.ok, versionState.why, versionState.gen = false, false, false, "", 0
		versionState.mu.Unlock()
	})
	if !Available() {
		t.Skip("git is not installed")
	}
	versionState.mu.Lock()
	versionState.known, versionState.probing, versionState.gen = false, false, 0
	versionState.mu.Unlock()
	now := time.Now()
	versionNow = func() time.Time { return now }

	gitVersion = func() (string, error) { return "", errors.New("boom") }
	probeVersion()
	done := make(chan struct{})
	defer close(done)
	ok, why, gen, final, known := WatchMergeTreeSupport(0, done)
	if ok || !known || final || !strings.Contains(why, "boom") || gen == 0 {
		t.Fatalf("after the failed probe: %v %q gen %d final %v known %v", ok, why, gen, final, known)
	}

	// The minute passes and git works. The watcher, asking from where it was,
	// is told.
	gitVersion = func() (string, error) { return "git version 2.43.0\n", nil }
	now = now.Add(2 * versionRetry)
	got := make(chan bool, 1)
	go func() {
		ok, _, g, final, known := WatchMergeTreeSupport(gen, done)
		got <- ok && known && final && g != gen
	}()
	select {
	case fine := <-got:
		if !fine {
			t.Error("the watcher was told something other than that git is fit, for good")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the watcher was not told the probe mended")
	}
}

// In a shallow clone a base that moved on shows no history in common with a
// branch made before it: the commits that would be shared were not fetched.
// That is no answer, and "nothing to compare" given for it would be remembered.
func TestShallowCloneWithNoSharedHistoryIsAnErrorNotEmpty(t *testing.T) {
	t.Parallel()
	origin, _ := radarRepo(t)
	shallow := filepath.Join(t.TempDir(), "shallow")
	url := "file:///" + strings.TrimPrefix(filepath.ToSlash(origin), "/")
	if out, err := runGit(t.TempDir(), "clone", "-q", "--depth", "1", url, shallow); err != nil {
		t.Skipf("no shallow clones here: %v %s", err, out)
	}
	wa := filepath.Join(t.TempDir(), "wa")
	if err := AddNewBranch(shallow, wa, "wa"); err != nil {
		t.Fatal(err)
	}
	// The origin moves, and the clone takes only the new tip.
	editLine(t, origin, "a.txt", 3, "moved on")
	commitAll(t, origin, "main moves")
	gitRun(t, shallow, "fetch", "-q", "--depth", "1", "origin", "main")
	gitRun(t, shallow, "reset", "-q", "--hard", "FETCH_HEAD")
	editLine(t, wa, "a.txt", 20, "edited in wa")

	ctx := context.Background()
	base, err := ResolveBase(ctx, wa)
	if err != nil || base == "" {
		t.Fatalf("ResolveBase = %q, %v", base, err)
	}
	if out, _ := runGit(wa, "merge-base", "HEAD", base); strings.TrimSpace(out) != "" {
		t.Skipf("this git still found a merge base (%s), so the case is not made", strings.TrimSpace(out))
	}
	common, _ := CommonDir(shallow)
	s, err := NewScratch(common)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	snap, err := Snapshot(ctx, wa, s, base, true)
	if !errors.Is(err, ErrShallow) {
		t.Errorf("snapshot = %+v, %v, want ErrShallow", snap, err)
	}
}

// A bare repository with linked worktrees has no main worktree to name the base.
// It is the branch the bare repository's HEAD names, so committed work in two
// worktrees is compared with it and not read as nothing.
func TestBareRepositoryLayoutHasABase(t *testing.T) {
	t.Parallel()
	origin, _ := radarRepo(t)
	bare := filepath.Join(t.TempDir(), "bare.git")
	gitRun(t, t.TempDir(), "clone", "-q", "--bare", origin, bare)
	wt := map[string]string{}
	for _, name := range []string{"wa", "wb"} {
		dir := filepath.Join(t.TempDir(), name)
		gitRun(t, bare, "worktree", "add", "-q", "-b", name, dir, "main")
		wt[name] = dir
	}
	editLine(t, wt["wa"], "a.txt", 5, "from a")
	commitAll(t, wt["wa"], "a commits")
	editLine(t, wt["wb"], "a.txt", 5, "from b")
	commitAll(t, wt["wb"], "b commits")

	ctx := context.Background()
	base, err := ResolveBase(ctx, wt["wa"])
	if err != nil || len(base) != 40 {
		t.Fatalf("ResolveBase = %q, %v, want the commit main is on", base, err)
	}
	common, err := CommonDir(wt["wa"])
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewScratch(common)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	sa, err := Snapshot(ctx, wt["wa"], s, base, false)
	if err != nil || sa.Empty || sortedPaths(sa.Paths) != "a.txt" {
		t.Fatalf("snapshot of a committed branch = %+v, %v, want a.txt", sa, err)
	}
	sb, err := Snapshot(ctx, wt["wb"], s, base, false)
	if err != nil || sb.Empty {
		t.Fatalf("snapshot of b = %+v, %v", sb, err)
	}
	out, conflict, err := MergeTree(ctx, wt["wa"], s, sa.Commit, sb.Commit)
	if err != nil || !conflict {
		t.Errorf("conflict = %v, err = %v, out %q, want a conflict", conflict, err, out)
	}
}

// With no main worktree on a branch and no default branch to be found, the base
// is unknown, and says so in a way a caller can tell from a failure to ask.
func TestNoBaseBranchIsErrNoBase(t *testing.T) {
	t.Parallel()
	origin, _ := radarRepo(t)
	bare := filepath.Join(t.TempDir(), "bare.git")
	gitRun(t, t.TempDir(), "clone", "-q", "--bare", origin, bare)
	dir := filepath.Join(t.TempDir(), "w")
	gitRun(t, bare, "worktree", "add", "-q", "-b", "w", dir, "main")
	gitRun(t, bare, "branch", "-m", "main", "trunk")
	gitRun(t, bare, "symbolic-ref", "HEAD", "refs/heads/ghost")
	gitRun(t, bare, "remote", "remove", "origin")
	id, err := ResolveBase(context.Background(), dir)
	if !errors.Is(err, ErrNoBase) {
		t.Errorf("ResolveBase = %q, %v, want ErrNoBase", id, err)
	}
}

// The answer, its number and whether it is final are one reading. A probe that
// finishes while a watcher reads must not give it the old failure with the new
// number and a final flag, which the server would announce and then stop at.
// Here the state flips between a failed answer that is not final and a good one
// that is, as a finishing probe flips it, while a watcher reads it over and over.
func TestWatchMergeTreeSupportReadsOneConsistentAnswer(t *testing.T) {
	oldNow := versionNow
	t.Cleanup(func() {
		versionNow = oldNow
		versionState.mu.Lock()
		versionState.known, versionState.probing, versionState.ok, versionState.why, versionState.gen = false, false, false, "", 0
		versionState.until = time.Time{}
		versionState.mu.Unlock()
	})
	now := time.Now()
	versionNow = func() time.Time { return now }
	versionState.mu.Lock()
	versionState.known, versionState.probing, versionState.gen = true, false, 0
	versionState.mu.Unlock()

	stop := make(chan struct{})
	flipped := make(chan struct{})
	go func() {
		defer close(flipped)
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			versionState.mu.Lock()
			versionState.gen++
			if i%2 == 0 {
				versionState.ok, versionState.why, versionState.until = false, "could not read the version of git: boom", now.Add(time.Hour)
			} else {
				versionState.ok, versionState.why, versionState.until = true, "", time.Time{}
			}
			versionState.mu.Unlock()
		}
	}()
	done := make(chan struct{})
	defer close(done)
	for i := 0; i < 20000; i++ {
		ok, why, _, final, known := WatchMergeTreeSupport(0, done)
		if !known {
			t.Fatal("the answer was not known")
		}
		if final != ok || (ok && why != "") || (!ok && why == "") {
			close(stop)
			<-flipped
			t.Fatalf("a mixed answer after %d reads: ok %v, why %q, final %v", i, ok, why, final)
		}
	}
	close(stop)
	<-flipped
}

// A tag called main must not stand in for the branch: the fallback looks under
// refs/heads.
func TestFallbackBaseIsTheBranchNotATagOfTheSameName(t *testing.T) {
	t.Parallel()
	origin, _ := radarRepo(t)
	a := strings.TrimSpace(gitRun(t, origin, "rev-parse", "HEAD"))
	gitRun(t, origin, "checkout", "-q", "-b", "other")
	editLine(t, origin, "a.txt", 3, "on other")
	commitAll(t, origin, "other moves")
	b := strings.TrimSpace(gitRun(t, origin, "rev-parse", "HEAD"))
	gitRun(t, origin, "tag", "main", b)
	bare := filepath.Join(t.TempDir(), "bare.git")
	gitRun(t, t.TempDir(), "clone", "-q", "--bare", origin, bare)
	gitRun(t, bare, "symbolic-ref", "HEAD", "refs/heads/ghost")
	dir := filepath.Join(t.TempDir(), "w")
	gitRun(t, bare, "worktree", "add", "-q", "-b", "w", dir, "refs/heads/main")
	bases, err := ResolveBases(context.Background(), dir)
	if err != nil || len(bases.List) != 1 {
		t.Fatalf("ResolveBases = %+v, %v, want the one branch main", bases, err)
	}
	id := bases.List[0].ID
	if id != a {
		t.Errorf("the base is %s, want the branch main at %s and not the tag at %s", id, a, b)
	}
}

// A base the fallback guessed that has nothing in common with a pane is not
// "nothing to compare": the snapshot says it was unrelated, so the radar can
// tell it from a base that was right.
func TestAGuessedBaseWithNoSharedHistoryIsSaidToBeUnrelated(t *testing.T) {
	t.Parallel()
	origin, _ := radarRepo(t)
	bare := filepath.Join(t.TempDir(), "bare.git")
	gitRun(t, t.TempDir(), "clone", "-q", "--bare", origin, bare)
	gitRun(t, bare, "branch", "-m", "main", "trunk")
	orphan := strings.TrimSpace(gitRun(t, bare, "commit-tree", "4b825dc642cb6eb9a060e54bf8d69288fbee4904", "-m", "unrelated"))
	gitRun(t, bare, "update-ref", "refs/heads/main", orphan)
	gitRun(t, bare, "symbolic-ref", "HEAD", "refs/heads/ghost")
	dir := filepath.Join(t.TempDir(), "w")
	gitRun(t, bare, "worktree", "add", "-q", "-b", "w", dir, "trunk")
	editLine(t, dir, "a.txt", 4, "committed in w")
	commitAll(t, dir, "w commits")

	ctx := context.Background()
	bases, err := ResolveBases(ctx, dir)
	if err != nil || len(bases.List) != 1 || bases.List[0].ID != orphan {
		t.Fatalf("ResolveBases = %+v, %v, want the orphan main alone", bases, err)
	}
	base := orphan
	common, _ := CommonDir(dir)
	s, err := NewScratch(common)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	snap, err := Snapshot(ctx, dir, s, base, false)
	if err != nil || !snap.Empty || !snap.Unrelated {
		t.Errorf("snapshot = %+v, %v, want Empty and Unrelated", snap, err)
	}
}

// A main worktree on a detached HEAD names no branch, and HEAD there is wherever
// it was left: the base comes from the fallback.
func TestDetachedMainWorktreeIsNotTheBase(t *testing.T) {
	t.Parallel()
	repo, _ := radarRepo(t)
	main := strings.TrimSpace(gitRun(t, repo, "rev-parse", "HEAD"))
	editLine(t, repo, "a.txt", 3, "later")
	commitAll(t, repo, "main moves")
	later := strings.TrimSpace(gitRun(t, repo, "rev-parse", "HEAD"))
	gitRun(t, repo, "checkout", "-q", "--detach", main)
	bases, err := ResolveBases(context.Background(), repo)
	if err != nil || len(bases.List) != 1 || bases.List[0].ID != later {
		t.Errorf("ResolveBases = %+v, %v, want only the branch main at %s: the detached main worktree is not a base", bases, err, later)
	}
}

// Two panes that move a submodule to different commits meet at its path, and
// merge-tree decides: the radar no longer leaves the gitlink out.
func TestTwoSubmoduleBumpsMeetAndConflict(t *testing.T) {
	t.Parallel()
	repo, _ := radarRepo(t)
	inner := newRepo(t)
	gitRun(t, repo, "-c", "protocol.file.allow=always", "submodule", "add", "-q", inner, "vendor")
	gitRun(t, repo, "commit", "-m", "add submodule")
	wt := map[string]string{}
	for _, name := range []string{"wa", "wb"} {
		dir := filepath.Join(t.TempDir(), name)
		if err := AddNewBranch(repo, dir, name); err != nil {
			t.Fatal(err)
		}
		wt[name] = dir
	}
	// Two commits of the submodule's own, neither in the other's history.
	write(t, inner, "one.txt", "1\n")
	commitAll(t, inner, "one")
	one := strings.TrimSpace(gitRun(t, inner, "rev-parse", "HEAD"))
	gitRun(t, inner, "reset", "-q", "--hard", "HEAD~1")
	write(t, inner, "two.txt", "2\n")
	commitAll(t, inner, "two")
	two := strings.TrimSpace(gitRun(t, inner, "rev-parse", "HEAD"))
	for name, id := range map[string]string{"wa": one, "wb": two} {
		gitRun(t, wt[name], "update-index", "--cacheinfo", "160000,"+id+",vendor")
		gitRun(t, wt[name], "commit", "-q", "-m", name+" bumps vendor")
	}
	ctx := context.Background()
	sa, s, err := snapOf(t, repo, wt["wa"], false)
	if err != nil {
		t.Fatal(err)
	}
	sb, err := Snapshot(ctx, wt["wb"], s, BaseOf(ctx, repo), false)
	if err != nil {
		t.Fatal(err)
	}
	if sortedPaths(sa.Paths) != "vendor" || sortedPaths(sb.Paths) != "vendor" {
		t.Fatalf("paths = %v and %v, want the submodule path in each", sa.Paths, sb.Paths)
	}
	out, conflict, err := MergeTree(ctx, repo, s, sa.Commit, sb.Commit)
	if err != nil {
		t.Fatal(err)
	}
	if !conflict || !strings.Contains(out, "vendor") {
		t.Errorf("merge-tree: conflict = %v, %q, want a conflict at vendor", conflict, out)
	}
}

// The directories a file moved out of are a second set: only real changed paths
// count toward the cap, so four hundred moves between directories (eight hundred
// paths, four hundred directories) are under it, where counting both reached it.
func TestMovedDirectoriesDoNotCountTowardTheCap(t *testing.T) {
	t.Parallel()
	repo, _ := radarRepo(t)
	const n = 400
	for i := 0; i < n; i++ {
		d := filepath.Join(repo, fmt.Sprintf("d%03d", i))
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
		write(t, d, "f.txt", fmt.Sprintf("file %d\nwith some body\nto be moved\n", i))
	}
	commitAll(t, repo, "many directories")
	dir := filepath.Join(t.TempDir(), "w")
	if err := AddNewBranch(repo, dir, "w"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		if err := os.Rename(filepath.Join(dir, fmt.Sprintf("d%03d", i)), filepath.Join(dir, fmt.Sprintf("e%03d", i))); err != nil {
			t.Fatal(err)
		}
	}
	commitAll(t, dir, "move them all")
	snap, _, err := snapOf(t, repo, dir, false)
	if err != nil {
		t.Fatalf("snapshot = %v, want the moves under the cap", err)
	}
	if len(snap.Paths) != 2*n || len(snap.Dirs) != n {
		t.Errorf("%d paths and %d directories, want %d and %d", len(snap.Paths), len(snap.Dirs), 2*n, n)
	}
}

// A stale origin/HEAD, or a main that is not where the pane came from, is passed
// over for the candidate nearest the pane, and one that shares no history is
// never chosen.
func TestChooseBasePrefersTheNearestRelatedCandidate(t *testing.T) {
	t.Parallel()
	origin, _ := radarRepo(t)
	c1 := strings.TrimSpace(gitRun(t, origin, "rev-parse", "HEAD"))
	for i := 2; i <= 4; i++ {
		editLine(t, origin, "a.txt", i, fmt.Sprintf("main edit %d", i))
		commitAll(t, origin, fmt.Sprintf("main %d", i))
	}
	c4 := strings.TrimSpace(gitRun(t, origin, "rev-parse", "HEAD"))
	bare := filepath.Join(t.TempDir(), "bare.git")
	gitRun(t, t.TempDir(), "clone", "-q", "--bare", origin, bare)
	gitRun(t, bare, "symbolic-ref", "HEAD", "refs/heads/ghost")
	// origin's default branch, recorded long ago and never fetched since.
	gitRun(t, bare, "update-ref", "refs/remotes/origin/main", c1)
	gitRun(t, bare, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	orphan := strings.TrimSpace(gitRun(t, bare, "commit-tree", "4b825dc642cb6eb9a060e54bf8d69288fbee4904", "-m", "unrelated"))
	gitRun(t, bare, "update-ref", "refs/heads/master", orphan)
	dir := filepath.Join(t.TempDir(), "w")
	gitRun(t, bare, "worktree", "add", "-q", "-b", "w", dir, "refs/heads/main")
	editLine(t, dir, "a.txt", 20, "w work")
	commitAll(t, dir, "w commits")

	ctx := context.Background()
	bases, err := ResolveBases(ctx, dir)
	if err != nil || len(bases.List) != 3 || bases.List[0].ID != c1 {
		t.Fatalf("ResolveBases = %+v, %v, want origin's old main first, then main, then the orphan", bases, err)
	}
	got, err := ChooseBase(ctx, dir, "", bases)
	if err != nil || got != c4 {
		t.Errorf("ChooseBase = %q, %v, want main at %s: nearest, not first and not unrelated", got, err, c4)
	}

	// With only unrelated candidates there is nothing to choose.
	lonely := &Bases{List: []Base{{Ref: "refs/heads/master", ID: orphan}, {Ref: "refs/heads/x", ID: orphan}}}
	if got, err := ChooseBase(ctx, dir, "", lonely); !errors.Is(err, ErrUnrelated) {
		t.Errorf("ChooseBase over unrelated candidates = %q, %v, want ErrUnrelated", got, err)
	}
}

// One git process says whether any candidate moved, or the main worktree went to
// another branch.
func TestBasesFreshSeesAMovedBaseAndASwitchedMainWorktree(t *testing.T) {
	t.Parallel()
	repo, wt := radarRepo(t, "wa")
	ctx := context.Background()
	bases, err := ResolveBases(ctx, wt["wa"])
	if err != nil {
		t.Fatal(err)
	}
	if fresh, err := bases.Fresh(ctx); err != nil || !fresh {
		t.Fatalf("Fresh = %v, %v, want fresh", fresh, err)
	}
	editLine(t, repo, "a.txt", 3, "main moves")
	commitAll(t, repo, "main moves")
	if fresh, _ := bases.Fresh(ctx); fresh {
		t.Error("Fresh after the base branch moved")
	}
	bases, _ = ResolveBases(ctx, wt["wa"])
	gitRun(t, repo, "checkout", "-q", "-b", "elsewhere")
	write(t, repo, "elsewhere.txt", "x")
	commitAll(t, repo, "work on another branch")
	if fresh, _ := bases.Fresh(ctx); fresh {
		t.Error("Fresh after the main worktree went to another branch")
	}
}

// A partial clone on a git that cannot be told not to fetch what it lacks is
// reported, and a git that can, and a whole clone, are not.
func TestLazyFetchPossibleForAPartialCloneOnAnOldGit(t *testing.T) {
	origin, _ := radarRepo(t)
	gitRun(t, origin, "config", "uploadpack.allowFilter", "true")
	gitRun(t, origin, "config", "uploadpack.allowAnySHA1InWant", "true")
	partial := filepath.Join(t.TempDir(), "partial")
	url := "file:///" + strings.TrimPrefix(filepath.ToSlash(origin), "/")
	if out, err := runGit(t.TempDir(), "clone", "-q", "--filter=blob:none", url, partial); err != nil {
		t.Skipf("no partial clones here: %v %s", err, out)
	}
	old := versionState.major
	oldMinor := versionState.minor
	t.Cleanup(func() {
		versionState.mu.Lock()
		versionState.major, versionState.minor = old, oldMinor
		versionState.mu.Unlock()
	})
	ctx := context.Background()
	for _, tc := range []struct {
		major, minor int
		dir          string
		want         bool
	}{
		{2, 43, partial, true},
		{2, 38, partial, true},
		{2, 44, partial, false},
		{3, 0, partial, false},
		{2, 43, origin, false},
	} {
		versionState.mu.Lock()
		versionState.major, versionState.minor = tc.major, tc.minor
		versionState.mu.Unlock()
		got, err := LazyFetchPossible(ctx, tc.dir)
		if err != nil || got != tc.want {
			t.Errorf("git %d.%d in %s: %v, %v, want %v", tc.major, tc.minor, filepath.Base(tc.dir), got, err, tc.want)
		}
	}
}

// The status carries the whole id of the commit, of which Head is the start.
func TestStatusHasTheFullCommitId(t *testing.T) {
	t.Parallel()
	repo := newRepo(t)
	st := StatusOf(repo)
	full := strings.TrimSpace(gitRun(t, repo, "rev-parse", "HEAD"))
	if st.Commit != full || !strings.HasPrefix(full, st.Head) {
		t.Errorf("Commit = %q, Head = %q, want the id %q and its start", st.Commit, st.Head, full)
	}
}

// Fresh watches every ref that resolved, not only the first one at each commit:
// a ref hidden behind another at the same commit still moves the candidates.
func TestBasesFreshWatchesRefsHiddenAtTheSameCommit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// The main worktree is on dev, at the same commit as main; main then moves.
	repo, wt := radarRepo(t, "wa")
	editLine(t, wt["wa"], "a.txt", 3, "wa work")
	commitAll(t, wt["wa"], "wa commits")
	gitRun(t, repo, "checkout", "-q", "-b", "dev")
	bases, err := ResolveBases(ctx, wt["wa"])
	if err != nil {
		t.Fatal(err)
	}
	if len(bases.List) != 1 || len(bases.watch) < 2 {
		t.Fatalf("List %d, watch %d: want one candidate commit and two refs watched", len(bases.List), len(bases.watch))
	}
	if fresh, _ := bases.Fresh(ctx); !fresh {
		t.Fatal("setup: not fresh")
	}
	gitRun(t, repo, "branch", "-f", "main", "wa")
	if fresh, _ := bases.Fresh(ctx); fresh {
		t.Error("main moved behind dev at the same commit and Fresh did not see it")
	}

	// A local main equal to origin's, which a fetch then moves.
	repo2, wt2 := radarRepo(t, "wb")
	main := strings.TrimSpace(gitRun(t, repo2, "rev-parse", "HEAD"))
	gitRun(t, repo2, "update-ref", "refs/remotes/origin/main", main)
	gitRun(t, repo2, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	editLine(t, wt2["wb"], "a.txt", 4, "wb work")
	commitAll(t, wt2["wb"], "wb commits")
	newer := strings.TrimSpace(gitRun(t, wt2["wb"], "rev-parse", "HEAD"))
	bases, err = ResolveBases(ctx, wt2["wb"])
	if err != nil || len(bases.List) != 1 || len(bases.watch) < 2 {
		t.Fatalf("ResolveBases = %+v, %v", bases, err)
	}
	gitRun(t, repo2, "update-ref", "refs/remotes/origin/main", newer)
	if fresh, _ := bases.Fresh(ctx); fresh {
		t.Error("origin/main moved behind main at the same commit and Fresh did not see it")
	}
}

// A main worktree that was detached when the candidates were resolved, and later
// gets a branch of its own with a commit on it, changes the candidates.
func TestBasesFreshSeesADetachedMainWorktreeGainABranch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo, wt := radarRepo(t, "wa")
	gitRun(t, repo, "checkout", "-q", "--detach")
	bases, err := ResolveBases(ctx, wt["wa"])
	if err != nil {
		t.Fatal(err)
	}
	if fresh, _ := bases.Fresh(ctx); !fresh {
		t.Fatal("setup: not fresh")
	}
	gitRun(t, repo, "checkout", "-q", "-b", "feat")
	write(t, repo, "feat.txt", "x\n")
	commitAll(t, repo, "feat commits")
	if fresh, _ := bases.Fresh(ctx); fresh {
		t.Error("the detached main worktree got a branch with a commit and Fresh did not see it")
	}
}

// The partial-clone check reads values: a remote whose promisor is false is not
// one, one whose promisor is true is, so is a .promisor pack, and a configuration
// that cannot be read fails closed on an old git and open on a new one.
func TestLazyFetchPossibleReadsValuesAndFailsClosed(t *testing.T) {
	old, oldMinor := versionState.major, versionState.minor
	t.Cleanup(func() {
		versionState.mu.Lock()
		versionState.major, versionState.minor = old, oldMinor
		versionState.mu.Unlock()
	})
	setVersion := func(major, minor int) {
		versionState.mu.Lock()
		versionState.major, versionState.minor = major, minor
		versionState.mu.Unlock()
	}
	ctx := context.Background()
	setVersion(2, 43)

	repo := newRepo(t)
	gitRun(t, repo, "remote", "add", "origin", "file:///nowhere")
	gitRun(t, repo, "config", "remote.origin.promisor", "false")
	if got, err := LazyFetchPossible(ctx, repo); err != nil || got {
		t.Errorf("promisor=false: %v, %v, want not a partial clone", got, err)
	}
	gitRun(t, repo, "config", "remote.origin.promisor", "true")
	if got, err := LazyFetchPossible(ctx, repo); err != nil || !got {
		t.Errorf("promisor=true: %v, %v, want a partial clone", got, err)
	}
	setVersion(2, 44)
	if got, _ := LazyFetchPossible(ctx, repo); got {
		t.Error("a partial clone on git 2.44 was reported as able to lazy-fetch")
	}
	setVersion(2, 43)
	gitRun(t, repo, "config", "remote.origin.promisor", "false")

	// A .promisor pack marks one too.
	pack := filepath.Join(repo, ".git", "objects", "pack")
	if err := os.MkdirAll(pack, 0o700); err != nil {
		t.Fatal(err)
	}
	write(t, pack, "pack-1.promisor", "")
	if got, err := LazyFetchPossible(ctx, repo); err != nil || !got {
		t.Errorf("a .promisor pack: %v, %v, want a partial clone", got, err)
	}
	if err := os.Remove(filepath.Join(pack, "pack-1.promisor")); err != nil {
		t.Fatal(err)
	}

	// A configuration git cannot parse: closed on an old git, with the error.
	cfg := filepath.Join(repo, ".git", "config")
	data, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, append(data, []byte("\n[broken\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := LazyFetchPossible(ctx, repo); err == nil || !got {
		t.Errorf("an unreadable configuration on git 2.43: %v, %v, want closed (true) with an error", got, err)
	}
	setVersion(2, 44)
	if got, _ := LazyFetchPossible(ctx, repo); got {
		t.Error("an unreadable configuration on git 2.44 skipped the radar, which can be told not to fetch")
	}
}

// A directory holding a repository of its own, untracked, is not a changed path of
// the checkout: git add -A would make a gitlink of it, which no other checkout can
// share and which could only make a false chip. One somebody staged is kept.
func TestUntrackedNestedRepositoryIsLeftOutOfTheChangedPaths(t *testing.T) {
	t.Parallel()
	repo, wt := radarRepo(t, "wa")
	dir := wt["wa"]
	nested := filepath.Join(dir, "scratch-project")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	gitRun(t, nested, "init", "-q", "--initial-branch=main")
	write(t, nested, "x.txt", "x\n")
	gitRun(t, nested, "add", "-A")
	gitRun(t, nested, "commit", "-q", "-m", "inside")
	editLine(t, dir, "a.txt", 3, "edited")

	snap, _, err := snapOf(t, repo, dir, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := sortedPaths(snap.Paths); got != "a.txt" {
		t.Errorf("paths = %s, want only a.txt: the untracked repository is not a changed path", got)
	}

	// Staged by hand, it is the checkout's own change and is kept.
	gitRun(t, dir, "add", "scratch-project")
	snap, _, err = snapOf(t, repo, dir, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := sortedPaths(snap.Paths); got != "a.txt,scratch-project" {
		t.Errorf("paths = %s, want a.txt and the staged repository", got)
	}
}

// Work only inside a registered submodule is not the checkout's: the status that
// decides whether a pane is clean does not count it, so such a pane is clean to
// the radar and is not snapshotted again every refresh.
func TestWorkInsideARegisteredSubmoduleLeavesTheCheckoutClean(t *testing.T) {
	t.Parallel()
	repo, _ := radarRepo(t)
	inner := newRepo(t)
	gitRun(t, repo, "-c", "protocol.file.allow=always", "submodule", "add", "-q", inner, "vendor")
	gitRun(t, repo, "commit", "-m", "add submodule")
	write(t, repo, "vendor/changed.txt", "work in the submodule\n")
	editLine(t, filepath.Join(repo, "vendor"), "README.md", 1, "edited inside")
	st, err := StatusWithin(repo, 20*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if st.HasChanges() {
		t.Errorf("status = %+v, want a superproject with only submodule work to be clean", st)
	}
}

// What merge-tree says of two submodule bumps where one commit is a descendant of
// the other (they could be combined), with the submodule not checked out in the
// panes' worktrees and with it checked out.
func TestSubmoduleBumpsThatCouldBeCombined(t *testing.T) {
	t.Parallel()
	repo, _ := radarRepo(t)
	inner := newRepo(t)
	gitRun(t, repo, "-c", "protocol.file.allow=always", "submodule", "add", "-q", inner, "vendor")
	gitRun(t, repo, "commit", "-m", "add submodule")
	write(t, inner, "one.txt", "1\n")
	commitAll(t, inner, "one")
	one := strings.TrimSpace(gitRun(t, inner, "rev-parse", "HEAD"))
	write(t, inner, "two.txt", "2\n")
	commitAll(t, inner, "two")
	two := strings.TrimSpace(gitRun(t, inner, "rev-parse", "HEAD"))
	ctx := context.Background()

	run := func(name string, checkedOut bool) bool {
		wt := map[string]string{}
		for _, n := range []string{"wa" + name, "wb" + name} {
			dir := filepath.Join(t.TempDir(), n)
			if err := AddNewBranch(repo, dir, n); err != nil {
				t.Fatal(err)
			}
			wt[n] = dir
			if checkedOut {
				gitRun(t, dir, "-c", "protocol.file.allow=always", "submodule", "update", "--init", "-q")
			}
		}
		for n, id := range map[string]string{"wa" + name: one, "wb" + name: two} {
			if checkedOut {
				gitRun(t, filepath.Join(wt[n], "vendor"), "fetch", "-q", inner, "+refs/heads/*:refs/remotes/inner/*")
				gitRun(t, filepath.Join(wt[n], "vendor"), "checkout", "-q", id)
				gitRun(t, wt[n], "add", "vendor")
			} else {
				gitRun(t, wt[n], "update-index", "--cacheinfo", "160000,"+id+",vendor")
			}
			gitRun(t, wt[n], "commit", "-q", "-m", n+" bumps vendor")
		}
		sa, s, err := snapOf(t, repo, wt["wa"+name], false)
		if err != nil {
			t.Fatal(err)
		}
		sb, err := Snapshot(ctx, wt["wb"+name], s, BaseOf(ctx, repo), false)
		if err != nil {
			t.Fatal(err)
		}
		_, conflict, err := MergeTree(ctx, wt["wa"+name], s, sa.Commit, sb.Commit)
		if err != nil {
			t.Fatal(err)
		}
		return conflict
	}
	notOut := run("n", false)
	out := run("c", true)
	t.Logf("fast-forwardable submodule bumps: not checked out conflict = %v, checked out conflict = %v", notOut, out)
	if !out {
	} else {
		t.Error("a checked-out, fast-forwardable pair gave a conflict")
	}
	if !notOut {
		t.Error("the not-checked-out pair gave no conflict, which the docs say it does")
	}
}

// An index refused for an instant is copied on the second try, and not built from
// HEAD (which would lose the stat cache, and the skip-worktree bits of a sparse
// checkout); a refusal that is not transient, or one that stays, still falls back.
func TestSeededIndexRetriesOnceWhenTheIndexIsRefused(t *testing.T) {
	repo, wt := radarRepo(t, "wa")
	common, _ := CommonDir(repo)
	s, err := NewScratch(common)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	old := copyIndexFn
	t.Cleanup(func() { copyIndexFn = old })
	ctx := context.Background()
	idx := s.index(wt["wa"])

	calls := 0
	copyIndexFn = func(from, to string) error {
		calls++
		if calls == 1 {
			return &os.PathError{Op: "open", Path: from, Err: fs.ErrPermission}
		}
		return old(from, to)
	}
	if seeded, _ := seedIndex(ctx, wt["wa"], s.envFor(idx), idx); !seeded || calls != 2 {
		t.Errorf("seeded = %v after %d tries, want seeded on the second", seeded, calls)
	}

	calls = 0
	copyIndexFn = func(from, to string) error {
		calls++
		return &os.PathError{Op: "open", Path: from, Err: fs.ErrPermission}
	}
	if seeded, _ := seedIndex(ctx, wt["wa"], s.envFor(idx), idx); seeded || calls != 2 {
		t.Errorf("a refusal that stays: seeded = %v after %d tries, want not seeded after two", seeded, calls)
	}

	calls = 0
	copyIndexFn = func(from, to string) error {
		calls++
		return errors.New("the index was not read whole")
	}
	if seeded, _ := seedIndex(ctx, wt["wa"], s.envFor(idx), idx); seeded || calls != 1 {
		t.Errorf("a torn copy: seeded = %v after %d tries, want not seeded after one", seeded, calls)
	}
}

// An untracked folder holding a repository with no commits makes git add -A fail
// with status 128 and stage nothing: the checkout must still be snapshotted, with
// the folder left out. One with a commit, and a registered submodule beside it,
// are as before.
func TestUntrackedNestedRepositoryWithNoCommitsDoesNotStopTheSnapshot(t *testing.T) {
	t.Parallel()
	repo, wt := radarRepo(t, "wa", "wb")
	for _, name := range []string{"wa", "wb"} {
		dir := wt[name]
		gitRun(t, dir, "init", "-q", "--initial-branch=main", "emb") // no commits in it
		with := filepath.Join(dir, "emb-committed")
		if err := os.MkdirAll(with, 0o700); err != nil {
			t.Fatal(err)
		}
		gitRun(t, with, "init", "-q", "--initial-branch=main")
		write(t, with, "x.txt", "x\n")
		gitRun(t, with, "add", "-A")
		gitRun(t, with, "commit", "-q", "-m", "inside")
	}
	editLine(t, wt["wa"], "a.txt", 5, "from a")
	editLine(t, wt["wb"], "a.txt", 5, "from b")

	paths, conflict := predicted(t, repo, wt["wa"], wt["wb"], true, true)
	if !conflict || len(paths) != 1 || paths[0] != "a.txt" {
		t.Errorf("conflict = %v, paths = %v, want a conflict in a.txt with the nested repositories left out", conflict, paths)
	}
	snap, _, err := snapOf(t, repo, wt["wa"], true)
	if err != nil {
		t.Fatalf("snapshot with an empty nested repository: %v", err)
	}
	if got := sortedPaths(snap.Paths); got != "a.txt" {
		t.Errorf("paths = %s, want only a.txt", got)
	}
}

// A bare repository whose HEAD is pointed at another branch changes the candidates,
// though no ref the candidates came from moved.
func TestBasesFreshSeesABareRepositoryHeadRetargeted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	origin, _ := radarRepo(t)
	bare := filepath.Join(t.TempDir(), "bare.git")
	gitRun(t, t.TempDir(), "clone", "-q", "--bare", origin, bare)
	dir := filepath.Join(t.TempDir(), "w")
	gitRun(t, bare, "worktree", "add", "-q", "-b", "w", dir, "refs/heads/main")
	editLine(t, dir, "a.txt", 3, "w work")
	commitAll(t, dir, "w commits")
	bases, err := ResolveBases(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if fresh, _ := bases.Fresh(ctx); !fresh {
		t.Fatal("setup: not fresh")
	}
	gitRun(t, bare, "symbolic-ref", "HEAD", "refs/heads/w")
	if fresh, _ := bases.Fresh(ctx); fresh {
		t.Error("the bare repository's HEAD was retargeted and Fresh did not see it")
	}
}

// makeUnreadable takes the right to read path away from everybody, including the
// test, and gives it back when the test ends. It skips the test when that cannot be
// done here.
func makeUnreadable(t *testing.T, path string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		if out, err := exec.Command("icacls", path, "/deny", "*S-1-1-0:(R)").CombinedOutput(); err != nil {
			t.Skipf("cannot deny read: %v %s", err, out)
		}
		t.Cleanup(func() { _ = exec.Command("icacls", path, "/remove:d", "*S-1-1-0").Run() })
	} else {
		if err := os.Chmod(path, 0); err != nil {
			t.Skipf("cannot deny read: %v", err)
		}
		t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
	}
	if f, err := os.Open(path); err == nil {
		f.Close()
		t.Skip("the file is still readable (running as an administrator or root?)")
	}
}

// nestedRepos makes n untracked folders holding a repository with no commits and n
// holding one with a commit, in dir.
func nestedRepos(t *testing.T, dir string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		gitRun(t, dir, "init", "-q", "--initial-branch=main", fmt.Sprintf("empty-%d", i))
		with := filepath.Join(dir, fmt.Sprintf("committed-%d", i))
		if err := os.MkdirAll(with, 0o700); err != nil {
			t.Fatal(err)
		}
		gitRun(t, with, "init", "-q", "--initial-branch=main")
		write(t, with, "x.txt", "x\n")
		gitRun(t, with, "add", "-A")
		gitRun(t, with, "commit", "-q", "-m", "inside")
	}
}

// Any number of untracked repositories, with commits and without, are left out, and
// the checkout's own changes are all that is compared.
func TestManyNestedRepositoriesAreLeftOutAndTheRestIsStaged(t *testing.T) {
	t.Parallel()
	repo, wt := radarRepo(t, "wa")
	nestedRepos(t, wt["wa"], 7)
	editLine(t, wt["wa"], "a.txt", 4, "edited")
	write(t, wt["wa"], "new.txt", "new\n")
	snap, _, err := snapOf(t, repo, wt["wa"], true)
	if err != nil {
		t.Fatal(err)
	}
	if got := sortedPaths(snap.Paths); got != "a.txt,new.txt" {
		t.Errorf("paths = %s, want a.txt and new.txt", got)
	}
}

// A file git cannot read, among more nested repositories than git's error message
// keeps lines of, fails the snapshot: the real error comes after the lines kept, and
// the snapshot must not go on without the file because it was cut off.
func TestAnUnreadableFileAmongManyNestedRepositoriesFailsTheSnapshot(t *testing.T) {
	t.Parallel()
	for _, which := range []string{"with commits", "empty", "both"} {
		repo, wt := radarRepo(t, "wa")
		dir := wt["wa"]
		switch which {
		case "both":
			nestedRepos(t, dir, 7)
		case "empty":
			for i := 0; i < 8; i++ {
				gitRun(t, dir, "init", "-q", "--initial-branch=main", fmt.Sprintf("empty-%d", i))
			}
		case "with commits":
			nestedRepos(t, dir, 7)
			for i := 0; i < 7; i++ {
				_ = os.RemoveAll(filepath.Join(dir, fmt.Sprintf("empty-%d", i)))
			}
		}
		write(t, dir, "secret.txt", "cannot be read\n")
		makeUnreadable(t, filepath.Join(dir, "secret.txt"))
		editLine(t, dir, "a.txt", 4, "edited")
		snap, _, err := snapOf(t, repo, dir, true)
		if err == nil {
			t.Errorf("%s: the snapshot succeeded with paths %v, leaving out a file git could not read", which, snap.Paths)
			continue
		}
		if !strings.Contains(err.Error(), "secret.txt") && !strings.Contains(strings.ToLower(err.Error()), "permission denied") {
			t.Errorf("%s: the error does not say what went wrong: %v", which, err)
		}
	}
}

// Whatever happens to a path, a snapshot either has it or is an error: never a
// snapshot that quietly lacks a file. Here a path longer than Windows allows by
// default; where it cannot be made, or git adds it, nothing is wrong either way.
func TestAPathGitCannotAddIsNeverQuietlyLeftOut(t *testing.T) {
	t.Parallel()
	repo, wt := radarRepo(t, "wa")
	dir := wt["wa"]
	long := dir
	for len(long) < dir2len(dir)+300 {
		long = filepath.Join(long, strings.Repeat("d", 40))
	}
	mk := long
	if err := os.MkdirAll(mk, 0o700); err != nil {
		t.Skipf("cannot make a path that long: %v", err)
	}
	if err := os.WriteFile(filepath.Join(mk, "deep.txt"), []byte("deep\n"), 0o600); err != nil {
		t.Skipf("cannot write in a path that long: %v", err)
	}
	snap, _, err := snapOf(t, repo, dir, true)
	if err != nil {
		if runtime.GOOS == "windows" {
			// core.longpaths is set on the scratch commands, so a long path is staged.
			t.Fatalf("a path past MAX_PATH was refused although core.longpaths is on: %v", err)
		}
		return // refused loudly: fine
	}
	found := false
	for _, p := range snap.Paths {
		found = found || strings.HasSuffix(p, "deep.txt")
	}
	if !found {
		t.Errorf("the snapshot succeeded without the deep file: %v", snap.Paths)
	}
}

func dir2len(d string) int { return len(d) }

// A nested repository and a file that are gone by the time git add runs do not
// fail the snapshot: git add -A has nothing to say about what is not there.
func TestAPathDeletedBetweenListingAndAddingIsNotAnError(t *testing.T) {
	repo, wt := radarRepo(t, "wa")
	dir := wt["wa"]
	nestedRepos(t, dir, 2)
	write(t, dir, "vanishing.txt", "gone soon\n")
	editLine(t, dir, "a.txt", 4, "edited")
	old := beforeAdd
	t.Cleanup(func() { beforeAdd = old })
	beforeAdd = func() {
		_ = os.RemoveAll(filepath.Join(dir, "empty-0"))
		_ = os.RemoveAll(filepath.Join(dir, "committed-1"))
		_ = os.Remove(filepath.Join(dir, "vanishing.txt"))
	}
	snap, _, err := snapOf(t, repo, dir, true)
	if err != nil {
		t.Fatalf("snapshot with paths deleted in the gap: %v", err)
	}
	if got := sortedPaths(snap.Paths); got != "a.txt" {
		t.Errorf("paths = %s, want a.txt", got)
	}
}
