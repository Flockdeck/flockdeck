package workspace

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/store"
)

// TestFanOutReadsAChatPanesOwnTranscript covers a fan-out from a pane running
// Flockdeck's own chat client. Its plan is in the transcript the chat client
// keeps, but only Claude Code's store was ever asked, so the plan was read off
// the redrawn screen instead — or not found at all.
func TestFanOutReadsAChatPanesOwnTranscript(t *testing.T) {
	isolateConfig(t)
	base, err := store.Dir()
	if err != nil {
		t.Fatalf("state directory: %v", err)
	}
	chats := filepath.Join(base, "chats")
	if err := os.MkdirAll(chats, 0o700); err != nil {
		t.Fatal(err)
	}
	const id = "33333333-3333-3333-3333-333333333333"
	transcript := `{"type":"user","ts":1,"text":"plan the refactor"}` + "\n" +
		`{"type":"assistant","ts":2,"text":"Here is the plan:\n\n1. Split the router into its own package\n2. Add a timeout to every outbound request\n"}` + "\n"
	if err := os.WriteFile(filepath.Join(chats, id+".jsonl"), []byte(transcript), 0o600); err != nil {
		t.Fatal(err)
	}

	w := &Workspace{panes: map[string]*Pane{
		id: {ID: id, Kind: session.KindClaude, Agent: "anthropic"},
	}}
	if _, ok := w.specFor("", "anthropic"); !ok {
		t.Skip("the catalog has no built-in anthropic agent")
	}
	tasks, fromTranscript := w.PlanSourceFor(id).Tasks()
	if !fromTranscript {
		t.Fatalf("tasks = %q came from the screen, want the chat transcript", tasks)
	}
	if len(tasks) != 2 || tasks[0] != "Split the router into its own package" {
		t.Errorf("tasks = %q, want the two steps of the plan", tasks)
	}
}
