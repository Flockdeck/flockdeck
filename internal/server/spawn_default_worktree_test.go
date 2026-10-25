package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/gitx"
	"github.com/jmwri/flockdeck/internal/hooks"
)

// defaultTreeServer is a server over a git repository, with a parent agent pane
// in it and a "gocli" agent that starts anywhere.
func defaultTreeServer(t *testing.T) (spawn func(hooks.SpawnRequest) (hooks.SpawnResult, error), repo string) {
	t.Helper()
	if !gitx.Available() {
		t.Skip("git is not installed")
	}
	srv, ws, repo := newRepoServer(t)
	rolesAgent(t, srv)
	hook := ws.HookServer()
	if hook == nil {
		t.Skip("no hook server")
	}
	parent := spawnGoPane(t, srv, ws)
	return func(req hooks.SpawnRequest) (hooks.SpawnResult, error) {
		req.Agent = "gocli"
		return hooks.Spawn(hook.BaseURL(), hook.Token(), parent, req)
	}, repo
}

func sameDir(a, b string) bool {
	ea, _ := filepath.EvalSymlinks(a)
	eb, _ := filepath.EvalSymlinks(b)
	return filepath.Clean(ea) == filepath.Clean(eb)
}

// A helper an agent starts in a role that works on its own branch is given a
// worktree and a branch, from the parent's current commit, whatever the parent's
// tree holds, and the reply says which.
func TestAHelperIsGivenAWorktreeByDefault(t *testing.T) {
	spawn, repo := defaultTreeServer(t)
	dirty := filepath.Join(repo, "unsaved.txt")
	if err := os.WriteFile(dirty, []byte("half done\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, req := range []hooks.SpawnRequest{
		{Task: "fix the parser", Role: "worker"},
		{Task: "repair the lexer"}, // no role
	} {
		res, err := spawn(req)
		if err != nil {
			t.Fatalf("%+v: %v", req, err)
		}
		if res.Branch == "" || res.Cwd == "" || sameDir(res.Cwd, repo) {
			t.Fatalf("%+v: reply = %+v, want a branch and a worktree other than the parent's", req, res)
		}
		if _, err := os.Stat(filepath.Join(res.Cwd, "README.md")); err != nil {
			t.Errorf("the worktree has no checkout: %v", err)
		}
		if _, err := os.Stat(filepath.Join(res.Cwd, "unsaved.txt")); err == nil {
			t.Error("the parent's uncommitted file is in the helper's worktree")
		}
	}
	if _, err := os.Stat(dirty); err != nil {
		t.Errorf("the parent's uncommitted file was touched: %v", err)
	}
	if wts, err := gitx.List(repo); err != nil || len(wts) != 3 {
		t.Errorf("worktrees = %v (%v), want the checkout and two more", wts, err)
	}
}

// Two helpers with the same task get two branches.
func TestTwoHelpersWithOneTaskGetTwoBranches(t *testing.T) {
	spawn, _ := defaultTreeServer(t)
	a, err := spawn(hooks.SpawnRequest{Task: "fix the parser", Role: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := spawn(hooks.SpawnRequest{Task: "fix the parser", Role: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	if a.Branch == b.Branch || a.Cwd == b.Cwd {
		t.Errorf("both helpers got %+v and %+v", a, b)
	}
}

// -no-worktree and the roles that read or share the checkout are left in it.
func TestOptOutAndReadOnlyRolesShareTheCheckout(t *testing.T) {
	spawn, repo := defaultTreeServer(t)
	for _, req := range []hooks.SpawnRequest{
		{Task: "fix the parser", Role: "worker", NoWorktree: true},
		{Task: "look at the branch", Role: "reviewer"},
		{Task: "plan it", Role: "planner"},
		{Task: "run it", Role: "manager"},
		{Task: "watch the build", Shell: true},
	} {
		res, err := spawn(req)
		if err != nil {
			t.Fatalf("%+v: %v", req, err)
		}
		if res.Branch != "" || !sameDir(res.Cwd, repo) {
			t.Errorf("%+v: reply = %+v, want the parent's checkout and no branch", req, res)
		}
	}
}

// -worktree still names the branch.
func TestNamedWorktreeIsKept(t *testing.T) {
	spawn, _ := defaultTreeServer(t)
	res, err := spawn(hooks.SpawnRequest{Task: "x", Role: "worker", Branch: "my-branch"})
	if err != nil || res.Branch != "my-branch" {
		t.Fatalf("reply = %+v, %v", res, err)
	}
}

// Outside a git repository the default is refused, not quietly turned into a
// shared checkout, and -no-worktree starts it.
func TestNoRepositoryRefusesTheDefaultWorktree(t *testing.T) {
	srv, ws := newTestServer(t)
	rolesAgent(t, srv)
	hook := ws.HookServer()
	if hook == nil {
		t.Skip("no hook server")
	}
	parent := spawnGoPane(t, srv, ws)
	spawn := func(req hooks.SpawnRequest) (hooks.SpawnResult, error) {
		req.Agent = "gocli"
		return hooks.Spawn(hook.BaseURL(), hook.Token(), parent, req)
	}
	if _, err := spawn(hooks.SpawnRequest{Task: "x", Role: "worker"}); err == nil || !strings.Contains(err.Error(), "-no-worktree") {
		t.Fatalf("err = %v, want a refusal that names -no-worktree", err)
	}
	if res, err := spawn(hooks.SpawnRequest{Task: "x", Role: "worker", NoWorktree: true}); err != nil || res.Branch != "" {
		t.Fatalf("with -no-worktree: %+v, %v", res, err)
	}
}

// The built-in roles say which ones get a worktree by default.
func TestRolePoliciesMatchWhatTheDefaultDoes(t *testing.T) {
	want := map[string]bool{"worker": true, "tester": true, "docs": true, "manager": false, "planner": false, "reviewer": false}
	seen := 0
	for _, r := range agent.BuiltinRoles() {
		tree, ok := want[r.ID]
		if !ok {
			continue
		}
		seen++
		if got := r.Worktree == "" || r.Worktree == agent.WorktreeOwn; got != tree {
			t.Errorf("role %s: worktree %q, gets a worktree by default = %v, want %v", r.ID, r.Worktree, got, tree)
		}
	}
	if seen != len(want) {
		t.Errorf("found %d of the %d roles", seen, len(want))
	}
}
