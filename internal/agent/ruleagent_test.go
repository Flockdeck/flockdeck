package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

// A rule naming an agent the catalog does not have never matches, since ids
// are matched exactly; the notice says so, rather than routing seeming to
// ignore it. A rule naming an agent that is here says nothing.
func TestARoutingRuleNamingNoAgentHereIsNamedInTheNotice(t *testing.T) {
	c := Merge(&File{Routing: json.RawMessage(`{"mode": "suggest", "rules": [
		{"name": "design work", "model": "opus", "agent": "Claude", "when": {"task": "design"}},
		{"name": "codex tests", "tier": "small", "when": {"agent": "codx"}},
		{"name": "fine", "model": "opus", "agent": "claude", "when": {"task": "plan"}}]}`)})
	for _, want := range []string{`"design work"`, `"Claude"`, `"codex tests"`, `"codx"`} {
		if !strings.Contains(c.Notice, want) {
			t.Errorf("notice %q does not name %s", c.Notice, want)
		}
	}
	if strings.Contains(c.Notice, `"fine"`) {
		t.Errorf("notice %q names a rule whose agent is here", c.Notice)
	}
	if len(c.Routing.Rules) != 3 {
		t.Errorf("kept %d rules, want all three", len(c.Routing.Rules))
	}
}
