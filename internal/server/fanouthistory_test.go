package server

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/workspace"
)

// TestWaitingLabelMatchesTheLiveCardsKindLabel covers the Go side of the
// webui's kindLabel: the same phrasing outcomeOf builds for a pane's
// "needs input" row, read here off the same ask/permission views the
// phone's own waiting card is built from.
func TestWaitingLabelMatchesTheLiveCardsKindLabel(t *testing.T) {
	cases := []struct {
		name, tool, input, want string
	}{
		{"a question", "AskUserQuestion",
			`{"questions":[{"question":"pick one","options":[{"label":"a"}]}]}`, "a question"},
		{"a bash command", "Bash", `{"command":"ls","description":"list"}`, "a command to run"},
		{"an edit", "Edit", `{"filePath":"a.go"}`, "a file write"},
		{"a multi-edit", "MultiEdit", `{"filePath":"a.go","edits":[{"oldString":"a","newString":"b"}]}`, "a file write"},
		{"a write", "Write", `{"filePath":"a.go","content":"x"}`, "a file write"},
		{"another tool", "SomeMCPTool", `{}`, "to use SomeMCPTool"},
		{"nothing waiting on", "", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := waitingLabel(tc.tool, tc.input); got != tc.want {
				t.Errorf("waitingLabel(%q, %q) = %q, want %q", tc.tool, tc.input, got, tc.want)
			}
		})
	}
}

// TestFanoutOutcomeMatchesOutcomeOfsThreeKinds covers what the history panel
// reads off a pane whose process is not around to ask for its live status --
// the failed and done kinds, which do not depend on a real session.
func TestFanoutOutcomeMatchesOutcomeOfsThreeKinds(t *testing.T) {
	srv, _ := newTestServer(t)

	t.Run("failed", func(t *testing.T) {
		p := &workspace.Pane{ID: "p1", Err: errors.New("could not start")}
		kind, detail := srv.fanoutOutcome(p)
		if kind != "failed" || detail != "could not start" {
			t.Errorf("got (%q, %q), want (\"failed\", \"could not start\")", kind, detail)
		}
	})

	t.Run("done with no reply seen yet", func(t *testing.T) {
		p := &workspace.Pane{ID: "p2"}
		kind, detail := srv.fanoutOutcome(p)
		if kind != "done" || detail != "" {
			t.Errorf("got (%q, %q), want (\"done\", \"\")", kind, detail)
		}
	})

	t.Run("done with the agent's latest reply", func(t *testing.T) {
		p := &workspace.Pane{ID: "p3"}
		srv.preview.set(p.ID, lastReplyView{Text: "wrapped it up"})
		kind, detail := srv.fanoutOutcome(p)
		if kind != "done" || detail != "wrapped it up" {
			t.Errorf("got (%q, %q), want (\"done\", \"wrapped it up\")", kind, detail)
		}
	})

	t.Run("failed outranks a reply already seen", func(t *testing.T) {
		p := &workspace.Pane{ID: "p4", Err: errors.New("boom")}
		srv.preview.set(p.ID, lastReplyView{Text: "was about to finish"})
		kind, detail := srv.fanoutOutcome(p)
		if kind != "failed" || detail != "boom" {
			t.Errorf("got (%q, %q), want (\"failed\", \"boom\")", kind, detail)
		}
	})
}

// panesOf reads a tab's current panes on the workspace goroutine.
func panesOf(t *testing.T, srv *Server, ws *workspace.Workspace, tabID string) []string {
	t.Helper()
	ids, ok := ask(srv, func() []string {
		tab := ws.Tab(tabID)
		if tab == nil {
			return nil
		}
		return tab.Tree.Panes()
	})
	if !ok {
		t.Fatal("server closed")
	}
	return ids
}

// settlePane forces a spawned pane's session to a fixed status, the way
// TestProjectCountsFollowTheAgentsProject does, so a fan-out's outcome is
// read deterministically rather than raced against a real `go` process.
func settlePane(t *testing.T, srv *Server, ws *workspace.Workspace, id string, st session.Status) {
	t.Helper()
	ok, _ := ask(srv, func() bool {
		p := ws.Pane(id)
		if p == nil || p.Sess == nil {
			return false
		}
		p.Sess.SetStatus(st, "")
		return true
	})
	if !ok {
		t.Fatalf("pane %s never started", id)
	}
}

