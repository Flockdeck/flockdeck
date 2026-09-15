package server

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/route"
	"github.com/jmwri/flockdeck/internal/workspace"
)

func catalogRouting(policy string) *agent.Catalog {
	return agent.Merge(&agent.File{Routing: json.RawMessage(policy)})
}

// Routes are positional against the tasks, with nothing where a row is left
// alone, and there are none at all while routing is off.
func TestRouteRowsArePositionalAndOnlyWhenOn(t *testing.T) {
	tasks := []string{"add a health endpoint", "", "run the tests", "refactor the store"}
	if routes, mode, note := routeRows(agent.Merge(nil), "/p", "claude", "sonnet", tasks); routes != nil || mode != "" || note != "" {
		t.Errorf("with routing off: %v, %q, %q", routes, mode, note)
	}
	routes, mode, note := routeRows(catalogRouting(`{"mode": "suggest"}`), "/p", "claude", "sonnet", tasks)
	if mode != agent.RoutingSuggest || note != "" || len(routes) != len(tasks) {
		t.Fatalf("routes %v, mode %q, note %q", routes, mode, note)
	}
	if routes[0] != nil || routes[1] != nil {
		t.Errorf("rows no rule matched were routed: %+v, %+v", routes[0], routes[1])
	}
	if r := routes[2]; r == nil || r.Model != "haiku" || r.Rule != "run the tests" || r.Up || r.Reason == "" {
		t.Errorf("the test run was routed %+v", r)
	}
	if r := routes[3]; r == nil || r.Model != "opus" || !r.Up {
		t.Errorf("the refactor was routed %+v", r)
	}
	// Nothing to change is no list, not a list of nothing.
	if routes, _, _ := routeRows(catalogRouting(`{"mode": "suggest"}`), "/p", "claude", "sonnet", []string{"add a flag"}); routes != nil {
		t.Errorf("routes = %v", routes)
	}
}

// A project routed with Strategy "cost" gets a suggestion even for a row no
// rule recognises, unlike the default (balanced) strategy, which leaves such
// a row alone -- see TestRouteRowsArePositionalAndOnlyWhenOn.
func TestRouteRowsWithCostStrategyRouteUnmatchedRows(t *testing.T) {
	c := catalogRouting(`{"mode": "suggest", "strategy": "cost"}`)
	routes, _, _ := routeRows(c, "/p", "claude", "sonnet", []string{"add a health endpoint"})
	if len(routes) != 1 || routes[0] == nil || routes[0].Model != "haiku" || routes[0].Rule != "" || routes[0].Reason == "" {
		t.Fatalf("routes = %+v, want the unmatched row routed to haiku with no rule name", routes)
	}
}

// A project's own policy replaces every project's.
func TestRouteRowsFollowTheProjectsOwnPolicy(t *testing.T) {
	off := json.RawMessage(`{"mode": "off"}`)
	c := agent.Merge(&agent.File{Routing: json.RawMessage(`{"mode": "suggest"}`),
		Projects: map[string]agent.Defaults{"/work/quiet": {Routing: &off}}})
	if routes, _, _ := routeRows(c, "/work/quiet", "claude", "sonnet", []string{"run the tests"}); routes != nil {
		t.Errorf("a project with routing off was routed: %v", routes)
	}
	if routes, _, _ := routeRows(c, "/work/other", "claude", "sonnet", []string{"run the tests"}); routes == nil {
		t.Error("a project with no policy of its own was not routed by every project's")
	}
}

// Where routing can do nothing because of the run's model or its agent, the
// dialog is told why, so it can say what would let it.
func TestRouteRowsSayWhyNothingCanBeRouted(t *testing.T) {
	c := catalogRouting(`{"mode": "suggest"}`)
	routes, _, note := routeRows(c, "", "claude", "", []string{"run the tests"})
	if routes != nil || !strings.Contains(note, "Default") || !strings.Contains(note, "Choose a model") {
		t.Errorf("on Claude's Default: %v, note %q", routes, note)
	}
	if _, _, note := routeRows(c, "", "aider", "", []string{"run the tests"}); !strings.Contains(note, "tier") {
		t.Errorf("on an agent with no tiers: note %q", note)
	}
}

