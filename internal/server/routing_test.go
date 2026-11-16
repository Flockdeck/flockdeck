package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/store"
	"github.com/jmwri/flockdeck/internal/workspace"
)

func catalogRouting(policy string) *agent.Catalog {
	return agent.Merge(&agent.File{Routing: json.RawMessage(policy)})
}

func TestPaneRouteSaysWhichWayTheModelMoved(t *testing.T) {
	c := agent.Merge(nil)
	tests := []struct {
		pane           workspace.Pane
		rule, from, to string
	}{
		{workspace.Pane{Agent: "claude", Model: "haiku", Routed: "tests", RoutedFrom: "sonnet"}, "tests", "sonnet", "down"},
		{workspace.Pane{Agent: "claude", Model: "opus", Routed: "hard", RoutedFrom: "sonnet"}, "hard", "sonnet", "up"},
		{workspace.Pane{Agent: "claude", Model: "opus", Routed: "hard", RoutedFrom: "gone"}, "hard", "gone", ""},
		{workspace.Pane{Agent: "claude", Model: "opus"}, "", "", ""},
	}
	for _, tc := range tests {
		rule, from, _, dir := paneRoute(c, &tc.pane)
		if rule != tc.rule || from != tc.from || dir != tc.to {
			t.Errorf("%+v: %q %q %q, want %q %q %q", tc.pane, rule, from, dir, tc.rule, tc.from, tc.to)
		}
	}
}

// A pane routed to another agent has its RoutedFrom model's tier read off
// that agent's own catalog entry, not the pane's current one.
func TestPaneRouteReadsTheBaselineModelsTierFromItsOwnAgent(t *testing.T) {
	c := agent.Merge(nil)
	pane := workspace.Pane{Agent: "local", Model: "qwen2.5-coder",
		Routed: "tests go local", RoutedFrom: "sonnet", RoutedFromAgent: "claude"}
	rule, from, fromAgent, dir := paneRoute(c, &pane)
	if rule != "tests go local" || from != "sonnet" || fromAgent != "claude" || dir != "" {
		t.Errorf("rule %q, from %q on %q, dir %q; want dir left unset -- local's own model is not Claude's",
			rule, from, fromAgent, dir)
	}
}

// ollamaCatalog is a catalog with a second agent, "local", that offers one
// tiered model, for the cross-agent tests below. Its program is the go tool,
// which is on PATH wherever these tests run.
func ollamaCatalog(routing string) *agent.Catalog {
	return agent.Merge(&agent.File{
		Defaults: agent.Defaults{Agent: "claude", Model: "sonnet"},
		Routing:  json.RawMessage(routing),
		Agents: []json.RawMessage{json.RawMessage(`{"id": "local", "exe": "go",
			"models": [{"id": "qwen2.5-coder", "name": "Qwen 2.5 Coder", "tier": "small"}]}`)},
	})
}

// otherAgentFor refuses an agent whose program is not installed, so a spawn
// never cuts a worktree for a pane that could never start.
func TestOtherAgentForRefusesAnAgentThatIsNotInstalled(t *testing.T) {
	c := agent.Merge(&agent.File{Agents: []json.RawMessage{json.RawMessage(
		`{"id": "ghost", "exe": "flockdeck-no-such-program"}`)}})
	agent.Refresh()
	if _, ok := otherAgentFor(c, "")("ghost"); ok {
		t.Error("an agent whose program is not on PATH was reported usable")
	}
}

// A spawned helper is routed only in auto mode, and it records what it would
// have run without routing. A saved mode of suggest, which an earlier build
// had for Fan out's dialog, routes nothing.
func TestRouteSpawnChoiceOnlyInAutoMode(t *testing.T) {
	for _, mode := range []string{"off", "suggest"} {
		if d, _, _ := routeSpawnChoice(ollamaCatalog(`{"mode": "`+mode+`"}`), "", "run the tests"); d.Routed {
			t.Errorf("mode %q: decided %+v, want nothing routed outside auto mode", mode, d)
		}
	}
	auto := ollamaCatalog(`{"mode": "auto"}`)
	d, baseAgent, baseModel := routeSpawnChoice(auto, "", "run the tests")
	if !d.Routed || d.Model != "haiku" || d.Agent != "" || baseAgent != "claude" || baseModel != "sonnet" {
		t.Errorf("decided %+v (base %q on %q)", d, baseModel, baseAgent)
	}
}

