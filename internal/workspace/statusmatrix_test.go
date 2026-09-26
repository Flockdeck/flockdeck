package workspace

import (
	"os"
	"testing"

	"github.com/jmwri/flockdeck/internal/hooks"
	"github.com/jmwri/flockdeck/internal/session"
)

// The tests in this file are docs/status-matrix.md's rows as the workspace
// sees them: events arriving at handleHook -- for the right pane, the wrong
// one, a stale start of the right one -- and what the pane, its tab and the
// counts drawn from it end up saying. A row the code gets wrong today is
// written as the behaviour it should have and skipped through knownBug.

// knownBug skips a case the matrix records as a confirmed bug, unless
// FLOCKDECK_STATUS_BUGS asks for the known bugs to be run.
func knownBug(t *testing.T, row, why string) {
	t.Helper()
	if os.Getenv("FLOCKDECK_STATUS_BUGS") == "" {
		t.Skipf("known bug, docs/status-matrix.md row %s: %s (FLOCKDECK_STATUS_BUGS=1 runs it)", row, why)
	}
}

// deliver hands events to handleHook as the pane's own current start would
// send them.
func deliver(ws *Workspace, p *Pane, evs ...hooks.Event) {
	for _, ev := range evs {
		ev.SessionID, ev.Launch = p.ID, p.launch
		ws.handleHook(ev)
	}
}

// TestStatusMatrixHookRouting is rows C6-C9: which pane an event is applied
// to, if any.
func TestStatusMatrixHookRouting(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	a := agentPaneIn(t, ws, root, "a")
	b := agentPaneIn(t, ws, root, "b")
	deliver(ws, a, hooks.Event{Event: "UserPromptSubmit"})
	deliver(ws, b, hooks.Event{Event: "UserPromptSubmit"}, hooks.Event{Event: "Stop"})

	t.Run("C6/an event from a stale start of the pane is dropped", func(t *testing.T) {
		ws.handleHook(hooks.Event{SessionID: a.ID, Launch: "an-earlier-start", Event: "Stop"})
		if st, _ := a.Sess.Status(); st != session.StatusWorking {
			t.Errorf("status = %v, want working: the Stop was the process before's", st)
		}
	})
	t.Run("C7/an event naming no start is taken as the pane's own", func(t *testing.T) {
		ws.handleHook(hooks.Event{SessionID: b.ID, Event: "PreToolUse", Tool: "Bash"})
		if st, detail := b.Sess.Status(); st != session.StatusWorking || detail != "Bash" {
			t.Errorf("status = %v %q, want working on Bash", st, detail)
		}
		deliver(ws, b, hooks.Event{Event: "Stop"})
	})
	t.Run("C8/an event for no open pane changes nothing", func(t *testing.T) {
		ws.handleHook(hooks.Event{SessionID: "no-such-pane", Event: "Notification", NotificationType: "permission_prompt"})
		if waiting, _ := ws.AttentionCount(); waiting != 0 {
			t.Errorf("waiting = %d after an event for no pane, want 0", waiting)
		}
	})
	t.Run("C8/an event for one pane leaves the others alone", func(t *testing.T) {
		deliver(ws, a, hooks.Event{Event: "PreToolUse", Tool: "AskUserQuestion"})
		if st, _ := b.Sess.Status(); st != session.StatusIdle {
			t.Errorf("the other pane's status = %v, want idle", st)
		}
		deliver(ws, a, hooks.Event{Event: "PostToolUse", Tool: "AskUserQuestion"})
	})
	t.Run("C9/an event for a pane with no session is dropped", func(t *testing.T) {
		ws.mu.Lock()
		sess := b.Sess
		b.Sess = nil
		ws.mu.Unlock()
		ws.handleHook(hooks.Event{SessionID: b.ID, Launch: b.launch, Event: "UserPromptSubmit"})
		ws.mu.Lock()
		b.Sess = sess
		ws.mu.Unlock()
		if st, _ := b.Sess.Status(); st != session.StatusIdle {
			t.Errorf("status = %v, want idle: nothing was there to apply it to", st)
		}
	})
}

