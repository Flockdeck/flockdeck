package workspace

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/store"
)

// startedPane adds a pane running agentID, as a restored layout would, and
// starts it.
func startedPane(t *testing.T, ws *Workspace, root, agentID string, saved Transport) *Pane {
	t.Helper()
	p := &Pane{ID: "wayback-" + agentID, Kind: session.KindClaude, Cwd: root, Root: root, Name: "wayback", Agent: agentID, savedTransport: saved}
	ws.mu.Lock()
	ws.panes[p.ID] = p
	ws.mu.Unlock()
	ws.startPane(p, true)
	return p
}

// noAgentsOnPath leaves nothing on PATH, so a terminal agent reads as not
// installed and no process is started by a test that restarts a pane as one.
func noAgentsOnPath(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", t.TempDir())
	agent.Refresh()
}

// A structured pane that cannot start because new panes are set to terminal
// says how to go on without naming prefs.json, and can be restarted as the
// terminal agent.
func TestAStructuredPaneRefusedByThePreferenceCanRestartAsTerminal(t *testing.T) {
	isolateConfig(t)
	if err := store.SavePrefs(store.Prefs{Panes: store.PanesTerminal}); err != nil {
		t.Fatal(err)
	}
	noAgentsOnPath(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	p := startedPane(t, ws, root, "claude-acp", TransportStructured)
	if p.Err == nil {
		t.Fatal("a structured pane started with structured panes off")
	}
	if strings.Contains(p.Err.Error(), "prefs.json") || strings.Contains(p.Err.Error(), "structuredPanes") {
		t.Errorf("the message sends people to the file: %q", p.Err)
	}
	if !strings.Contains(p.Err.Error(), "Settings") {
		t.Errorf("the message does not name Settings: %q", p.Err)
	}
	if got := ws.TerminalWayBack(p); got != "claude" {
		t.Fatalf("way back = %q, want claude", got)
	}
	if err := ws.RestartPaneAsTerminal(p.ID); err != nil {
		t.Fatalf("restart as terminal: %v", err)
	}
	if p.Agent != "claude" || p.savedTransport != TransportTerminal || p.Structured() {
		t.Errorf("pane is %q, saved %q, structured %v", p.Agent, p.savedTransport, p.Structured())
	}
	// claude is not on PATH here, so the pane reads as a terminal agent that is
	// unavailable, which has no way back of its own.
	if !errors.Is(p.Err, agent.ErrUnavailable) {
		t.Errorf("err = %v, want the terminal agent's unavailable", p.Err)
	}
	if got := ws.TerminalWayBack(p); got != "" {
		t.Errorf("a terminal pane is offered %q", got)
	}
	if err := ws.RestartPaneAsTerminal(p.ID); !errors.Is(err, ErrNoTerminalWayBack) {
		t.Errorf("a second restart as terminal: %v", err)
	}
}

// Every reason a structured pane can fail to start gets the way back where
// the agent has a terminal sibling: the directory gone, and the agent not
// there, as well as the preference.
func TestTheWayBackIsOfferedForAStructuredPaneThatFailedToStart(t *testing.T) {
	isolateConfig(t)
	noAgentsOnPath(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	missing := t.TempDir() + "/gone"
	p := &Pane{ID: "wayback-dir", Kind: session.KindClaude, Cwd: missing, Root: root, Name: "dir", Agent: "opencode-acp", savedTransport: TransportStructured}
	ws.mu.Lock()
	ws.panes[p.ID] = p
	ws.mu.Unlock()
	ws.startPane(p, true)
	if p.Err == nil {
		t.Fatal("started in a directory that is not there")
	}
	if got := ws.TerminalWayBack(p); got != "opencode" {
		t.Errorf("way back = %q, want opencode", got)
	}
}

// An agent with no terminal sibling, a terminal pane, a shell, and a pane that
// is not found get no way back, and the command is refused.
func TestNoWayBackWhereThereIsNoTerminalSibling(t *testing.T) {
	isolateConfig(t)
	noAgentsOnPath(t)
	writeAgents(t, map[string]any{
		"version": 1,
		"agents":  []map[string]any{{"id": "mine-acp", "name": "Mine", "runner": "acp", "acp": map[string]any{"adapter": "opencode"}}},
	})
	root := t.TempDir()
	ws := newTestWorkspace(t, root)

	if spec, ok := ws.agents().Find("mine-acp"); !ok || spec.Runner != agent.RunnerACP || spec.Terminal != "" {
		t.Fatalf("the test agent is not a structured entry with no sibling: %+v, %v", spec, ok)
	}
	own := startedPane(t, ws, root, "mine-acp", TransportStructured)
	waitStopped(t, own)
	term := startedPane(t, ws, root, "claude", TransportTerminal)
	gone := startedPane(t, ws, root, "no-such-agent", "")
	for name, p := range map[string]*Pane{"no sibling": own, "terminal agent": term, "unknown agent": gone} {
		if got := ws.TerminalWayBack(p); got != "" {
			t.Errorf("%s: offered %q", name, got)
		}
		if err := ws.RestartPaneAsTerminal(p.ID); !errors.Is(err, ErrNoTerminalWayBack) {
			t.Errorf("%s: restart as terminal: %v", name, err)
		}
	}
	if err := ws.RestartPaneAsTerminal("nobody"); !errors.Is(err, ErrNoTerminalWayBack) {
		t.Errorf("a pane that is not there: %v", err)
	}
	if got := ws.TerminalWayBack(nil); got != "" {
		t.Errorf("nil pane: %q", got)
	}
	if own.Agent != "mine-acp" {
		t.Errorf("the refused pane's agent became %q", own.Agent)
	}
}

// waitStopped waits for a structured pane's agent to have stopped or to need
// setting up, which is how an adapter that is not installed reads.
func waitStopped(t *testing.T, p *Pane) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		if st, _ := p.Status(); st == session.StatusExited || st == session.StatusBlocked {
			return
		}
		if time.Now().After(deadline) {
			st, _ := p.Status()
			t.Fatalf("pane status is %v, want exited or blocked (err %v)", st, p.Err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A structured agent whose adapter is not installed starts a runtime that ends
// at once. The pane reads as stopped, and can be restarted as the terminal
// agent, which closes that runtime.
func TestAStructuredAgentThatEndedCanRestartAsTerminal(t *testing.T) {
	isolateConfig(t)
	noAgentsOnPath(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	p := startedPane(t, ws, root, "claude-acp", TransportStructured)
	waitStopped(t, p)
	if got := ws.TerminalWayBack(p); got != "claude" {
		t.Fatalf("way back = %q, want claude", got)
	}
	if err := ws.RestartPaneAsTerminal(p.ID); err != nil {
		t.Fatal(err)
	}
	if p.Structured() || p.Agent != "claude" || p.savedTransport != TransportTerminal {
		t.Errorf("pane is %q, saved %q, structured %v", p.Agent, p.savedTransport, p.Structured())
	}
}
