package server

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/workspace"
)

// startAgentReply is what runStartAgent read back: either a success, with the
// new pane's id and the request id it was asked to echo, or a notice, with
// its text and whether it was an error.
type startAgentReply struct {
	paneID, reqID string
	notice        string
	isErr         bool
	got           bool
}

// runStartAgent calls startAgent directly, as runFanout calls fanout, and
// reads back whichever comes first: an agentStarted message or a notice.
func runStartAgent(t *testing.T, srv *Server, cmd command) startAgentReply {
	t.Helper()
	c := &controlClient{out: make(chan []byte, 32)}
	return runStartAgentOn(t, srv, c, cmd)
}

// runStartAgentOn is runStartAgent against a controlClient the caller keeps,
// so a test can send it startAgent more than once and see its rate limiter
// carry state between calls, the way one window's really does.
func runStartAgentOn(t *testing.T, srv *Server, c *controlClient, cmd command) startAgentReply {
	t.Helper()
	cmd.Cmd = "startAgent"
	srv.startAgent(c, cmd)
	deadline := time.After(15 * time.Second)
	for {
		select {
		case raw := <-c.out:
			var head struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(raw, &head) != nil {
				continue
			}
			switch head.Type {
			case "agentStarted":
				var msg struct {
					PaneID string `json:"paneId"`
					ID     string `json:"id"`
				}
				if err := json.Unmarshal(raw, &msg); err != nil {
					t.Fatalf("agentStarted: %v", err)
				}
				return startAgentReply{paneID: msg.PaneID, reqID: msg.ID, got: true}
			case "notice":
				var msg noticeMsg
				if err := json.Unmarshal(raw, &msg); err != nil {
					t.Fatalf("notice: %v", err)
				}
				return startAgentReply{notice: msg.Text, isErr: msg.Error, got: true}
			default:
				continue
			}
		case <-deadline:
			t.Fatal("startAgent never replied")
		}
	}
}

// TestStartAgentStartsWithTheTaskNotAHelper covers the whole point of the
// feature: a phone starting an agent gets a pane running the task it typed,
// and that pane is the user's own -- not a helper, which since v0.3.7 is not
// notified while its parent stays open, and a pane nobody is watching that
// never notifies its user is worse than not starting it at all.
func TestStartAgentStartsWithTheTaskNotAHelper(t *testing.T) {
	srv, ws := fanoutServer(t)
	root := ws.ActiveRoot()

	reply := runStartAgent(t, srv, command{ID: "req-1", Root: root, Agent: "gocli", Task: "reticulate the splines"})
	if reply.notice != "" {
		t.Fatalf("startAgent said %q, want it to succeed", reply.notice)
	}
	if reply.paneID == "" {
		t.Fatal("startAgent did not answer with a pane id")
	}

	p, ok := ask(srv, func() *workspace.Pane { return ws.Pane(reply.paneID) })
	if !ok || p == nil {
		t.Fatal("the pane startAgent named does not exist")
	}
	if p.Task != "reticulate the splines" {
		t.Errorf("pane task = %q, want the task it was started with", p.Task)
	}
	// Pane.Parent is unexported outside the workspace package, so it is read
	// the same way the phone's chat view would notice a helper: by asking
	// whether an idle nudge would be suppressed is not exposed either, but
	// TestAgentSpawnRecordsParentButAUserFanoutDoesNot in the workspace
	// package pins Spawn's own rule that only SpawnedByAgent sets it, and
	// StartAgent never sets it -- this test is what would catch a change to
	// StartAgent that started setting it.
}

// TestStartAgentAnswersWithTheNewPaneID covers the reply a window waits on to
// open the chat view straight away: the id of the pane just started, and the
// id of the request it was asked for, so a window that sent more than one
// can tell which answer is which.
func TestStartAgentAnswersWithTheNewPaneID(t *testing.T) {
	srv, ws := fanoutServer(t)
	root := ws.ActiveRoot()

	reply := runStartAgent(t, srv, command{ID: "abc123", Root: root, Agent: "gocli", Task: "add a health endpoint"})
	if reply.notice != "" {
		t.Fatalf("startAgent said %q, want it to succeed", reply.notice)
	}
	if reply.reqID != "abc123" {
		t.Errorf("reply id = %q, want the request's own %q", reply.reqID, "abc123")
	}
	if ok, _ := ask(srv, func() bool { return ws.Pane(reply.paneID) != nil }); !ok {
		t.Errorf("the answered pane id %q does not name a real pane", reply.paneID)
	}
}

// TestStartAgentDoesNotMoveDeskFocus covers a phone starting an agent while
// somebody is sitting at the desk: the whole point of leaving Pane.Parent
// empty is that the pane notifies like any other, but that is no excuse to
// also drag the desk's own window onto a tab it never asked to see -- a
// phone cannot see the desk, and must not move it.
func TestStartAgentDoesNotMoveDeskFocus(t *testing.T) {
	srv, ws := fanoutServer(t)
	root := ws.ActiveRoot()
	before := wherePlaced(t, srv, ws)

	reply := runStartAgent(t, srv, command{Root: root, Agent: "gocli", Task: "write parser tests"})
	if reply.notice != "" {
		t.Fatalf("startAgent said %q, want it to succeed", reply.notice)
	}

	after := wherePlaced(t, srv, ws)
	if after != before {
		t.Errorf("focus moved from %+v to %+v; starting an agent from the phone must not move the desk", before, after)
	}
}