// TestStatusMatrixTurnsThroughTheWorkspace is sections B and C at the point
// events really arrive, with the background bookkeeping handleHook does
// alongside the status.
func TestStatusMatrixTurnsThroughTheWorkspace(t *testing.T) {
	ev := func(event, tool string) hooks.Event { return hooks.Event{Event: event, Tool: tool} }
	cases := []struct {
		row        string
		name       string
		events     []hooks.Event
		want       session.Status
		wantDetail string
		background int
		bug        string
	}{
		{row: "B2", name: "a turn with tools ends idle",
			events: []hooks.Event{ev("UserPromptSubmit", ""), ev("PreToolUse", "Bash"), ev("PostToolUse", "Bash"), ev("Stop", "")},
			want:   session.StatusIdle},
		{row: "B6", name: "a permission prompt is waiting on the tool",
			events: []hooks.Event{ev("UserPromptSubmit", ""), ev("PreToolUse", "Bash"), ev("PermissionRequest", ""),
				{Event: "Notification", NotificationType: "permission_prompt"}},
			want: session.StatusWaiting, wantDetail: "Bash"},
		{row: "B9", name: "a turn ending on a denial is blocked",
			events: []hooks.Event{ev("UserPromptSubmit", ""), ev("PreToolUse", "Bash"), ev("PermissionDenied", "Bash"), ev("Stop", "")},
			want:   session.StatusBlocked, wantDetail: "Bash"},
		{row: "B11", name: "the user stopping a tool is idle",
			events: []hooks.Event{ev("UserPromptSubmit", ""), ev("PreToolUse", "Bash"), ev("Interrupted", "Bash")},
			want:   session.StatusIdle},
		{row: "B13", name: "a compaction mid-turn stays working and keeps background work",
			events: []hooks.Event{ev("UserPromptSubmit", ""),
				{Event: "PostToolUse", Tool: "Bash", Background: hooks.BackgroundStart, BackgroundID: "shell:b1"},
				{Event: "SessionStart", Source: "compact"}},
			want: session.StatusWorking, background: 1},
		{row: "B14", name: "/clear at the prompt stays idle and forgets background work",
			events: []hooks.Event{ev("UserPromptSubmit", ""),
				{Event: "PostToolUse", Tool: "Bash", Background: hooks.BackgroundStart, BackgroundID: "shell:b1"},
				ev("Stop", ""), ev("SessionEnd", ""), {Event: "SessionStart", Source: "clear"}},
			want: session.StatusIdle},
		{row: "B15", name: "a resume keeps background work",
			events: []hooks.Event{ev("UserPromptSubmit", ""),
				{Event: "PostToolUse", Tool: "Bash", Background: hooks.BackgroundStart, BackgroundID: "shell:b1"},
				ev("Stop", ""), {Event: "SessionStart", Source: "resume"}},
			want: session.StatusIdle, background: 1},
		{row: "B16", name: "the idle nudge after a turn leaves it idle",
			events: []hooks.Event{ev("UserPromptSubmit", ""), ev("Stop", ""), {Event: "Notification", NotificationType: "idle_prompt"}},
			want:   session.StatusIdle},
		{row: "B20", name: "a foreground subagent's turn ends idle with nothing left running",
			events: []hooks.Event{ev("UserPromptSubmit", ""), ev("PreToolUse", "Task"),
				{Event: "SubagentStart", Background: hooks.BackgroundStart, BackgroundID: "agent:a1"},
				ev("PreToolUse", "Read"), ev("PostToolUse", "Read"),
				{Event: "SubagentStop", Background: hooks.BackgroundEnd, BackgroundID: "agent:a1"},
				ev("PostToolUse", "Task"), ev("Stop", "")},
			want: session.StatusIdle},
		{row: "B21", name: "a background subagent's turn ends idle with it counted",
			events: []hooks.Event{ev("UserPromptSubmit", ""), ev("PreToolUse", "Task"),
				{Event: "SubagentStart", Background: hooks.BackgroundStart, BackgroundID: "agent:a1"},
				ev("PostToolUse", "Task"), ev("Stop", "")},
			want: session.StatusIdle, background: 1},
		{row: "B21", name: "a background subagent finishing after the turn leaves the pane idle",
			events: []hooks.Event{ev("UserPromptSubmit", ""), ev("PreToolUse", "Task"),
				{Event: "SubagentStart", Background: hooks.BackgroundStart, BackgroundID: "agent:a1"},
				ev("PostToolUse", "Task"), ev("Stop", ""),
				ev("PreToolUse", "Read"), ev("PostToolUse", "Read"),
				{Event: "SubagentStop", Background: hooks.BackgroundEnd, BackgroundID: "agent:a1"}},
			want: session.StatusIdle},
		{row: "B22", name: "a background shell leaves the pane idle, counted",
			events: []hooks.Event{ev("UserPromptSubmit", ""),
				{Event: "PostToolUse", Tool: "Bash", Background: hooks.BackgroundStart, BackgroundID: "shell:b1"}, ev("Stop", "")},
			want: session.StatusIdle, background: 1},
		{row: "C1", name: "the last tool's PostToolUse delivered after the Stop",
			events: []hooks.Event{ev("UserPromptSubmit", ""), ev("PreToolUse", "Bash"), ev("Stop", ""), ev("PostToolUse", "Bash")},
			want:   session.StatusIdle},
		{row: "C14", name: "a lost Stop, then the idle nudge",
			events: []hooks.Event{ev("UserPromptSubmit", ""), ev("PreToolUse", "Bash"), ev("PostToolUse", "Bash"),
				{Event: "Notification", NotificationType: "idle_prompt"}},
			want: session.StatusIdle},
		{row: "C18", name: "/clear forgets a denial",
			events: []hooks.Event{ev("UserPromptSubmit", ""), ev("PermissionDenied", "Bash"),
				{Event: "SessionStart", Source: "clear"}, ev("Stop", "")},
			want: session.StatusIdle},
	}
	for _, c := range cases {
		t.Run(c.row+"/"+c.name, func(t *testing.T) {
			if c.bug != "" {
				knownBug(t, c.row, c.bug)
			}
			isolateConfig(t)
			root := t.TempDir()
			ws := newTestWorkspace(t, root)
			p := agentPaneIn(t, ws, root, "one")
			deliver(ws, p, c.events...)
			if st, detail := p.Sess.Status(); st != c.want || detail != c.wantDetail {
				t.Errorf("status = %v %q, want %v %q", st, detail, c.want, c.wantDetail)
			}
			if got := p.Sess.BackgroundTasks(); got != c.background {
				t.Errorf("background tasks = %d, want %d", got, c.background)
			}
		})
	}
}

