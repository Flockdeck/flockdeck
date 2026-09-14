package workspace

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/store"
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

// A pane routed to another agent keeps that agent's name too, so a restore
// does not read RoutedFrom's model against the wrong agent's tiers.
func TestSaveRestoreKeepsTheAgentRoutingMovedFrom(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()

	ws := newTestWorkspace(t, root)
	ws.NewTabWith(Choice{Kind: session.KindClaude, Agent: "openai-compatible", Model: "qwen2.5-coder"}, root, "routed")
	id := ws.CurrentTab().Focus
	p := ws.Pane(id)
	p.Routed, p.RoutedFrom, p.RoutedFromAgent = "tests go local", "sonnet", "claude"
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
	if got.RoutedFrom != "sonnet" || got.RoutedFromAgent != "claude" {
		t.Errorf("restored routed from %q on %q, want sonnet on claude", got.RoutedFrom, got.RoutedFromAgent)
	}
}

// A same-agent route survives Spawn even when SpawnOptions names no agent at
// all: a fan-out row or a spawned helper left to the project's own default,
// which is the ordinary case, since routing changes the agent only when a
// rule crosses to another one. Requiring the resolved agent to equal
// SpawnOptions.Agent unconditionally dropped the badge here, because that
// field is "" for exactly this case.
func TestASameAgentRouteSurvivesTheProjectsDefaultAgent(t *testing.T) {
	isolateConfig(t)
	dir, err := store.Dir()
	if err != nil {
		t.Fatalf("state directory: %v", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"version": 1, "defaults": {"agent": "gocli"},
		"agents": [{"id": "gocli", "name": "Go", "exe": "go", "args": [{"value": "{{prompt}}"}]}]}`
	if err := os.WriteFile(filepath.Join(dir, agent.ConfigName), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	ws.NewTab(session.KindShell, root, "lead")
	id, err := ws.Spawn(ws.CurrentTab().Focus, SpawnOptions{
		Task: "reticulate the splines", Model: "small-model",
		Routed: "run the tests", RoutedFrom: "big-model",
	})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	p := ws.Pane(id)
	if p.Agent != "gocli" || p.Routed != "run the tests" || p.RoutedFrom != "big-model" {
		t.Errorf("pane = %+v, want the routed choice kept on the project's own default agent", p)
	}
}