// TestStartAgentRefusesAnUnknownRoot covers a phone naming a project that is
// not open on this desk. Opening one on its say-so was never on offer: it
// would put a project on the desk's tab bar that nobody at the desk asked
// for, from a device that cannot see it to close again.
func TestStartAgentRefusesAnUnknownRoot(t *testing.T) {
	srv, ws := fanoutServer(t)
	before, _ := ask(srv, func() int { return len(ws.Projects()) })
	stray := t.TempDir()

	reply := runStartAgent(t, srv, command{Root: stray, Agent: "gocli", Task: "do something"})
	if reply.paneID != "" {
		t.Fatalf("startAgent started a pane for a root that was never opened: %q", reply.paneID)
	}
	if !reply.isErr || !strings.Contains(reply.notice, filepath.Base(stray)) {
		t.Errorf("notice = %q (err=%v), want a refusal naming %s", reply.notice, reply.isErr, filepath.Base(stray))
	}
	after, _ := ask(srv, func() int { return len(ws.Projects()) })
	if after != before {
		t.Error("a root that was not open before startAgent is open now; it must never open one")
	}
}

// TestStartAgentRefusesAnUnknownAgent covers a request naming an agent the
// catalog has never heard of, the same refusal applyAgentDefault gives a
// default nobody has ever installed.
func TestStartAgentRefusesAnUnknownAgent(t *testing.T) {
	srv, ws := fanoutServer(t)
	root := ws.ActiveRoot()

	reply := runStartAgent(t, srv, command{Root: root, Agent: "no-such-agent", Task: "do something"})
	if reply.paneID != "" {
		t.Fatalf("startAgent started a pane for an unknown agent: %q", reply.paneID)
	}
	if !reply.isErr || !strings.Contains(reply.notice, "no-such-agent") {
		t.Errorf("notice = %q (err=%v), want a refusal naming the unknown agent", reply.notice, reply.isErr)
	}
}

// TestStartAgentRefusesAnEmptyTask covers a phone that sent nothing to say:
// starting an agent with no task is refused the same way Spawn refuses one,
// before anything -- a root check, a worktree -- is done on its behalf.
func TestStartAgentRefusesAnEmptyTask(t *testing.T) {
	srv, ws := fanoutServer(t)
	root := ws.ActiveRoot()

	reply := runStartAgent(t, srv, command{Root: root, Agent: "gocli", Task: "   "})
	if reply.paneID != "" {
		t.Fatal("startAgent started a pane with no task")
	}
	if !reply.isErr {
		t.Errorf("notice = %q, want a refusal", reply.notice)
	}
}

// TestSnapshotSaysStartAgentIsUnderstood covers what a phone reads to decide
// whether to offer "New agent" at all: canStartAgent on the state message,
// true for any desktop new enough to run this test. An older desktop simply
// never sends the field, which is what the phone treats as unsupported.
func TestSnapshotSaysStartAgentIsUnderstood(t *testing.T) {
	srv, _ := newTestServer(t)
	msg, ok := ask(srv, srv.snapshot)
	if !ok {
		t.Fatal("server closed")
	}
	if !msg.CanStartAgent {
		t.Error("the snapshot does not say startAgent is understood")
	}
}

// TestStartAgentInAFreshWorktree covers the phone's "in a new worktree"
// switch, which reuses the fan-out's own worktree naming rather than a
// second copy of it: the branch is named from the task, and the pane works
// in a worktree of its own, not in the project's own checkout.
func TestStartAgentInAFreshWorktree(t *testing.T) {
	srv, ws, repo := newRepoServer(t)
	writeAgents(t, revealAgents)
	ws.ReloadAgents()

	reply := runStartAgent(t, srv, command{Root: repo, Agent: "gocli", Task: "add a health endpoint", Worktree: true})
	if reply.notice != "" {
		t.Fatalf("startAgent said %q, want it to succeed", reply.notice)
	}

	p, ok := ask(srv, func() *workspace.Pane { return ws.Pane(reply.paneID) })
	if !ok || p == nil {
		t.Fatal("the pane startAgent named does not exist")
	}
	if samePath(filepath.Clean(p.Cwd), filepath.Clean(repo)) {
		t.Errorf("pane cwd = %s, want a worktree of its own rather than the project itself", p.Cwd)
	}
	wantBranch := workspace.BranchNameFor("add a health endpoint")
	if p.Branch != wantBranch {
		t.Errorf("branch = %q, want the fan-out's own naming for the task, %q", p.Branch, wantBranch)
	}
}

// TestStartAgentIsRateLimitedPerWindow covers the backstop against a phone
// calling startAgent in a tight loop: each call spawns a real agent process
// and, with worktree:true, cuts a worktree, and nothing before this bounded
// how many of those one window could ask for in a burst.
func TestStartAgentIsRateLimitedPerWindow(t *testing.T) {
	srv, ws := fanoutServer(t)
	root := ws.ActiveRoot()
	c := &controlClient{out: make(chan []byte, 256)}

	for i := 0; i < startAgentRateLimit; i++ {
		reply := runStartAgentOn(t, srv, c, command{Root: root, Agent: "gocli", Task: "task"})
		if reply.notice != "" {
			t.Fatalf("call %d: startAgent said %q, want it to succeed", i, reply.notice)
		}
	}

	reply := runStartAgentOn(t, srv, c, command{Root: root, Agent: "gocli", Task: "one too many"})
	if !reply.isErr {
		t.Fatalf("call %d: startAgent said %q, want a rate-limit refusal", startAgentRateLimit, reply.notice)
	}
	if reply.paneID != "" {
		t.Error("a rate-limited call must not start a pane")
	}

	// A second window is unaffected: the limit is per connection, not global,
	// so it must not be told to wait for a burst it had no part in.
	other := &controlClient{out: make(chan []byte, 8)}
	reply = runStartAgentOn(t, srv, other, command{Root: root, Agent: "gocli", Task: "from another window"})
	if reply.notice != "" {
		t.Fatalf("a fresh window was refused by another window's rate limit: %q", reply.notice)
	}
}
