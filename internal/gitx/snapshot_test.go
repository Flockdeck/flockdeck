package gitx

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// radarRepo is newRepo with a thirty line file, committed, and the worktrees
// named, each on a branch of the same name. The first is the main checkout.
// A git too old for merge-tree skips the test.
func radarRepo(t *testing.T, names ...string) (repo string, trees map[string]string) {
	t.Helper()
	repo = newRepo(t)
	if ok, why := CheckMergeTree(); !ok {
		t.Skip("merge-tree --write-tree is not available: " + why)
	}
	var lines []string
	for i := 1; i <= 30; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	write(t, repo, "a.txt", strings.Join(lines, "\n")+"\n")
	write(t, repo, "b.txt", "one\ntwo\nthree\nfour\nfive\nsix\nseven\neight\n")
	write(t, repo, "bin.dat", "\x00\x01\x02 base\x00\n")
	gitRun(t, repo, "add", "-A")
	gitRun(t, repo, "commit", "-m", "base files")
	trees = map[string]string{}
	for _, name := range names {
		dir := filepath.Join(t.TempDir(), name)
		if err := AddNewBranch(repo, dir, name); err != nil {
			t.Fatal(err)
		}
		trees[name] = dir
	}
	return repo, trees
}

// predicted snapshots two checkouts and merges them, the way the radar does.
func predicted(t *testing.T, repo, a, b string, aDirty, bDirty bool) (paths []string, conflict bool) {
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
	base := BaseOf(ctx, repo)
	sa, err := Snapshot(ctx, a, s, base, aDirty)
	if err != nil {
		t.Fatalf("snapshot of %s: %v", a, err)
	}
	sb, err := Snapshot(ctx, b, s, base, bDirty)
	if err != nil {
		t.Fatalf("snapshot of %s: %v", b, err)
	}
	if sa.Empty || sb.Empty {
		t.Fatalf("a snapshot was empty: %+v %+v", sa, sb)
	}
	out, conflict, err := MergeTree(ctx, repo, s, sa.Commit, sb.Commit)
	if err != nil {
		t.Fatalf("merge-tree: %v", err)
	}
	// The tree id, then the conflicted names.
	fields := strings.Split(strings.TrimRight(out, "\x00"), "\x00")
	if len(fields) == 0 || len(fields[0]) < 40 {
		t.Fatalf("merge-tree said %q, which does not start with a tree id", out)
	}
	for _, f := range fields[1:] {
		if f != "" {
			paths = append(paths, f)
		}
	}
	sort.Strings(paths)
	return paths, conflict
}

func editLine(t *testing.T, dir, name string, n int, text string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(data), "\n")
	lines[n-1] = text
	write(t, dir, name, strings.Join(lines, "\n"))
}

func commitAll(t *testing.T, dir, msg string) {
	t.Helper()
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-m", msg)
}

// An edit on line 5 that is not committed, against a commit on line 6, is the
// spike's adjacent-line case: git's own merge calls it a conflict.
func TestSnapshotsOfAdjacentEditsConflict(t *testing.T) {
	t.Parallel()
	repo, wt := radarRepo(t, "wa", "wb")
	editLine(t, wt["wa"], "a.txt", 5, "changed by a")
	editLine(t, wt["wb"], "a.txt", 6, "changed by b")
	commitAll(t, wt["wb"], "b edits line 6")

	paths, conflict := predicted(t, repo, wt["wa"], wt["wb"], true, false)
	if !conflict || len(paths) != 1 || paths[0] != "a.txt" {
		t.Errorf("conflict = %v, paths = %v, want a conflict in a.txt", conflict, paths)
	}
}

func TestSnapshotsOfFarApartEditsMergeCleanly(t *testing.T) {
	t.Parallel()
	repo, wt := radarRepo(t, "wa", "wb")
	editLine(t, wt["wa"], "a.txt", 5, "changed by a")
	editLine(t, wt["wb"], "a.txt", 25, "changed by b")
	commitAll(t, wt["wb"], "b edits line 25")

	paths, conflict := predicted(t, repo, wt["wa"], wt["wb"], true, false)
	if conflict || len(paths) != 0 {
		t.Errorf("conflict = %v, paths = %v, want none", conflict, paths)
	}
}