// TestClosingASettledFanOutTabRecordsItsHistory covers the common path: a
// fan-out's tab is closed whole, with every pane still in it, the way the
// tab strip's own close button does it.
func TestClosingASettledFanOutTabRecordsItsHistory(t *testing.T) {
	srv, ws := fanoutServer(t)
	root := ws.ActiveRoot()
	conn := dialControl(t, srv)
	nextState(t, conn, nil)

	runFanout(t, srv, fanoutRequest{Tasks: []string{"first-task", "second-task"}, Agent: "gocli"})
	got := wherePlaced(t, srv, ws)
	tabID := got.tab
	panes := panesOf(t, srv, ws, tabID)
	if len(panes) != 2 {
		t.Fatalf("the fan-out's tab holds %d panes, want 2", len(panes))
	}
	settlePane(t, srv, ws, panes[0], session.StatusIdle)
	settlePane(t, srv, ws, panes[1], session.StatusWaiting)

	sendCmd(t, conn, command{Cmd: "closeTab", ID: tabID})
	nextState(t, conn, func(s stateMsg) bool {
		for _, tb := range s.Tabs {
			if tb.ID == tabID {
				return false
			}
		}
		return true
	})

	sendCmd(t, conn, command{Cmd: "fanoutHistory", Root: root})
	var msg fanoutHistoryMsg
	readUntil(t, conn, "fanoutHistory", &msg)
	if msg.Root != root {
		t.Errorf("history answered for %q, want %q", msg.Root, root)
	}
	if len(msg.Items) != 1 {
		t.Fatalf("got %d jobs, want 1: %+v", len(msg.Items), msg.Items)
	}
	job := msg.Items[0]
	if job.Title != "Fan out" {
		t.Errorf("job title = %q, want %q", job.Title, "Fan out")
	}
	if len(job.Panes) != 2 {
		t.Fatalf("job holds %d panes, want 2: %+v", len(job.Panes), job.Panes)
	}
	kinds := map[string]bool{job.Panes[0].Kind: true, job.Panes[1].Kind: true}
	if !kinds["done"] || !kinds["needs"] {
		t.Errorf("job panes are %+v, want one done and one needing input", job.Panes)
	}
	for _, p := range job.Panes {
		if p.Task != "first-task" && p.Task != "second-task" {
			t.Errorf("pane task = %q, want one of the fanned-out tasks", p.Task)
		}
	}
}

