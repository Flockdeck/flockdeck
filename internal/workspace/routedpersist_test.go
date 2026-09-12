package workspace

import (
	"testing"

	"github.com/jmwri/flockdeck/internal/session"
)

// A pane started on a model routing chose comes back saying so, and why:
// otherwise its header would show a model the user never picked with nothing
// to explain it.
func TestSaveRestoreKeepsWhatRoutingChose(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()

	ws := newTestWorkspace(t, root)
	ws.NewTabWith(Choice{Kind: session.KindClaude, Agent: "codex", Model: "gpt-5.6-luna"}, root, "routed")
	id := ws.CurrentTab().Focus
	p := ws.Pane(id)
	p.Routed, p.RoutedFrom = "run the tests", "gpt-5.6-terra"
	if err := ws.SaveAll(); err != nil {
		t.Fatalf("save: %v", err)
	}
	ws.Close()

	again := newTestWorkspace(t, root)
	if ok, err := again.Restore(); err != nil || !ok {
		t.Fatalf("restore: ok=%v err=%v", ok, err)
	}
	got := again.Pane(id)
	if got == nil {
		t.Fatal("the pane was not restored")
	}
	if got.Model != "gpt-5.6-luna" || got.Routed != "run the tests" || got.RoutedFrom != "gpt-5.6-terra" {
		t.Errorf("restored %q, routed by %q from %q", got.Model, got.Routed, got.RoutedFrom)
	}
}
