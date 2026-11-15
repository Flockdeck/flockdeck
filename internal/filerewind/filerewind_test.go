package filerewind

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t.invalid", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func write(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, dir, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		return "<" + err.Error() + ">"
	}
	return strings.ReplaceAll(string(b), "\r\n", "\n")
}

func exists(dir, rel string) bool {
	_, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(rel)))
	return err == nil
}

// newRepo makes a repository with committed a.txt and b.txt, ignoring ignored/ and *.log.
func newRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	// The temp folder may be inside a repository on a developer machine.
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	gitIn(t, dir, "init", "-q")
	write(t, dir, "a.txt", "a1\n")
	write(t, dir, "b.txt", "b1\n")
	write(t, dir, ".gitignore", "ignored/\n*.log\n")
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "first")
	return dir
}

func paths(l Listing) string {
	var out []string
	for _, c := range l.Changes {
		out = append(out, string(c.Action)+":"+c.Path)
	}
	return strings.Join(out, ",")
}

func confirmAll(l Listing) []Confirm {
	out := []Confirm{}
	for _, c := range l.Changes {
		out = append(out, Confirm{Path: c.Path, ID: c.ID})
	}
	return out
}

func blockedHas(err error, path, reason string) bool {
	var be *BlockedError
	if !errors.As(err, &be) {
		return false
	}
	for _, b := range be.Items {
		if b.Path == path && b.Reason == reason {
			return true
		}
	}
	return false
}

func capture(t *testing.T, dir, name string) Checkpoint {
	t.Helper()
	cp, err := Capture(context.Background(), dir, RefPrefix+name)
	if err != nil {
		t.Fatal(err)
	}
	return cp
}

func TestRestoreEditedDeletedAndCreated(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "a.txt", "a2\n")
	write(t, dir, "c.txt", "c1\n")
	write(t, dir, "ignored/x.txt", "x1\n")
	ctx := context.Background()
	cp := capture(t, dir, "t/1")
	write(t, dir, "a.txt", "a3\n")
	if err := os.Remove(filepath.Join(dir, "b.txt")); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "c.txt", "c2\n")
	write(t, dir, "new/d.txt", "d1\n")
	write(t, dir, "ignored/x.txt", "x2\n")

	plan, err := Plan(ctx, dir, cp.Tree)
	if err != nil {
		t.Fatal(err)
	}
	if want := "overwrite:a.txt,recreate:b.txt,overwrite:c.txt,remove:new/d.txt"; paths(plan) != want || len(plan.Blocked) != 0 {
		t.Fatalf("plan = %s (blocked %v), want %s", paths(plan), plan.Blocked, want)
	}
	res, err := Restore(ctx, dir, cp.Tree, confirmAll(plan), "t-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Failed) != 0 || res.Removed != 1 || res.Written != 3 || res.Undo != UndoPrefix+"t-1" {
		t.Fatalf("result = %+v", res)
	}
	for rel, body := range map[string]string{"a.txt": "a2\n", "b.txt": "b1\n", "c.txt": "c1\n", "ignored/x.txt": "x2\n"} {
		if read(t, dir, rel) != body {
			t.Errorf("%s = %q, want %q", rel, read(t, dir, rel), body)
		}
	}
	if exists(dir, "new/d.txt") || exists(dir, "new") {
		t.Error("the file created later, and its empty folder, should be gone")
	}
	if got := gitIn(t, dir, "show", res.Undo+":a.txt"); strings.ReplaceAll(got, "\r\n", "\n") != "a3\n" {
		t.Errorf("undo ref holds a.txt = %q", got)
	}
}

func TestRestoreLeavesIndexAndBranchAlone(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "a.txt", "a2\n")
	gitIn(t, dir, "add", "a.txt")
	cp := capture(t, dir, "t/2")
	headBefore := gitIn(t, dir, "rev-parse", "HEAD")
	stagedBefore := gitIn(t, dir, "diff", "--cached")
	if !strings.Contains(stagedBefore, "+a2") {
		t.Fatalf("staged diff = %q", stagedBefore)
	}
	write(t, dir, "a.txt", "a3\n")
	plan, _ := Plan(context.Background(), dir, cp.Tree)
	if _, err := Restore(context.Background(), dir, cp.Tree, confirmAll(plan), "t-2"); err != nil {
		t.Fatal(err)
	}
	if gitIn(t, dir, "rev-parse", "HEAD") != headBefore {
		t.Error("HEAD moved")
	}
	if got := gitIn(t, dir, "diff", "--cached"); got != stagedBefore {
		t.Errorf("staged diff after restore = %q, want %q", got, stagedBefore)
	}
	if got := gitIn(t, dir, "diff"); got != "" {
		t.Errorf("unstaged diff after restore = %q", got)
	}
}

