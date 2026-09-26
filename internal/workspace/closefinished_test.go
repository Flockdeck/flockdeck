package workspace

import (
	"errors"
	"testing"

	"github.com/jmwri/flockdeck/internal/hooks"
	"github.com/jmwri/flockdeck/internal/layout"
	"github.com/jmwri/flockdeck/internal/session"
)

// agentPaneIn opens a shell pane in its own tab and marks it as running an
// agent, the same trick conversation_test.go uses (addAgentPane) to give a
// pane an agent's Kind without starting a real CLI. What CloseFinishedPanes
// looks at -- Status and Err -- come from the real session underneath either
// way.
func agentPaneIn(t *testing.T, ws *Workspace, root, title string) *Pane {
	t.Helper()
	tab := ws.NewTab(session.KindShell, root, title)
	p := ws.Pane(tab.Tree.Panes()[0])
	if p == nil || p.Sess == nil {
		t.Fatal("the pane did not start")
	}
	p.Kind = session.KindAgent
	return p
}

// TestCloseFinishedPanesLeavesWorkingWaitingAndFailedPanesAlone covers the
// three statuses that must never be touched with no confirmation: a pane
// still working, one waiting on a person, and one whose last turn ended in an
// error (StatusIdle with Err set, read as "failed" the same way the web UI's
// outcomeOf does) -- left for a person to close by hand on purpose.
func TestCloseFinishedPanesLeavesWorkingWaitingAndFailedPanesAlone(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)

	working := agentPaneIn(t, ws, root, "working")
	working.Sess.SetStatus(session.StatusWorking, "")

	waiting := agentPaneIn(t, ws, root, "waiting")
	waiting.Sess.SetStatus(session.StatusWaiting, "")

	failed := agentPaneIn(t, ws, root, "failed")
	failed.Sess.SetStatus(session.StatusIdle, "")
	failed.Err = errors.New("boom")

	panes, tabs := ws.CloseFinishedPanes()
	if panes != 0 || tabs != 0 {
		t.Fatalf("CloseFinishedPanes() = (%d, %d), want (0, 0)", panes, tabs)
	}
	for _, p := range []*Pane{working, waiting, failed} {
		if ws.Pane(p.ID) == nil {
			t.Errorf("pane %s was closed but should have been left alone", p.ID)
		}
	}
}

// TestCloseFinishedPanesLeavesAQuietShellAlone covers a shell pane that has
// simply gone quiet at its own prompt: it reads StatusIdle the same way a
// settled agent does, but sitting at a prompt is what an open shell is for,
// not something finished, so it must not be swept up.
func TestCloseFinishedPanesLeavesAQuietShellAlone(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)

	tab := ws.NewTab(session.KindShell, root, "shell")
	shell := ws.Pane(tab.Tree.Panes()[0])
	if shell == nil || shell.Sess == nil {
		t.Fatal("the shell pane did not start")
	}
	shell.Sess.SetStatus(session.StatusIdle, "")

	panes, tabs := ws.CloseFinishedPanes()
	if panes != 0 || tabs != 0 {
		t.Fatalf("CloseFinishedPanes() = (%d, %d), want (0, 0)", panes, tabs)
	}
	if ws.Pane(shell.ID) == nil {
		t.Error("a quiet shell was closed as though it were finished")
	}
}

