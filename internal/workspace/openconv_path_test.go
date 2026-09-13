package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jmwri/flockdeck/internal/store"
)

// TestAConversationIDThatIsAPathIsRefused covers the id a window resumes a
// conversation by, which becomes the pane's id and so a file name: the chat
// client's transcript, the pane's settings file, removed when the pane goes.
// An id with a way out of a directory in it was taken as it came, so a
// transcript planted beside the chats folder was opened as one of them, under
// a pane id that named somewhere else again.
func TestAConversationIDThatIsAPathIsRefused(t *testing.T) {
	isolateConfig(t)
	goExe, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go is not on PATH")
	}
	base, err := store.Dir()
	if err != nil {
		t.Fatalf("state directory: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(base, "chats"), 0o700); err != nil {
		t.Fatal(err)
	}
	// Where chats/../escaped.jsonl lands: a transcript, as far as a check
	// that the conversation is stored can tell.
	if err := os.WriteFile(filepath.Join(base, "escaped.jsonl"),
		[]byte(`{"type":"user","ts":1,"text":"hello"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	// As TestAConversationReopensWithTheAgentThatRecordedIt: an API pane runs
	// `<HookBinary> chat`, and `go chat` exits at once.
	ws, err := New(Options{Root: root, HookBinary: goExe})
	if err != nil {
		t.Fatalf("new workspace: %v", err)
	}
	t.Cleanup(ws.Close)

	for _, id := range []string{"../escaped", `..\escaped`, "sub/escaped", `sub\escaped`, ".."} {
		if err := ws.OpenConversationAs(id, root, "escaped", "openai"); err == nil {
			t.Errorf("OpenConversationAs(%q) opened it; want it refused", id)
		}
		if ws.Pane(id) != nil {
			t.Errorf("a pane was opened under the id %q", id)
		}
	}
}