// TestAFanOutStillWorkingIsNotRecorded covers a tab closed mid-run: nothing
// finished, so there is no job to remember, and history stays empty.
func TestAFanOutStillWorkingIsNotRecorded(t *testing.T) {
	srv, ws := newTestServer(t)

	// revealAgents' "go", given a task it does not recognise as a command,
	// errors out and exits almost at once -- fine for every other test here,
	// which only needs the pane to settle, but Session.SetStatus refuses to
	// move a session already marked StatusExited (see its own guard), so
	// settlePane's forced StatusWorking below only sticks if it wins a race
	// against that exit. It usually did, but not reliably enough on every
	// runner -- flaky on macOS CI. This agent blocks instead, so the pane
	// really is still running when settlePane says so, and there is nothing
	// left to race.
	hang := filepath.Join(t.TempDir(), "hang.go")
	if err := os.WriteFile(hang, []byte("package main\n\nimport \"time\"\n\nfunc main() { time.Sleep(time.Hour) }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeAgents(t, `{"version": 1, "agents": [{"id": "gocli", "name": "Go", "exe": "go",
		"args": [{"value": "run"}, {"value": "{{prompt}}"}]}]}`)
	ws.ReloadAgents()

	root := ws.ActiveRoot()
	conn := dialControl(t, srv)
	nextState(t, conn, nil)

	// The first task is the file that blocks; the second is still not a
	// command "go run" understands, so it errors out and settles quickly --
	// exactly what the pane meant to read as idle needs.
	runFanout(t, srv, fanoutRequest{Tasks: []string{hang, "second-task"}, Agent: "gocli"})
	got := wherePlaced(t, srv, ws)
	tabID := got.tab
	panes := panesOf(t, srv, ws, tabID)
	settlePane(t, srv, ws, panes[0], session.StatusWorking)
	settlePane(t, srv, ws, panes[1], session.StatusIdle)

	sendCmd(t, conn, command{Cmd: "closeTab", ID: tabID})
	nextState(t, conn, func(s stateMsg) bool {
		for _, tb := range s.Tabs {
			if tb.ID == tabID {
				return false
			}
		}
		return true
	})

	sendCmd(t, conn, command{Cmd: "fanoutHistory", Root: root})
	var msg fanoutHistoryMsg
	readUntil(t, conn, "fanoutHistory", &msg)
	if len(msg.Items) != 0 {
		t.Errorf("a tab closed while still working left %d jobs in the history, want 0: %+v", len(msg.Items), msg.Items)
	}
}

// TestASettledJobClosedPaneByPaneIsRecordedOnce covers a job dismantled one
// pane at a time rather than by its tab: the first close still sees every
// pane, and everything after that must not add a second, smaller record for
// the same job.
func TestASettledJobClosedPaneByPaneIsRecordedOnce(t *testing.T) {
	srv, ws := fanoutServer(t)
	root := ws.ActiveRoot()
	conn := dialControl(t, srv)
	nextState(t, conn, nil)

	runFanout(t, srv, fanoutRequest{Tasks: []string{"first-task", "second-task"}, Agent: "gocli"})
	got := wherePlaced(t, srv, ws)
	tabID := got.tab
	panes := panesOf(t, srv, ws, tabID)
	settlePane(t, srv, ws, panes[0], session.StatusIdle)
	settlePane(t, srv, ws, panes[1], session.StatusIdle)

	// The first pane closes; the tab survives with the second still in it.
	sendCmd(t, conn, command{Cmd: "closePane", ID: panes[0]})
	nextState(t, conn, func(s stateMsg) bool { return len(panesOf(t, srv, ws, tabID)) == 1 })

	// The second, and last, pane closes the tab. Neither this nor the first
	// close must be lost: the job was captured whole on the first.
	sendCmd(t, conn, command{Cmd: "closePane", ID: panes[1]})
	nextState(t, conn, func(s stateMsg) bool {
		for _, tb := range s.Tabs {
			if tb.ID == tabID {
				return false
			}
		}
		return true
	})

	sendCmd(t, conn, command{Cmd: "fanoutHistory", Root: root})
	var msg fanoutHistoryMsg
	readUntil(t, conn, "fanoutHistory", &msg)
	if len(msg.Items) != 1 {
		t.Fatalf("a job closed pane by pane left %d jobs in the history, want exactly 1: %+v", len(msg.Items), msg.Items)
	}
	if len(msg.Items[0].Panes) != 2 {
		t.Errorf("the recorded job holds %d panes, want both of the job's original 2", len(msg.Items[0].Panes))
	}
}

// TestAnOrdinaryMultiPaneTabIsNeverRecorded covers a tab built by hand out of
// two ordinary splits, the same shape a settled fan-out collapses to but
// never one, since no fan-out filled it. See workspace.Tab.Delegated.
func TestAnOrdinaryMultiPaneTabIsNeverRecorded(t *testing.T) {
	srv, ws := newTestServer(t)
	root := ws.ActiveRoot()
	conn := dialControl(t, srv)
	nextState(t, conn, nil)

	lead := wherePlaced(t, srv, ws)
	tabID := lead.tab
	sendCmd(t, conn, command{Cmd: "splitPane", ID: lead.pane, Dir: "h", Kind: "shell"})
	nextState(t, conn, func(s stateMsg) bool { return len(panesOf(t, srv, ws, tabID)) == 2 })

	sendCmd(t, conn, command{Cmd: "closeTab", ID: tabID})
	nextState(t, conn, func(s stateMsg) bool {
		for _, tb := range s.Tabs {
			if tb.ID == tabID {
				return false
			}
		}
		return true
	})

	sendCmd(t, conn, command{Cmd: "fanoutHistory", Root: root})
	var msg fanoutHistoryMsg
	readUntil(t, conn, "fanoutHistory", &msg)
	if len(msg.Items) != 0 {
		t.Errorf("an ordinary split tab was recorded as a fan-out job: %+v", msg.Items)
	}
}
