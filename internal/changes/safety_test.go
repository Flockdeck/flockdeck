package changes

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// The tests change a checkout between two reads, so they read it afresh; the one
// test of the cache turns it on.
func init() { branchCacheTTL = 0 }

// linkDir makes link a directory link to target: a junction on Windows, which
// needs no privilege, and a symbolic link elsewhere. It says what it made.
func linkDir(t *testing.T, link, target string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
			t.Skipf("no junction here: %v: %s", err, out)
		}
		return "junction"
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}
	return "symlink"
}

// A directory of the checkout that was swapped for a link to somewhere else must
// not be read through: a committed x/secret whose x is now a link to a folder
// outside the checkout is that folder's file, and the window is not given it.
func TestAFileBehindALinkedDirectoryIsNotRead(t *testing.T) {
	dir := repo(t)
	runGit(t, dir, "checkout", "-q", "-b", "feat")
	write(t, dir, "x/id_rsa", "committed\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "x")
	outside := t.TempDir()
	write(t, outside, "id_rsa", "PRIVATE KEY\n")
	if err := os.RemoveAll(filepath.Join(dir, "x")); err != nil {
		t.Fatal(err)
	}
	kind := linkDir(t, filepath.Join(dir, "x"), outside)
	t.Logf("the folder was swapped for a %s", kind)

	if data, _, ok := readWorking(dir, "x/id_rsa"); ok || strings.Contains(string(data), "PRIVATE") {
		t.Errorf("readWorking went through the %s: %q", kind, data)
	}
	if text, problem, found := workingText(dir, "x/id_rsa"); strings.Contains(text, "PRIVATE") || (found && problem == "" && text != "") {
		t.Errorf("workingText = %q %q %v", text, problem, found)
	}
	for _, against := range []string{"merge-base", "worktree", "head"} {
		c, ref := ReadContext(context.Background(), "p1", dir, "x/id_rsa", against, "new", 1, 10)
		if ref == nil {
			for _, l := range c.Lines {
				if strings.Contains(l.Text, "PRIVATE") {
					t.Errorf("%s: diffContext returned the outside file: %+v", against, c)
				}
			}
		}
	}
	d, ref := Diff(context.Background(), "p1", dir, DiffRequest{File: "x/id_rsa", Against: "worktree", Context: 3})
	if ref == nil {
		for _, h := range d.Hunks {
			for _, l := range h.Lines {
				if strings.Contains(l.Text, "PRIVATE") {
					t.Errorf("diffHunks returned the outside file: %+v", d)
				}
			}
		}
	}
}

// A file that is a link is its target text, and a link to a folder is not
// followed.
func TestAFileThatIsALinkIsItsTargetText(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	write(t, outside, "secret.txt", "PRIVATE\n")
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(dir, "l.txt")); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}
	data, _, ok := readWorking(dir, "l.txt")
	if !ok || strings.Contains(string(data), "PRIVATE") || !strings.Contains(string(data), "secret.txt") {
		t.Errorf("a link read as %q, %v, want its target path", data, ok)
	}
}

func TestReadWorkingReadsOnlyARegularFileWithinTheLimit(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "a/ok.txt", "hello\n")
	if data, size, ok := readWorking(dir, "a/ok.txt"); !ok || string(data) != "hello\n" || size != 6 {
		t.Errorf("a plain file: %q %d %v", data, size, ok)
	}
	if _, _, ok := readWorking(dir, "a"); ok {
		t.Error("a folder was read as a file")
	}
	if _, _, ok := readWorking(dir, "../x"); ok {
		t.Error("a path outside the checkout was read")
	}
	if _, _, ok := readWorking(dir, "a/none"); ok {
		t.Error("a missing file was read")
	}
	big := make([]byte, maxContextFile+1)
	if err := os.WriteFile(filepath.Join(dir, "big"), big, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, size, ok := readWorking(dir, "big"); ok || size != maxContextFile+1 {
		t.Errorf("a file over the limit: size %d, ok %v", size, ok)
	}
	if _, problem, found := workingText(dir, "big"); problem == "" || !found {
		t.Errorf("an oversize file: problem %q found %v, want an error", problem, found)
	}
}

