package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/route"
	"github.com/jmwri/flockdeck/internal/session"
)

// turnTestWorkspace is a workspace whose catalog is the caller's routing policy
// and one agent, "openai", that is `go version`: real enough to start a process
// and exit, which is all routeFirstTurn's restart needs to prove it happened,
// and with the three tiers routing chooses between.
func turnTestWorkspace(t *testing.T, routing agent.RoutingPolicy) (*Workspace, string) {
	t.Helper()
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

	ws.catalogMu.Lock()
	ws.catalog = &agent.Catalog{Specs: append(agent.Builtins(), goTierAgent(goExe)), Routing: routing}
	ws.catalogMu.Unlock()

	return ws, root
}

// goTierAgent is an agent that runs `go version` and offers one model of each
// tier. Its transcripts are read by the reader for the program it runs, so
// giveTranscript points it at a copy of go named claude.
func goTierAgent(exe string) agent.Spec {
	return agent.Spec{
		ID: "openai", Name: "Go stand-in", Runner: agent.RunnerCLI, Exe: exe,
		Args: []agent.Arg{agent.Lit("version")},
		Models: []agent.Model{
			{ID: "gpt-5.6-sol", Name: "Sol", Tier: agent.TierTop},
			{ID: "gpt-5.6-terra", Name: "Terra", Tier: agent.TierMid},
			{ID: "gpt-5.6-luna", Name: "Luna", Tier: agent.TierSmall},
		},
		DefaultModel: "gpt-5.6-terra",
		Caps:         agent.Caps{Resume: true, Transcript: true},
	}
}

