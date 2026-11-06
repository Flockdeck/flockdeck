package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/remote"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/store"
)

// writeAgents writes the user's agents.json into the isolated state directory.
func writeAgents(t *testing.T, body map[string]any) {
	t.Helper()
	dir, err := store.Dir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, agent.ConfigName), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

// A pane opened with no agent chosen is the structured one by default, and the
// picker's terminal row (the id claude) stays terminal.
func TestANewPaneDefaultsToStructuredAndTheTerminalRowStaysTerminal(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	if got, _ := ws.resolveChoice(root, "", ""); got != "claude-acp" {
		t.Errorf("a pane that asked for nothing runs %q, want claude-acp", got)
	}
	if got, _ := ws.resolveChoice(root, "claude", ""); got != "claude" {
		t.Errorf("the terminal row became %q", got)
	}
	if got, _ := ws.resolveChoice(root, "claude-acp", ""); got != "claude-acp" {
		t.Errorf("the structured row became %q", got)
	}
}

// Terminal chosen, or remote devices reaching this machine with nothing chosen,
// keeps terminal as the default.
func TestTerminalIsTheDefaultWhenChosenOrWhereRemoteAccessIsOn(t *testing.T) {
	isolateConfig(t)
	if err := store.SavePrefs(store.Prefs{Panes: store.PanesTerminal}); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	if got, _ := ws.resolveChoice(root, "", ""); got != "claude" {
		t.Errorf("terminal chosen: a new pane runs %q", got)
	}
	if got, _ := ws.resolveSpawnChoice(root, "opencode", "", false); got != "opencode" {
		t.Errorf("terminal chosen: a spawn of opencode runs %q", got)
	}

	isolateConfig(t)
	cfg := remote.Config{Relay: "https://relay.example", HostID: "h1", AccountID: "a1", Token: "fdh_x"}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	ws = newTestWorkspace(t, root)
	if got, _ := ws.resolveChoice(root, "", ""); got != "claude" {
		t.Errorf("remote access on: a new pane runs %q", got)
	}
	if got, _ := ws.resolveSpawnChoice(root, "claude", "", false); got != "claude" {
		t.Errorf("remote access on: a spawn of claude runs %q", got)
	}
}

// A spawn naming a bare id gets the structured entry; -terminal gets the
// terminal one; an agent with no structured side is left alone; Codex and
// Gemini stay terminal until their structured entries are listed.
func TestASpawnResolvesABareIDAndTerminalOptsOut(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	for _, tc := range []struct {
		agent    string
		terminal bool
		want     string
	}{
		{"claude", false, "claude-acp"},
		{"opencode", false, "opencode-acp"},
		{"claude", true, "claude"},
		{"opencode", true, "opencode"},
		{"claude-acp", true, "claude"},
		{"claude-acp", false, "claude-acp"},
		{"codex", false, "codex"},
		{"gemini", false, "gemini"},
		{"aider", false, "aider"},
		{"aider", true, "aider"},
		{"", false, "claude-acp"},
		{"", true, "claude"},
	} {
		if got, _ := ws.resolveSpawnChoice(root, tc.agent, "", tc.terminal); got != tc.want {
			t.Errorf("spawn %q (terminal %v) runs %q, want %q", tc.agent, tc.terminal, got, tc.want)
		}
	}
}

// An agents.json entry over claude runs as the user wrote it, for a new pane
// and for a spawn, and -terminal on it is the same entry.
func TestAUserEntryOverClaudeKeepsWorking(t *testing.T) {
	isolateConfig(t)
	writeAgents(t, map[string]any{
		"version": 1,
		"agents":  []map[string]any{{"id": "claude", "exe": os.Args[0]}},
	})
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	if got, _ := ws.resolveChoice(root, "", ""); got != "claude" {
		t.Errorf("a new pane runs %q with the user's own claude", got)
	}
	if got, _ := ws.resolveSpawnChoice(root, "claude", "", false); got != "claude" {
		t.Errorf("a spawn runs %q with the user's own claude", got)
	}
	if got, _ := ws.resolveSpawnChoice(root, "claude", "", true); got != "claude" {
		t.Errorf("-terminal runs %q", got)
	}
	if spec, ok := ws.agents().Find("claude"); !ok || spec.Exe == "claude" {
		t.Errorf("the user's exe was lost: %+v", spec)
	}
}

// A pane saved with no agent was a terminal pane. When the default has since
// become a structured entry it still comes back as a terminal, and a layout that
// says it was structured still gets the structured agent.
func TestAnEmptyAgentPaneSavedAsTerminalRestoresAsTerminal(t *testing.T) {
	for _, tc := range []struct {
		name       string
		saved      Transport
		structured bool
	}{
		{"terminal", TransportTerminal, false},
		{"older layout with no transport", "", false},
		{"structured", TransportStructured, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateConfig(t)
			writeAgents(t, map[string]any{
				"version":  1,
				"defaults": map[string]any{"agent": "claude-acp"},
			})
			onPath(t, "claude")
			root := t.TempDir()
			ws := newTestWorkspace(t, root)
			useFakeAgent(ws)
			p := &Pane{
				ID: "restored-" + tc.name, Kind: session.KindClaude, Cwd: root, Root: root,
				Name: "restored", savedTransport: tc.saved,
			}
			ws.mu.Lock()
			ws.panes[p.ID] = p
			ws.mu.Unlock()
			ws.startPane(p, true)
			if got := p.Structured(); got != tc.structured {
				t.Errorf("structured = %v, want %v (err %v)", got, tc.structured, p.Err)
			}
		})
	}
}

// A spawn of a bare agent and one asking for terminal sit in the same tab, the
// structured one a structured pane and the other a terminal.
func TestATabCanHoldAStructuredAndATerminalHelper(t *testing.T) {
	ws, lead, _ := structuredCrew(t)
	var kinds [2]bool
	for i, terminal := range []bool{false, true} {
		id, err := ws.Spawn(lead, SpawnOptions{
			Task: "mixed", Role: "worker", Agent: "opencode", Terminal: terminal,
			Split: true, Kind: session.KindClaude, SpawnedByAgent: true,
		})
		if err != nil {
			t.Fatalf("spawn (terminal %v): %v", terminal, err)
		}
		kinds[i] = ws.Pane(id).Structured()
	}
	if !kinds[0] || kinds[1] {
		t.Errorf("structured flags %v, want [true false]", kinds)
	}
}