// TestStatusMatrixAfterExit is row C10 through the workspace: an exited pane
// takes no status from a hook that arrives after it.
func TestStatusMatrixAfterExit(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	p := agentPaneIn(t, ws, root, "gone")
	deliver(ws, p, hooks.Event{Event: "UserPromptSubmit"})
	p.Sess.SetStatus(session.StatusExited, "")
	deliver(ws, p,
		hooks.Event{Event: "PreToolUse", Tool: "AskUserQuestion"},
		hooks.Event{Event: "Notification", NotificationType: "permission_prompt"},
		hooks.Event{Event: "Stop"},
		hooks.Event{Event: "SessionEnd"})
	if st, _ := p.Sess.Status(); st != session.StatusExited {
		t.Errorf("status = %v, want exited", st)
	}
	if !ws.PaneFinished(p.ID) {
		t.Error("an exited pane is not finished")
	}
}

// TestStatusMatrixDerivedCounts is section F: what the top bar, the tabs and
// "close finished" read off each status.
func TestStatusMatrixDerivedCounts(t *testing.T) {
	cases := []struct {
		row       string
		status    session.Status
		agent     bool
		waiting   int
		working   int
		attention bool
		finished  bool
	}{
		{"F1", session.StatusStarting, true, 0, 0, false, false},
		{"F1", session.StatusWorking, true, 0, 1, false, false},
		{"F1", session.StatusWaiting, true, 1, 0, true, false},
		{"F1", session.StatusBlocked, true, 1, 0, true, false},
		{"F1", session.StatusIdle, true, 0, 0, false, true},
		{"F1", session.StatusExited, true, 0, 0, false, true},
		// A shell printing counts as working in the top bar -- a build is
		// work -- and a shell at its prompt is not finished: an open shell is
		// for typing into.
		{"F2", session.StatusWorking, false, 0, 1, false, false},
		{"F2", session.StatusIdle, false, 0, 0, false, false},
		{"F2", session.StatusExited, false, 0, 0, false, true},
	}
	for _, c := range cases {
		t.Run(c.row+"/"+c.status.String(), func(t *testing.T) {
			isolateConfig(t)
			root := t.TempDir()
			ws := newTestWorkspace(t, root)
			tab := ws.NewTab(session.KindShell, root, "one")
			p := ws.Pane(tab.Tree.Panes()[0])
			if p == nil || p.Sess == nil {
				t.Fatal("the pane did not start")
			}
			if c.agent {
				p.Kind = session.KindAgent
			}
			p.Sess.SetStatus(c.status, "")
			waiting, working := ws.AttentionCount()
			if waiting != c.waiting || working != c.working {
				t.Errorf("AttentionCount = (%d, %d), want (%d, %d)", waiting, working, c.waiting, c.working)
			}
			if got := ws.TabNeedsAttention(tab); got != c.attention {
				t.Errorf("TabNeedsAttention = %v, want %v", got, c.attention)
			}
			if got := ws.PaneFinished(p.ID); got != c.finished {
				t.Errorf("PaneFinished = %v, want %v", got, c.finished)
			}
		})
	}
}