// For an API agent the reason says what the two models cost, with the day
// the prices were read; for a command-line agent it says nothing of money.
func TestARoutedAPIRowSaysWhatBothModelsCost(t *testing.T) {
	c := catalogRouting(`{"mode": "suggest"}`)
	routes, _, _ := routeRows(c, "", "anthropic", "claude-sonnet-5", []string{"run the tests"})
	if len(routes) != 1 || routes[0] == nil {
		t.Fatalf("routes = %v", routes)
	}
	for _, want := range []string{"Haiku 4.5", "Sonnet 5", "per M tokens", "prices checked"} {
		if !strings.Contains(routes[0].Reason, want) {
			t.Errorf("reason %q does not mention %q", routes[0].Reason, want)
		}
	}
	routes, _, _ = routeRows(c, "", "claude", "sonnet", []string{"run the tests"})
	if strings.Contains(routes[0].Reason, "$") {
		t.Errorf("a command-line agent's reason prices it: %q", routes[0].Reason)
	}
}

// The rule a row was routed by travels with the row and changes nothing else
// about it, and a window from before routing sends no rules at all.
func TestPlanJobsCarriesTheRuleAndNothingElseChanges(t *testing.T) {
	req := fanoutRequest{Tasks: []string{"run the tests", "", "add a flag"}, Agent: "claude", Model: "sonnet",
		TaskAgents: []string{"claude", "", ""}, TaskModels: []string{"haiku", "", ""}}
	old, _ := planJobs(req, "/repo")
	req.TaskRouted = []string{"run the tests"}
	routed, _ := planJobs(req, "/repo")
	if len(old) != 2 || len(routed) != 2 {
		t.Fatalf("jobs %d and %d, want 2", len(old), len(routed))
	}
	if routed[0].routed != "run the tests" || routed[1].routed != "" || old[0].routed != "" {
		t.Errorf("routed = %q, %q; before %q", routed[0].routed, routed[1].routed, old[0].routed)
	}
	for i := range old {
		a, b := *old[i], *routed[i]
		b.routed = ""
		if !reflect.DeepEqual(a, b) {
			t.Errorf("job %d is %+v with a rule and %+v without", i, b, a)
		}
	}
}

// The log records what routing chose and what became of it, and never the
// task: that is already in the user's own transcripts.
func TestTheFanOutLogHoldsTheChoicesAndNoTask(t *testing.T) {
	secret := "fix the payroll export for ACME"
	j := &fanoutJob{task: secret, agent: "claude", model: "haiku", routed: "rename or move"}
	plain := &fanoutJob{task: "another secret", agent: "claude", model: "sonnet"}
	req := fanoutRequest{Tasks: []string{secret}, Model: "sonnet",
		Overrides: []routeOverride{{Rule: "hard work", Agent: "claude", Routed: "opus", Chosen: "sonnet"}}}
	entries := fanoutRouteLog(req, "/repo", map[*fanoutJob]string{j: "pane-1", plain: "pane-2"})
	if len(entries) != 2 {
		t.Fatalf("entries = %+v", entries)
	}
	data, _ := json.Marshal(entries)
	if strings.Contains(string(data), "payroll") || strings.Contains(string(data), "secret") {
		t.Errorf("the log holds the task: %s", data)
	}
	var kept, over *route.LogEntry
	for i := range entries {
		switch entries[i].Outcome {
		case route.OutcomeKept:
			kept = &entries[i]
		case route.OutcomeOverridden + "sonnet":
			over = &entries[i]
		}
	}
	if kept == nil || kept.Pane != "pane-1" || kept.Baseline != "sonnet" || kept.Routed != "haiku" || kept.Rule != "rename or move" {
		t.Errorf("the kept row is logged as %+v", kept)
	}
	if over == nil || over.Routed != "opus" || over.Rule != "hard work" {
		t.Errorf("the overridden row is logged as %+v", over)
	}
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
	pane := workspace.Pane{Agent: "openai-compatible", Model: "qwen2.5-coder",
		Routed: "tests go local", RoutedFrom: "sonnet", RoutedFromAgent: "claude"}
	rule, from, fromAgent, dir := paneRoute(c, &pane)
	if rule != "tests go local" || from != "sonnet" || fromAgent != "claude" || dir != "" {
		t.Errorf("rule %q, from %q on %q, dir %q; want dir left unset -- openai-compatible's own model has no tier here",
			rule, from, fromAgent, dir)
	}
}

