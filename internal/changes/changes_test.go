package changes

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runGit runs a git command in dir with no configuration but the test's own, so a
// test repository does not read or write the person's.
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_SYSTEM="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func write(t *testing.T, dir, name, text string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// repo makes a repository on main with a few committed files.
func repo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "-q", "-b", "main")
	write(t, dir, "a.txt", "one\ntwo\nthree\n")
	write(t, dir, "b.txt", "keep\n")
	write(t, dir, "old.txt", strings.Repeat("line of a file to rename\n", 20))
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "first")
	return dir
}

func byPath(b Branch) map[string]File {
	m := map[string]File{}
	for _, f := range b.AllFiles() {
		m[f.Path] = f
	}
	return m
}

func TestReadListsCommittedUncommittedAndUntrackedFiles(t *testing.T) {
	dir := repo(t)
	runGit(t, dir, "checkout", "-q", "-b", "feat")
	write(t, dir, "a.txt", "one\ntwo changed\nthree\nfour\n")
	runGit(t, dir, "commit", "-q", "-am", "edit a")
	runGit(t, dir, "mv", "old.txt", "new.txt")
	write(t, dir, "b.txt", "keep\nmore\n")
	write(t, dir, "fresh.txt", "x\ny\n")
	write(t, dir, "go.sum", "lock\n")
	write(t, dir, "bin.dat", "ab\x00cd")

	b := Read(context.Background(), "p1", dir, MaxFiles)
	if b.Error != "" {
		t.Fatalf("error: %s", b.Error)
	}
	if b.Branch != "feat" || b.Base != "main" || b.BaseSha == "" || b.HeadSha == "" || b.Detached {
		t.Errorf("branch = %+v", b)
	}
	if *b.Ahead != 1 || *b.Behind != 0 {
		t.Errorf("ahead %d behind %d, want 1 and 0", *b.Ahead, *b.Behind)
	}
	if b.LastCommit == nil || b.LastCommit.Subject != "edit a" {
		t.Errorf("last commit = %+v", b.LastCommit)
	}
	if b.Checks == nil || b.Reviews == nil || b.Comments == nil {
		t.Error("checks, reviews and comments are not written as empty lists")
	}
	f := byPath(b)
	if a := f["a.txt"]; a.Status != "M" || a.Added != 2 || a.Removed != 1 {
		t.Errorf("a.txt = %+v, want M +2 -1", a)
	}
	if n := f["new.txt"]; n.Status != "R" || n.OldPath != "old.txt" || n.Similarity != 100 || !n.Staged {
		t.Errorf("new.txt = %+v, want a staged rename of old.txt", n)
	}
	if bb := f["b.txt"]; bb.Added != 1 || bb.Staged {
		t.Errorf("b.txt = %+v, want one added line, unstaged", bb)
	}
	if fr := f["fresh.txt"]; fr.Status != "A" || fr.Added != 2 {
		t.Errorf("fresh.txt = %+v, want A +2", fr)
	}
	if !f["go.sum"].Generated {
		t.Error("go.sum is not marked generated")
	}
	if d := f["bin.dat"]; !d.Binary || d.SizeAfter == nil || *d.SizeAfter != 5 || d.Added != 0 {
		t.Errorf("bin.dat = %+v, want binary of 5 bytes", d)
	}
	for _, x := range b.Files {
		if x.Owner != "p1" {
			t.Errorf("%s has owner %q", x.Path, x.Owner)
		}
	}
	if *b.Untracked != 3 || *b.Uncommitted < 2 {
		t.Errorf("uncommitted %d untracked %d", *b.Uncommitted, *b.Untracked)
	}
}

func TestReadOmitsFilesPastTheLimitAndPagesThroughAll(t *testing.T) {
	dir := repo(t)
	runGit(t, dir, "checkout", "-q", "-b", "feat")
	for _, n := range []string{"f1", "f2", "f3", "f4", "f5"} {
		write(t, dir, n+".txt", n+"\n")
	}
	b := Read(context.Background(), "p1", dir, 2)
	if len(b.Files) != 2 || b.Omitted != 3 || len(b.AllFiles()) != 5 {
		t.Fatalf("files %d omitted %d all %d, want 2, 3 and 5", len(b.Files), b.Omitted, len(b.AllFiles()))
	}
	if got := Page(b.AllFiles(), 3, 10); len(got) != 2 {
		t.Errorf("page from 3 has %d files, want 2", len(got))
	}
	if got := Page(b.AllFiles(), 9, 10); got == nil || len(got) != 0 {
		t.Errorf("page past the end = %v, want an empty list", got)
	}
}

