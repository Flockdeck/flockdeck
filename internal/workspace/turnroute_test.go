package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/route"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/store"
)

// turnTestWorkspace is a workspace whose catalog is entirely the caller's: an
// API-runner agent needs no CLI on PATH, and `go`, standing in for it exactly
// as TestAConversationReopensWithTheAgentThatRecordedIt uses it, is real
// enough to start a process and exit, which is all routeFirstTurn's restart
// needs to prove it happened.
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
	ws.catalog = &agent.Catalog{Specs: agent.Builtins(), Routing: routing}
	ws.catalogMu.Unlock()

	return ws, root
}

// giveTranscript makes a pane look like it has already had a turn: the same
// file transcriptExists reads, written the way TestResumeAsksTheAgentsOwnReader
// writes one for the chat client's own reader.
func giveTranscript(t *testing.T, paneID string) {
	t.Helper()
	dir, err := store.Dir()
	if err != nil {
		t.Fatalf("state directory: %v", err)
	}
	chats := filepath.Join(dir, "chats")
	if err := os.MkdirAll(chats, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(chats, paneID+".jsonl")
	if err := os.WriteFile(path, []byte(`{"type":"user","ts":1,"text":"hello"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestRouteFirstTurnRoutesAFreshPaneOntoARoutedModel covers the design this
// package settled on: a pane opened by hand knows nothing to route on until
// its first prompt exists, so resolveChoice cannot route it the way a
// fan-out row or a spawned helper is -- and routing catches up the moment the
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
// routeSpawnChoice answers to and for the same reason: a prompt about to be
// typed straight into a live terminal has no dialog left to show a suggested
// model in, so "suggest" -- which exists to fill one in -- has nothing to do
// here and routes nothing.
func TestRouteFirstTurnOnlyRoutesInAutoMode(t *testing.T) {
	ws, root := turnTestWorkspace(t, agent.RoutingPolicy{
		Mode: agent.RoutingSuggest,
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
	giveTranscript(t, p.ID)
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