// Review item 4: a listed file edited after the list was shown is not on the
// confirmed list any more, although its path is.
func TestARestoreRefusesAFileEditedAfterTheListWasShown(t *testing.T) {
	dir := newRepo(t)
	ctx := context.Background()
	cp := capture(t, dir, "t/3")
	write(t, dir, "a.txt", "a2\n")
	plan, err := Plan(ctx, dir, cp.Tree)
	if err != nil {
		t.Fatal(err)
	}
	confirmed := confirmAll(plan)
	write(t, dir, "a.txt", "a3 edited later\n")
	_, err = Restore(ctx, dir, cp.Tree, confirmed, "t-3")
	var ce *ChangedError
	if !errors.As(err, &ce) || !errors.Is(err, ErrChanged) {
		t.Fatalf("err = %v", err)
	}
	if read(t, dir, "a.txt") != "a3 edited later\n" {
		t.Error("the later edit was overwritten")
	}
	if gitIn(t, dir, "for-each-ref", UndoPrefix) != "" {
		t.Error("an undo ref was made for a refused restore")
	}
	if err := Check(ctx, dir, cp.Tree, confirmed); !errors.Is(err, ErrChanged) {
		t.Errorf("check err = %v", err)
	}
	// A path added or listed twice is refused too.
	now, _ := Plan(ctx, dir, cp.Tree)
	c := confirmAll(now)
	for _, bad := range [][]Confirm{append(c, Confirm{Path: "zzz", ID: "x"}), append(c, c[0]), {}} {
		if _, err := Restore(ctx, dir, cp.Tree, bad, "t-3"); !errors.Is(err, ErrChanged) {
			t.Errorf("%v: err = %v", bad, err)
		}
	}
}

// Review item 1a: a checkpointed file that is a folder now, with an ignored file in it.
func TestARestoreNeverDeletesAnIgnoredFileInAFolderInTheWay(t *testing.T) {
	dir := newRepo(t)
	ctx := context.Background()
	write(t, dir, "x", "x was a file\n")
	cp := capture(t, dir, "t/4")
	if err := os.Remove(filepath.Join(dir, "x")); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "x/ignored/keep.txt", "precious\n")
	write(t, dir, "x/seen.txt", "visible\n")
	plan, err := Plan(ctx, dir, cp.Tree)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Blocked) == 0 {
		t.Fatalf("plan = %s, nothing blocked", paths(plan))
	}
	_, err = Restore(ctx, dir, cp.Tree, confirmAll(plan), "t-4")
	if !blockedHas(err, "x", ReasonFolder) {
		t.Fatalf("err = %v, plan %+v", err, plan.Blocked)
	}
	if read(t, dir, "x/ignored/keep.txt") != "precious\n" || read(t, dir, "x/seen.txt") != "visible\n" {
		t.Error("a file was touched by a refused restore")
	}
	if gitIn(t, dir, "for-each-ref", UndoPrefix) != "" {
		t.Error("an undo ref was made for a refused restore")
	}
}

// Review item 1b: a file that was untracked at the checkpoint and is ignored now.
func TestARestoreNeverOverwritesAFileThatIsIgnoredNow(t *testing.T) {
	dir := newRepo(t)
	ctx := context.Background()
	write(t, dir, "notes.txt", "old notes\n")
	cp := capture(t, dir, "t/5")
	write(t, dir, ".gitignore", "ignored/\n*.log\nnotes.txt\n")
	write(t, dir, "notes.txt", "new notes\n")
	plan, err := Plan(ctx, dir, cp.Tree)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Restore(ctx, dir, cp.Tree, confirmAll(plan), "t-5")
	if !blockedHas(err, "notes.txt", ReasonIgnored) {
		t.Fatalf("err = %v, plan %s %+v", err, paths(plan), plan.Blocked)
	}
	if read(t, dir, "notes.txt") != "new notes\n" {
		t.Error("the ignored file was overwritten")
	}
}

// A file where the saved files had a folder.
func TestARestoreRefusesToReplaceAnIgnoredFileThatIsInTheWay(t *testing.T) {
	dir := newRepo(t)
	ctx := context.Background()
	write(t, dir, "d/inner.txt", "inner\n")
	cp := capture(t, dir, "t/6")
	if err := os.RemoveAll(filepath.Join(dir, "d")); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "d", "now a file\n")
	write(t, dir, ".gitignore", "ignored/\n*.log\nd\n")
	plan, _ := Plan(ctx, dir, cp.Tree)
	_, err := Restore(ctx, dir, cp.Tree, confirmAll(plan), "t-6")
	if !blockedHas(err, "d/inner.txt", ReasonInWay) {
		t.Fatalf("err = %v, plan %s %+v", err, paths(plan), plan.Blocked)
	}
	if read(t, dir, "d") != "now a file\n" {
		t.Error("the file in the way was removed")
	}
}