// One pane renames a file the other edits: git carries the edit across, so it
// is not a textual conflict. Both paths are still in the changed set, which is
// what lets the radar's filter bring the pair to merge-tree at all.
func TestRenameAgainstEditIsClean(t *testing.T) {
	t.Parallel()
	repo, wt := radarRepo(t, "wa", "wb")
	gitRun(t, wt["wa"], "mv", "b.txt", "d.txt")
	commitAll(t, wt["wa"], "a renames b.txt")
	editLine(t, wt["wb"], "b.txt", 3, "edited by b")

	paths, conflict := predicted(t, repo, wt["wa"], wt["wb"], false, true)
	if conflict || len(paths) != 0 {
		t.Errorf("conflict = %v, paths = %v, want a clean merge", conflict, paths)
	}

	ctx := context.Background()
	common, _ := CommonDir(repo)
	s, err := NewScratch(common)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	snap, err := Snapshot(ctx, wt["wa"], s, BaseOf(ctx, repo), false)
	if err != nil {
		t.Fatal(err)
	}
	got := append([]string{}, snap.Paths...)
	sort.Strings(got)
	if strings.Join(got, ",") != "b.txt,d.txt" {
		t.Errorf("paths of a rename = %v, want both b.txt and d.txt", got)
	}
}

func TestDeleteAgainstEditConflicts(t *testing.T) {
	t.Parallel()
	repo, wt := radarRepo(t, "wa", "wb")
	gitRun(t, wt["wa"], "rm", "-q", "b.txt")
	commitAll(t, wt["wa"], "a deletes b.txt")
	editLine(t, wt["wb"], "b.txt", 3, "edited by b")

	paths, conflict := predicted(t, repo, wt["wa"], wt["wb"], false, true)
	if !conflict || len(paths) != 1 || paths[0] != "b.txt" {
		t.Errorf("conflict = %v, paths = %v, want a conflict in b.txt", conflict, paths)
	}
}

// Two new files at one path are in no commit and in no index, and still meet.
func TestUntrackedAgainstUntrackedAtOnePathConflicts(t *testing.T) {
	t.Parallel()
	repo, wt := radarRepo(t, "wa", "wb")
	write(t, wt["wa"], "notes.txt", "written by a\n")
	write(t, wt["wb"], "notes.txt", "written by b\n")

	paths, conflict := predicted(t, repo, wt["wa"], wt["wb"], true, true)
	if !conflict || len(paths) != 1 || paths[0] != "notes.txt" {
		t.Errorf("conflict = %v, paths = %v, want a conflict in notes.txt", conflict, paths)
	}
}

func TestBinaryFileEditedOnBothSidesConflicts(t *testing.T) {
	t.Parallel()
	repo, wt := radarRepo(t, "wa", "wb")
	write(t, wt["wa"], "bin.dat", "\x00\x01\x02 from a\x00\n")
	write(t, wt["wb"], "bin.dat", "\x00\x01\x02 from b\x00\n")
	commitAll(t, wt["wb"], "b changes the binary")

	paths, conflict := predicted(t, repo, wt["wa"], wt["wb"], true, false)
	if !conflict || len(paths) != 1 || paths[0] != "bin.dat" {
		t.Errorf("conflict = %v, paths = %v, want a conflict in bin.dat", conflict, paths)
	}
}