// A path is a path and not a pattern, and the diff is of the head the branch read
// named, not of whatever HEAD is by the time git runs.
func TestAPathThatLooksLikeAPatternIsThatFileOnly(t *testing.T) {
	dir := repo(t)
	runGit(t, dir, "checkout", "-q", "-b", "feat")
	write(t, dir, "[ab].txt", "star\n")
	write(t, dir, "a.txt", "one\ntwo changed\nthree\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "star")
	d := diffOf(t, dir, "[ab].txt", "head")
	add, _ := changedLines(d)
	if len(add) != 1 || add[0] != "star" {
		t.Errorf("the diff of [ab].txt holds %q, want only star", add)
	}
	c, ref := ReadContext(context.Background(), "p1", dir, "[ab].txt", "head", "new", 1, 5)
	if ref != nil || len(c.Lines) != 1 || c.Lines[0].Text != "star" {
		t.Errorf("context of [ab].txt: %+v %+v", c, ref)
	}
}

func TestContextOfAnOldSideCannotNameANewerCommit(t *testing.T) {
	dir := repo(t)
	runGit(t, dir, "checkout", "-q", "-b", "feat")
	write(t, dir, "a.txt", "one\ntwo\nthree\nfour\n")
	runGit(t, dir, "commit", "-q", "-am", "four")
	b := Read(context.Background(), "p1", dir, MaxFiles)
	// A commit after the read does not change what the old side of the worktree is.
	write(t, dir, "a.txt", "later\n")
	runGit(t, dir, "commit", "-q", "-am", "later")
	content, problem, found := blobText(context.Background(), dir, b.HeadSha, "a.txt")
	if problem != "" || !found || !strings.HasPrefix(content, "one\n") {
		t.Errorf("the file at the head that was read: %q %q %v", content, problem, found)
	}
}

// A read that cannot be finished says so in error and is not an empty file.
func TestAContextReadThatFailsSetsTheError(t *testing.T) {
	dir := repo(t)
	runGit(t, dir, "checkout", "-q", "-b", "feat")
	write(t, dir, "a.txt", "one\nchanged\nthree\n")
	runGit(t, dir, "commit", "-q", "-am", "edit")
	ctx, cancel := context.WithCancel(context.Background())
	b := Read(ctx, "p1", dir, MaxFiles)
	cancel()
	if _, problem, _ := blobText(ctx, dir, b.HeadSha, "a.txt"); problem == "" {
		t.Error("a read that was cancelled is not an error")
	}
	big := strings.Repeat("x\n", maxContextFile/2+10)
	write(t, dir, "big.txt", big)
	runGit(t, dir, "add", "big.txt")
	runGit(t, dir, "commit", "-q", "-m", "big")
	c, ref := ReadContext(context.Background(), "p1", dir, "big.txt", "head", "new", 1, 5)
	if ref != nil || c.Error == "" || c.Eof || len(c.Lines) != 0 {
		t.Errorf("an oversize file: %+v %+v, want an error and no lines", c, ref)
	}
}

func TestTheSizesOfABinaryFileAreThoseOfTheCommitsForAHeadDiff(t *testing.T) {
	dir := repo(t)
	runGit(t, dir, "checkout", "-q", "-b", "feat")
	write(t, dir, "bin.dat", "a\x00b")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "bin")
	write(t, dir, "bin.dat", "a\x00bcdefgh")
	d := diffOf(t, dir, "bin.dat", "head")
	if !d.Binary || d.SizeAfter == nil || *d.SizeAfter != 3 || d.SizeBefore != nil {
		t.Errorf("head: %+v, want after 3 and no before", d)
	}
	w := diffOf(t, dir, "bin.dat", "merge-base")
	if w.SizeAfter == nil || *w.SizeAfter != 9 {
		t.Errorf("merge-base: %+v, want after 9", w)
	}
}