// Review item 2: a junction or symbolic link inside the folder.
func makeLink(t *testing.T, link, target string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput()
		if err != nil {
			t.Skipf("no junction: %v %s", err, out)
		}
		return
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("no symlink: %v", err)
	}
}

func TestAFileBehindALinkIsNeitherSavedNorWritten(t *testing.T) {
	dir := newRepo(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "out.txt"), []byte("outside 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	makeLink(t, filepath.Join(dir, "link"), outside)
	ctx := context.Background()
	cp := capture(t, dir, "t/7")
	// Nothing behind the link is in the checkpoint.
	if out := gitIn(t, dir, "ls-tree", "-r", "--name-only", cp.Tree); strings.Contains(out, "out.txt") || strings.Contains(out, "link") {
		t.Fatalf("the checkpoint holds files behind the link: %q", out)
	}
	if err := os.WriteFile(filepath.Join(outside, "out.txt"), []byte("outside 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "a.txt", "a2\n")
	plan, err := Plan(ctx, dir, cp.Tree)
	if err != nil {
		t.Fatal(err)
	}
	if want := "overwrite:a.txt"; paths(plan) != want {
		t.Fatalf("plan = %s, want %s", paths(plan), want)
	}
	if _, err := Restore(ctx, dir, cp.Tree, confirmAll(plan), "t-7"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(outside, "out.txt")); strings.ReplaceAll(string(b), "\r\n", "\n") != "outside 2\n" {
		t.Errorf("a file outside the folder was changed: %q", b)
	}
}

func TestARestoreThatNeedsALinkIsRefused(t *testing.T) {
	dir := newRepo(t)
	outside := t.TempDir()
	write(t, dir, "sub/f.txt", "f1\n")
	cp := capture(t, dir, "t/8")
	if err := os.RemoveAll(filepath.Join(dir, "sub")); err != nil {
		t.Fatal(err)
	}
	makeLink(t, filepath.Join(dir, "sub"), outside)
	ctx := context.Background()
	plan, err := Plan(ctx, dir, cp.Tree)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Restore(ctx, dir, cp.Tree, confirmAll(plan), "t-8")
	if !blockedHas(err, "sub/f.txt", ReasonLink) {
		t.Fatalf("err = %v, plan %s %+v", err, paths(plan), plan.Blocked)
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Errorf("something was written through the link: %v", entries)
	}
}

// Review item 5: a smudge filter is not run.
func TestAFileWithAGitFilterIsRefusedAndTheFilterNeverRuns(t *testing.T) {
	dir := newRepo(t)
	marker := filepath.Join(t.TempDir(), "ran")
	write(t, dir, ".gitattributes", "*.dat filter=probe\n")
	write(t, dir, "p.dat", "one\n")
	cp := capture(t, dir, "t/9")
	// The driver is configured only now, so capturing did not run it.
	script := "touch " + filepath.ToSlash(marker)
	if runtime.GOOS == "windows" {
		script = "echo x> " + marker
	}
	gitIn(t, dir, "config", "filter.probe.smudge", script)
	write(t, dir, "p.dat", "two\n")
	ctx := context.Background()
	plan, err := Plan(ctx, dir, cp.Tree)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Restore(ctx, dir, cp.Tree, confirmAll(plan), "t-9")
	if !blockedHas(err, "p.dat", ReasonFilter) {
		t.Fatalf("err = %v, plan %s %+v", err, paths(plan), plan.Blocked)
	}
	if exists(filepath.Dir(marker), "ran") {
		t.Error("the filter ran")
	}
}

// Review item 6: GIT_DIR in the environment must not redirect the ref.
func TestAGitDirInTheEnvironmentDoesNotChooseTheRepository(t *testing.T) {
	a := newRepo(t)
	b := newRepo(t)
	t.Setenv("GIT_DIR", filepath.Join(b, ".git"))
	t.Setenv("GIT_WORK_TREE", b)
	capture(t, a, "t/10")
	t.Setenv("GIT_DIR", "")
	os.Unsetenv("GIT_DIR")
	os.Unsetenv("GIT_WORK_TREE")
	if gitIn(t, a, "for-each-ref", RefPrefix) == "" {
		t.Error("the ref is not in the pane's repository")
	}
	if gitIn(t, b, "for-each-ref", RefPrefix) != "" {
		t.Error("the ref went into the other repository")
	}
}

// Review item 7: a folder too big to save quickly is not saved, and the answer says so.
func TestAFolderWithTooManyOrTooLargeFilesIsNotSaved(t *testing.T) {
	dir := newRepo(t)
	saved := limits
	defer func() { limits = saved }()
	limits.files = 3
	for i := 0; i < 4; i++ {
		write(t, dir, "u"+string(rune('a'+i))+".txt", "x\n")
	}
	if _, err := Capture(context.Background(), dir, RefPrefix+"t/11"); !errors.Is(err, ErrTooBig) {
		t.Fatalf("too many: err = %v", err)
	}
	limits = saved
	limits.file = 10
	write(t, dir, "big.bin", strings.Repeat("y", 100))
	if _, err := Capture(context.Background(), dir, RefPrefix+"t/11"); !errors.Is(err, ErrTooBig) {
		t.Fatalf("too large: err = %v", err)
	}
	if gitIn(t, dir, "for-each-ref", RefPrefix) != "" {
		t.Error("a ref was made for a checkpoint that was not taken")
	}
}

// Review item 8.
func TestSweepRemovesStaleRunsAndTrimsUndoRefs(t *testing.T) {
	dir := newRepo(t)
	capture(t, dir, "deadrun/1")
	capture(t, dir, "liverun/1")
	Register(dir, "liverun")
	defer Unregister("liverun")
	head := strings.TrimSpace(gitIn(t, dir, "rev-parse", "HEAD"))
	for i := 0; i < UndoMaxCount+2; i++ {
		gitIn(t, dir, "update-ref", UndoPrefix+"r/u"+string(rune('a'+i%26))+string(rune('a'+i/26)), head)
	}
	r, _ := locate(context.Background(), dir)
	r.sweep(time.Now())
	if refs := gitIn(t, dir, "for-each-ref", "--format=%(refname)", RefPrefix); !strings.Contains(refs, "deadrun") || !strings.Contains(refs, "liverun") {
		t.Errorf("a recent checkpoint was swept: %q", refs)
	}
	if n := strings.Count(gitIn(t, dir, "for-each-ref", UndoPrefix), "\n"); n != UndoMaxCount {
		t.Errorf("undo refs = %d, want %d", n, UndoMaxCount)
	}
	r.sweep(time.Now().Add(StaleRunAge + time.Hour))
	refs := gitIn(t, dir, "for-each-ref", "--format=%(refname)", RefPrefix)
	if strings.Contains(refs, "deadrun") || !strings.Contains(refs, "liverun") {
		t.Errorf("after a week: %q", refs)
	}
	r.sweep(time.Now().Add(UndoMaxAge + time.Hour))
	if gitIn(t, dir, "for-each-ref", UndoPrefix) != "" {
		t.Error("old undo refs were kept")
	}
}

// Review item 10.
func TestANestedRepositoryIsReportedAndNotTouched(t *testing.T) {
	dir := newRepo(t)
	inner := filepath.Join(dir, "inner")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, inner, "init", "-q")
	write(t, inner, "i.txt", "i1\n")
	gitIn(t, inner, "add", "-A")
	gitIn(t, inner, "commit", "-q", "-m", "inner")
	cp := capture(t, dir, "t/12")
	write(t, inner, "i.txt", "i2\n")
	gitIn(t, inner, "add", "-A")
	gitIn(t, inner, "commit", "-q", "-m", "inner two")
	write(t, dir, "a.txt", "a2\n")
	plan, err := Plan(context.Background(), dir, cp.Tree)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Skipped) != 1 || plan.Skipped[0] != "inner" || paths(plan) != "overwrite:a.txt" {
		t.Fatalf("plan = %s, skipped %v", paths(plan), plan.Skipped)
	}
}

// Review item 11: the folder spelled in another case.
func TestAFolderOpenedInAnotherCaseStillListsItsFiles(t *testing.T) {
	if runtime.GOOS != "windows" && runtime.GOOS != "darwin" {
		t.Skip("case-insensitive file systems only")
	}
	dir := newRepo(t)
	write(t, dir, "src/s.txt", "s1\n")
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "src")
	upper := filepath.Join(dir, "SRC")
	cp := capture(t, upper, "t/13")
	write(t, dir, "src/s.txt", "s2\n")
	plan, err := Plan(context.Background(), upper, cp.Tree)
	if err != nil {
		t.Fatal(err)
	}
	if paths(plan) != "overwrite:src/s.txt" {
		t.Fatalf("plan = %s", paths(plan))
	}
}

