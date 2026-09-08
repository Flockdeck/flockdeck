package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/agent-wrapper/internal/session"
	"github.com/jmwri/agent-wrapper/internal/workspace"
)

// newRepoServer starts a server whose project is a real git repository with
// one commit.
func newRepoServer(t *testing.T) (*Server, *workspace.Workspace, string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)

	repo := t.TempDir()
	for _, args := range [][]string{
		{"init", "--initial-branch=main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
		{"config", "commit.gpgsign", "false"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git %v: %v: %s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-m", "initial"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git %v: %v: %s", args, err, out)
		}
	}

	ws, err := workspace.New(workspace.Options{Root: repo})
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	t.Cleanup(ws.Close)
	ws.NewTab(session.KindShell, repo, "work")

	srv, err := New(ws)
	if err != nil {
		t.Fatalf("server: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	ws.SetWake(srv.Wake)
	return srv, ws, repo
}

// TestReviewCommitFlow walks the panel end to end: see what changed, read a
// diff, commit it, and find the tree clean afterwards.
func TestReviewCommitFlow(t *testing.T) {
	srv, _, repo := newRepoServer(t)
	conn := dialControl(t, srv)
	nextState(t, conn, nil)

	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("hello\nfrom an agent\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "added.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var ch changesMsg
	sendCmd(t, conn, command{Cmd: "changes", Path: repo})
	readUntil(t, conn, "changes", &ch)
	if ch.Error != "" {
		t.Fatalf("changes: %s", ch.Error)
	}
	if ch.Branch != "main" {
		t.Errorf("branch = %q, want main", ch.Branch)
	}
	if len(ch.Files) != 2 {
		t.Fatalf("files = %d, want 2: %+v", len(ch.Files), ch.Files)
	}
	if ch.HasRemote {
		t.Error("a repository with no remote should not offer a push")
	}

	// The diff of the modified file shows the added line.
	var d diffMsg
	sendCmd(t, conn, command{Cmd: "diff", Path: repo, Text: "README.md"})
	readUntil(t, conn, "diff", &d)
	if !strings.Contains(d.Text, "+from an agent") {
		t.Errorf("diff missing the change:\n%s", d.Text)
	}

	// Committing clears the working tree.
	sendCmd(t, conn, command{Cmd: "commit", Path: repo, Text: "ship the agent's work"})
	deadline := 0
	for deadline < 3 {
		readUntil(t, conn, "changes", &ch)
		if len(ch.Files) == 0 {
			break
		}
		deadline++
	}
	if len(ch.Files) != 0 {
		t.Errorf("expected a clean tree after committing, got %+v", ch.Files)
	}

	cmd := exec.Command("git", "log", "-1", "--pretty=%s")
	cmd.Dir = repo
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git log: %v", err)
	}
	if strings.TrimSpace(string(out)) != "ship the agent's work" {
		t.Errorf("commit subject = %q", strings.TrimSpace(string(out)))
	}
}

// TestCommitWithoutMessageIsRefused checks the error reaches the window rather
// than git being left waiting for an editor.
func TestCommitWithoutMessageIsRefused(t *testing.T) {
	srv, _, repo := newRepoServer(t)
	conn := dialControl(t, srv)
	nextState(t, conn, nil)

	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sendCmd(t, conn, command{Cmd: "commit", Path: repo, Text: "   "})

	var note noticeMsg
	readUntil(t, conn, "notice", &note)
	if !note.Error {
		t.Errorf("expected an error notice, got %+v", note)
	}
}

// TestChangesOutsideARepository covers a project that is not version
// controlled.
func TestChangesOutsideARepository(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	nextState(t, conn, nil)

	var ch changesMsg
	sendCmd(t, conn, command{Cmd: "changes"})
	readUntil(t, conn, "changes", &ch)
	if ch.Error == "" {
		t.Error("expected an explanation rather than an empty panel")
	}
}

// TestAgentsListsEveryPane covers the cross-project overview.
func TestAgentsListsEveryPane(t *testing.T) {
	srv, ws := newTestServer(t)
	conn := dialControl(t, srv)
	nextState(t, conn, nil)

	other := t.TempDir()
	if err := ws.OpenProject(other); err != nil {
		t.Fatalf("open project: %v", err)
	}
	nextState(t, conn, func(s stateMsg) bool { return len(s.Projects) == 2 })

	var ag agentsMsg
	sendCmd(t, conn, command{Cmd: "agents"})
	readUntil(t, conn, "agents", &ag)
	if len(ag.Items) < 2 {
		t.Fatalf("overview listed %d panes, want at least one per project", len(ag.Items))
	}

	// Panes from both projects appear, which is the point: the tab bar only
	// shows the active one.
	roots := map[string]bool{}
	for _, a := range ag.Items {
		roots[a.Root] = true
		if a.PaneID == "" || a.TabID == "" {
			t.Errorf("pane %+v cannot be jumped to without both ids", a)
		}
	}
	if len(roots) != 2 {
		t.Errorf("panes came from %d projects, want 2", len(roots))
	}
}

// TestRevealPaneSwitchesProjectAndTab covers clicking a row in the overview.
func TestRevealPaneSwitchesProjectAndTab(t *testing.T) {
	srv, ws := newTestServer(t)
	conn := dialControl(t, srv)
	st := nextState(t, conn, nil)

	first := st.Tabs[0]
	firstPane := first.Root.Pane
	firstRoot := ws.ActiveRoot()

	other := t.TempDir()
	if err := ws.OpenProject(other); err != nil {
		t.Fatalf("open: %v", err)
	}
	nextState(t, conn, func(s stateMsg) bool { return s.Root == other })

	// Jump back to the pane in the first project.
	sendCmd(t, conn, command{Cmd: "revealPane", Root: firstRoot, Node: first.ID, ID: firstPane})
	got := nextState(t, conn, func(s stateMsg) bool { return s.Root == firstRoot })
	if got.ActiveTab != first.ID {
		t.Errorf("active tab = %q, want %q", got.ActiveTab, first.ID)
	}
}

// TestUniqueBranchAvoidsCollisions is what stops two children sharing one
// checkout: task descriptions that begin alike truncate to the same branch.
func TestUniqueBranchAvoidsCollisions(t *testing.T) {
	used := map[string]bool{}

	first := uniqueBranch("", "agent/reply-with-the-single-word", used)
	used[first] = true
	second := uniqueBranch("", "agent/reply-with-the-single-word", used)
	used[second] = true
	third := uniqueBranch("", "agent/reply-with-the-single-word", used)

	if first == second || second == third || first == third {
		t.Fatalf("branches collided: %q %q %q", first, second, third)
	}
	if first != "agent/reply-with-the-single-word" {
		t.Errorf("first branch = %q, want the plain name", first)
	}
	if second != "agent/reply-with-the-single-word-2" {
		t.Errorf("second branch = %q, want a numbered suffix", second)
	}
}

// TestWorkspaceQueriesUnblockOnClose covers shutdown: `do` drops queued work
// once the server is closing, so anything waiting for a reply from the
// workspace goroutine has to notice that rather than block for ever.
func TestWorkspaceQueriesUnblockOnClose(t *testing.T) {
	srv, _, _ := newRepoServer(t)
	if err := srv.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.reviewDir("")
		srv.activeRoot()
		srv.panesPerPath([]string{"."})
		srv.paneByID("nope")
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a workspace query blocked after the server was closed")
	}
}

// TestPanesPerPathCreditsTheDeepestWorktree covers a worktree kept inside the
// repository it came from: a pane in it is under both paths, and it belongs to
// the checkout it is actually working in.
func TestPanesPerPathCreditsTheDeepestWorktree(t *testing.T) {
	srv, ws, repo := newRepoServer(t)

	inner := filepath.Join(repo, ".worktrees", "feature")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	ws.NewTab(session.KindShell, inner, "feature")

	counts := srv.panesPerPath([]string{repo, inner})
	if counts[inner] != 1 {
		t.Errorf("inner worktree has %d panes, want 1", counts[inner])
	}
	// newRepoServer's own tab is the only pane left in the main checkout.
	if counts[repo] != 1 {
		t.Errorf("main checkout has %d panes, want 1", counts[repo])
	}
}
