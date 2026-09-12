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
		rule, from, dir := paneRoute(c, &tc.pane)
		if rule != tc.rule || from != tc.from || dir != tc.to {
			t.Errorf("%+v: %q %q %q, want %q %q %q", tc.pane, rule, from, dir, tc.rule, tc.from, tc.to)
		}
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