// The same rule does nothing at all while the policy's crossAgent switch is
// off, which is what a file that says nothing about it already means.
func TestRouteSpawnChoiceLeavesCrossAgentRulesAloneWithoutTheSwitch(t *testing.T) {
	policy := `{"mode": "auto", "rules": [
		{"name": "tests go local", "model": "qwen2.5-coder", "agent": "local", "when": {"task": "tests"}}
	]}`
	if d, _, _ := routeSpawnChoice(ollamaCatalog(policy), "", "run the tests"); d.Routed {
		t.Errorf("decided %+v, want nothing routed with crossAgent off", d)
	}
}

// A spawned helper can be routed to another agent, under the policy's
// crossAgent switch.
func TestRouteSpawnChoiceCanCrossAgents(t *testing.T) {
	policy := `{"mode": "auto", "crossAgent": true, "rules": [
		{"name": "tests go local", "model": "qwen2.5-coder", "agent": "local", "when": {"task": "tests"}}
	]}`
	d, baseAgent, baseModel := routeSpawnChoice(ollamaCatalog(policy), "", "run the tests")
	if !d.Routed || d.Agent != "local" || d.Model != "qwen2.5-coder" || baseAgent != "claude" || baseModel != "sonnet" {
		t.Errorf("decided %+v (base %q on %q)", d, baseModel, baseAgent)
	}
	if !strings.Contains(d.Reason, "Qwen 2.5 Coder") {
		t.Errorf("reason %q does not name the model", d.Reason)
	}
}

// Where routing can do nothing because of the model or the agent, Settings
// says why, so it can say what would let it.
func TestRouteNoteSaysWhyNothingCanBeRouted(t *testing.T) {
	c := agent.Merge(nil)
	claude, _ := c.Find("claude")
	if note := routeNote(claude, ""); !strings.Contains(note, "Default") || !strings.Contains(note, "Choose a model") {
		t.Errorf("on Claude's Default: note %q", note)
	}
	if note := routeNote(claude, "sonnet"); note != "" {
		t.Errorf("on a tiered model: note %q, want none", note)
	}
	aider, _ := c.Find("aider")
	if note := routeNote(aider, ""); !strings.Contains(note, "tier") {
		t.Errorf("on an agent with no tiers: note %q", note)
	}
}

// Settings lists the rules in words, and says why routing would do nothing
// for what a project starts.
func TestSettingsSeeTheRulesInForce(t *testing.T) {
	v := routingOf(catalogRouting(`{"mode": "auto", "floor": "mid"}`), "")
	if v.Every.Mode != agent.RoutingAuto || v.Every.Floor != agent.TierMid || !v.BuiltIn || len(v.Rules) == 0 {
		t.Fatalf("routing view %+v", v)
	}
	if !strings.Contains(v.Note, "Default") {
		t.Errorf("note = %q, want it to say Claude's Default is left alone", v.Note)
	}
	found := false
	for _, r := range v.Rules {
		found = found || r.Name == "rename or move" && strings.Contains(r.When, "at most 25 words") && r.Choice == "small"
	}
	if !found {
		t.Errorf("rules = %+v", v.Rules)
	}
	if v := routingOf(agent.Merge(nil), ""); v.Every.Mode != agent.RoutingOff || v.Note != "" {
		t.Errorf("with nothing said: %+v", v)
	}
}

// Settings shows Strategy alongside Mode and Floor, defaulting to balanced
// the same way an unset Mode shows as off.
func TestSettingsSeeTheStrategy(t *testing.T) {
	v := routingOf(catalogRouting(`{"mode": "auto"}`), "")
	if v.Every.Strategy != agent.StrategyBalanced {
		t.Errorf("strategy = %q with nothing set, want balanced", v.Every.Strategy)
	}
	v = routingOf(catalogRouting(`{"mode": "auto", "strategy": "cost"}`), "")
	if v.Every.Strategy != agent.StrategyCost {
		t.Errorf("strategy = %q, want cost", v.Every.Strategy)
	}
}

// A routing.jsonl an older build left is the user's: the command that used to
// empty it only says so now, and settings no longer read it.
func TestClearRoutingLogLeavesTheFileAlone(t *testing.T) {
	dir, err := store.Dir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "routing.jsonl")
	const line = `{"kind":"fanout","rule":"rename or move","outcome":"kept"}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	c := &controlClient{out: make(chan []byte, 8)}
	(&Server{}).clearRoutingLog(c)
	if len(c.out) == 0 {
		t.Error("the command said nothing")
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != line {
		t.Errorf("routing.jsonl = %q, %v; want it untouched", got, err)
	}
}