// ollamaCatalog is a catalog with a local, OpenAI-compatible agent that
// offers one tiered model, for the cross-agent tests below.
func ollamaCatalog(routing string) *agent.Catalog {
	return agent.Merge(&agent.File{
		Defaults: agent.Defaults{Agent: "claude", Model: "sonnet"},
		Routing:  json.RawMessage(routing),
		Agents: []json.RawMessage{json.RawMessage(`{"id": "openai-compatible",
			"api": {"baseURL": "http://127.0.0.1:11434/v1"},
			"models": [{"id": "qwen2.5-coder", "name": "Qwen 2.5 Coder", "tier": "small"}]}`)},
	})
}

// stubReachable makes endpointReachable answer ok without dialing anything,
// restoring the real dial and the reachability cache on cleanup.
func stubReachable(t *testing.T, ok bool) {
	t.Helper()
	orig := dialReachable
	dialReachable = func(string) bool { return ok }
	agent.Refresh()
	t.Cleanup(func() {
		dialReachable = orig
		reach.Lock()
		reach.seen = map[string]reachResult{}
		reach.Unlock()
	})
}

// A row moves to another agent only with the policy's own switch on, and the
// reason names the agent, the model, and that it costs nothing per token.
func TestRouteRowsMoveARowToAnotherAgent(t *testing.T) {
	stubReachable(t, true)
	policy := `{"mode": "suggest", "crossAgent": true, "rules": [
		{"name": "tests go local", "model": "qwen2.5-coder", "agent": "openai-compatible", "when": {"task": "tests"}}
	]}`
	routes, _, _ := routeRows(ollamaCatalog(policy), "", "claude", "sonnet", []string{"run the tests"})
	if len(routes) != 1 || routes[0] == nil {
		t.Fatalf("routes = %v", routes)
	}
	r := routes[0]
	if r.Agent != "openai-compatible" || r.Model != "qwen2.5-coder" || r.Rule != "tests go local" {
		t.Errorf("routed %+v", r)
	}
	if !strings.Contains(r.Reason, "Qwen 2.5 Coder") || !strings.Contains(r.Reason, "no per-token cost") {
		t.Errorf("reason %q does not name the model and its cost", r.Reason)
	}
}

// The same rule does nothing at all while the policy's crossAgent switch is
// off, which is what a file that says nothing about it already means.
func TestRouteRowsLeaveCrossAgentRulesAloneWithoutTheSwitch(t *testing.T) {
	stubReachable(t, true)
	policy := `{"mode": "suggest", "rules": [
		{"name": "tests go local", "model": "qwen2.5-coder", "agent": "openai-compatible", "when": {"task": "tests"}}
	]}`
	routes, _, _ := routeRows(ollamaCatalog(policy), "", "claude", "sonnet", []string{"run the tests"})
	if routes != nil {
		t.Errorf("routes = %v, want nothing routed with crossAgent off", routes)
	}
}

// otherAgentFor refuses an endpoint that does not answer, so a fan-out never
// cuts a worktree for a pane that could never connect.
func TestOtherAgentForRefusesAnUnreachableEndpoint(t *testing.T) {
	stubReachable(t, false)
	c := ollamaCatalog(`{}`)
	if _, ok := otherAgentFor(c, "")("openai-compatible"); ok {
		t.Error("an endpoint that answers false to every dial was reported usable")
	}
}

// endpointReachable remembers its answer, so a fan-out asking about a dozen
// rows dials an endpoint once rather than once per row.
func TestEndpointReachableCaches(t *testing.T) {
	calls := 0
	orig := dialReachable
	dialReachable = func(string) bool { calls++; return true }
	t.Cleanup(func() {
		dialReachable = orig
		reach.Lock()
		reach.seen = map[string]reachResult{}
		reach.Unlock()
	})
	endpointReachable("http://127.0.0.1:11434/v1")
	endpointReachable("http://127.0.0.1:11434/v1")
	if calls != 1 {
		t.Errorf("dialed %d times, want the second answer cached", calls)
	}
}

