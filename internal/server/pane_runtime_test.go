package server

import (
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/workspace"
)

// fakeRuntime is a pane that is not a terminal: it answers every question the
// graph and the pane view ask from its own fields, and has no session behind it
// for them to read.
type fakeRuntime struct {
	status  session.Status
	detail  string
	input   string
	options []workspace.PermissionOption
	reply   string
	ran     bool
	started time.Time
	// sess is the process the pane was made with, which closing it still ends.
	sess *session.Session
}

func (f fakeRuntime) Kind() workspace.PaneKind { return workspace.PaneKindStructured }
func (f fakeRuntime) Caps() workspace.PaneCaps {
	return workspace.PaneCaps{Structured: true, AnswerPermission: true}
}
func (f fakeRuntime) Status() (session.Status, string) { return f.status, f.detail }
func (f fakeRuntime) StatusSince() time.Time           { return f.started }
func (f fakeRuntime) StartedAt() time.Time             { return f.started }
func (f fakeRuntime) HasRun() bool                     { return f.ran }
func (f fakeRuntime) Activity() workspace.Activity {
	if f.status == session.StatusWaiting {
		return workspace.ActivityWaiting
	}
	return workspace.ActivityWorking
}
func (f fakeRuntime) Waiting() workspace.WaitingOn {
	return workspace.WaitingOn{Detail: f.detail, ToolInput: f.input}
}
func (f fakeRuntime) PermissionOptions(string) []workspace.PermissionOption { return f.options }
func (f fakeRuntime) Background() workspace.BackgroundWork                  { return workspace.BackgroundWork{} }
func (f fakeRuntime) LastReply() (workspace.LastReply, bool) {
	return workspace.LastReply{Text: f.reply, Kind: "text"}, f.reply != ""
}
func (f fakeRuntime) Wake(string) error { return nil }
func (f fakeRuntime) Close() error      { return f.sess.Close() }
func (f fakeRuntime) Record(bool) error { return nil }
func (f fakeRuntime) Conversation() workspace.ConversationSource {
	return workspace.ConversationSource{}
}

// A pane of another kind shows in the graph and the pane view from what its
// runtime says, with no consumer looking at its session or its agent.
func TestAPaneOfAnotherKindIsReadThroughItsRuntime(t *testing.T) {
	e := newGraphEnv(t)
	always := []workspace.PermissionOption{
		{ID: "a", Name: "Allow", Kind: optionAllowOnce},
		{ID: "b", Name: "Always allow", Kind: optionAllowAlways},
	}
	ask(e.srv, func() bool {
		e.ws.SetRuntime(e.dev, fakeRuntime{status: session.StatusWaiting, detail: "Bash",
			input: `{"command":"make test"}`, options: always, ran: true, started: time.Now(), sess: e.ws.Pane(e.dev).Sess})
		e.ws.SetRuntime(e.eyes, fakeRuntime{status: session.StatusWorking, reply: "Reading the diff", ran: true, started: time.Now(), sess: e.ws.Pane(e.eyes).Sess})
		// The sessions behind them say something else, which must not be read.
		e.ws.Pane(e.dev).Sess.SetStatusFull(session.StatusIdle, "", "")
		e.ws.Pane(e.eyes).Sess.SetStatusFull(session.StatusIdle, "", "")
		return true
	})

	g := e.snapshotGraph(t)
	dev, ok := g.node(e.dev)
	if !ok {
		t.Fatal("no node for the structured pane")
	}
	if dev.Kind != graphKindStructured || dev.Status != "waiting" || dev.Activity != "waiting" {
		t.Errorf("kind %q, status %q, activity %q", dev.Kind, dev.Status, dev.Activity)
	}
	eyes, _ := g.node(e.eyes)
	if eyes.Status != "working" || eyes.Live != "Reading the diff" {
		t.Errorf("status %q, live %q; want the runtime's", eyes.Status, eyes.Live)
	}
	if boss, _ := g.node(e.boss); boss.Kind != graphKindTerminal {
		t.Errorf("the manager is a %q pane", boss.Kind)
	}
	if !hasNeed(g, e.dev) {
		t.Errorf("the waiting pane is not in the needs list: %+v", g.Teams)
	}

	snap, _ := ask(e.srv, func() stateMsg { return e.srv.snapshot() })
	perm := snap.Panes[e.dev].Permission
	if perm == nil || perm.Summary == "" || len(perm.Options) != 2 || perm.Options[1].Kind != optionAllowAlways {
		t.Errorf("permission = %+v, want the runtime's options", perm)
	}
	if last := snap.Panes[e.eyes].Last; last == nil || last.Text != "Reading the diff" {
		t.Errorf("last reply = %+v", last)
	}
}

func hasNeed(g graphMsg, pane string) bool {
	for _, tm := range g.Teams {
		for _, n := range tm.Needs {
			if n.Pane == pane {
				return true
			}
		}
	}
	return false
}
