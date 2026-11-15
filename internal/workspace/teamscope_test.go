package workspace

import (
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/hooks"
	"github.com/jmwri/flockdeck/internal/session"
)

// namedAgentIn opens an agent pane in a project, with a role and a peer name.
func namedAgentIn(t *testing.T, ws *Workspace, root, title, role, peer string) *Pane {
	t.Helper()
	p := agentPaneIn(t, ws, root, title)
	ws.mu.Lock()
	p.Role, p.PeerName = role, peer
	ws.mu.Unlock()
	return p
}

// A message names its target by pane id, by name or by role, and none of the
// three reaches a pane of another project.
func TestAMessageDoesNotResolveToAPaneOfAnotherProject(t *testing.T) {
	isolateConfig(t)
	ws, first, second := twoProjects(t)
	me := namedAgentIn(t, ws, first, "me", "", "mine")
	far := namedAgentIn(t, ws, second, "far", "reviewer", "faraway")

	for _, target := range []string{far.ID, "faraway", "reviewer"} {
		if res, err := ws.SendMessage(me.ID, target, "hello"); err == nil {
			t.Errorf("a message to %q was delivered: %+v", target, res)
		}
	}
	if n := len(ws.team.inbox.List(far.ID, true)); n != 0 {
		t.Errorf("the pane of the other project has %d messages", n)
	}
}

// A manager may name only the panes of its own project.
func TestPaneNameRefusesAPaneOfAnotherProject(t *testing.T) {
	isolateConfig(t)
	ws, first, second := twoProjects(t)
	boss := namedAgentIn(t, ws, first, "boss", "manager", "boss")
	far := agentPaneIn(t, ws, second, "far")
	far.Sess.SetStatus(session.StatusIdle, "")

	_, err := ws.PaneNamed(hooks.PaneNameRequest{Auth: hooks.Auth{Pane: boss.ID}, Target: far.ID, Text: "renamed"})
	if err == nil || !strings.Contains(err.Error(), "not in your project") {
		t.Fatalf("err = %v, want a refusal for a pane of another project", err)
	}
}

func TestPaneInProject(t *testing.T) {
	isolateConfig(t)
	ws, first, second := twoProjects(t)
	a := agentPaneIn(t, ws, first, "a")
	b := agentPaneIn(t, ws, first, "b")
	far := agentPaneIn(t, ws, second, "far")
	for _, c := range []struct {
		self, id string
		want     bool
	}{{a.ID, b.ID, true}, {a.ID, a.ID, true}, {a.ID, far.ID, false}, {far.ID, a.ID, false}, {a.ID, "gone", false}, {"gone", a.ID, false}} {
		if got := ws.PaneInProject(c.self, c.id); got != c.want {
			t.Errorf("PaneInProject(%q, %q) = %v, want %v", c.self, c.id, got, c.want)
		}
	}
}

func TestCloseFinishedPanesOfLeavesOtherProjectsAlone(t *testing.T) {
	isolateConfig(t)
	ws, first, second := twoProjects(t)
	me := agentPaneIn(t, ws, first, "me")
	mine := agentPaneIn(t, ws, first, "mine")
	mine.Sess.SetStatus(session.StatusIdle, "")
	far := agentPaneIn(t, ws, second, "far")
	far.Sess.SetStatus(session.StatusIdle, "")

	if panes, _, _ := ws.CloseFinishedPanesOf(me.ID); panes != 1 {
		t.Fatalf("closed %d panes, want 1", panes)
	}
	if ws.Pane(mine.ID) != nil {
		t.Error("an idle pane of the project is open")
	}
	if ws.Pane(far.ID) == nil {
		t.Error("an idle pane of another project was closed")
	}
	if panes, _, _ := ws.CloseFinishedPanesOf("gone"); panes != 0 || ws.Pane(far.ID) == nil {
		t.Error("a caller that is not open closed panes")
	}
}