// Review item 12: the folder the pane works in is not removed, and what was
// written is counted from the files.
func TestRemovingCreatedFilesStopsAtThePanesFolder(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "sub/keep.txt", "k\n")
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "sub")
	pane := filepath.Join(dir, "sub")
	cp := capture(t, pane, "t/14")
	write(t, dir, "sub/deep/er/new.txt", "n\n")
	plan, err := Plan(context.Background(), pane, cp.Tree)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Restore(context.Background(), pane, cp.Tree, confirmAll(plan), "t-14"); err != nil {
		t.Fatal(err)
	}
	if !exists(dir, "sub") || exists(dir, "sub/deep") || read(t, dir, "sub/keep.txt") != "k\n" {
		t.Errorf("pane folder %v, deep %v", exists(dir, "sub"), exists(dir, "sub/deep"))
	}
	// A pane folder that holds nothing else is still not removed.
	dir2 := newRepo(t)
	write(t, dir2, "only/x.txt", "x\n")
	gitIn(t, dir2, "add", "-A")
	gitIn(t, dir2, "commit", "-q", "-m", "only")
	pane2 := filepath.Join(dir2, "only")
	if err := os.Remove(filepath.Join(pane2, "x.txt")); err != nil {
		t.Fatal(err)
	}
	cp2 := capture(t, pane2, "t/14b")
	write(t, dir2, "only/n/new.txt", "n\n")
	plan2, _ := Plan(context.Background(), pane2, cp2.Tree)
	if _, err := Restore(context.Background(), pane2, cp2.Tree, confirmAll(plan2), "t-14b"); err != nil {
		t.Fatal(err)
	}
	if !exists(dir2, "only") || exists(dir2, "only/n") {
		t.Errorf("only %v, n %v", exists(dir2, "only"), exists(dir2, "only/n"))
	}
}

