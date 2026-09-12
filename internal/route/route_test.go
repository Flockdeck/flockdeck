package route

import (
	"bufio"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/agent"
)

// claude is a Claude Code agent as routing sees it: a Default of no known size
// and one model in each tier.
func claude() agent.Spec {
	return agent.Spec{ID: "claude", Name: "Claude Code", Models: []agent.Model{
		{ID: "", Name: "Default"},
		{ID: "opus", Name: "Opus", Tier: agent.TierTop},
		{ID: "sonnet", Name: "Sonnet", Tier: agent.TierMid},
		{ID: "haiku", Name: "Haiku", Tier: agent.TierSmall},
		{ID: "opusplan", Name: "Opus plan"},
	}}
}

func on(rules ...agent.RoutingRule) agent.RoutingPolicy {
	if rules == nil {
		rules = []agent.RoutingRule{}
	}
	return agent.RoutingPolicy{Mode: agent.RoutingSuggest, Rules: rules}
}

func small(name, task string) agent.RoutingRule {
	return agent.RoutingRule{Name: name, Tier: agent.TierSmall, When: agent.RuleMatch{Task: task}}
}

func TestDecide(t *testing.T) {
	codex := agent.Spec{ID: "codex", Name: "Codex", Models: []agent.Model{
		{ID: "", Name: "Default"},
		{ID: "gpt-6-astra", Tier: agent.TierTop},
		{ID: "gpt-5.6-sol", Tier: agent.TierTop},
		{ID: "gpt-5.6-terra", Tier: agent.TierMid},
	}}
	tests := []struct {
		name   string
		policy agent.RoutingPolicy
		in     Input
		model  string // "" is left alone
		rule   string
		up     bool
	}{
		{name: "off routes nothing",
			policy: agent.RoutingPolicy{Mode: agent.RoutingOff},
			in:     Input{Task: "run the tests", Agent: claude(), Current: "sonnet"}},
		{name: "a policy that says nothing is off",
			policy: agent.RoutingPolicy{},
			in:     Input{Task: "run the tests", Agent: claude(), Current: "sonnet"}},
		{name: "a built-in rule moves mechanical work down",
			policy: agent.RoutingPolicy{Mode: agent.RoutingSuggest},
			in:     Input{Task: "run the tests", Agent: claude(), Current: "sonnet"},
			model:  "haiku", rule: "run the tests"},
		{name: "and hard work up",
			policy: agent.RoutingPolicy{Mode: agent.RoutingAuto},
			in:     Input{Task: "refactor the store", Agent: claude(), Current: "sonnet"},
			model:  "opus", rule: "hard work", up: true},
		{name: "no match changes nothing",
			policy: agent.RoutingPolicy{Mode: agent.RoutingSuggest},
			in:     Input{Task: "add a health endpoint", Agent: claude(), Current: "sonnet"}},
		{name: "the first match wins",
			policy: on(small("first", "tests"), agent.RoutingRule{Name: "second", Tier: agent.TierTop, When: agent.RuleMatch{Task: "tests"}}),
			in:     Input{Task: "fix the tests", Agent: claude(), Current: "sonnet"},
			model:  "haiku", rule: "first"},
		{name: "the first match wins even when it can do nothing",
			policy: on(small("first", "tests"), agent.RoutingRule{Name: "second", Tier: agent.TierTop, When: agent.RuleMatch{Task: "tests"}}),
			in:     Input{Task: "fix the tests", Agent: claude(), Current: "haiku"}},
		{name: "a task is matched whatever its case",
			policy: on(small("tests", "^run the tests")),
			in:     Input{Task: "Run The Tests", Agent: claude(), Current: "opus"},
			model:  "haiku", rule: "tests"},
		{name: "maxWords bounds a rule",
			policy: on(agent.RoutingRule{Name: "short", Tier: agent.TierSmall, When: agent.RuleMatch{Task: "rename", MaxWords: 3}}),
			in:     Input{Task: "rename it and much else", Agent: claude(), Current: "sonnet"}},
		{name: "minWords bounds a rule",
			policy: on(agent.RoutingRule{Name: "long", Tier: agent.TierTop, When: agent.RuleMatch{MinWords: 4}}),
			in:     Input{Task: "one two three four", Agent: claude(), Current: "sonnet"},
			model:  "opus", rule: "long", up: true},
		{name: "files are matched against the paths a task names",
			policy: on(agent.RoutingRule{Name: "sql", Tier: agent.TierTop, When: agent.RuleMatch{Files: []string{"**/migrations/**"}}}),
			in:     Input{Task: `edit db\migrations\0003.sql to add a column`, Agent: claude(), Current: "sonnet"},
			model:  "opus", rule: "sql", up: true},
		{name: "a files rule with no path named does not match",
			policy: on(agent.RoutingRule{Name: "sql", Tier: agent.TierTop, When: agent.RuleMatch{Files: []string{"**/*.sql"}}}),
			in:     Input{Task: "add a column", Agent: claude(), Current: "sonnet"}},
		{name: "kind narrows a rule",
			policy: on(agent.RoutingRule{Name: "turns", Tier: agent.TierSmall, When: agent.RuleMatch{Kind: KindTurn}}),
			in:     Input{Kind: KindFanout, Task: "anything", Agent: claude(), Current: "sonnet"}},
		{name: "agent narrows a rule",
			policy: on(agent.RoutingRule{Name: "codex only", Tier: agent.TierSmall, When: agent.RuleMatch{Agent: "codex"}}),
			in:     Input{Task: "anything", Agent: claude(), Current: "sonnet"}},
		{name: "a rule may name one agent's model",
			policy: on(agent.RoutingRule{Name: "opus please", Model: "opus", Agent: "claude", When: agent.RuleMatch{Task: "design"}}),
			in:     Input{Task: "design the cache", Agent: claude(), Current: "haiku"},
			model:  "opus", rule: "opus please", up: true},
		{name: "a rule for another agent's model says nothing here",
			policy: on(agent.RoutingRule{Name: "opus please", Model: "opus", Agent: "claude", When: agent.RuleMatch{Task: "design"}}),
			in:     Input{Task: "design the cache", Agent: codex, Current: "gpt-5.6-terra"}},
		{name: "the floor raises what a rule chose",
			policy: agent.RoutingPolicy{Mode: agent.RoutingSuggest, Floor: agent.TierMid, Rules: []agent.RoutingRule{small("tests", "tests")}},
			in:     Input{Task: "run the tests", Agent: claude(), Current: "opus"},
			model:  "sonnet", rule: "tests"},
		{name: "a floor nobody can read is taken as top",
			policy: agent.RoutingPolicy{Mode: agent.RoutingSuggest, Floor: "medium", Rules: []agent.RoutingRule{small("tests", "tests")}},
			in:     Input{Task: "run the tests", Agent: claude(), Current: "opus"}},
		{name: "never downgrade holds",
			policy: on(small("tests", "tests")),
			in:     Input{Task: "run the tests", Agent: claude(), Current: "sonnet", NoDowngrade: true}},
		{name: "never downgrade still lets work move up",
			policy: on(agent.RoutingRule{Name: "hard", Tier: agent.TierTop, When: agent.RuleMatch{Task: "race"}}),
			in:     Input{Task: "fix the race", Agent: claude(), Current: "sonnet", NoDowngrade: true},
			model:  "opus", rule: "hard", up: true},
		{name: "a model of no known size is never routed from",
			policy: on(small("tests", "tests")),
			in:     Input{Task: "run the tests", Agent: claude(), Current: ""}},
		{name: "nor up from",
			policy: on(agent.RoutingRule{Name: "hard", Tier: agent.TierTop, When: agent.RuleMatch{Task: "race"}}),
			in:     Input{Task: "fix the race", Agent: claude(), Current: "opusplan"}},
		{name: "nothing is routed to a model of no known size",
			policy: on(agent.RoutingRule{Name: "plan", Model: "opusplan", Agent: "claude", When: agent.RuleMatch{Task: "plan"}}),
			in:     Input{Task: "plan the release", Agent: claude(), Current: "sonnet"}},
		{name: "an agent with no model in the tier is left alone",
			policy: on(small("tests", "tests")),
			in:     Input{Task: "run the tests", Agent: codex, Current: "gpt-5.6-terra"}},
		{name: "of two models in a tier, the cheaper is chosen",
			policy: on(agent.RoutingRule{Name: "hard", Tier: agent.TierTop, When: agent.RuleMatch{Task: "race"}}),
			in:     Input{Task: "fix the race", Agent: codex, Current: "gpt-5.6-terra"},
			model:  "gpt-5.6-sol", rule: "hard", up: true},
		{name: "work already in the tier stays on its model",
			policy: on(agent.RoutingRule{Name: "hard", Tier: agent.TierTop, When: agent.RuleMatch{Task: "race"}}),
			in:     Input{Task: "fix the race", Agent: codex, Current: "gpt-6-astra"}},
		{name: "a rule whose pattern does not compile is passed over",
			policy: on(small("broken", "(unclosed"), small("works", "tests")),
			in:     Input{Task: "run the tests", Agent: claude(), Current: "sonnet"},
			model:  "haiku", rule: "works"},
		{name: "no rules is no routing",
			policy: on(),
			in:     Input{Task: "run the tests", Agent: claude(), Current: "sonnet"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := Decide(tc.policy, tc.in)
			if d.Model != tc.model || d.Routed != (tc.model != "") {
				t.Fatalf("decided %+v, want model %q", d, tc.model)
			}
			if d.Routed && (d.Rule != tc.rule || d.Up != tc.up || d.Source != SourceRule || d.Reason == "") {
				t.Errorf("decided %+v, want rule %q, up %v, and a reason", d, tc.rule, tc.up)
			}
		})
	}
}

