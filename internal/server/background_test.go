package server

import (
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/hooks"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/workspace"
)

// TestBackgroundWorkReachesTheWindow covers paneView.Background and
// agentView.Background: an agent's turn can end with a background command or
// subagent still running, and it then reads idle like one that is done. The
// count is what tells the two apart, in the pane's header and in the list of
// agents, so it has to reach both -- and go again once the work has ended.
func TestBackgroundWorkReachesTheWindow(t *testing.T) {
	srv, ws := newTestServer(t)
	conn := dialControl(t, srv)
	nextState(t, conn, nil)
	// A shell pane standing in for an agent, the way close_test.go's do: a
	// shell's own count is never sent, since it has no hooks to keep one.
	p, ok := ask(srv, func() *workspace.Pane {
		p := ws.Pane(ws.CurrentTab().Tree.Panes()[0])
		p.Kind = session.KindAgent
		return p
	})
	if !ok {
		t.Fatal("server closed")
	}
	id := p.ID

	listed := func(want int) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for {
			var ag agentsMsg
			sendCmd(t, conn, command{Cmd: "agents"})
			readUntil(t, conn, "agents", &ag)
			got := -1
			for _, a := range ag.Items {
				if a.PaneID == id {
					got = a.Background
				}
			}
			if got == want {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("the agents list counts %d background tasks, want %d", got, want)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}

	p.Sess.NoteBackground("PostToolUse", hooks.BackgroundStart, "shell:b1")
	p.Sess.NoteBackground("SubagentStart", hooks.BackgroundStart, "agent:a1")
	st := nextState(t, conn, func(s stateMsg) bool { return s.Panes[id].Background == 2 })
	if got := st.Panes[id].Background; got != 2 {
		t.Fatalf("the pane's view counts %d background tasks, want 2", got)
	}
	listed(2)

	// What is known of the work goes with the count: what the start said,
	// when it was first heard of, and nothing made up for the subagent whose
	// start said nothing.
	p.Sess.NoteBackgroundWork("PostToolUse", hooks.BackgroundStart, "shell:b1", session.BackgroundInfo{Type: "shell", Description: "Sleep", Command: "sleep 9"})
	p.Sess.SetBackgroundWork([]string{"shell:b1", "agent:a1", "monitor:m1"}, nil)
	st = nextState(t, conn, func(s stateMsg) bool { return len(s.Panes[id].BackgroundWork) == 3 })
	views := map[string]backgroundView{}
	for _, v := range st.Panes[id].BackgroundWork {
		views[v.ID] = v
	}
	if v := views["b1"]; v.Kind != "shell" || v.Command != "sleep 9" || v.Description != "Sleep" || v.Listed || v.Since == "" {
		t.Errorf("the shell's view = %+v", v)
	}
	if v := views["a1"]; v.Kind != "subagent" || v.Description != "" || v.Command != "" || v.Listed {
		t.Errorf("the subagent's view = %+v", v)
	}
	if v := views["m1"]; v.Kind != "monitor" || !v.Listed {
		t.Errorf("the monitor first heard of in a list = %+v", v)
	}
	var ag agentsMsg
	sendCmd(t, conn, command{Cmd: "agents"})
	readUntil(t, conn, "agents", &ag)
	for _, a := range ag.Items {
		if a.PaneID == id && len(a.BackgroundWork) != 3 {
			t.Errorf("the agents list says %d pieces of background work, want 3", len(a.BackgroundWork))
		}
	}

	p.Sess.SetBackground(nil)
	st = nextState(t, conn, func(s stateMsg) bool { return s.Panes[id].Background == 0 })
	if got := st.Panes[id].Background; got != 0 {
		t.Fatalf("the pane's view still counts %d background tasks once they ended", got)
	}
	listed(0)
	if w := st.Panes[id].BackgroundWork; len(w) != 0 {
		t.Fatalf("the pane's view still lists %+v once the work ended", w)
	}
}

// TestBackgroundViewsAreBounded covers backgroundViews' limits: at most
// maxBackgroundShown pieces, each string cut, and the kind read from the id
// where nothing else said one.
func TestBackgroundViewsAreBounded(t *testing.T) {
	var work []session.BackgroundWork
	for i := 0; i < maxBackgroundShown+3; i++ {
		work = append(work, session.BackgroundWork{ID: "task:t" + string(rune('a'+i)), Since: time.Now()})
	}
	work[0].Command = strings.Repeat("x", 500)
	got := backgroundViews(work)
	if len(got) != maxBackgroundShown {
		t.Fatalf("%d views, want %d", len(got), maxBackgroundShown)
	}
	if len(got[0].Command) > maxBackgroundViewText || !strings.HasSuffix(got[0].Command, "…") {
		t.Errorf("a long command was not cut: %d bytes", len(got[0].Command))
	}
	if got[0].Kind != "" || got[0].ID != "ta" {
		t.Errorf("an end's kindless id = %+v", got[0])
	}
	if backgroundViews(nil) != nil {
		t.Error("no work is not left out")
	}
}
