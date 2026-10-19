package workspace

import (
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/session"
)

// A pane that has stopped reading blocks the write into it. A second send is
// refused while the first is in flight, so the blocked writes cannot pile up,
// and the flag is cleared again once the write has finished.
func TestSendToPaneAllowsOneWriteInFlight(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws, err := New(Options{Root: root})
	if err != nil {
		t.Fatalf("new workspace: %v", err)
	}
	t.Cleanup(ws.Close)
	tab := ws.NewTab(session.KindShell, root, "shell")
	p := ws.Pane(tab.Tree.Panes()[0])
	if p == nil || !p.Alive() {
		t.Fatal("the shell pane did not start")
	}

	p.sending = true
	if _, err := ws.SendToPane(p.ID, "echo hi", true); err != ErrSendInFlight {
		t.Fatalf("send with one in flight: err = %v, want ErrSendInFlight", err)
	}
	p.sending = false

	if _, err := ws.SendToPane(p.ID, "echo hi", true); err != nil {
		t.Fatalf("send: %v", err)
	}
	for deadline := time.Now().Add(10 * time.Second); sendingNow(ws, p); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the in-flight flag was never cleared after the write")
		}
	}
}

func TestSendToPaneRefusals(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws, err := New(Options{Root: root})
	if err != nil {
		t.Fatalf("new workspace: %v", err)
	}
	t.Cleanup(ws.Close)
	tab := ws.NewTab(session.KindShell, root, "shell")
	id := tab.Tree.Panes()[0]

	if _, err := ws.SendToPane("missing", "hi", true); err != ErrSendPaneGone {
		t.Errorf("missing pane: err = %v", err)
	}
	if _, err := ws.SendToPane(id, "hi", false); err != ErrSendShell {
		t.Errorf("shell without allowShell: err = %v", err)
	}
	if p := ws.Pane(id); sendingNow(ws, p) {
		t.Error("a refused send left the in-flight flag set")
	}
}

func sendingNow(w *Workspace, p *Pane) bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return p.sending
}

// Text sent to a pane from the graph is Flockdeck's, not the user's. A wake
// rule asks whether the user has been typing; it must not read Flockdeck's own
// earlier line as a yes.
func TestSendToPaneDoesNotCountAsTheUserTyping(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws, err := New(Options{Root: root})
	if err != nil {
		t.Fatalf("new workspace: %v", err)
	}
	t.Cleanup(ws.Close)
	tab := ws.NewTab(session.KindShell, root, "shell")
	p := ws.Pane(tab.Tree.Panes()[0])
	if p == nil || !p.Alive() {
		t.Fatal("the shell pane did not start")
	}

	if _, err := ws.SendToPane(p.ID, "echo hi", true); err != nil {
		t.Fatalf("send: %v", err)
	}
	for deadline := time.Now().Add(10 * time.Second); sendingNow(ws, p); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the write never finished")
		}
	}
	if got := p.Sess.LastUserInput(); !got.IsZero() {
		t.Errorf("SendToPane moved the user's input time to %v", got)
	}
	if p.Sess.LastFlockdeckInput().IsZero() {
		t.Error("SendToPane did not record Flockdeck's own input time")
	}

	// The prompt bar is the user's, and does move it.
	typeInto(p.Sess, "echo bar", false)
	if p.Sess.LastUserInput().IsZero() {
		t.Error("typeInto from the prompt bar did not record the user typing")
	}
}
