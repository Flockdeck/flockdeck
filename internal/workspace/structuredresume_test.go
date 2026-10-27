package workspace

import (
	"os"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/acp/acptest"
	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/session/transcript"
	"github.com/jmwri/flockdeck/internal/store"
	"github.com/jmwri/flockdeck/internal/structured"
)

// useFakeAgentWith is useFakeAgent with the fake agent's session options (see
// acptest.ArgSessions).
func useFakeAgentWith(ws *Workspace, sessions string) {
	exe, _ := os.Executable()
	ws.UseStructuredCommand(func(agent.Spec) *structured.Command {
		return &structured.Command{Path: exe, Args: []string{acptest.ArgAgent, acptest.ArgSessions + sessions}}
	})
}

// savedSessionOf is the session id the layout of root holds for pane id.
func savedSessionOf(t *testing.T, root, id string) string {
	t.Helper()
	st, err := store.Load(root)
	if err != nil || st == nil {
		t.Fatalf("load layout: %v", err)
	}
	var found *store.Pane
	var walk func(n *store.Node)
	walk = func(n *store.Node) {
		if n == nil {
			return
		}
		if n.Pane != nil && n.Pane.ID == id {
			found = n.Pane
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	for _, tab := range st.Tabs {
		walk(tab.Root)
	}
	if found == nil {
		t.Fatalf("pane %s is not in the layout", id)
	}
	return found.ACPSession
}

// restoreStructured saves a structured pane that has had one turn, quits, and
// restores it against an agent with the given session options.
func restoreStructured(t *testing.T, first, second string) (restored *Workspace, id, root string) {
	t.Helper()
	ws, lead, root := structuredCrew(t)
	useFakeAgentWith(ws, first)
	id = spawnStructured(t, ws, lead, "plain")
	statusWithin(t, ws, id, session.StatusIdle)
	if err := ws.SaveAll(); err != nil {
		t.Fatal(err)
	}
	if got := savedSessionOf(t, root, id); got != "sess-1" {
		t.Fatalf("the layout saved session %q", got)
	}
	ws.Close()

	restored = newTestWorkspace(t, root)
	useFakeAgentWith(restored, second)
	if ok, err := restored.Restore(); err != nil || !ok {
		t.Fatalf("restore: %v, %v", ok, err)
	}
	statusWithin(t, restored, id, session.StatusIdle)
	return restored, id, root
}

func TestARestoredStructuredPaneReopensItsSession(t *testing.T) {
	restored, id, _ := restoreStructured(t, "load,id=sess-1", "load,id=sess-1")
	sn, _ := restored.StructuredSnapshot(id)
	if sn.Resume != structured.ResumeLoaded || sn.SessionID != "sess-1" {
		t.Fatalf("resume = %q, session = %q", sn.Resume, sn.SessionID)
	}
	var said []string
	for _, e := range sn.Entries {
		if e.Kind == transcript.KindPrompt {
			said = append(said, e.Text)
		}
	}
	if len(said) != 2 || said[0] != "first question" {
		t.Errorf("the history was not replayed once: %q", said)
	}
	// The pane says nothing about a failure.
	for _, e := range sn.Entries {
		if e.Kind == transcript.KindNotice {
			t.Errorf("notice %q", e.Text)
		}
	}
}

func TestARestoredStructuredPaneWithAnAgentThatCannotResumeStartsANewSession(t *testing.T) {
	restored, id, root := restoreStructured(t, "load,id=sess-1", "id=sess-1")
	sn, _ := restored.StructuredSnapshot(id)
	if sn.Resume != structured.ResumeFailed {
		t.Fatalf("resume = %q", sn.Resume)
	}
	var notice string
	for _, e := range sn.Entries {
		if e.Kind == transcript.KindNotice {
			notice = e.Text
		}
	}
	if notice != structured.ResumeFailedText {
		t.Errorf("notice = %q", notice)
	}
	// The layout now holds the new session's id.
	if err := restored.SaveAll(); err != nil {
		t.Fatal(err)
	}
	if got := savedSessionOf(t, root, id); got != sn.SessionID {
		t.Errorf("saved %q, the session is %q", got, sn.SessionID)
	}
}

func TestARestoredStructuredPaneWithAStaleSessionStartsANewOne(t *testing.T) {
	restored, id, _ := restoreStructured(t, "load,id=sess-1", "load,resume,id=other")
	sn, _ := restored.StructuredSnapshot(id)
	if sn.Resume != structured.ResumeFailed || !strings.Contains(entriesText(sn.Entries), structured.ResumeFailedText) {
		t.Errorf("resume = %q, entries = %q", sn.Resume, entriesText(sn.Entries))
	}
	if strings.Contains(entriesText(sn.Entries), "SECRET-AGENT-TEXT") {
		t.Error("the agent's error text is in the conversation")
	}
}

func entriesText(es []transcript.Entry) string {
	var b strings.Builder
	for _, e := range es {
		b.WriteString(e.Text + e.Markdown + "\n")
	}
	return b.String()
}

func TestRestartingAStructuredPaneDoesNotResume(t *testing.T) {
	restored, id, _ := restoreStructured(t, "load,id=sess-1", "load,id=sess-1")
	if sn, _ := restored.StructuredSnapshot(id); sn.Resume != structured.ResumeLoaded {
		t.Fatalf("resume = %q", sn.Resume)
	}
	if !restored.RestartPaneByID(id) {
		t.Fatal("restart refused")
	}
	statusWithin(t, restored, id, session.StatusIdle)
	sn, _ := restored.StructuredSnapshot(id)
	if sn.Resume != structured.ResumeNone {
		t.Errorf("a restart resumed the session: %q", sn.Resume)
	}
}

func TestALayoutWithNoSessionIdStartsANewSession(t *testing.T) {
	ws, lead, root := structuredCrew(t)
	useFakeAgentWith(ws, "load,id=sess-1")
	id := spawnStructured(t, ws, lead, "plain")
	statusWithin(t, ws, id, session.StatusIdle)
	if err := ws.SaveAll(); err != nil {
		t.Fatal(err)
	}
	ws.Close()
	// A layout saved before the id was kept.
	st, err := store.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	var clear func(n *store.Node)
	clear = func(n *store.Node) {
		if n == nil {
			return
		}
		if n.Pane != nil {
			n.Pane.ACPSession = ""
		}
		for _, c := range n.Children {
			clear(c)
		}
	}
	for _, tab := range st.Tabs {
		clear(tab.Root)
	}
	if err := store.Save(root, st); err != nil {
		t.Fatal(err)
	}
	restored := newTestWorkspace(t, root)
	useFakeAgentWith(restored, "load,id=sess-1")
	if _, err := restored.Restore(); err != nil {
		t.Fatal(err)
	}
	statusWithin(t, restored, id, session.StatusIdle)
	if sn, _ := restored.StructuredSnapshot(id); sn.Resume != structured.ResumeNone {
		t.Errorf("resume = %q", sn.Resume)
	}
}