// TestCloseFinishedPanesClosesIdleAgentsAndExitedPanes covers what actually
// counts as finished: an agent that settled (StatusIdle) and any pane, agent
// or shell, whose process is simply gone (StatusExited). It also covers the
// point of going through ClosePaneByID rather than some new path of its own:
// a tab left with nothing but a finished pane closes itself, and a tab where
// one pane is closed and another is left waiting stays open with only the
// finished pane gone.
func TestCloseFinishedPanesClosesIdleAgentsAndExitedPanes(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)

	idleAgent := agentPaneIn(t, ws, root, "idle-agent")
	idleAgent.Sess.SetStatus(session.StatusIdle, "")
	idleTab := ws.tabOf(idleAgent.ID).ID

	exitedShell := agentPaneIn(t, ws, root, "exited-shell")
	exitedShell.Kind = session.KindShell
	exitedShell.Sess.SetStatus(session.StatusExited, "")
	exitedTab := ws.tabOf(exitedShell.ID).ID

	// A tab with one finished pane beside one still waiting: only the
	// finished one goes, and the tab -- not empty -- stays.
	mixedTab := ws.NewTab(session.KindShell, root, "mixed")
	ws.SplitPaneInWith(layout.Horizontal, Choice{Kind: session.KindShell}, root)
	var finishedInMix, waitingInMix *Pane
	for _, id := range mixedTab.Tree.Panes() {
		p := ws.Pane(id)
		p.Kind = session.KindAgent
		if finishedInMix == nil {
			finishedInMix = p
			p.Sess.SetStatus(session.StatusIdle, "")
		} else {
			waitingInMix = p
			p.Sess.SetStatus(session.StatusWaiting, "")
		}
	}

	panes, tabs := ws.CloseFinishedPanes()
	if panes != 3 {
		t.Errorf("panes closed = %d, want 3", panes)
	}
	if tabs != 2 {
		t.Errorf("tabs closed = %d, want 2 (idle-agent's and exited-shell's, not mixed's)", tabs)
	}

	if ws.Pane(idleAgent.ID) != nil {
		t.Error("the idle agent pane is still open")
	}
	if ws.Tab(idleTab) != nil {
		t.Error("the idle agent's now-empty tab is still open")
	}
	if ws.Pane(exitedShell.ID) != nil {
		t.Error("the exited shell pane is still open")
	}
	if ws.Tab(exitedTab) != nil {
		t.Error("the exited shell's now-empty tab is still open")
	}
	if ws.Pane(finishedInMix.ID) != nil {
		t.Error("the finished pane in the mixed tab is still open")
	}
	if ws.Pane(waitingInMix.ID) == nil {
		t.Error("the waiting pane in the mixed tab was closed")
	}
	if ws.Tab(mixedTab.ID) == nil {
		t.Error("the mixed tab was closed even though a waiting pane is still in it")
	}
}

// TestPaneFinishedMatchesCloseFinishedPanes covers the exported wrapper
// `flockdeck close` uses to decide whether a single named pane needs -force:
// it has to agree with CloseFinishedPanes' own reading of the same pane, or
// the two would disagree about what "finished" means for the very same pane.
func TestPaneFinishedMatchesCloseFinishedPanes(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)

	idle := agentPaneIn(t, ws, root, "idle")
	idle.Sess.SetStatus(session.StatusIdle, "")

	working := agentPaneIn(t, ws, root, "working")
	working.Sess.SetStatus(session.StatusWorking, "")

	failed := agentPaneIn(t, ws, root, "failed")
	failed.Sess.SetStatus(session.StatusIdle, "")
	failed.Err = errors.New("boom")

	if !ws.PaneFinished(idle.ID) {
		t.Error("an idle agent pane should count as finished")
	}
	if ws.PaneFinished(working.ID) {
		t.Error("a working pane should not count as finished")
	}
	if ws.PaneFinished(failed.ID) {
		t.Error("a pane whose last turn failed should not count as finished")
	}
	if ws.PaneFinished("no-such-pane") {
		t.Error("an unknown pane should not count as finished")
	}
}

// TestCloseFinishedPanesActsAcrossEveryOpenProject covers the scope decision:
// it reaches every open project, not only the one on screen, the same reach
// as the "All agents" overview -- a dev clearing out finished work does it
// once, not once per open project.
func TestCloseFinishedPanesActsAcrossEveryOpenProject(t *testing.T) {
	isolateConfig(t)
	ws, first, second := twoProjects(t)
	if got := ws.ActiveRoot(); got != first {
		t.Fatalf("active project = %s, want %s", got, first)
	}

	idleInSecond := agentPaneIn(t, ws, second, "idle-elsewhere")
	idleInSecond.Sess.SetStatus(session.StatusIdle, "")

	panes, tabs := ws.CloseFinishedPanes()
	if panes != 1 || tabs != 1 {
		t.Fatalf("CloseFinishedPanes() = (%d, %d), want (1, 1)", panes, tabs)
	}
	if ws.Pane(idleInSecond.ID) != nil {
		t.Error("an idle pane in a project other than the active one was left open")
	}
	if ws.ActiveRoot() != first {
		t.Error("closing a pane in another project switched the active project")
	}
}

