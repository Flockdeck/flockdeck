package workspace

import (
	"testing"

	"github.com/jmwri/flockdeck/internal/hooks"
	"github.com/jmwri/flockdeck/internal/session"
)

// TestActivityMatrix pins the one rule the pane's mark, the tab's mark, the
// project's mark and every count are drawn from: session status x background
// work x kind of pane, for a pane alone in its tab and project. Before there
// was one rule, the tab, the pane and the rail each had their own and the
// tallies a fourth, so an idle agent with background work was "working" in
// the tab and "idle" in the top bar.
func TestActivityMatrix(t *testing.T) {
	const (
		none = ActivityNone
		wait = ActivityWaiting
		work = ActivityWorking
	)
	cases := []struct {
		status     session.Status
		agent      bool
		background bool
		pane       Activity // the pane's own
		group      Activity // what its tab and project aggregate to
	}{
		{session.StatusStarting, true, false, ActivityStarting, none},
		{session.StatusStarting, true, true, ActivityStarting, none},
		{session.StatusWorking, true, false, work, work},
		{session.StatusWorking, true, true, work, work}, // counted once
		{session.StatusWaiting, true, false, wait, wait},
		{session.StatusWaiting, true, true, wait, wait}, // waiting outranks working
		{session.StatusBlocked, true, false, ActivityBlocked, wait},
		{session.StatusBlocked, true, true, ActivityBlocked, wait},
		{session.StatusIdle, true, false, ActivityIdle, none},
		{session.StatusIdle, true, true, work, work}, // the case this exists for
		{session.StatusExited, true, false, ActivityExited, none},
		{session.StatusExited, true, true, ActivityExited, none}, // nothing runs once the process has gone
		// A shell has no background work to count, and a build is work.
		{session.StatusWorking, false, false, work, work},
		{session.StatusIdle, false, true, ActivityIdle, none},
		{session.StatusExited, false, false, ActivityExited, none},
	}
	for _, c := range cases {
		name := c.status.String()
		if !c.agent {
			name += "/shell"
		}
		if c.background {
			name += "/background"
		}
		t.Run(name, func(t *testing.T) {
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
			if c.background {
				p.Sess.NoteBackground("PostToolUse", hooks.BackgroundStart, "shell:b1")
			}
			if st, _ := p.Sess.Status(); st != c.status {
				t.Fatalf("the status is %v: background work must leave it %v", st, c.status)
			}

			if got := PaneActivity(p); got != c.pane {
				t.Errorf("PaneActivity = %q, want %q", got, c.pane)
			}
			if got := ws.TabActivity(tab); got != c.group {
				t.Errorf("TabActivity = %q, want %q", got, c.group)
			}
			var proj *Project
			for _, pr := range ws.Projects() {
				if pr.Active {
					pr := pr
					proj = &pr
				}
			}
			if proj == nil {
				t.Fatal("no active project")
			}
			if proj.Activity != c.group {
				t.Errorf("Project.Activity = %q, want %q", proj.Activity, c.group)
			}
			// The counts are of the same panes: each is 1 exactly where the
			// aggregate is that activity.
			wantWaiting, wantWorking := b2i(c.group == wait), b2i(c.group == work)
			if w, k := ws.AttentionCount(); w != wantWaiting || k != wantWorking {
				t.Errorf("AttentionCount = (%d, %d), want (%d, %d)", w, k, wantWaiting, wantWorking)
			}
			if proj.Waiting != wantWaiting || proj.Working != wantWorking {
				t.Errorf("Project counts = (%d, %d), want (%d, %d)", proj.Waiting, proj.Working, wantWaiting, wantWorking)
			}
		})
	}
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// TestActivityAggregates pins the one priority order: waiting (blocked counts
// as waiting) outranks working, which outranks anything else, and the counts
// are of the same values.
func TestActivityAggregates(t *testing.T) {
	cases := []struct {
		in   []Activity
		want Activity
	}{
		{nil, ActivityNone},
		{[]Activity{ActivityIdle, ActivityExited, ActivityStarting, ActivityFailed}, ActivityNone},
		{[]Activity{ActivityIdle, ActivityWorking}, ActivityWorking},
		{[]Activity{ActivityWorking, ActivityWaiting, ActivityIdle}, ActivityWaiting},
		{[]Activity{ActivityWorking, ActivityBlocked}, ActivityWaiting},
		{[]Activity{ActivityWaiting, ActivityWorking, ActivityWorking}, ActivityWaiting},
	}
	for _, c := range cases {
		if got := Aggregate(c.in...); got != c.want {
			t.Errorf("Aggregate(%v) = %q, want %q", c.in, got, c.want)
		}
		var tally Tally
		for _, a := range c.in {
			tally.Add(a)
		}
		if tally.Activity() != c.want {
			t.Errorf("Tally(%v).Activity() = %q, want %q", c.in, tally.Activity(), c.want)
		}
	}
}

// TestActivityFollowsBackgroundWorkThroughHooks drives the real path -- the
// hook events Claude Code sends -- and checks the pane's status does not move
// while its activity, its tab's, its project's and the counts all do.
func TestActivityFollowsBackgroundWorkThroughHooks(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	p := agentPaneIn(t, ws, root, "one")
	tab := ws.tabOf(p.ID)
	all := func() (Activity, Activity, Activity, int) {
		_, working := ws.AttentionCount()
		var proj Activity
		for _, pr := range ws.Projects() {
			if pr.Active {
				proj = pr.Activity
			}
		}
		return PaneActivity(p), ws.TabActivity(tab), proj, working
	}
	deliver(ws, p,
		hooks.Event{Event: "UserPromptSubmit"},
		hooks.Event{Event: "PostToolUse", Tool: "Bash", Background: hooks.BackgroundStart, BackgroundID: "shell:b1"},
		hooks.Event{Event: "Stop"})
	if st, _ := p.Sess.Status(); st != session.StatusIdle {
		t.Fatalf("status = %v after Stop, want idle", st)
	}
	if a, b, c, n := all(); a != ActivityWorking || b != ActivityWorking || c != ActivityWorking || n != 1 {
		t.Errorf("idle over background work: pane %q tab %q project %q working %d, want working x3 and 1", a, b, c, n)
	}
	deliver(ws, p, hooks.Event{Event: "UserPromptSubmit", Background: hooks.BackgroundEnd, BackgroundID: "task:b1"}, hooks.Event{Event: "Stop"})
	if a, b, c, n := all(); a != ActivityIdle || b != ActivityNone || c != ActivityNone || n != 0 {
		t.Errorf("background work ended: pane %q tab %q project %q working %d, want idle, none, none, 0", a, b, c, n)
	}
}

// TestATabAndAProjectAggregateTheirPanes covers the aggregation over more than
// one pane: a tab holding an idle agent with background work and a waiting one
// is waiting; the project counts each once.
func TestATabAndAProjectAggregateTheirPanes(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	a := agentPaneIn(t, ws, root, "a")
	b := agentPaneIn(t, ws, root, "b")
	a.Sess.SetStatus(session.StatusIdle, "")
	a.Sess.NoteBackground("PostToolUse", hooks.BackgroundStart, "shell:b1")
	b.Sess.SetStatus(session.StatusIdle, "")
	tabA, tabB := ws.tabOf(a.ID), ws.tabOf(b.ID)
	if ws.TabActivity(tabA) != ActivityWorking || ws.TabActivity(tabB) != ActivityNone {
		t.Fatalf("tabs = %q, %q, want working and none", ws.TabActivity(tabA), ws.TabActivity(tabB))
	}
	b.Sess.SetStatus(session.StatusWaiting, "")
	w, k := ws.AttentionCount()
	if w != 1 || k != 1 {
		t.Errorf("AttentionCount = (%d, %d), want (1, 1)", w, k)
	}
	for _, pr := range ws.Projects() {
		if pr.Active && (pr.Activity != ActivityWaiting || pr.Waiting != 1 || pr.Working != 1) {
			t.Errorf("project = %q (%d waiting, %d working), want waiting (1, 1)", pr.Activity, pr.Waiting, pr.Working)
		}
	}
}