// The word diff of one diff is bounded as a whole, not only per line.
func TestWordDiffsOfOneDiffShareABudget(t *testing.T) {
	line := strings.Repeat("ab cd ", 100)
	other := strings.Repeat("ab ce ", 100)
	spent := 0
	marked := 0
	for i := 0; i < 1000; i++ {
		if o, _ := wordDiffBudget(line, other, &spent); len(o) > 0 {
			marked++
		}
	}
	if marked == 0 || marked == 1000 || spent > maxIntraCells+(len(tokens(line))+1)*(len(tokens(other))+1) {
		t.Errorf("%d pairs were marked, %d cells spent, limit %d", marked, spent, maxIntraCells)
	}
	var hunks []Hunk
	for i := 0; i < 500; i++ {
		hunks = append(hunks, Hunk{Lines: []Line{{T: "del", Text: line}, {T: "add", Text: other}}})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	markIntra(ctx, hunks)
	for _, h := range hunks {
		if len(h.Lines[0].Intra) > 0 {
			t.Fatal("a cancelled read still marked words")
		}
	}
}

func TestTheBranchIsReusedForAFewSeconds(t *testing.T) {
	branchCacheTTL = 2 * time.Second
	defer func() { branchCacheTTL = 0 }()
	dir := repo(t)
	runGit(t, dir, "checkout", "-q", "-b", "feat")
	write(t, dir, "a.txt", "changed\n")
	first := cachedBranch(context.Background(), "p1", dir)
	write(t, dir, "new.txt", "new\n")
	second := cachedBranch(context.Background(), "p1", dir)
	if len(first.AllFiles()) != len(second.AllFiles()) {
		t.Errorf("the second read was not the first: %d and %d files", len(first.AllFiles()), len(second.AllFiles()))
	}
	time.Sleep(2100 * time.Millisecond)
	if third := cachedBranch(context.Background(), "p1", dir); len(third.AllFiles()) != len(first.AllFiles())+1 {
		t.Errorf("after the time the file is still missing: %d files", len(third.AllFiles()))
	}
}

// A directory swapped for a link that stays inside the checkout, to .git say, is
// refused by both reads: the link is to a place the window was never shown.
func TestAFileBehindALinkInsideTheCheckoutIsRefused(t *testing.T) {
	dir := repo(t)
	runGit(t, dir, "checkout", "-q", "-b", "feat")
	write(t, dir, "x/config", "committed\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "x")
	if err := os.RemoveAll(filepath.Join(dir, "x")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(".git", filepath.Join(dir, "x")); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}
	for _, against := range []string{"merge-base", "worktree"} {
		c, ref := ReadContext(context.Background(), "p1", dir, "x/config", against, "new", 1, 50)
		if ref == nil || ref.Code != "badRequest" || len(c.Lines) != 0 {
			t.Errorf("diffContext %s: %+v %+v, want a refusal", against, c, ref)
		}
		if _, ref := Diff(context.Background(), "p1", dir, DiffRequest{File: "x/config", Against: against, Context: 3}); ref == nil || ref.Code != "badRequest" {
			t.Errorf("diffHunks %s: %+v, want a refusal", against, ref)
		}
	}
}

func TestAMissingFileAtACommitIsAnEmptySideAndNotAnError(t *testing.T) {
	dir := repo(t)
	b := Read(context.Background(), "p1", dir, MaxFiles)
	if _, problem, found := blobText(context.Background(), dir, b.HeadSha, "no/such/file.txt"); problem != "" || found {
		t.Errorf("a missing path: problem %q found %v, want neither", problem, found)
	}
}

// A missing path is told from a failed read by asking the tree, not by reading
// git's message.
func TestMissingAtCommitAsksTheTree(t *testing.T) {
	dir := repo(t)
	b := Read(context.Background(), "p1", dir, MaxFiles)
	if !missingAtCommit(context.Background(), dir, b.HeadSha, "no/such.txt") {
		t.Error("a path that is not in the commit was not called missing")
	}
	if missingAtCommit(context.Background(), dir, b.HeadSha, "a.txt") {
		t.Error("a path that is in the commit was called missing")
	}
	if missingAtCommit(context.Background(), dir, "0000000000000000000000000000000000000000", "a.txt") {
		t.Error("a commit that is not there was called a missing path")
	}
}
