package server

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/perch/internal/session"
	"github.com/jmwri/perch/internal/workspace"
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

// TestConnectingAWindowRefreshesGit covers the other half of leaving the git
// summaries alone while no window is open: opening one has to bring them up to
// date, well inside the fifteen second polling interval.
func TestConnectingAWindowRefreshesGit(t *testing.T) {
	srv, _, repo := newRepoServer(t)

	// Let the refresh made shortly after startup land first, so what follows
	// can only be explained by the connection.
	time.Sleep(1500 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(repo, "new-file.txt"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	conn := dialControl(t, srv)
	start := time.Now()
	nextState(t, conn, func(s stateMsg) bool {
		for _, p := range s.Panes {
			if p.Untracked > 0 {
				return true
			}
		}
		return false
	})
	// Anything near the polling interval means the refresh was the next tick
	// coming round rather than the connection asking for one.
	if elapsed := time.Since(start); elapsed > gitStatusInterval/2 {
		t.Errorf("the window waited %v for its git summary, want a refresh on connect", elapsed)
	}
}

// TestReviewFromASubdirectoryDiffsTheRightFile covers where panes actually
// sit: somewhere inside the project rather than at the top of it. git reports
// changed files relative to the repository root whichever directory it is
// asked from, so a review conducted from a subdirectory was looking for those
// names underneath it and finding nothing.
func TestReviewFromASubdirectoryDiffsTheRightFile(t *testing.T) {
	srv, _, repo := newRepoServer(t)
	conn := dialControl(t, srv)
	nextState(t, conn, nil)

	sub := filepath.Join(repo, "nested", "deeper")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("hello\nand more\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var ch changesMsg
	sendCmd(t, conn, command{Cmd: "changes", Path: sub})
	readUntil(t, conn, "changes", &ch)
	if ch.Error != "" {
		t.Fatalf("changes error: %s", ch.Error)
	}
	if ch.Cwd == sub {
		t.Errorf("changes came back for %q, want the repository root", ch.Cwd)
	}
	if filepath.Base(ch.Cwd) != filepath.Base(repo) {
		t.Errorf("cwd = %q, want the root of %q", ch.Cwd, repo)
	}
	if len(ch.Files) != 1 || ch.Files[0].Path != "README.md" {
		t.Fatalf("changed files = %+v, want README.md", ch.Files)
	}

	var d diffMsg
	sendCmd(t, conn, command{Cmd: "diff", Path: sub, Text: ch.Files[0].Path})
	readUntil(t, conn, "diff", &d)
	if d.Error != "" {
		t.Fatalf("diff error: %s", d.Error)
	}
	if !strings.Contains(d.Text, "and more") {
		t.Errorf("diff from a subdirectory did not show the change: %q", d.Text)
	}
}

// TestCommitRefreshesThePaneHeaders pins the reason the review panel asks for
// a git refresh at all: the dirty and untracked counts beside each pane's
// branch have to settle down once the work has been committed, without waiting
// for the polling interval to come round.
func TestCommitRefreshesThePaneHeaders(t *testing.T) {
	srv, _, repo := newRepoServer(t)
	conn := dialControl(t, srv)
	nextState(t, conn, nil)

	if err := os.WriteFile(filepath.Join(repo, "added.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dirty := func(s stateMsg) bool {
		for _, p := range s.Panes {
			if p.Dirty > 0 || p.Untracked > 0 {
				return true
			}
		}
		return false
	}
	nextState(t, conn, dirty)

	sendCmd(t, conn, command{Cmd: "commit", Path: repo, Text: "keep it"})
	start := time.Now()
	nextState(t, conn, func(s stateMsg) bool { return !dirty(s) })
	if elapsed := time.Since(start); elapsed > gitStatusInterval/2 {
		t.Errorf("the pane headers took %v to settle, want a refresh on commit", elapsed)
	}
}

// TestRemovingAWorktreeWithOpenPanesIsRefused covers the destructive half of
// the worktree panel: the directory goes with the worktree, and an agent left
// standing in it has nothing to tell it why.
func TestRemovingAWorktreeWithOpenPanesIsRefused(t *testing.T) {
	srv, _, repo := newRepoServer(t)
	conn := dialControl(t, srv)
	nextState(t, conn, nil)

	sendCmd(t, conn, command{Cmd: "worktreeRemove", Path: repo})
	var note noticeMsg
	readUntil(t, conn, "notice", &note)
	if !note.Error {
		t.Fatalf("expected an error notice, got %+v", note)
	}
	if !strings.Contains(note.Text, "still working in") {
		t.Errorf("notice = %q, want it to name the panes in the way", note.Text)
	}
	if _, err := os.Stat(repo); err != nil {
		t.Fatalf("the worktree was removed anyway: %v", err)
	}
}

// TestWorktreePanelDescribesEveryCheckout covers what the worktree panel is
// built from: the checkouts, the branches on offer, and the base a new one
// would start from. Nothing exercised it before the calls were made together.
func TestWorktreePanelDescribesEveryCheckout(t *testing.T) {
	_, _, repo := newRepoServer(t)
	gitCmd(t, repo, "branch", "spare")
	side := filepath.Join(t.TempDir(), "side")
	gitCmd(t, repo, "worktree", "add", "-b", "side", side)

	msg := collectWorktrees(repo)
	if msg.Error != "" {
		t.Fatalf("worktrees: %s", msg.Error)
	}
	if len(msg.Items) != 2 {
		t.Fatalf("%d checkouts, want the main one and the new one: %+v", len(msg.Items), msg.Items)
	}
	if !msg.Items[0].Main {
		t.Error("the first entry should be the main worktree")
	}
	if msg.Items[0].Branch != "main" || msg.Items[1].Branch != "side" {
		t.Errorf("branches = %q and %q, want main and side", msg.Items[0].Branch, msg.Items[1].Branch)
	}
	if msg.DefaultBase != "main" {
		t.Errorf("default base = %q, want main", msg.DefaultBase)
	}
	var names []string
	for _, b := range msg.Branches {
		names = append(names, b.Name)
	}
	sort.Strings(names)
	if strings.Join(names, ",") != "main,side,spare" {
		t.Errorf("branches offered = %v, want main, side and spare", names)
	}
	for _, b := range msg.Branches {
		if b.Name == "spare" && b.CheckedIn != "" {
			t.Errorf("spare is checked out at %q, but nothing has it", b.CheckedIn)
		}
		if b.Name == "side" && b.CheckedIn == "" {
			t.Error("side is checked out in the new worktree and should say so")
		}
	}

	// A directory that is not a repository is explained rather than passed
	// git's own wording, which names parent directories nobody asked about.
	outside := collectWorktrees(t.TempDir())
	if !strings.Contains(outside.Error, "not a git repository") {
		t.Errorf("error for a plain directory = %q", outside.Error)
	}
}

// gitCmd runs git in dir and fails the test if it does not succeed.
func gitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

// TestPanelsSayWhenTheCheckoutIsGone covers a pane left behind by a worktree
// that was removed under it: the directory is not missing a .git, it is not
// there at all, and saying so is the difference between looking for the
// problem and knowing it.
func TestPanelsSayWhenTheCheckoutIsGone(t *testing.T) {
	gone := filepath.Join(t.TempDir(), "removed-worktree")
	if err := os.MkdirAll(gone, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}

	if got := collectChanges(gone).Error; !strings.Contains(got, "no longer exists") {
		t.Errorf("changes error = %q, want it to say the directory is gone", got)
	}
	if got := collectWorktrees(gone).Error; !strings.Contains(got, "no longer exists") {
		t.Errorf("worktrees error = %q, want it to say the directory is gone", got)
	}

	// A directory that is there but holds no repository still gets the older
	// wording, which is the right answer for it.
	plain := t.TempDir()
	if got := collectChanges(plain).Error; !strings.Contains(got, "not a git repository") {
		t.Errorf("changes error for a plain directory = %q", got)
	}
}

// TestRepoRootAnswersWithoutGitWhereItCan covers the resolution done on every
// click in the file list and every commit.
func TestRepoRootAnswersWithoutGitWhereItCan(t *testing.T) {
	_, _, repo := newRepoServer(t)
	sub := filepath.Join(repo, "pkg", "inner")
	if err := os.MkdirAll(sub, 0o750); err != nil {
		t.Fatal(err)
	}
	side := filepath.Join(t.TempDir(), "side")
	gitCmd(t, repo, "worktree", "add", "-b", "side", side)

	for _, tc := range []struct{ in, want string }{
		{repo, repo},
		{sub, repo},
		// A linked worktree's .git is a file rather than a directory, and it
		// is still the top of its own working tree.
		{side, side},
	} {
		if got := repoRoot(tc.in); got != tc.want {
			t.Errorf("repoRoot(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}

	// A directory with no repository above it is its own answer, so a review
	// of it fails with something about that directory rather than another.
	plain := t.TempDir()
	if got := repoRoot(plain); got != plain {
		t.Errorf("repoRoot(%q) = %q, want it unchanged", plain, got)
	}
}

// TestPrunedSummarySaysWhatItDid covers the wording of a button that used to
// report success whether or not there was anything to clean up.
func TestPrunedSummarySaysWhatItDid(t *testing.T) {
	for _, tc := range []struct {
		n    int
		want string
	}{
		{0, "nothing to prune"},
		{1, "pruned 1 stale worktree record"},
		{3, "pruned 3 stale worktree records"},
	} {
		if got := prunedSummary(tc.n); !strings.Contains(got, tc.want) {
			t.Errorf("prunedSummary(%d) = %q, want it to contain %q", tc.n, got, tc.want)
		}
	}
	if strings.Contains(prunedSummary(1), "records") {
		t.Error("one record should not be described in the plural")
	}
}

// TestChangesPanelStopsAtALimitAndSaysSo covers a working tree with more
// changed files in it than a list of rows can usefully hold.
func TestChangesPanelStopsAtALimitAndSaysSo(t *testing.T) {
	_, _, repo := newRepoServer(t)
	for i := 0; i < 5; i++ {
		if err := os.WriteFile(filepath.Join(repo, fmt.Sprintf("f%d.txt", i)), []byte("new\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	all := collectChangesUpTo(repo, 100)
	if all.Error != "" {
		t.Fatalf("changes: %s", all.Error)
	}
	if len(all.Files) != 5 || all.Omitted != 0 {
		t.Fatalf("under the limit: %d files, %d omitted; want 5 and 0", len(all.Files), all.Omitted)
	}

	capped := collectChangesUpTo(repo, 2)
	if len(capped.Files) != 2 {
		t.Errorf("%d files sent, want the limit of 2", len(capped.Files))
	}
	if capped.Omitted != 3 {
		t.Errorf("omitted = %d, want 3 -- a list cut short without saying so reads as a clean tree", capped.Omitted)
	}
	// The branch summary is still the whole tree's, not the part that fitted.
	if capped.Branch != "main" {
		t.Errorf("branch = %q, want main", capped.Branch)
	}
}

// TestRemoteSummary pins what a window is told when git itself says little.
func TestRemoteSummary(t *testing.T) {
	cases := []struct {
		action, out, want string
	}{
		{"fetch", "", "fetched — nothing new"},
		{"pull", "  \n ", "already up to date"},
		{"push", "", "done"},
		{"push", "To github.com:x/y.git\n * [new branch] main -> main\n", "* [new branch] main -> main"},
		{"pull", "Updating a..b\nFast-forward\n 1 file changed\n", "1 file changed"},
	}
	for _, c := range cases {
		if got := remoteSummary(c.action, c.out); got != c.want {
			t.Errorf("remoteSummary(%q, %q) = %q, want %q", c.action, c.out, got, c.want)
		}
	}
}

// TestDiffOfAnUnchangedFileDoesNotBlameBinaryContent covers the empty diff.
// git reports binary content in its own words, so an empty diff means the file
// agrees with the last commit — saying otherwise sent people looking for a
// problem that was not there.
func TestDiffOfAnUnchangedFileDoesNotBlameBinaryContent(t *testing.T) {
	srv, _, repo := newRepoServer(t)
	conn := dialControl(t, srv)
	nextState(t, conn, nil)

	var d diffMsg
	sendCmd(t, conn, command{Cmd: "diff", Path: repo, Text: "README.md"})
	readUntil(t, conn, "diff", &d)
	if d.Error != "" {
		t.Fatalf("diff error: %s", d.Error)
	}
	if strings.Contains(d.Text, "binary") {
		t.Errorf("an unchanged file was reported as possibly binary: %q", d.Text)
	}
	if !strings.Contains(d.Text, "matches the last commit") {
		t.Errorf("diff text = %q, want it to say the file is unchanged", d.Text)
	}
}

// TestUnderPath pins which directories count as being inside a worktree. The
// answer decides the pane counts in the worktree panel and, since a worktree
// with panes in it is not removed without force, whether a checkout an agent
// is using can be deleted from under it.
func TestUnderPath(t *testing.T) {
	base := filepath.Join(t.TempDir(), "repo")
	cases := []struct {
		cwd  string
		want bool
	}{
		{base, true},
		{filepath.Join(base, "internal", "server"), true},
		// A directory whose name merely begins with dots is inside, not out.
		{filepath.Join(base, "..old-notes"), true},
		{filepath.Dir(base), false},
		{filepath.Join(filepath.Dir(base), "repo-other"), false},
		{"", false},
	}
	for _, c := range cases {
		if got := underPath(c.cwd, base); got != c.want {
			t.Errorf("underPath(%q, %q) = %v, want %v", c.cwd, base, got, c.want)
		}
	}

	// Windows names the same directory in either case; the two paths compared
	// here come from git and from however the project was opened.
	if runtime.GOOS == "windows" && !underPath(strings.ToUpper(base), strings.ToLower(base)) {
		t.Error("a difference in case made the same directory look unrelated")
	}
}
