package workspace

import (
	"encoding/json"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/route"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/store"
	"github.com/jmwri/flockdeck/internal/structured"
	"github.com/jmwri/flockdeck/internal/team"
)

// goroutinesBelow waits for the number of goroutines to come down to limit, and
// fails with the count if it does not. A process and its pipes leave a handful
// of goroutines behind for a moment after they end.
func goroutinesBelow(t *testing.T, limit int, what string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		n := runtime.NumGoroutine()
		if n <= limit {
			return
		}
		if time.Now().After(deadline) {
			buf := make([]byte, 1<<16)
			buf = buf[:runtime.Stack(buf, true)]
			t.Fatalf("%s: %d goroutines, want at most %d\n%s", what, n, limit, buf)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// A prompt sent to a structured pane goes over the protocol whatever turn
// routing would have chosen. Routing a turn restarts the pane on another model,
// which for a structured pane would start a second agent beside the first.
func TestATurnIsNeverRoutedIntoAStructuredPane(t *testing.T) {
	ws, lead, _ := structuredCrew(t)
	id := spawnStructured(t, ws, lead, "plain")
	statusWithin(t, ws, id, session.StatusIdle)

	// An agent with model tiers, and a rule that matches the prompt.
	ws.catalogMu.Lock()
	c := *ws.catalog
	c.Specs = append([]agent.Spec(nil), c.Specs...)
	for i := range c.Specs {
		if c.Specs[i].ID == "opencode-acp" {
			c.Specs[i].Models = []agent.Model{
				{ID: "small-model", Name: "Small", Tier: "small"},
				{ID: "top-model", Name: "Top", Tier: "top"},
			}
		}
	}
	c.Routing = agent.RoutingPolicy{Mode: agent.RoutingAuto, Rules: []agent.RoutingRule{
		{Name: "hard turn work", Tier: agent.TierTop, When: agent.RuleMatch{Kind: route.KindTurn, Task: "race"}},
	}}
	ws.catalog = &c
	ws.catalogMu.Unlock()

	p := ws.Pane(id)
	p.Model = "small-model"
	first := ws.StructuredRuntime(id)
	model := p.Model
	goroutines := runtime.NumGoroutine()

	res, err := ws.SendToPane(id, "fix the race in the cart", false)
	if err != nil {
		t.Fatalf("SendToPane: %v", err)
	}
	if res.Routed {
		t.Error("the turn was routed")
	}
	if got := ws.StructuredRuntime(id); got != first {
		t.Fatal("the pane was started again")
	}
	if p.Model != model || p.Routed != "" {
		t.Errorf("model %q (was %q), routed %q", p.Model, model, p.Routed)
	}
	if sn := first.Snapshot(); sn.Phase == structured.PhaseClosed {
		t.Error("the first agent was closed")
	}
	statusWithin(t, ws, id, session.StatusIdle)
	goroutinesBelow(t, goroutines+3, "after the turn")
}

// The spawn gate is AgentSpecByID: with structured panes off it refuses the
// agent itself, before anything tries to start it.
func TestAgentSpecByIDRefusesAStructuredAgentWhileTheyAreOff(t *testing.T) {
	isolateConfig(t)
	teamAgents(t)
	onPath(t, "opencode")
	ws := newTestWorkspace(t, t.TempDir())
	if _, err := ws.AgentSpecByID("opencode-acp"); err == nil || !strings.Contains(err.Error(), "turned off") {
		t.Errorf("with the preference off: %v", err)
	}
	if err := store.SavePrefs(store.Prefs{StructuredPanes: true}); err != nil {
		t.Fatal(err)
	}
	ws.ReloadAgents()
	if _, err := ws.AgentSpecByID("opencode-acp"); err != nil {
		t.Errorf("with the preference on: %v", err)
	}
	// Turning it off again applies when the catalog is read again.
	if err := store.SavePrefs(store.Prefs{}); err != nil {
		t.Fatal(err)
	}
	ws.ReloadAgents()
	if _, err := ws.AgentSpecByID("opencode-acp"); err == nil {
		t.Error("after turning the preference off and reloading")
	}
}

// A broadcast is typed text, and a structured pane has no keys: it is left out
// although it is a member of the broadcast, and the terminal agents beside it
// still get the text. A helper is spawned into a tab of its own, so the structured pane joins the
// terminal helper's tab and the test selects it; with the pane outside the
// current tab the filter would never be reached.
func TestABroadcastLeavesOutStructuredPanes(t *testing.T) {
	ws, lead, _ := structuredCrew(t)
	terminal, err := ws.Spawn(lead, SpawnOptions{Task: "t", Role: "worker", Agent: "sleeper", Kind: session.KindClaude, SpawnedByAgent: true})
	if err != nil {
		t.Fatal(err)
	}
	tab := ws.TabIDOf(terminal)
	id, err := ws.Spawn(lead, SpawnOptions{Task: "plain", Role: "worker", Agent: "opencode-acp", Kind: session.KindClaude, SpawnedByAgent: true, Tab: tab})
	if err != nil {
		t.Fatal(err)
	}
	statusWithin(t, ws, id, session.StatusIdle)
	if tab == "" || ws.TabIDOf(id) != tab {
		t.Fatalf("the helpers are in tabs %q and %q", tab, ws.TabIDOf(id))
	}
	ws.SelectTab(tab)
	ws.ToggleBroadcast()
	if !ws.InBroadcast(id) || !ws.InBroadcast(terminal) {
		t.Fatalf("in the broadcast: structured %v, terminal %v", ws.InBroadcast(id), ws.InBroadcast(terminal))
	}
	for _, focus := range []string{id, terminal} {
		var got []string
		for _, p := range ws.BroadcastTargetsFor(focus) {
			got = append(got, p.ID)
		}
		if len(got) != 1 || got[0] != terminal {
			t.Errorf("focused on %s, the targets are %v, want only %s", focus, got, terminal)
		}
	}
}

// Closing a pane, restarting it and quitting end its agent and leave no
// goroutine of the runtime behind.
func TestStructuredPanesLeaveNoGoroutinesBehind(t *testing.T) {
	isolateConfig(t)
	teamAgents(t)
	if err := store.SavePrefs(store.Prefs{StructuredPanes: true}); err != nil {
		t.Fatal(err)
	}
	onPath(t, "opencode")
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	useFakeAgent(ws)
	ws.NewTabWith(Choice{Kind: session.KindClaude, Agent: "sleeper"}, root, "")
	lead := ws.FocusedPane().ID
	ws.Pane(lead).Role = "manager"
	settled := func() int {
		time.Sleep(300 * time.Millisecond)
		return runtime.NumGoroutine()
	}
	base := settled()

	closed := spawnStructured(t, ws, lead, "hold-perm")
	statusWithin(t, ws, closed, session.StatusWaiting)
	if !ws.ClosePaneByID(closed) {
		t.Fatal("not closed")
	}
	goroutinesBelow(t, base+3, "after close")

	id := spawnStructured(t, ws, lead, "hold-perm")
	statusWithin(t, ws, id, session.StatusWaiting)
	running := settled()
	for i := 0; i < 3; i++ {
		if !ws.RestartPaneByID(id) {
			t.Fatal("restart refused")
		}
		statusWithin(t, ws, id, session.StatusIdle)
	}
	goroutinesBelow(t, running+3, "after three restarts")

	ws.Close()
	goroutinesBelow(t, base-1, "after quit")
}

// A layout written before panes had a transport has none in it. Its terminal
// pane comes back as a terminal, and a pane of a structured agent comes back as
// the agent's runner says.
func TestAStateFileWithNoTransportRestores(t *testing.T) {
	ws, lead, root := structuredCrew(t)
	id := spawnStructured(t, ws, lead, "plain")
	statusWithin(t, ws, id, session.StatusIdle)
	if err := ws.SaveAll(); err != nil {
		t.Fatal(err)
	}
	ws.Close()

	st, err := store.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	var strip func(n *store.Node)
	seen := 0
	strip = func(n *store.Node) {
		if n == nil {
			return
		}
		if n.Pane != nil {
			if n.Pane.Transport != "" {
				seen++
			}
			n.Pane.Transport = ""
		}
		for _, c := range n.Children {
			strip(c)
		}
	}
	for _, tab := range st.Tabs {
		strip(tab.Root)
	}
	if seen != 1 {
		t.Fatalf("%d panes were saved structured, want 1", seen)
	}
	if raw, _ := json.Marshal(st); strings.Contains(string(raw), "transport") {
		t.Fatal("the transport is still in the state")
	}
	if err := store.Save(root, st); err != nil {
		t.Fatal(err)
	}

	restored := newTestWorkspace(t, root)
	useFakeAgent(restored)
	if ok, err := restored.Restore(); err != nil || !ok {
		t.Fatalf("restore: %v, %v", ok, err)
	}
	if lp := restored.Pane(lead); lp == nil || lp.Structured() || lp.Err != nil {
		t.Errorf("the terminal pane came back as %+v", lp)
	}
	p := restored.Pane(id)
	if p == nil || p.Err != nil {
		t.Fatalf("the structured agent's pane: %+v", p)
	}
	statusWithin(t, restored, id, session.StatusIdle)
}

func TestSetupTextKeepsTheEndAndFitsTheLine(t *testing.T) {
	long := strings.Repeat("x", 400)
	cases := []structured.Setup{
		{Kind: structured.SetupNode, Message: "Node 20 or later is needed", Login: "Install it from nodejs.org, then restart Flockdeck."},
		{Kind: structured.SetupNode, Message: long, Login: "Install it from nodejs.org, then restart Flockdeck."},
		{Kind: structured.SetupLogin, Message: long, Login: "Run claude login.", Methods: []structured.AuthMethod{{Name: "Claude account"}, {Name: "API key"}}},
		{Kind: structured.SetupInstall, Message: long, Login: "npm i -g agent"},
	}
	for _, c := range cases {
		got := setupText("claude", &c)
		if n := len([]rune(got)); n == 0 || n > team.MaxWaitText {
			t.Errorf("%s: %d characters", c.Kind, n)
		}
		end := "Then start the pane again."
		if c.Kind == structured.SetupNode {
			end = "then restart Flockdeck."
		}
		if !strings.HasSuffix(got, end) || strings.Contains(got, "start the pane again") != (c.Kind != structured.SetupNode) {
			t.Errorf("%s: %q lost the end", c.Kind, got)
		}
	}
	if got := setupText("claude", &cases[2]); !strings.Contains(got, "does not sign in for you") {
		t.Errorf("login text = %q", got)
	}
}
