package server

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jmwri/flockdeck/internal/gitx"
	"github.com/jmwri/flockdeck/internal/session"
)

// trustOnly lays down a Claude Code configuration of the test's own in which
// dir is the one folder trusted, so the real ~/.claude.json is neither read nor
// written.
func trustOnly(t *testing.T, dir string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	data, err := json.Marshal(map[string]any{"projects": map[string]any{
		filepath.Clean(dir): map[string]any{"hasTrustDialogAccepted": true},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestAFanoutFromASubfolderWorksAndIsTrustedOnlyThere covers a fan-out started
// from repo/sub, the only folder of the repository its user had trusted. Every
// child worked at the top of its worktree instead of in the same folder, and
// was trusted there -- a whole checkout, on the word of one folder of it.
func TestAFanoutFromASubfolderWorksAndIsTrustedOnlyThere(t *testing.T) {
	if !gitx.Available() {
		t.Skip("git is not installed")
	}
	repo := newTestRepo(t)
	sub := filepath.Join(repo, "sub")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "parser.go"), []byte("package sub\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-m", "sub"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git %v failed (%v): %s", args, err, out)
		}
	}
	trustOnly(t, sub)
	c := &controlClient{out: make(chan []byte, 32)}
	jobs := []*fanoutJob{{task: "fix the parser", cwd: sub}}

	if !prepareWorktrees(c, repo, jobs) {
		t.Fatal("the fan-out stopped before making any worktrees")
	}
	j := jobs[0]
	if j.err != nil || !j.created {
		t.Fatalf("err = %v, created = %v; want a worktree of its own", j.err, j.created)
	}
	if want := filepath.Join(j.path, "sub"); j.cwd != want {
		t.Errorf("the child works in %s, want %s, the folder it was fanned out from", j.cwd, want)
	}

	inheritTrust(jobs, sub, session.InheritTrust, func(text string) { t.Errorf("unexpected notice: %s", text) })
	if !session.IsTrusted(filepath.Join(j.path, "sub")) {
		t.Error("the child's folder was not given the trust the same folder of the repository has")
	}
	if session.IsTrusted(j.path) {
		t.Error("the whole worktree was trusted, where only one folder of the repository is")
	}
}

// TestAFanoutFromAnUntrackedFolderTrustsNoWholeWorktree covers a fan-out from a
// folder git does not track, which its worktrees do not have. The child works
// at the top of its worktree, which stands for the top of the repository -- and
// nobody has trusted that, so no trust is carried to it.
func TestAFanoutFromAnUntrackedFolderTrustsNoWholeWorktree(t *testing.T) {
	if !gitx.Available() {
		t.Skip("git is not installed")
	}
	repo := newTestRepo(t)
	scratch := filepath.Join(repo, "scratch")
	if err := os.Mkdir(scratch, 0o700); err != nil {
		t.Fatal(err)
	}
	trustOnly(t, scratch)
	c := &controlClient{out: make(chan []byte, 32)}
	jobs := []*fanoutJob{{task: "fix the parser", cwd: scratch}}

	if !prepareWorktrees(c, repo, jobs) {
		t.Fatal("the fan-out stopped before making any worktrees")
	}
	j := jobs[0]
	if j.err != nil || !j.created {
		t.Fatalf("err = %v, created = %v; want a worktree of its own", j.err, j.created)
	}
	if j.cwd != j.path {
		t.Errorf("the child works in %s, want the top of its worktree, %s", j.cwd, j.path)
	}

	var notices []string
	inheritTrust(jobs, scratch, session.InheritTrust, func(text string) { notices = append(notices, text) })
	if session.IsTrusted(j.path) {
		t.Error("the whole worktree was trusted on the word of one untracked folder")
	}
	if len(notices) != 1 {
		t.Errorf("said %q, want one notice that there was no answer to carry over", notices)
	}
}