// What matters here is the snapshot's whole idea: nothing of the checkout is
// touched. Its index is byte for byte what it was and no lock is left, no
// object is added to the repository, and the scratch is gone after Close.
func TestSnapshotLeavesTheCheckoutAlone(t *testing.T) {
	t.Parallel()
	repo, wt := radarRepo(t, "wa")
	write(t, wt["wa"], "fresh.txt", "new\n")
	editLine(t, wt["wa"], "a.txt", 2, "edited")

	indexPath := strings.TrimSpace(gitRun(t, wt["wa"], "rev-parse", "--git-path", "index"))
	if !filepath.IsAbs(indexPath) {
		indexPath = filepath.Join(wt["wa"], indexPath)
	}
	before, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	statBefore, _ := os.Stat(indexPath)
	gitDirBefore := gitDirState(t, filepath.Join(repo, ".git"))

	common, _ := CommonDir(repo)
	s, err := NewScratch(common)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	snap, err := Snapshot(ctx, wt["wa"], s, BaseOf(ctx, repo), true)
	if err != nil {
		t.Fatal(err)
	}
	got := append([]string{}, snap.Paths...)
	sort.Strings(got)
	if strings.Join(got, ",") != "a.txt,fresh.txt" {
		t.Errorf("paths = %v, want a.txt and fresh.txt", got)
	}

	after, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("the checkout's index was changed")
	}
	if statAfter, _ := os.Stat(indexPath); !statAfter.ModTime().Equal(statBefore.ModTime()) {
		t.Error("the checkout's index was rewritten")
	}
	if _, err := os.Stat(indexPath + ".lock"); err == nil {
		t.Error("the checkout's index lock was left behind")
	}
	if gitDirAfter := gitDirState(t, filepath.Join(repo, ".git")); gitDirBefore != gitDirAfter {
		t.Errorf("the repository git directory changed:\n%s", lineDiff(gitDirBefore, gitDirAfter))
	}
	// And the working tree still says the same to git.
	if st := StatusOf(wt["wa"]); st.Dirty != 1 || st.Untracked != 1 {
		t.Errorf("status after = %+v, want 1 changed and 1 untracked", st)
	}

	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.Root()); !os.IsNotExist(err) {
		t.Errorf("the scratch directory is still there: %v", err)
	}
}

// gitDirState lists everything under a git directory with each file's size and
// modification time, so that a file written, rewritten or removed there shows.
// The time of an object file or a shared index is left out: git sets it again,
// on a file it finds it would have written, to keep gc from pruning what it
// just used, and that is the one thing it does to the real git directory that
// cannot be turned off. Their names and sizes still are listed.
func gitDirState(t *testing.T, root string) string {
	t.Helper()
	var lines []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		mtime := info.ModTime().UnixNano()
		if strings.HasPrefix(rel, "objects/") || strings.Contains(filepath.Base(rel), "sharedindex.") {
			mtime = 0
		}
		lines = append(lines, fmt.Sprintf("%s %d %d", rel, info.Size(), mtime))
		return nil
	})
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// A real index that git cannot read is no reason to give up on the pane: the
// scratch index is built from HEAD instead, and the answer is the same.
func TestSnapshotFallsBackWhenTheIndexCannotBeCopied(t *testing.T) {
	t.Parallel()
	repo, wt := radarRepo(t, "wa")
	editLine(t, wt["wa"], "a.txt", 2, "edited")
	indexPath := strings.TrimSpace(gitRun(t, wt["wa"], "rev-parse", "--git-path", "index"))
	if !filepath.IsAbs(indexPath) {
		indexPath = filepath.Join(wt["wa"], indexPath)
	}
	full, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}

	common, _ := CommonDir(repo)
	s, err := NewScratch(common)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	env := s.envFor(s.index(wt["wa"]))
	if seeded, _ := seedIndex(ctx, wt["wa"], env, s.index(wt["wa"])); !seeded {
		t.Fatal("an intact index was not taken as readable")
	}
	// A torn copy: the first half of the file. git ls-files lists what it
	// finds in one without complaint, so the checksum is what catches it.
	if err := os.WriteFile(indexPath, full[:len(full)/2], 0o600); err != nil {
		t.Fatal(err)
	}
	if seeded, _ := seedIndex(ctx, wt["wa"], env, s.index(wt["wa"])); seeded {
		t.Fatal("a half-written index was taken as readable")
	}
	snap, err := Snapshot(ctx, wt["wa"], s, BaseOf(ctx, repo), true)
	if err != nil {
		t.Fatalf("snapshot with an unreadable index: %v", err)
	}
	if len(snap.Paths) != 1 || snap.Paths[0] != "a.txt" {
		t.Errorf("paths = %v, want a.txt", snap.Paths)
	}
}