func TestWrittenIsCountedFromTheFilesNotTheExitStatus(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("a held-open file blocks a write on Windows")
	}
	dir := newRepo(t)
	cp := capture(t, dir, "t/15")
	write(t, dir, "a.txt", "a2\n")
	write(t, dir, "b.txt", "b2\n")
	plan, _ := Plan(context.Background(), dir, cp.Tree)
	held, err := os.Open(filepath.Join(dir, "a.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	res, err := Restore(context.Background(), dir, cp.Tree, confirmAll(plan), "t-15")
	if err != nil {
		t.Fatal(err)
	}
	onDisk := 0
	for _, f := range []string{"a.txt", "b.txt"} {
		if strings.HasSuffix(f, "") && (read(t, dir, f) == "a1\n" || read(t, dir, f) == "b1\n") {
			onDisk++
		}
	}
	if res.Written != onDisk || res.Written+len(res.Failed) < 2 {
		t.Errorf("written %d, on disk %d, failed %v", res.Written, onDisk, res.Failed)
	}
}

func TestNotARepoAndMidOperation(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	plain := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(plain))
	if err := Available(context.Background(), plain); !errors.Is(err, ErrNotRepo) {
		t.Fatalf("err = %v", err)
	}
	if _, err := Capture(context.Background(), plain, RefPrefix+"t/16"); !errors.Is(err, ErrNotRepo) {
		t.Fatalf("capture err = %v", err)
	}
	dir := newRepo(t)
	if err := os.WriteFile(filepath.Join(dir, ".git", "MERGE_HEAD"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Capture(context.Background(), dir, RefPrefix+"t/16"); !errors.Is(err, ErrMidOperation) {
		t.Fatalf("mid-operation err = %v", err)
	}
}

func TestCaptureRefusesAForeignRefDropKeepsOthersAndKeepMovesTheRef(t *testing.T) {
	dir := newRepo(t)
	if _, err := Capture(context.Background(), dir, "refs/heads/main"); err == nil {
		t.Fatal("a ref outside the prefix was accepted")
	}
	capture(t, dir, "t/17")
	Drop(dir, "refs/heads/master")
	if gitIn(t, dir, "branch", "--list") == "" {
		t.Fatal("Drop touched a branch")
	}
	kept, err := Keep(dir, RefPrefix+"t/17")
	if err != nil || !strings.HasPrefix(kept, UndoPrefix) {
		t.Fatalf("kept = %q, %v", kept, err)
	}
	if gitIn(t, dir, "for-each-ref", RefPrefix) != "" || !strings.Contains(gitIn(t, dir, "for-each-ref", UndoPrefix), kept) {
		t.Error("the ref was not moved")
	}
	capture(t, dir, "t/18")
	Drop(dir, RefPrefix+"t/18")
	if gitIn(t, dir, "for-each-ref", RefPrefix) != "" {
		t.Fatal("the checkpoint ref is still there")
	}
}

func TestParseRawSkipsSubmodulesAndOddPaths(t *testing.T) {
	sha := strings.Repeat("a", 40)
	out := ":160000 160000 " + sha + " " + sha + " M\x00sm\x00:100644 100644 " + sha + " " + sha + " M\x00f.txt\x00"
	l, err := parseRaw(out, "")
	if err != nil || len(l.Changes) != 1 || l.Changes[0].Path != "f.txt" || len(l.Skipped) != 1 || l.Changes[0].ID == "" {
		t.Fatalf("l = %+v, err = %v", l, err)
	}
	for _, p := range []string{"../x", "/abs", "a/../../x", `a\b`} {
		bad := ":100644 100644 " + sha + " " + sha + " M\x00" + p + "\x00"
		if _, err := parseRaw(bad, ""); err == nil {
			t.Errorf("path %q accepted", p)
		}
	}
}

func ctxb() context.Context { return context.Background() }

// The size limits apply to a checkpoint, not to listing or restoring from one.
func TestTheSizeLimitsDoNotApplyAfterTheCheckpointIsSaved(t *testing.T) {
	dir := newRepo(t)
	cp := capture(t, dir, "t/20")
	saved := limits
	defer func() { limits = saved }()
	limits.files, limits.file = 3, 10
	for i := 0; i < 5; i++ {
		write(t, dir, "n"+string(rune('a'+i))+".txt", "x\n")
	}
	write(t, dir, "big.bin", strings.Repeat("y", 100))
	plan, err := Plan(ctxb(), dir, cp.Tree)
	if err != nil {
		t.Fatalf("plan after the agent left many and large files: %v", err)
	}
	if len(plan.Changes) != 6 {
		t.Fatalf("plan = %s", paths(plan))
	}
	res, err := Restore(ctxb(), dir, cp.Tree, confirmAll(plan), "t-20")
	if err != nil || res.Removed != 6 || len(res.Failed) != 0 {
		t.Fatalf("restore: %+v, %v", res, err)
	}
}

// A slow comparison now has its own error, not the one for a slow save.
func TestASlowComparisonIsNotASlowSave(t *testing.T) {
	if !errors.Is(nowErr(ErrSlow), ErrSlowNow) || errors.Is(nowErr(ErrSlow), ErrSlow) {
		t.Fatal("ErrSlow is not turned into ErrSlowNow when listing")
	}
	dir := newRepo(t)
	cp := capture(t, dir, "t/21")
	ctx, cancel := context.WithCancel(ctxb())
	cancel()
	if _, err := Plan(ctx, dir, cp.Tree); errors.Is(err, ErrSlow) {
		t.Errorf("Plan returned the save error: %v", err)
	}
}

func TestTheTotalLimitIs128MBAndTheErrorNamesTheFile(t *testing.T) {
	if MaxTotalBytes != 128<<20 {
		t.Fatalf("MaxTotalBytes = %d", MaxTotalBytes)
	}
	dir := newRepo(t)
	saved := limits
	defer func() { limits = saved }()
	limits.file = 10
	write(t, dir, "sub/huge.bin", strings.Repeat("y", 100))
	_, err := Capture(ctxb(), dir, RefPrefix+"t/22")
	var te *TooBigError
	if !errors.As(err, &te) || !errors.Is(err, ErrTooBig) || te.Kind != FileTooLarge || te.Path != "sub/huge.bin" || te.Size != 100 {
		t.Fatalf("err = %#v", err)
	}
	limits = saved
	limits.total = 150
	os.Remove(filepath.Join(dir, "sub", "huge.bin"))
	write(t, dir, "p1.bin", strings.Repeat("y", 100))
	write(t, dir, "p2.bin", strings.Repeat("y", 100))
	_, err = Capture(ctxb(), dir, RefPrefix+"t/22")
	if !errors.As(err, &te) || te.Kind != TotalTooLarge || te.Path == "" {
		t.Fatalf("total: err = %#v", err)
	}
	limits = saved
	limits.files = 2
	write(t, dir, "p3.bin", "z")
	_, err = Capture(ctxb(), dir, RefPrefix+"t/22")
	if !errors.As(err, &te) || te.Kind != TooManyFiles || te.Files < 3 {
		t.Fatalf("count: err = %#v", err)
	}
}

// Notes.txt renamed to notes.txt is one file, not a refusal.
func TestACaseOnlyRenameOfAnUntrackedFileRestores(t *testing.T) {
	if runtime.GOOS != "windows" && runtime.GOOS != "darwin" {
		t.Skip("case-insensitive file systems only")
	}
	dir := newRepo(t)
	write(t, dir, "Notes.txt", "n1\n")
	cp := capture(t, dir, "t/23")
	if err := os.Rename(filepath.Join(dir, "Notes.txt"), filepath.Join(dir, "notes.txt")); err != nil {
		t.Fatal(err)
	}
	plan, err := Plan(ctxb(), dir, cp.Tree)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Blocked) != 0 {
		t.Fatalf("blocked: %v (plan %s)", plan.Blocked, paths(plan))
	}
	if _, err := Restore(ctxb(), dir, cp.Tree, confirmAll(plan), "t-23"); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, e := range entries {
		if strings.EqualFold(e.Name(), "notes.txt") {
			names = append(names, e.Name())
		}
	}
	if len(names) != 1 || names[0] != "Notes.txt" || read(t, dir, "Notes.txt") != "n1\n" {
		t.Errorf("names = %v", names)
	}
}

// A file edited between the check and the write stops the restore.
func TestAFileEditedAfterTheUndoCopyStopsTheRestore(t *testing.T) {
	dir := newRepo(t)
	cp := capture(t, dir, "t/24")
	write(t, dir, "a.txt", "a2\n")
	write(t, dir, "new.txt", "n\n")
	plan, err := Plan(ctxb(), dir, cp.Tree)
	if err != nil {
		t.Fatal(err)
	}
	beforeWrite = func() {
		write(t, dir, "a.txt", "typed meanwhile\n")
		write(t, dir, "new.txt", "also edited\n")
	}
	defer func() { beforeWrite = nil }()
	_, err = Restore(ctxb(), dir, cp.Tree, confirmAll(plan), "t-24")
	var ce *ChangedError
	if !errors.As(err, &ce) {
		t.Fatalf("err = %v", err)
	}
	if read(t, dir, "a.txt") != "typed meanwhile\n" || read(t, dir, "new.txt") != "also edited\n" {
		t.Error("a file was written or removed")
	}
	if gitIn(t, dir, "for-each-ref", UndoPrefix) != "" {
		t.Error("an undo ref was left for a restore that did nothing")
	}
}

// git's own temporary files in the shared object folder are not ours to delete.
func TestAFailedCaptureLeavesOtherProcessesTemporaryObjects(t *testing.T) {
	dir := newRepo(t)
	tmp := filepath.Join(dir, ".git", "objects", "tmp_obj_other")
	if err := os.WriteFile(tmp, []byte("in flight"), 0o644); err != nil {
		t.Fatal(err)
	}
	saved := limits
	defer func() { limits = saved }()
	limits.files = 1
	write(t, dir, "x1", "1")
	write(t, dir, "x2", "2")
	if _, err := Capture(ctxb(), dir, RefPrefix+"t/25"); !errors.Is(err, ErrTooBig) {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Lstat(tmp); err != nil {
		t.Error("another process's temporary object file was removed")
	}
}

func backdatedCheckpoint(t *testing.T, dir, ref string, age time.Duration) {
	t.Helper()
	head := strings.TrimSpace(gitIn(t, dir, "rev-parse", "HEAD^{tree}"))
	when := time.Now().Add(-age).Format(time.RFC3339)
	cmd := exec.Command("git", "-c", "user.name=t", "-c", "user.email=t@t.invalid", "commit-tree", head, "-m", "old")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_COMMITTER_DATE="+when, "GIT_AUTHOR_DATE="+when)
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "update-ref", ref, strings.TrimSpace(string(out)))
}

// A pane of another Flockdeck process keeps its checkpoints however old.
func TestSweepKeepsTheRunsOfAnotherLiveProcess(t *testing.T) {
	dir := newRepo(t)
	backdatedCheckpoint(t, dir, RefPrefix+"otherlive/1", 10*24*time.Hour)
	backdatedCheckpoint(t, dir, RefPrefix+"dead/1", 10*24*time.Hour)
	backdatedCheckpoint(t, dir, RefPrefix+"stalemarker/1", 10*24*time.Hour)
	r, err := locate(ctxb(), dir)
	if err != nil {
		t.Fatal(err)
	}
	r.touchMarker("otherlive")
	r.touchMarker("stalemarker")
	old := time.Now().Add(-2 * liveTTL)
	if err := os.Chtimes(r.markerPath("stalemarker"), old, old); err != nil {
		t.Fatal(err)
	}
	r.sweep(time.Now())
	refs := gitIn(t, dir, "for-each-ref", "--format=%(refname)", RefPrefix)
	if !strings.Contains(refs, "otherlive") || strings.Contains(refs, "dead/") || strings.Contains(refs, "stalemarker") {
		t.Errorf("refs after the sweep: %q", refs)
	}
	if _, err := os.Stat(r.markerPath("stalemarker")); err == nil {
		t.Error("the marker of a dead run was kept")
	}
}

func TestRegisterWritesAMarkerAndUnregisterRemovesIt(t *testing.T) {
	dir := newRepo(t)
	r, _ := locate(ctxb(), dir)
	Register(dir, "mine")
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(r.markerPath("mine")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no marker")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !r.liveElsewhere("mine", time.Now()) {
		t.Error("a fresh marker is not live")
	}
	Unregister("mine")
	for {
		if _, err := os.Stat(r.markerPath("mine")); err != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the marker was not removed")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A file ignored after the checkpoint is not overwritten: the restore is refused.
func TestAFileIgnoredAfterTheCheckpointBlocksTheRestore(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "gen.txt", "saved\n")
	cp := capture(t, dir, "t/26")
	write(t, dir, ".gitignore", "ignored/\n*.log\ngen.txt\n")
	write(t, dir, "gen.txt", "new content\n")
	plan, err := Plan(ctxb(), dir, cp.Tree)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Restore(ctxb(), dir, cp.Tree, confirmAll(plan), "t-26")
	if !blockedHas(err, "gen.txt", ReasonIgnored) {
		t.Fatalf("plan %s, blocked %v, err %v", paths(plan), plan.Blocked, err)
	}
	if read(t, dir, "gen.txt") != "new content\n" {
		t.Error("the ignored file was overwritten")
	}
}

// git cannot encode a file: the answer names it.
func TestAnEncodingFailureNamesTheFile(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, ".gitattributes", "*.u16 working-tree-encoding=UTF-16\n")
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "attrs")
	write(t, dir, "docs/bad.u16", "plain bytes, no byte order mark\n")
	_, err := Capture(ctxb(), dir, RefPrefix+"t/27")
	var fe *FileError
	if !errors.As(err, &fe) || !errors.Is(err, ErrEncoding) || fe.Path != "docs/bad.u16" {
		t.Fatalf("err = %#v", err)
	}
}