// A spawned helper is routed only in auto mode, since there is no dialog for
// suggest to fill in, and it records what it would have run without routing.
func TestRouteSpawnChoiceOnlyInAutoMode(t *testing.T) {
	suggest := ollamaCatalog(`{"mode": "suggest"}`)
	if d, _, _ := routeSpawnChoice(suggest, "", "run the tests"); d.Routed {
		t.Errorf("decided %+v, want nothing routed outside auto mode", d)
	}
	auto := ollamaCatalog(`{"mode": "auto"}`)
	d, baseAgent, baseModel := routeSpawnChoice(auto, "", "run the tests")
	if !d.Routed || d.Model != "haiku" || d.Agent != "" || baseAgent != "claude" || baseModel != "sonnet" {
		t.Errorf("decided %+v (base %q on %q)", d, baseModel, baseAgent)
	}
}

// A spawned helper can be routed to another agent too, under the same switch
// a fan-out row is.
func TestRouteSpawnChoiceCanCrossAgents(t *testing.T) {
	stubReachable(t, true)
	policy := `{"mode": "auto", "crossAgent": true, "rules": [
		{"name": "tests go local", "model": "qwen2.5-coder", "agent": "openai-compatible", "when": {"task": "tests"}}
	]}`
	d, baseAgent, baseModel := routeSpawnChoice(ollamaCatalog(policy), "", "run the tests")
	if !d.Routed || d.Agent != "openai-compatible" || d.Model != "qwen2.5-coder" || baseAgent != "claude" || baseModel != "sonnet" {
		t.Errorf("decided %+v (base %q on %q)", d, baseModel, baseAgent)
	}
}

// Settings lists the rules in words, and says why routing would do nothing
// for what a project starts.
func TestSettingsSeeTheRulesInForce(t *testing.T) {
	v := routingOf(catalogRouting(`{"mode": "suggest", "floor": "mid"}`), "")
	if v.Every.Mode != agent.RoutingSuggest || v.Every.Floor != agent.TierMid || !v.BuiltIn || len(v.Rules) == 0 {
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
	v := routingOf(catalogRouting(`{"mode": "suggest"}`), "")
	if v.Every.Strategy != agent.StrategyBalanced {
		t.Errorf("strategy = %q with nothing set, want balanced", v.Every.Strategy)
	}
	v = routingOf(catalogRouting(`{"mode": "auto", "strategy": "cost"}`), "")
	if v.Every.Strategy != agent.StrategyCost {
		t.Errorf("strategy = %q, want cost", v.Every.Strategy)
	}
}

// The rules Settings lists carry the log's own account of how often each was
// kept versus overridden, so a person editing rules by hand has the evidence
// routing.jsonl exists for.
func TestSettingsSeeTheRulesOverrideRate(t *testing.T) {
	dir := t.TempDir()
	old := routingLogDir
	routingLogDir = func() (string, error) { return dir, nil }
	defer func() { routingLogDir = old }()

	entries := []route.LogEntry{
		{Kind: route.KindFanout, Source: route.SourceRule, Rule: "rename or move", Baseline: "sonnet", Routed: "haiku", Outcome: route.OutcomeKept},
		{Kind: route.KindFanout, Source: route.SourceRule, Rule: "rename or move", Baseline: "sonnet", Routed: "haiku", Outcome: route.OutcomeOverridden + "opus"},
	}
	if err := route.AppendLog(dir, entries...); err != nil {
		t.Fatal(err)
	}
	v := routingOf(catalogRouting(`{"mode": "suggest"}`), "")
	found := false
	for _, r := range v.Rules {
		if r.Name != "rename or move" {
			continue
		}
		found = true
		if r.Kept != 1 || r.Overridden != 1 {
			t.Errorf("rename or move = %+v, want 1 kept and 1 overridden", r)
		}
	}
	if !found {
		t.Fatalf("rules = %+v, want rename or move among them", v.Rules)
	}
}
