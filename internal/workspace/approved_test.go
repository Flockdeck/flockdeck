package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/session"
)

// What was approved is what runs: a spawn carrying an approved agent and company
// refuses to start one that is not.
func TestASpawnRunsWhatWasApprovedAndNothingElse(t *testing.T) {
	batonAgents(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	ws.NewTab(session.KindShell, root, "lead")
	parent := ws.CurrentTab().Focus
	b := testBaton()
	bp := prepared(t, ws, root, b, "")

	// The agent and company it was approved for: it starts.
	id, err := ws.Spawn(parent, SpawnOptions{Kind: session.KindClaude, Agent: "gosession", Baton: &bp, Approved: &ApprovedTarget{Agent: "gosession", Provider: "go", ApprovalRequired: true}})
	if err != nil || id == "" {
		t.Fatalf("a spawn for the approved agent failed: %v", err)
	}
	// Another agent than the one approved.
	if _, err := ws.Spawn(parent, SpawnOptions{Kind: session.KindClaude, Agent: "goprompt", Baton: &bp, Approved: &ApprovedTarget{Agent: "gosession", Provider: "go", ApprovalRequired: true}}); err == nil || !strings.Contains(err.Error(), "approved") {
		t.Errorf("another agent than the approved one started: %v", err)
	}
	// The same agent for another company: its exe was changed while the user was asked.
	if _, err := ws.Spawn(parent, SpawnOptions{Kind: session.KindClaude, Agent: "gosession", Baton: &bp, Approved: &ApprovedTarget{Agent: "gosession", Provider: "git", ApprovalRequired: true}}); err == nil || !strings.Contains(err.Error(), "settings changed") {
		t.Errorf("another company than the approved one started: %v", err)
	}
	// The agent was removed from the catalog.
	if _, err := ws.Spawn(parent, SpawnOptions{Kind: session.KindClaude, Agent: "gosession", Baton: &bp, Approved: &ApprovedTarget{Agent: "gone", Provider: "go", ApprovalRequired: true}}); err == nil {
		t.Error("an approval for an agent that is not there started one")
	}
}

// Where a worktree will be is worked out without making it, and is where it is
// made, so the project it belongs to can be judged before anything is cut.
func TestPlannedWorktreeIsWherePrepareWorktreeMakesIt(t *testing.T) {
	isolateConfig(t)
	repo := t.TempDir()
	for _, args := range [][]string{
		{"init", "--initial-branch=main"},
		{"config", "user.email", "t@e.com"},
		{"config", "user.name", "T"},
		{"config", "commit.gpgsign", "false"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git %v: %v: %s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "f.txt"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-m", "init"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git %v: %v: %s", args, err, out)
		}
	}
	ws := newTestWorkspace(t, repo)
	planned := ws.PlannedWorktree(repo, "fix-auth")
	if _, err := os.Stat(planned); err == nil {
		t.Fatalf("planning made %s", planned)
	}
	made, err := ws.PrepareWorktree(repo, "fix-auth")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Clean(made) != filepath.Clean(planned) {
		t.Errorf("planned %s, made %s", planned, made)
	}
	// Asked again, the branch has its worktree, and that is where it is.
	if again := ws.PlannedWorktree(repo, "fix-auth"); filepath.Clean(again) != filepath.Clean(made) {
		t.Errorf("planned %s for a branch whose worktree is %s", again, made)
	}
}

// When nobody was asked, the helper must run for the company the baton came from: a
// folder that turns out to send it elsewhere is refused, and one that does not is not.
func TestASpawnNobodyWasAskedAboutMustRunForTheCompanyTheBatonCameFrom(t *testing.T) {
	batonAgents(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	ws.NewTab(session.KindShell, root, "lead")
	parent := ws.CurrentTab().Focus
	bp := prepared(t, ws, root, testBaton(), "")
	same := &ApprovedTarget{Agent: "gosession", Provider: "go", SourceProvider: "go"}
	if _, err := ws.Spawn(parent, SpawnOptions{Kind: session.KindClaude, Agent: "gosession", Baton: &bp, Approved: same}); err != nil {
		t.Fatalf("a spawn for the company the baton came from: %v", err)
	}
	other := &ApprovedTarget{Agent: "gosession", Provider: "go", SourceProvider: "anthropic"}
	if _, err := ws.Spawn(parent, SpawnOptions{Kind: session.KindClaude, Agent: "gosession", Baton: &bp, Approved: other}); err == nil || !strings.Contains(err.Error(), "nothing approved") {
		t.Errorf("a spawn to another company than the baton came from, with no approval, started: %v", err)
	}
	// An approval for a pair is accepted when the company is the approved one.
	approved := &ApprovedTarget{Agent: "gosession", Provider: "go", SourceProvider: "anthropic", ApprovalRequired: true}
	if _, err := ws.Spawn(parent, SpawnOptions{Kind: session.KindClaude, Agent: "gosession", Baton: &bp, Approved: approved}); err != nil {
		t.Errorf("an approved pair was refused: %v", err)
	}
}