// A rule that matched and could do nothing says why, so the dialog and the
// help can explain a row routing left alone.
func TestARuleThatChangesNothingSaysWhy(t *testing.T) {
	d := Decide(on(small("tests", "tests")), Input{Task: "run the tests", Agent: claude(), Current: ""})
	if d.Routed || !strings.Contains(d.Reason, "Default") || !strings.Contains(d.Reason, "no known size") {
		t.Errorf("decided %+v, want a reason naming the Default and why it is left alone", d)
	}
}

// The built-in rules against a fixed list of tasks, so a change to one of them
// shows in review as the answers it changes.
func TestTheBuiltinRulesOnExampleTasks(t *testing.T) {
	f, err := os.Open(filepath.Join("testdata", "tasks.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	policy := agent.RoutingPolicy{Mode: agent.RoutingSuggest}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		want, task, ok := strings.Cut(line, "\t")
		if !ok {
			t.Fatalf("%q has no tab", line)
		}
		d := Decide(policy, Input{Kind: KindFanout, Task: task, Agent: claude(), Current: "sonnet"})
		got := d.Tier
		if !d.Routed {
			got = "-"
		}
		if got != want {
			t.Errorf("%q is routed %s (%s), want %s", task, got, d.Reason, want)
		}
	}
}

func TestMatchGlob(t *testing.T) {
	tests := []struct {
		glob, name string
		want       bool
	}{
		{"**/*.sql", "db/0003.sql", true},
		{"**/*.sql", "0003.sql", true},
		{"*.sql", "db/deep/0003.sql", true},
		{"**/migrations/**", "migrations/0003.sql", true},
		{"**/migrations/**", "db/Migrations/x/0003.sql", true},
		{"**/migrations/**", "db/migration.go", false},
		{"internal/*/route.go", "internal/route/route.go", true},
		{"internal/*/route.go", "internal/a/b/route.go", false},
		{`db\migrations\**`, "db/migrations/0003.sql", true},
		{"./migrations/**", "migrations/0003.sql", true},
	}
	for _, tc := range tests {
		if got := MatchGlob(tc.glob, tc.name); got != tc.want {
			t.Errorf("MatchGlob(%q, %q) = %v, want %v", tc.glob, tc.name, got, tc.want)
		}
	}
}

func TestFilesInReadsThePathsATaskNames(t *testing.T) {
	got := filesIn(`fix internal\route\route.go and README.md, then ./cmd/x/main.go.`)
	want := []string{"internal/route/route.go", "README.md", "cmd/x/main.go"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("filesIn = %q, want %q", got, want)
	}
}

// Routing makes no request of any kind: deciding is pure, and the log is a
// file on this machine. Nothing here may so much as import the network.
func TestRoutingReachesNoNetwork(t *testing.T) {
	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			if p == "net" || strings.HasPrefix(p, "net/") || p == "os/exec" || strings.HasSuffix(p, "/chat") {
				t.Errorf("%s imports %s", name, p)
			}
		}
	}
}

// A rule for another agent's model does not match this agent's work, so the
// rules after it are still tried.
func TestARuleForAnotherAgentsModelLetsTheNextRuleDecide(t *testing.T) {
	policy := on(
		agent.RoutingRule{Name: "codex mini", Model: "gpt-5.4-mini", Agent: "codex", When: agent.RuleMatch{Task: "tests"}},
		small("tests", "tests"),
	)
	d := Decide(policy, Input{Task: "run the tests", Agent: claude(), Current: "sonnet"})
	if d.Model != "haiku" || d.Rule != "tests" {
		t.Errorf("decided %+v, want haiku by rule \"tests\"", d)
	}
}