// TestCloseFinishedPanesLeavesAnIdleAgentWithBackgroundWorkAlone covers an
// agent whose turn has ended while a background command or subagent is still
// running: it reads idle, but closing it would kill that work. Once the work
// has ended it is finished like any other, and an exited pane is closed
// whatever it was running.
func TestCloseFinishedPanesLeavesAnIdleAgentWithBackgroundWorkAlone(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)

	busy := agentPaneIn(t, ws, root, "busy")
	for _, ev := range []hooks.Event{
		{Event: "PostToolUse", Tool: "Bash", Background: hooks.BackgroundStart, BackgroundID: "shell:b1"},
		{Event: "SubagentStart", Background: hooks.BackgroundStart, BackgroundID: "agent:a1"},
		{Event: "Stop"},
	} {
		ev.SessionID, ev.Launch = busy.ID, busy.launch
		ws.handleHook(ev)
	}
	if st, _ := busy.Sess.Status(); st != session.StatusIdle {
		t.Fatalf("status = %v, want idle", st)
	}
	if ws.PaneFinished(busy.ID) {
		t.Fatal("PaneFinished is true with background work running")
	}
	if panes, _ := ws.CloseFinishedPanes(); panes != 0 || ws.Pane(busy.ID) == nil {
		t.Fatalf("CloseFinishedPanes closed a pane with background work (closed %d)", panes)
	}

	// One of two ending is not enough.
	ws.handleHook(hooks.Event{SessionID: busy.ID, Launch: busy.launch, Event: "SubagentStop", Background: hooks.BackgroundEnd, BackgroundID: "agent:a1"})
	if ws.PaneFinished(busy.ID) {
		t.Fatal("PaneFinished is true with a background command still running")
	}
	ws.handleHook(hooks.Event{SessionID: busy.ID, Launch: busy.launch, Event: "PostToolUse", Tool: "KillShell", Background: hooks.BackgroundEnd, BackgroundID: "shell:b1"})
	ws.handleHook(hooks.Event{SessionID: busy.ID, Launch: busy.launch, Event: "Stop"})
	if !ws.PaneFinished(busy.ID) {
		t.Fatal("PaneFinished is false once the background work ended")
	}

	// An exited pane goes whatever it left running.
	gone := agentPaneIn(t, ws, root, "gone")
	gone.Sess.NoteBackground("PostToolUse", hooks.BackgroundStart, "shell:x")
	gone.Sess.SetStatus(session.StatusExited, "")
	if !ws.PaneFinished(gone.ID) {
		t.Error("an exited pane with background work was not finished")
	}

	if panes, _ := ws.CloseFinishedPanes(); panes != 2 {
		t.Errorf("panes closed = %d, want 2", panes)
	}
}

// TestBackgroundWorkIsForgottenOnAFreshConversation covers /clear: work the
// last conversation started is not something the new one is waiting on.
func TestBackgroundWorkIsForgottenOnAFreshConversation(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	p := agentPaneIn(t, ws, root, "cleared")
	p.Sess.NoteBackground("PostToolUse", hooks.BackgroundStart, "shell:b1")
	ws.handleHook(hooks.Event{SessionID: p.ID, Launch: p.launch, Event: "SessionStart", Source: "compact"})
	if p.Sess.BackgroundTasks() != 1 {
		t.Fatal("a compaction forgot background work")
	}
	ws.handleHook(hooks.Event{SessionID: p.ID, Launch: p.launch, Event: "SessionStart", Source: "clear"})
	if p.Sess.BackgroundTasks() != 0 {
		t.Fatal("a cleared conversation still has background work")
	}
}

// TestAnAgentWhoseBackgroundCommandEndedByItselfIsFinished covers a
// run_in_background command that simply finishes, which Claude Code fires no
// hook of its own for. What it does do is submit a <task-notification> as a
// prompt, so the agent can read the result, and say at the end of the turn
// that follows what is still in flight; either is enough for the pane to be
// finished again, where it used to stay open until the conversation was
// cleared.
func TestAnAgentWhoseBackgroundCommandEndedByItselfIsFinished(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)

	for _, c := range []struct {
		name string
		end  []hooks.Event
	}{
		{"the notification", []hooks.Event{
			{Event: "UserPromptSubmit", Background: hooks.BackgroundEnd, BackgroundID: "task:b1"},
			{Event: "Stop"}}},
		{"the next Stop's list", []hooks.Event{
			{Event: "UserPromptSubmit"},
			{Event: "Stop", BackgroundTasks: inFlight()}}},
	} {
		p := agentPaneIn(t, ws, root, c.name)
		deliver(ws, p,
			hooks.Event{Event: "UserPromptSubmit"},
			hooks.Event{Event: "PostToolUse", Tool: "Bash", Background: hooks.BackgroundStart, BackgroundID: "shell:b1"},
			hooks.Event{Event: "Stop", BackgroundTasks: inFlight("shell:b1")})
		if ws.PaneFinished(p.ID) {
			t.Fatalf("%s: PaneFinished is true with the command still running", c.name)
		}
		deliver(ws, p, c.end...)
		if !ws.PaneFinished(p.ID) {
			t.Errorf("%s: PaneFinished is false once the command had ended (background tasks = %d)", c.name, p.Sess.BackgroundTasks())
		}
	}
}