// A checkout with nothing against the base is not snapshotted, and a branch
// whose work is all committed is, without a scratch index.
func TestSnapshotOfCleanAndCommittedCheckouts(t *testing.T) {
	t.Parallel()
	repo, wt := radarRepo(t, "wa", "wb")
	common, _ := CommonDir(repo)
	s, err := NewScratch(common)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	base := BaseOf(ctx, repo)

	if snap, err := Snapshot(ctx, wt["wa"], s, base, false); err != nil || !snap.Empty {
		t.Errorf("a clean checkout at the base: %+v, %v, want Empty", snap, err)
	}

	write(t, wt["wb"], "c.txt", "committed\n")
	commitAll(t, wt["wb"], "work")
	snap, err := Snapshot(ctx, wt["wb"], s, base, false)
	if err != nil || snap.Empty || len(snap.Paths) != 1 || snap.Paths[0] != "c.txt" {
		t.Errorf("a committed branch: %+v, %v, want c.txt", snap, err)
	}
	if head := strings.TrimSpace(gitRun(t, wt["wb"], "rev-parse", "HEAD")); snap.Commit != head {
		t.Errorf("a checkout with nothing uncommitted should be snapshotted as its HEAD, got %s", snap.Commit)
	}
}

// Before the first commit there is no HEAD to stand on.
func TestSnapshotOfAnUnbornCheckoutFails(t *testing.T) {
	t.Parallel()
	if !Available() {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	gitRun(t, dir, "init", "--initial-branch=main")
	write(t, dir, "x.txt", "x\n")
	common, err := CommonDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewScratch(common)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if snap, err := Snapshot(context.Background(), dir, s, "", true); err == nil {
		t.Errorf("snapshot of an unborn branch = %+v, want an error", snap)
	}
}

// A submodule moved to another commit is a changed path like any other: two panes
// bumping it differently must meet, and merge-tree decides.
func TestSnapshotIncludesASubmoduleBump(t *testing.T) {
	t.Parallel()
	repo, wt := radarRepo(t, "wa")
	inner := newRepo(t)
	gitRun(t, repo, "-c", "protocol.file.allow=always", "submodule", "add", "-q", inner, "vendor")
	gitRun(t, repo, "commit", "-m", "add submodule")
	// The worktree branch was made before the submodule; move it up to it.
	gitRun(t, wt["wa"], "merge", "-q", "main")
	write(t, wt["wa"], "a.txt", "changed\n")

	common, _ := CommonDir(repo)
	s, err := NewScratch(common)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	// Measured from before the submodule was added, so that its gitlink is in
	// the set of changes.
	base := strings.TrimSpace(gitRun(t, repo, "rev-parse", "main~1"))
	snap, err := Snapshot(ctx, wt["wa"], s, base, true)
	if err != nil {
		t.Fatal(err)
	}
	has := map[string]bool{}
	for _, p := range snap.Paths {
		has[p] = true
	}
	if !has["vendor"] || !has["a.txt"] {
		t.Errorf("paths = %v, want the submodule path and a.txt", snap.Paths)
	}
}

func TestSnapshotRefusesATreeOfTooManyChanges(t *testing.T) {
	t.Parallel()
	repo, wt := radarRepo(t, "wa")
	for i := 0; i <= MaxSnapshotFiles; i++ {
		write(t, wt["wa"], fmt.Sprintf("gen-%04d.txt", i), "x\n")
	}
	common, _ := CommonDir(repo)
	s, err := NewScratch(common)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	_, err = Snapshot(ctx, wt["wa"], s, BaseOf(ctx, repo), true)
	if !errors.Is(err, ErrTooManyFiles) {
		t.Errorf("err = %v, want ErrTooManyFiles", err)
	}
}

func TestMergeTreeNeedsGit238(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		out  string
		ok   bool
		want string
	}{
		{"git version 2.43.0.windows.1\n", true, ""},
		{"git version 2.38.0\n", true, ""},
		{"git version 2.39.5 (Apple Git-154)\n", true, ""},
		{"git version 3.0.0\n", true, ""},
		{"git version 2.37.9\n", false, "needs git 2.38 or newer, and this is git 2.37"},
		{"git version 2.34.1\n", false, "needs git 2.38 or newer, and this is git 2.34"},
		{"git version 1.9.5\n", false, "this is git 1.9"},
		{"git version 2.38rc1\n", true, ""},
		{"not git at all\n", false, "could not read the version"},
		{"", false, "could not read the version"},
	} {
		ok, why := checkMergeTree(tc.out, nil)
		if ok != tc.ok || !strings.Contains(why, tc.want) {
			t.Errorf("checkMergeTree(%q) = %v, %q; want %v, containing %q", tc.out, ok, why, tc.ok, tc.want)
		}
	}
	if ok, why := checkMergeTree("", errors.New("boom")); ok || !strings.Contains(why, "boom") {
		t.Errorf("a failed version probe = %v, %q, want it off and saying why", ok, why)
	}
}

