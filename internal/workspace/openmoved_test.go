package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jmwri/flockdeck/internal/hooks"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/store"
)

// TestAConversationLeftBehindReopensInAPaneOfItsOwn covers reopening, from
// history, the conversation a pane was in before its agent moved on with
// /clear. The pane that started it is still open, in another conversation now,
// so focusing it would show the wrong one, and a second pane cannot take its
// id: the conversation gets a pane of its own under an id of its own.
func TestAConversationLeftBehindReopensInAPaneOfItsOwn(t *testing.T) {
	isolateConfig(t)
	goExe, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go is not on PATH")
	}
	root := t.TempDir()
	ws, err := New(Options{Root: root, HookBinary: goExe})
	if err != nil {
		t.Fatalf("new workspace: %v", err)
	}
	t.Cleanup(ws.Close)

	ws.NewTab(session.KindShell, root, "lead")
	first := ws.FocusedPane()
	// The conversation it started in is stored — as a chat, which needs no
	// Claude to resume — and its agent has moved on to another.
	base, err := store.Dir()
	if err != nil {
		t.Fatalf("state directory: %v", err)
	}
	chats := filepath.Join(base, "chats")
	if err := os.MkdirAll(chats, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(chats, first.ID+".jsonl"),
		[]byte(`{"type":"user","ts":1,"text":"hello"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ws.handleHook(hooks.Event{SessionID: first.ID, Event: "SessionStart", Source: "clear",
		Conversation: "66666666-6666-6666-6666-666666666666"})

	if err := ws.OpenConversation(first.ID, root, "before the clear"); err != nil {
		t.Fatalf("open: %v", err)
	}
	opened := ws.FocusedPane()
	if opened == nil || opened.ID == first.ID {
		t.Fatalf("the conversation left behind was not given a pane of its own (focused %v)", opened)
	}
	if got := ws.ConversationOf(opened.ID); got != first.ID {
		t.Errorf("the new pane is in conversation %q, want the one left behind, %q", got, first.ID)
	}
	if got := ws.ConversationOf(first.ID); got == first.ID {
		t.Error("the pane that moved on is back in the conversation it left")
	}
}
