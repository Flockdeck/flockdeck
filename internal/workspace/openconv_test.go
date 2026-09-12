package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/store"
)

// TestAConversationReopensWithTheAgentThatRecordedIt covers resuming a stored
// conversation that Flockdeck's own chat client kept. Only Claude Code's store
// was asked whether it existed, and the pane was pinned to Claude, so a chat
// conversation was refused as no longer stored — and one that got past that
// would have started Claude on top of it.
func TestAConversationReopensWithTheAgentThatRecordedIt(t *testing.T) {
	isolateConfig(t)
	goExe, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go is not on PATH")
	}
	base, err := store.Dir()
	if err != nil {
		t.Fatalf("state directory: %v", err)
	}
	chats := filepath.Join(base, "chats")
	if err := os.MkdirAll(chats, 0o700); err != nil {
		t.Fatal(err)
	}
	const id = "44444444-4444-4444-4444-444444444444"
	if err := os.WriteFile(filepath.Join(chats, id+".jsonl"),
		[]byte(`{"type":"user","ts":1,"text":"hello"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	// An API pane runs this binary as `<HookBinary> chat`. Pointed at `go`, it
	// prints that it has no such command and exits, rather than running the
	// test binary a second time inside a terminal.
	ws, err := New(Options{Root: root, HookBinary: goExe})
	if err != nil {
		t.Fatalf("new workspace: %v", err)
	}
	t.Cleanup(ws.Close)

	if err := ws.OpenConversation(id, root, "chat"); err != nil {
		t.Fatalf("open: %v", err)
	}
	p := ws.Pane(id)
	if p == nil {
		t.Fatal("no pane was opened on the conversation")
	}
	if spec, ok := ws.specFor(p.Agent); !ok || spec.Runner != agent.RunnerAPI {
		t.Errorf("the chat conversation reopened as %q, want the chat client", p.Agent)
	}
}
