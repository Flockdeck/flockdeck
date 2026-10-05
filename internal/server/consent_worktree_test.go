package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/hooks"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/workspace"
)

const gatewaySettings = `{"env": {"ANTHROPIC_BASE_URL": "https://corp-gateway.example/anthropic"}}`

func gitRun(t *testing.T, repo string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = repo
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Skipf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// plainRepo makes a git repository, with or without a committed .claude/settings.json
// that sends Claude Code through a gateway.
func plainRepo(t *testing.T, gateway bool) string {
	t.Helper()
	repo := t.TempDir()
	gitRun(t, repo, "init", "--initial-branch=main")
	gitRun(t, repo, "config", "user.email", "t@e.com")
	gitRun(t, repo, "config", "user.name", "T")
	gitRun(t, repo, "config", "commit.gpgsign", "false")
	if gateway {
		if err := os.MkdirAll(filepath.Join(repo, ".claude"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo, ".claude", "settings.json"), []byte(gatewaySettings), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "f.txt"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "add", ".")
	gitRun(t, repo, "commit", "-m", "init")
	return repo
}

// branchWithGateway makes a branch that commits the gateway settings and leaves it with
// no worktree, as a branch somebody already had.
func branchWithGateway(t *testing.T, repo, name string) {
	t.Helper()
	gitRun(t, repo, "checkout", "-q", "-b", name)
	if err := os.MkdirAll(filepath.Join(repo, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".claude", "settings.json"), []byte(gatewaySettings), 0o600); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "add", ".")
	gitRun(t, repo, "commit", "-m", "route through the gateway")
	gitRun(t, repo, "checkout", "-q", "main")
}

// fakeClaude puts a program called claude on the path, so that agents that run it are
// Claude Code to the scrubber by their program as well as by their id.
func fakeClaude(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	body := "@echo off\r\nexit /b 0\r\n"
	name := "claude.cmd"
	if runtime.GOOS != "windows" {
		body, name = "#!/bin/sh\nexit 0\n", "claude"
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func writeCatalog(t *testing.T, gitExe string) {
	t.Helper()
	fakeClaude(t)
	path, err := agent.ConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	body := `{"version": 1, "agents": [
		{"id": "claude-a", "name": "Claude A", "exe": "claude", "args": [{"value": "{{session}}"}], "caps": {"resume": true}},
		{"id": "claude-b", "name": "Claude B", "exe": "claude", "args": [{"value": "{{session}}"}], "caps": {"resume": true}},
		{"id": "gitcli", "name": "Git as an agent", "exe": "` + gitExe + `", "args": [{"value": "{{session}}"}]}]}`
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func parentIn(t *testing.T, srv *Server, ws *workspace.Workspace, repo string) string {
	t.Helper()
	id, ok := ask(srv, func() string {
		ws.ReloadAgents()
		return ws.NewTabWith(workspace.Choice{Kind: session.KindClaude, Agent: "claude-a"}, repo, "parent").Focus
	})
	if !ok || id == "" {
		t.Fatal("the parent pane did not start")
	}
	return id
}

// recordApproved records what each baton spawn carries into Spawn, and puts the hook back.
func recordApproved(t *testing.T) *[]*workspace.ApprovedTarget {
	t.Helper()
	var got []*workspace.ApprovedTarget
	old := spawnApproved
	spawnApproved = func(a *workspace.ApprovedTarget) { got = append(got, a) }
	t.Cleanup(func() { spawnApproved = old })
	return &got
}

func worktreeListed(t *testing.T, repo, branch string) bool {
	t.Helper()
	return strings.Contains(gitRun(t, repo, "worktree", "list", "--porcelain"), "refs/heads/"+branch)
}

// With the gateway committed everywhere, the source and the helper are both through it:
// a helper of the same agent needs no approval, and the first spawn must start.
func TestAWorktreeWithACommittedGatewayStillStartsASameAgentHelper(t *testing.T) {
	srv, ws := newTestServer(t)
	repo := plainRepo(t, true)
	writeCatalog(t, "git")
	parent := parentIn(t, srv, ws, repo)
	hook := ws.HookServer()
	if hook == nil {
		t.Skip("no hook server")
	}
	res, err := hooks.Spawn(hook.BaseURL(), hook.Token(), parent,
		hooks.SpawnRequest{Task: "x", Agent: "claude-a", Baton: "self", Branch: "wt-self"})
	if err != nil {
		t.Fatalf("a helper of the same agent, in a worktree whose settings hold a gateway the source has too, was refused: %v", err)
	}
	if res.Cwd == "" || !strings.Contains(res.Cwd, "wt-self") {
		t.Errorf("the helper is not in the worktree: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(res.Cwd, ".claude", "settings.json")); err != nil {
		t.Errorf("the worktree does not have the committed settings the test is about: %v", err)
	}
}

// The source has no gateway; an existing branch, with no worktree, commits one. The
// worktree would put the helper through a gateway the source was not: it is not
// started silently. It is asked about, and with no flag it is refused before anything
// is made.
func TestAnExistingBranchThatCommitsAGatewayIsNotStartedSilently(t *testing.T) {
	srv, ws := newTestServer(t)
	repo := plainRepo(t, false)
	branchWithGateway(t, repo, "gw-branch")
	writeCatalog(t, "git")
	parent := parentIn(t, srv, ws, repo)
	hook := ws.HookServer()
	if hook == nil {
		t.Skip("no hook server")
	}
	_, err := hooks.Spawn(hook.BaseURL(), hook.Token(), parent,
		hooks.SpawnRequest{Task: "x", Agent: "claude-a", Baton: "self", Branch: "gw-branch"})
	if err == nil || !strings.Contains(err.Error(), "-baton-send-elsewhere") {
		t.Fatalf("err = %v, want the refusal that asks for the flag", err)
	}
	if worktreeListed(t, repo, "gw-branch") {
		t.Error("a worktree was made for a spawn that was refused")
	}
}

// The user approves one company going to another. The worktree holds settings the
// planned path did not read, but they do not change the company that was approved.
func TestAnApprovedSpawnIsAcceptedWhenTheWorktreeReadsOtherSettingsAndTheSameCompany(t *testing.T) {
	srv, ws := newTestServer(t)
	repo := plainRepo(t, false)
	branchWithGateway(t, repo, "gw-for-git")
	writeCatalog(t, "git")
	conn := dialWindow(t, srv)
	parent := parentIn(t, srv, ws, repo)
	hook := ws.HookServer()
	if hook == nil {
		t.Skip("no hook server")
	}
	got := recordApproved(t)
	shortApprovalWait(t, 20*time.Second)
	result := make(chan error, 1)
	go func() {
		_, err := hooks.Spawn(hook.BaseURL(), hook.Token(), parent,
			hooks.SpawnRequest{Task: "x", Agent: "gitcli", Baton: "self", Branch: "gw-for-git", BatonElsewhere: true})
		result <- err
	}()
	n := nextApproval(t, conn)
	sendCmd(t, conn, command{Cmd: "approveBaton", Text: n.Action.Send["text"]})
	select {
	case err := <-result:
		if err != nil && strings.Contains(err.Error(), "settings changed") {
			t.Fatalf("an approved spawn was refused for settings that do not change its company: %v", err)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("never answered")
	}
	if len(*got) != 1 || (*got)[0] == nil || !(*got)[0].ApprovalRequired {
		t.Errorf("the spawn carried %+v, want an approval that was required", *got)
	}
}

// A company that is not known has to be approved, and the approval covers exactly that:
// the helper starts when the real worktree is also unknown.
func TestAnUnknownCompanyIsApprovedAndThenStarts(t *testing.T) {
	srv, ws := newTestServer(t)
	repo := plainRepo(t, false)
	branchWithGateway(t, repo, "gw-unknown")
	writeCatalog(t, "git")
	conn := dialWindow(t, srv)
	parent := parentIn(t, srv, ws, repo)
	hook := ws.HookServer()
	if hook == nil {
		t.Skip("no hook server")
	}
	shortApprovalWait(t, 20*time.Second)
	result := make(chan error, 1)
	go func() {
		_, err := hooks.Spawn(hook.BaseURL(), hook.Token(), parent,
			hooks.SpawnRequest{Task: "x", Agent: "claude-b", Baton: "self", Branch: "gw-unknown", BatonElsewhere: true})
		result <- err
	}()
	n := nextApproval(t, conn)
	if !strings.Contains(n.Text, "gateway") {
		t.Errorf("the notice does not say the company is through a gateway: %s", n.Text)
	}
	sendCmd(t, conn, command{Cmd: "approveBaton", Text: n.Action.Send["text"]})
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("an approved spawn to an unknown company did not start: %v", err)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("never answered")
	}
}

// The gateway committed on the branch appears only after the approval was given for a
// company that was known: Spawn works it out again where the helper really is, and
// refuses, with the worktree it made taken away.
func TestARefusalAfterTheWorktreeWasMadeTakesItAway(t *testing.T) {
	srv, ws := newTestServer(t)
	repo := plainRepo(t, true)
	writeCatalog(t, "git")
	conn := dialWindow(t, srv)
	parent := parentIn(t, srv, ws, repo)
	hook := ws.HookServer()
	if hook == nil {
		t.Skip("no hook server")
	}
	shortApprovalWait(t, 20*time.Second)
	result := make(chan error, 1)
	go func() {
		_, err := hooks.Spawn(hook.BaseURL(), hook.Token(), parent,
			hooks.SpawnRequest{Task: "x", Agent: "gitcli", Baton: "self", Branch: "wt-gone", BatonElsewhere: true})
		result <- err
	}()
	n := nextApproval(t, conn)
	// The agent the helper would run is another company's program by the time it is
	// approved.
	writeCatalog(t, "go")
	if _, ok := ask(srv, func() bool { ws.ReloadAgents(); return true }); !ok {
		t.Fatal("the workspace did not answer")
	}
	sendCmd(t, conn, command{Cmd: "approveBaton", Text: n.Action.Send["text"]})
	select {
	case err := <-result:
		if err == nil || !strings.Contains(err.Error(), "settings changed") || !strings.Contains(err.Error(), "were removed") {
			t.Fatalf("err = %v, want the refusal and the removal of the worktree it made", err)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("never answered")
	}
	if worktreeListed(t, repo, "wt-gone") {
		t.Error("the worktree is still listed")
	}
	if out := gitRun(t, repo, "branch", "--list", "wt-gone"); out != "" {
		t.Errorf("the branch is still there: %s", out)
	}
}

// What a baton spawn carries into Spawn: always something, and ApprovalRequired only
// when the user was asked. A hook that never set it, or set it for every spawn, fails.
func TestASpawnThatNeededNoApprovalCarriesNoApprovalRequired(t *testing.T) {
	srv, ws := newTestServer(t)
	batonAgent(t, ws)
	pane := spawnGoPane(t, srv, ws)
	hook := ws.HookServer()
	if hook == nil {
		t.Skip("no hook server")
	}
	got := recordApproved(t)
	if _, err := hooks.Spawn(hook.BaseURL(), hook.Token(), pane, hooks.SpawnRequest{Task: "x", Agent: "gocli", Baton: "self"}); err != nil {
		t.Fatalf("a same-agent spawn: %v", err)
	}
	if len(*got) != 1 || (*got)[0] == nil {
		t.Fatalf("the spawn carried %+v: every baton spawn carries what was worked out", *got)
	}
	if a := (*got)[0]; a.ApprovalRequired || a.Agent != "gocli" {
		t.Errorf("a spawn nobody was asked about carried %+v", a)
	}
}
