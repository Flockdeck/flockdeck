package filerewind

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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

// newRepo makes a repository with a committed file a.txt and b.txt.
func newRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q")
	write(t, dir, "a.txt", "a1\n")
	write(t, dir, "b.txt", "b1\n")
	write(t, dir, ".gitignore", "ignored/\n")
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "first")
	return dir
}

func paths(cs []Change) []string {
	var out []string
	for _, c := range cs {
		out = append(out, string(c.Action)+":"+c.Path)
	}
	return out
}

func TestRestoreEditedDeletedAndCreated(t *testing.T) {
	dir := newRepo(t)
	// State at the prompt: a.txt edited and unstaged, c.txt untracked, ignored file.
	write(t, dir, "a.txt", "a2\n")
	write(t, dir, "c.txt", "c1\n")
	write(t, dir, "ignored/x.txt", "x1\n")
	ctx := context.Background()
	cp, err := Capture(ctx, dir, RefPrefix+"t/1")
	if err != nil {
		t.Fatal(err)
	}
	// The agent's work afterwards.
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
	got := strings.Join(paths(plan), ",")
	want := "overwrite:a.txt,recreate:b.txt,overwrite:c.txt,remove:new/d.txt"
	if got != want {
		t.Fatalf("plan = %s, want %s", got, want)
	}
	var list []string
	for _, c := range plan {
		list = append(list, c.Path)
	}
	res, _, err := Restore(ctx, dir, cp.Tree, list, "t-1")
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
	// The files before the restore are kept under the undo ref.
	if got := gitIn(t, dir, "show", res.Undo+":a.txt"); got != "a3\n" {
		t.Errorf("undo ref holds a.txt = %q", got)
	}
	if got := gitIn(t, dir, "show", res.Undo+":new/d.txt"); got != "d1\n" {
		t.Errorf("undo ref holds new/d.txt = %q", got)
	}
}

func TestRestoreLeavesIndexAndBranchAlone(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "a.txt", "a2\n")
	gitIn(t, dir, "add", "a.txt")
	cp, err := Capture(context.Background(), dir, RefPrefix+"t/2")
	if err != nil {
		t.Fatal(err)
	}
	headBefore := gitIn(t, dir, "rev-parse", "HEAD")
	stagedBefore := gitIn(t, dir, "diff", "--cached")
	if !strings.Contains(stagedBefore, "+a2") {
		t.Fatalf("staged diff = %q", stagedBefore)
	}
	write(t, dir, "a.txt", "a3\n")
	if _, _, err := Restore(context.Background(), dir, cp.Tree, []string{"a.txt"}, "t-2"); err != nil {
		t.Fatal(err)
	}
	if gitIn(t, dir, "rev-parse", "HEAD") != headBefore {
		t.Error("HEAD moved")
	}
	// The index still holds the staged a2, and the file is a2 again.
	if got := gitIn(t, dir, "diff", "--cached"); got != stagedBefore {
		t.Errorf("staged diff after restore = %q, want %q", got, stagedBefore)
	}
	if got := gitIn(t, dir, "diff"); got != "" {
		t.Errorf("unstaged diff after restore = %q", got)
	}
}

func TestRestoreRefusesAChangedList(t *testing.T) {
	dir := newRepo(t)
	ctx := context.Background()
	cp, err := Capture(ctx, dir, RefPrefix+"t/3")
	if err != nil {
		t.Fatal(err)
	}
	write(t, dir, "a.txt", "a2\n")
	// The person confirmed a.txt; then b.txt changed as well.
	write(t, dir, "b.txt", "b2\n")
	res, now, err := Restore(ctx, dir, cp.Tree, []string{"a.txt"}, "t-3")
	if !errors.Is(err, ErrChanged) {
		t.Fatalf("err = %v", err)
	}
	if len(now) != 2 || res.Undo != "" {
		t.Fatalf("now = %v, result = %+v", now, res)
	}
	if read(t, dir, "a.txt") != "a2\n" || read(t, dir, "b.txt") != "b2\n" {
		t.Error("a file was written although the list did not match")
	}
	// A path that is not on the plan, or listed twice, is also refused.
	for _, bad := range [][]string{{"a.txt", "b.txt", "zzz"}, {"a.txt", "a.txt"}, {}} {
		if _, _, err := Restore(ctx, dir, cp.Tree, bad, "t-3"); !errors.Is(err, ErrChanged) {
			t.Errorf("%v: err = %v", bad, err)
		}
	}
	if gitIn(t, dir, "for-each-ref", UndoPrefix) != "" {
		t.Error("an undo ref was made for a refused restore")
	}
}

func TestPlanIsScopedToTheFolder(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "sub/s.txt", "s1\n")
	ctx := context.Background()
	cp, err := Capture(ctx, filepath.Join(dir, "sub"), RefPrefix+"t/4")
	if err != nil {
		t.Fatal(err)
	}
	write(t, dir, "sub/s.txt", "s2\n")
	write(t, dir, "a.txt", "a2\n")
	plan, err := Plan(ctx, filepath.Join(dir, "sub"), cp.Tree)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(paths(plan), ","); got != "overwrite:sub/s.txt" {
		t.Fatalf("plan = %s", got)
	}
}

func TestNotARepoAndMidOperation(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	plain := t.TempDir()
	// The temp dir may sit inside a repository on a developer machine; make one
	// that cannot by pointing git's ceiling at its parent.
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(plain))
	if err := Available(context.Background(), plain); !errors.Is(err, ErrNotRepo) {
		t.Fatalf("err = %v", err)
	}
	if _, err := Capture(context.Background(), plain, RefPrefix+"t/5"); !errors.Is(err, ErrNotRepo) {
		t.Fatalf("capture err = %v", err)
	}
	dir := newRepo(t)
	if err := os.WriteFile(filepath.Join(dir, ".git", "MERGE_HEAD"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Capture(context.Background(), dir, RefPrefix+"t/5"); !errors.Is(err, ErrMidOperation) {
		t.Fatalf("mid-operation err = %v", err)
	}
}

func TestCaptureRefusesAForeignRefAndDropKeepsOthers(t *testing.T) {
	dir := newRepo(t)
	if _, err := Capture(context.Background(), dir, "refs/heads/main"); err == nil {
		t.Fatal("a ref outside the prefix was accepted")
	}
	if _, err := Capture(context.Background(), dir, RefPrefix+"t/6"); err != nil {
		t.Fatal(err)
	}
	Drop(dir, "refs/heads/master") // not ours: ignored
	if gitIn(t, dir, "branch", "--list") == "" {
		t.Fatal("Drop touched a branch")
	}
	Drop(dir, RefPrefix+"t/6")
	if gitIn(t, dir, "for-each-ref", RefPrefix) != "" {
		t.Fatal("the checkpoint ref is still there")
	}
}

func TestParseRawSkipsSubmodulesAndOddPaths(t *testing.T) {
	sha := strings.Repeat("a", 40)
	out := ":160000 160000 " + sha + " " + sha + " M\x00sm\x00:100644 100644 " + sha + " " + sha + " M\x00f.txt\x00"
	cs, err := parseRaw(out)
	if err != nil || len(cs) != 1 || cs[0].Path != "f.txt" {
		t.Fatalf("cs = %v, err = %v", cs, err)
	}
	for _, p := range []string{"../x", "/abs", "a/../../x", `a\b`} {
		bad := ":100644 100644 " + sha + " " + sha + " M\x00" + p + "\x00"
		if _, err := parseRaw(bad); err == nil {
			t.Errorf("path %q accepted", p)
		}
	}
}