// lineDiff says which lines of a and b are in one and not the other.
func lineDiff(a, b string) string {
	in := func(s string) map[string]bool {
		m := map[string]bool{}
		for _, l := range strings.Split(s, "\n") {
			m[l] = true
		}
		return m
	}
	ma, mb := in(a), in(b)
	var out []string
	for l := range ma {
		if !mb[l] {
			out = append(out, "- "+l)
		}
	}
	for l := range mb {
		if !ma[l] {
			out = append(out, "+ "+l)
		}
	}
	sort.Strings(out)
	return strings.Join(out, "\n")
}

// A checkout holding one file past the size limit is not copied into the
// scratch store: Snapshot says ErrTooBig before git add reads it.
func TestSnapshotRefusesAFileOverTheSizeLimit(t *testing.T) {
	t.Parallel()
	repo, wt := radarRepo(t, "wa")
	big := make([]byte, maxSnapshotFileBytes+1)
	if err := os.WriteFile(filepath.Join(wt["wa"], "dump.bin"), big, 0o600); err != nil {
		t.Fatal(err)
	}
	snap, s, err := snapOf(t, repo, wt["wa"], true)
	if !errors.Is(err, ErrTooBig) {
		t.Fatalf("err = %v, want ErrTooBig", err)
	}
	if snap.Commit != "" {
		t.Errorf("a snapshot was made: %+v", snap)
	}
	// Nothing was written to the scratch object store.
	if n := countFiles(t, filepath.Join(s.Root(), "obj")); n > 1 {
		t.Errorf("%d files in the scratch object store, want only the alternates file", n)
	}
	// The same name with a small file in it is copied as usual.
	if err := os.WriteFile(filepath.Join(wt["wa"], "dump.bin"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := snapOf(t, repo, wt["wa"], true); err != nil {
		t.Errorf("a small file: %v", err)
	}
}

func countFiles(t *testing.T, root string) int {
	t.Helper()
	n := 0
	_ = filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			n++
		}
		return nil
	})
	return n
}

func TestCheckSizeAddsUpTheFilesAndSkipsWhatIsNotOne(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, f := range []string{"a", "b", "c"} {
		write(t, dir, f, strings.Repeat("x", 10))
	}
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	listed := []string{"a", "b", "c", "gone", "sub", "nested/", ""}
	if err := checkSize(dir, listed, 10, 30); err != nil {
		t.Errorf("30 bytes against 30: %v", err)
	}
	if err := checkSize(dir, listed, 10, 29); !errors.Is(err, ErrTooBig) {
		t.Errorf("30 bytes against 29: %v, want ErrTooBig", err)
	}
	if err := checkSize(dir, listed, 9, 1000); !errors.Is(err, ErrTooBig) {
		t.Errorf("a 10 byte file against 9: %v, want ErrTooBig", err)
	}
}
