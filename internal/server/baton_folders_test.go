package server

import (
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/baton"
)

// Where a baton came from is judged in the source pane's own folder, not in the
// folder of the pane that spawns the helper: a pane named by its id can work in a
// checkout whose settings send its agent through a gateway.
func TestTheSourceCompanyIsJudgedInTheSourcePanesOwnFolder(t *testing.T) {
	srv, ws := newTestServer(t)
	writeCatalog(t, "git")
	clean, gateway := plainRepo(t, false), plainRepo(t, true)
	parent := parentIn(t, srv, ws, clean)

	change := func(dir string) string {
		h := &handoff{Baton: baton.Baton{ID: "20261001-090000-0a1b2c"}, Source: "claude-a", Dir: dir, Scrubber: baton.NewScrubber()}
		got, _ := ask(srv, func() string {
			c, _, _ := srv.batonChange(h, parent, clean, clean, "", "claude-b", "", "")
			return c
		})
		return got
	}
	if got := change(""); got != "" {
		t.Errorf("with the source judged in the spawning pane's folder: %q, want no change of company", got)
	}
	if got := change(gateway); got == "" {
		t.Error("the source pane works behind a gateway, and that was not noticed")
	}
}

// The agent a window starts is judged where it will run: in the worktree the
// branch asks for, which can commit settings that send it through a gateway.
func TestAWindowStartIsJudgedInTheWorktreeItWillRunIn(t *testing.T) {
	srv, ws := newTestServer(t)
	writeCatalog(t, "git")
	repo := plainRepo(t, false)
	branchWithGateway(t, repo, "gw")
	parentIn(t, srv, ws, repo)
	target, err := ws.AgentSpecByID("claude-b")
	if err != nil {
		t.Fatal(err)
	}
	if p, why := srv.targetProvider(target, repo, ""); p != "anthropic" {
		t.Errorf("in the checkout itself: %q (%s), want anthropic", p, why)
	}
	if p, why := srv.targetProvider(target, repo, "gw"); p != "" || why == "" {
		t.Errorf("in the worktree of a branch that commits a gateway: %q (%s), want a company that is not known", p, why)
	}
	if p, _ := srv.targetProvider(target, repo, "fresh-branch"); p != "anthropic" {
		t.Errorf("in a worktree for a new branch: %q, want anthropic", p)
	}
}

// The same through the window: starting into the worktree of a branch that sends
// the agent through a gateway is a change of company, though the pane's own
// folder is not, and the tick is asked for.
func TestAWindowStartIntoAGatewayBranchNeedsTheTick(t *testing.T) {
	srv, ws := newTestServer(t)
	writeCatalog(t, "git")
	repo := plainRepo(t, false)
	branchWithGateway(t, repo, "gw")
	pane := parentIn(t, srv, ws, repo)
	conn := dialControl(t, srv)
	sendCmd(t, conn, command{Cmd: "makeBaton", ID: pane})
	var draft batonDraftMsg
	readUntil(t, conn, "batonDraft", &draft)
	sendCmd(t, conn, command{Cmd: "startFromBaton", ID: pane, Text: draft.Text, Agent: "claude-b", Branch: "gw"})
	var e batonErrorMsg
	readUntil(t, conn, "batonError", &e)
	if !strings.Contains(e.Error, "different company") {
		t.Errorf("batonError = %+v, want the tick to be asked for", e)
	}
}