// A file the agent replaced with a folder of the same name: the checkpoint has
// x, the files now have x/a.txt. Check and Restore both read the listed paths.
func TestAFileReplacedByAFolderRestores(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "x", "was a file\n")
	cp := capture(t, dir, "t/30")
	if err := os.Remove(filepath.Join(dir, "x")); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "x/a.txt", "now a folder\n")
	plan, err := Plan(ctxb(), dir, cp.Tree)
	if err != nil {
		t.Fatal(err)
	}
	if want := "recreate:x,remove:x/a.txt"; paths(plan) != want || len(plan.Blocked) != 0 {
		t.Fatalf("plan = %s (blocked %v), want %s", paths(plan), plan.Blocked, want)
	}
	if err := Check(ctxb(), dir, cp.Tree, confirmAll(plan)); err != nil {
		t.Fatalf("check: %v", err)
	}
	res, err := Restore(ctxb(), dir, cp.Tree, confirmAll(plan), "t-30")
	if err != nil || len(res.Failed) != 0 || res.Written != 1 || res.Removed != 1 {
		t.Fatalf("restore: %+v, %v", res, err)
	}
	if read(t, dir, "x") != "was a file\n" {
		t.Errorf("x = %q", read(t, dir, "x"))
	}
}

// Check makes the read Restore makes, so a failure of it shows before the
// conversation moves: a path that git cannot read stops it.
func TestCheckFailsWhereRestoreWouldBeforeAnythingMoves(t *testing.T) {
	dir := newRepo(t)
	cp := capture(t, dir, "t/31")
	write(t, dir, "n.txt", "n\n")
	plan, _ := Plan(ctxb(), dir, cp.Tree)
	ctx, cancel := context.WithCancel(ctxb())
	cancel()
	if err := Check(ctx, dir, cp.Tree, confirmAll(plan)); err == nil {
		t.Fatal("a cancelled check passed")
	}
	if err := Check(ctxb(), dir, cp.Tree, confirmAll(plan)); err != nil {
		t.Fatalf("check: %v", err)
	}
}