// giveTranscript makes a pane look like it has already had a turn: it writes
// the Claude Code transcript transcriptExists reads, and renames the stand-in
// agent's program to claude so that the Claude reader is the one asked.
func giveTranscript(t *testing.T, ws *Workspace, paneID string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	dir := filepath.Join(home, "projects", "p")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, paneID+".jsonl"),
		[]byte(`{"type":"user","message":{"role":"user","content":"hello"}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	goExe, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(goExe)
	if err != nil {
		t.Fatal(err)
	}
	named := filepath.Join(t.TempDir(), "claude"+filepath.Ext(goExe))
	if err := os.WriteFile(named, data, 0o755); err != nil {
		t.Fatal(err)
	}
	ws.catalogMu.Lock()
	for i := range ws.catalog.Specs {
		if ws.catalog.Specs[i].ID == "openai" {
			ws.catalog.Specs[i].Exe = named
		}
	}
	ws.catalogMu.Unlock()
}

// TestRouteFirstTurnRoutesAFreshPaneOntoARoutedModel covers the design this
// package settled on: a pane opened by hand knows nothing to route on until
// its first prompt exists, so resolveChoice cannot route it the way a
// spawned helper is -- and routing catches up the moment the
// prompt is finally known, restarting the pane onto the routed model with
// that prompt as its opening argument rather than typing it into the process
// that was going to run without it.
func TestRouteFirstTurnRoutesAFreshPaneOntoARoutedModel(t *testing.T) {
	ws, root := turnTestWorkspace(t, agent.RoutingPolicy{
		Mode: agent.RoutingAuto,
		Rules: []agent.RoutingRule{
			{Name: "hard turn work", Tier: agent.TierTop, When: agent.RuleMatch{Kind: route.KindTurn, Task: "race"}},
		},
	})

	ws.NewTabWith(Choice{Kind: session.KindClaude, Agent: "openai", Model: "gpt-5.6-luna"}, root, "")
	p := ws.FocusedPane()
	if p == nil || p.Sess == nil {
		t.Fatalf("the pane did not start: %v", p.Err)
	}
	firstLaunch := p.launch

	ws.SendPrompt("fix the race in the session store", true)

	if p.Model == "gpt-5.6-luna" {
		t.Errorf("model = %q, want it routed off the model the pane opened on", p.Model)
	}
	if p.Routed != "hard turn work" {
		t.Errorf("Routed = %q, want the rule's name", p.Routed)
	}
	if p.RoutedFrom != "gpt-5.6-luna" {
		t.Errorf("RoutedFrom = %q, want the model the pane opened on", p.RoutedFrom)
	}
	if p.RoutedFromAgent != "" {
		t.Errorf("RoutedFromAgent = %q, turn routing never crosses agents", p.RoutedFromAgent)
	}
	if p.Task != "fix the race in the session store" {
		t.Errorf("Task = %q, want the prompt that was sent", p.Task)
	}
	if p.launch == firstLaunch {
		t.Error("the pane's process was never restarted onto the routed model")
	}
}

// TestRouteFirstTurnDoesNothingWithoutAMatch covers the ordinary case: most
// prompts match no rule, and a pane that was never routed must never be
// restarted for nothing -- that would lose whatever a live terminal was
// already showing to save no rule anything.
func TestRouteFirstTurnDoesNothingWithoutAMatch(t *testing.T) {
	ws, root := turnTestWorkspace(t, agent.RoutingPolicy{
		Mode: agent.RoutingAuto,
		Rules: []agent.RoutingRule{
			{Name: "hard turn work", Tier: agent.TierTop, When: agent.RuleMatch{Kind: route.KindTurn, Task: "race"}},
		},
	})

	ws.NewTabWith(Choice{Kind: session.KindClaude, Agent: "openai", Model: "gpt-5.6-luna"}, root, "")
	p := ws.FocusedPane()
	if p == nil || p.Sess == nil {
		t.Fatalf("the pane did not start: %v", p.Err)
	}
	firstLaunch := p.launch

	ws.SendPrompt("write the changelog entry", true)

	if p.Model != "gpt-5.6-luna" {
		t.Errorf("model = %q, want it left alone with nothing to route", p.Model)
	}
	if p.Routed != "" {
		t.Errorf("Routed = %q, want none", p.Routed)
	}
	if p.launch != firstLaunch {
		t.Error("the pane's process was restarted though nothing routed")
	}
}

// TestRouteFirstTurnOnlyRoutesInAutoMode covers the same gate
// routeSpawnChoice answers to. "suggest", which an earlier build used to fill
// in Fan out's dialog and which an old agents.json may still hold, routes
// nothing here.
func TestRouteFirstTurnOnlyRoutesInAutoMode(t *testing.T) {
	ws, root := turnTestWorkspace(t, agent.RoutingPolicy{
		Mode: "suggest",
		Rules: []agent.RoutingRule{
			{Name: "hard turn work", Tier: agent.TierTop, When: agent.RuleMatch{Kind: route.KindTurn}},
		},
	})

	ws.NewTabWith(Choice{Kind: session.KindClaude, Agent: "openai", Model: "gpt-5.6-luna"}, root, "")
	p := ws.FocusedPane()
	if p == nil || p.Sess == nil {
		t.Fatalf("the pane did not start: %v", p.Err)
	}

	ws.SendPrompt("fix the race in the session store", true)

	if p.Model != "gpt-5.6-luna" || p.Routed != "" {
		t.Errorf("model = %q, routed = %q; suggest mode must route nothing here", p.Model, p.Routed)
	}
}

// TestRouteFirstTurnLeavesAConversationAlone covers the eligibility gate: once
// a pane has a stored transcript it has already had its first turn, whether
// this run started it or a restore brought it back, and routing must never
// restart a conversation already underway out from under it.
func TestRouteFirstTurnLeavesAConversationAlone(t *testing.T) {
	ws, root := turnTestWorkspace(t, agent.RoutingPolicy{
		Mode: agent.RoutingAuto,
		Rules: []agent.RoutingRule{
			{Name: "hard turn work", Tier: agent.TierTop, When: agent.RuleMatch{Kind: route.KindTurn}},
		},
	})

	ws.NewTabWith(Choice{Kind: session.KindClaude, Agent: "openai", Model: "gpt-5.6-luna"}, root, "")
	p := ws.FocusedPane()
	if p == nil || p.Sess == nil {
		t.Fatalf("the pane did not start: %v", p.Err)
	}
	giveTranscript(t, ws, p.ID)
	firstLaunch := p.launch

	ws.SendPrompt("fix the race in the session store", true)

	if p.Model != "gpt-5.6-luna" || p.Routed != "" {
		t.Errorf("model = %q, routed = %q; a pane with a transcript must never be routed", p.Model, p.Routed)
	}
	if p.launch != firstLaunch {
		t.Error("the pane's process was restarted though it already had a conversation")
	}
}

// TestRouteFirstTurnLeavesAShellAlone covers the other half of IsAgent's
// distinction: a shell pane runs no agent and so has no model for routing to
// choose between, whatever the policy says.
func TestRouteFirstTurnLeavesAShellAlone(t *testing.T) {
	ws, root := turnTestWorkspace(t, agent.RoutingPolicy{
		Mode: agent.RoutingAuto,
		Rules: []agent.RoutingRule{
			{Name: "hard turn work", Tier: agent.TierTop, When: agent.RuleMatch{Kind: route.KindTurn}},
		},
	})

	ws.NewTab(session.KindShell, root, "shell")
	p := ws.FocusedPane()
	if p == nil || p.Sess == nil {
		t.Fatalf("the shell did not start: %v", p.Err)
	}
	firstLaunch := p.launch

	ws.SendPrompt("fix the race in the session store", true)

	if p.launch != firstLaunch {
		t.Error("a shell pane was restarted as though it were routable")
	}
}