func TestReadSaysWhyACheckoutCannotBeRead(t *testing.T) {
	plain := t.TempDir()
	if b := Read(context.Background(), "p1", plain, MaxFiles); b.Error == "" || b.Pane != "p1" || b.Files != nil || b.Checks != nil {
		t.Errorf("a folder with no repository: %+v", b)
	}
	if b := Read(context.Background(), "p1", filepath.Join(plain, "gone"), MaxFiles); b.Error != "the directory is gone" {
		t.Errorf("a missing folder: %+v", b)
	}
	empty := t.TempDir()
	runGit(t, empty, "init", "-q", "-b", "main")
	if b := Read(context.Background(), "p1", empty, MaxFiles); b.Error == "" {
		t.Errorf("a repository with no commit: %+v", b)
	}
}

// Two worktrees of one repository that change the same path are each other's
// conflict; a file only one changes, and a separate repository that changes the
// same path, are not.
func TestReadTeamMarksFilesChangedOnAnotherBranch(t *testing.T) {
	dir := repo(t)
	w1, w2 := filepath.Join(t.TempDir(), "w1"), filepath.Join(t.TempDir(), "w2")
	runGit(t, dir, "worktree", "add", "-q", "-b", "one", w1)
	runGit(t, dir, "worktree", "add", "-q", "-b", "two", w2)
	write(t, w1, "a.txt", "from one\n")
	write(t, w1, "only1.txt", "1\n")
	write(t, w2, "a.txt", "from two\n")
	other := repo(t)
	runGit(t, other, "checkout", "-q", "-b", "x")
	write(t, other, "a.txt", "elsewhere\n")

	team := ReadTeam(context.Background(), "/root", "proj",
		[]PaneRef{{ID: "p1", Cwd: w1}, {ID: "p2", Cwd: w2}, {ID: "p3", Cwd: other}, {ID: "p4", Cwd: t.TempDir()}}, 7)
	if team.Rev != 7 || team.Root != "/root" || len(team.Branches) != 4 {
		t.Fatalf("team = %+v", team)
	}
	if team.Partial == nil || team.Partial.Read != 3 || team.Partial.Total != 4 {
		t.Errorf("partial = %+v, want 3 of 4", team.Partial)
	}
	cw := func(i int, path string) []ConflictWith { return byPath(team.Branches[i])[path].ConflictWith }
	if got := cw(0, "a.txt"); len(got) != 1 || got[0].Pane != "p2" || got[0].Path != "a.txt" {
		t.Errorf("p1 a.txt conflicts with %+v, want p2", got)
	}
	if got := cw(1, "a.txt"); len(got) != 1 || got[0].Pane != "p1" {
		t.Errorf("p2 a.txt conflicts with %+v, want p1", got)
	}
	if got := cw(0, "only1.txt"); len(got) != 0 {
		t.Errorf("only1.txt conflicts with %+v", got)
	}
	if got := cw(2, "a.txt"); len(got) != 0 {
		t.Errorf("a file in another repository conflicts with %+v", got)
	}
	if team.Branches[3].Error == "" {
		t.Error("the folder with no repository has no error")
	}
}

func TestParseRawAndNumstat(t *testing.T) {
	raw := ":100644 100644 aaa bbb M\x00a.txt\x00:100644 100644 aaa bbb R087\x00old name.txt\x00new name.txt\x00:000000 100644 0000 ccc A\x00n.txt\x00:100755 100644 aaa bbb M\x00mode.sh\x00"
	files := parseRaw(raw)
	if len(files) != 4 {
		t.Fatalf("files = %+v", files)
	}
	if r := files[1]; r.Status != "R" || r.OldPath != "old name.txt" || r.Path != "new name.txt" || r.Similarity != 87 {
		t.Errorf("rename = %+v", r)
	}
	if files[3].ModeChange != "100755 100644" {
		t.Errorf("mode change = %q", files[3].ModeChange)
	}
	n := parseNumstat("3\t1\ta.txt\x00-\t-\tbin.dat\x005\t0\t\x00old name.txt\x00new name.txt\x00")
	if n["a.txt"] != (count{added: 3, removed: 1}) || !n["bin.dat"].binary || n["new name.txt"].added != 5 {
		t.Errorf("numstat = %+v", n)
	}
}

func TestGenerated(t *testing.T) {
	for p, want := range map[string]bool{
		"go.sum": true, "web/package-lock.json": true, "a/vendor/x.go": true, "node_modules/x/y.js": true,
		"app.min.js": true, "x.pb.go": true, "main.go": false, "vendors.go": false, "docs/lock.md": false,
	} {
		if Generated(p) != want {
			t.Errorf("Generated(%q) = %v", p, !want)
		}
	}
}