func TestSweepRemovesOldTemporaryObjectsOnly(t *testing.T) {
	dir := newRepo(t)
	objs := filepath.Join(dir, ".git", "objects")
	old := time.Now().Add(-2 * time.Hour)
	mk := func(rel string, mt time.Time) string {
		p := filepath.Join(objs, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("half"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, mt, mt); err != nil {
			t.Fatal(err)
		}
		return p
	}
	oldTop, oldSub := mk("tmp_obj_old", old), mk("ab/tmp_obj_old", old)
	fresh, sub := mk("tmp_obj_fresh", time.Now()), mk("ab/tmp_obj_fresh", time.Now())
	other := mk("ab/cdef", old) // a real object file name pattern is not touched
	r, _ := locate(ctxb(), dir)
	r.sweep(time.Now())
	for _, p := range []string{oldTop, oldSub} {
		if _, err := os.Lstat(p); err == nil {
			t.Errorf("%s is still there", p)
		}
	}
	for _, p := range []string{fresh, sub, other} {
		if _, err := os.Lstat(p); err != nil {
			t.Errorf("%s was removed", p)
		}
	}
}

// A marker dated in the future does not protect its run, and is removed.
func TestAMarkerFromTheFutureIsStale(t *testing.T) {
	dir := newRepo(t)
	backdatedCheckpoint(t, dir, RefPrefix+"skewed/1", 10*24*time.Hour)
	r, _ := locate(ctxb(), dir)
	r.touchMarker("skewed")
	future := time.Now().Add(48 * time.Hour)
	if err := os.Chtimes(r.markerPath("skewed"), future, future); err != nil {
		t.Fatal(err)
	}
	if r.liveElsewhere("skewed", time.Now()) {
		t.Error("a marker dated two days ahead counts as live")
	}
	r.touchMarker("fine")
	if !r.liveElsewhere("fine", time.Now().Add(-5*time.Second)) {
		t.Error("a few seconds of clock difference made a fresh marker stale")
	}
	r.sweep(time.Now())
	if strings.Contains(gitIn(t, dir, "for-each-ref", "--format=%(refname)", RefPrefix), "skewed") {
		t.Error("the run with a future marker kept its checkpoint")
	}
	if _, err := os.Stat(r.markerPath("skewed")); err == nil {
		t.Error("the future marker was kept")
	}
}
