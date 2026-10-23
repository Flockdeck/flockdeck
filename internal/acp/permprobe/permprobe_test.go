package permprobe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func r(tool, pattern, action string) Rule {
	return Rule{Permission: tool, Pattern: pattern, Action: action}
}

// listing is the output of the agent list: each agent, then its rules as an
// indented JSON array, the way opencode prints them.
func listing(agents ...Agent) []byte {
	var b strings.Builder
	for _, a := range agents {
		b.WriteString(a.Name + " (" + a.Mode + ")\n")
		j, _ := json.MarshalIndent(a.Rules, "  ", "  ")
		b.WriteString("  " + string(j) + "\n")
	}
	return []byte(b.String())
}

func build(rules ...Rule) Agent { return Agent{Name: "build", Mode: "primary", Rules: rules} }

func TestUnaskedFollowsTheLastMatchingRule(t *testing.T) {
	for name, tc := range map[string]struct {
		rules []Rule
		want  bool
	}{
		"the default allows":                  {[]Rule{r("*", "*", "allow")}, true},
		"asked after the default":             {[]Rule{r("*", "*", "allow"), r("bash", "*", "ask")}, false},
		"denied":                              {[]Rule{r("*", "*", "allow"), r("bash", "*", "deny")}, false},
		"an allow after an ask":               {[]Rule{r("bash", "*", "ask"), r("bash", "*", "allow")}, true},
		"an ask after an allow":               {[]Rule{r("bash", "*", "allow"), r("bash", "*", "ask")}, false},
		"allowed for some commands":           {[]Rule{r("bash", "*", "ask"), r("bash", "git *", "allow")}, true},
		"allowed before the ask":              {[]Rule{r("bash", "git *", "allow"), r("bash", "*", "ask")}, false},
		"a deny for some commands":            {[]Rule{r("bash", "*", "ask"), r("bash", "rm *", "deny")}, false},
		"no rule at all":                      {[]Rule{r("read", "*", "allow")}, true},
		"another tool's allow":                {[]Rule{r("bash", "*", "ask"), r("edit", "*", "allow")}, false},
		"an action not known":                 {[]Rule{r("bash", "*", "permit")}, true},
		"every tool denied":                   {[]Rule{r("*", "*", "deny")}, false},
		"every tool asks":                     {[]Rule{r("*", "*", "ask")}, false},
		"every tool denied, bash allowed":     {[]Rule{r("*", "*", "deny"), r("bash", "*", "allow")}, true},
		"bash asked, then every tool allowed": {[]Rule{r("bash", "*", "ask"), r("*", "*", "allow")}, true},
	} {
		if got := Unasked(tc.rules, "bash"); got != tc.want {
			t.Errorf("%s: %v, want %v", name, got, tc.want)
		}
	}
	if !AnyUnasked([]Rule{r("bash", "*", "ask"), r("edit", "*", "allow")}, "bash", "edit") {
		t.Error("an allowed edit was not found")
	}
}

func TestInlineKeepsDenialsAndTurnsAllowsIntoAsks(t *testing.T) {
	rules := []Rule{
		r("*", "*", "allow"),
		r("bash", "git *", "allow"), r("bash", "rm *", "deny"), r("bash", "*", "allow"),
		r("edit", "*", "deny"),
	}
	got, ok := Inline(rules, "bash", "edit")
	if !ok {
		t.Fatal("not built")
	}
	// The user's own list ended with an allow for every command, which shadowed
	// the deny before it; the ask takes its place and the order is the same.
	want := `{"permission":{"bash":{"git *":"ask","rm *":"deny","*":"ask"},"edit":"deny"}}`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestInlineOfTheDefaultsAsks(t *testing.T) {
	got, ok := Inline([]Rule{r("*", "*", "allow"), r("doom_loop", "*", "ask")}, "bash", "edit")
	if !ok || got != `{"permission":{"bash":"ask","edit":"ask"}}` {
		t.Errorf("got %q, %v", got, ok)
	}
}

func TestInlineKeepsADenyThatComesAfterTheAsk(t *testing.T) {
	got, _ := Inline([]Rule{r("bash", "*", "ask"), r("bash", "rm *", "deny"), r("bash", "git *", "allow")}, "bash")
	if got != `{"permission":{"bash":{"*":"ask","rm *":"deny","git *":"ask"}}}` {
		t.Errorf("got %s", got)
	}
}

func TestInlineAddsTheRuleForEveryCommandWhenThereIsNone(t *testing.T) {
	got, _ := Inline([]Rule{r("bash", "rm *", "deny")}, "bash")
	if got != `{"permission":{"bash":{"*":"ask","rm *":"deny"}}}` {
		t.Errorf("got %s", got)
	}
}

func TestInlineQuotesPatterns(t *testing.T) {
	pattern := `say "hi" \ *`
	got, ok := Inline([]Rule{r("bash", "*", "ask"), r("bash", pattern, "deny")}, "bash")
	var v struct {
		Permission map[string]map[string]string `json:"permission"`
	}
	if !ok || json.Unmarshal([]byte(got), &v) != nil || v.Permission["bash"][pattern] != "deny" {
		t.Errorf("got %s, %v", got, ok)
	}
}

func TestInlineRefusesWhatItCannotKeep(t *testing.T) {
	var many []Rule
	for i := 0; i < maxRules+1; i++ {
		many = append(many, r("bash", strings.Repeat("a", i+1), "deny"))
	}
	for name, rules := range map[string][]Rule{
		"an action not known": {r("bash", "*", "permit")},
		"an empty pattern":    {r("bash", "", "deny")},
		"too many rules":      many,
		"a long list":         {r("bash", "*", "ask"), r("bash", strings.Repeat("x", MaxInline), "deny")},
	} {
		if got, ok := Inline(rules, "bash"); ok {
			t.Errorf("%s: built %.80s", name, got)
		}
	}
}

func TestParseAgentsReadsOpencodesListing(t *testing.T) {
	out := append([]byte("loading config...\n"), listing(
		build(r("bash", "*", "ask")),
		Agent{Name: "general", Mode: "subagent", Rules: []Rule{r("bash", "*", "allow"), r("edit", `C:\x\*`, "deny")}},
	)...)
	agents, err := ParseAgents(out)
	if err != nil || len(agents) != 2 || agents[0].Name != "build" || agents[1].Mode != "subagent" ||
		agents[1].Rules[1].Pattern != `C:\x\*` {
		t.Fatalf("%+v, %v", agents, err)
	}
	// Windows line ends.
	crlf := strings.ReplaceAll(string(listing(build(r("bash", "*", "ask")))), "\n", "\r\n")
	if agents, err := ParseAgents([]byte(crlf)); err != nil || len(agents) != 1 {
		t.Errorf("crlf: %+v, %v", agents, err)
	}
	for name, bad := range map[string]string{
		"nothing":            "nothing",
		"rules not an array": "build (primary)\n  {\"a\":1}\n",
		"cut off":            "build (primary)\n  [{\"permission\":\"bash\"",
		"no rules":           "build (primary)\n",
	} {
		if agents, err := ParseAgents([]byte(bad)); err == nil {
			t.Errorf("%s: no error, %+v", name, agents)
		}
	}
	var many strings.Builder
	for i := 0; i <= maxAgents; i++ {
		many.WriteString("a" + strings.Repeat("x", i) + " (primary)\n  []\n")
	}
	if _, err := ParseAgents([]byte(many.String())); err == nil {
		t.Error("too many agents accepted")
	}
}

// fake is a program whose list depends on the setting it was started with.
type fake struct {
	calls  [][]string
	agents func(setting string) []Agent // the list with a setting; "" for none
	// failAt makes the nth run fail; garbled makes every run print junk.
	failAt  int
	garbled bool
}

func (f *fake) run(_ context.Context, _ string, _ []string, _ string, env []string) ([]byte, error) {
	f.calls = append(f.calls, env)
	if f.failAt == len(f.calls) {
		return nil, errors.New("exit status 1")
	}
	if f.garbled {
		return []byte("nope"), nil
	}
	setting := ""
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "SETTING="); ok {
			setting = v
		}
	}
	return listing(f.agents(setting)...), nil
}

// settingRules is the rules a setting gives a tool, as opencode lists them after
// the defaults; granular maps come out sorted, which is enough for these fixtures.
func settingRules(setting string) []Rule {
	var v struct {
		Permission map[string]json.RawMessage `json:"permission"`
	}
	_ = json.Unmarshal([]byte(setting), &v)
	out := []Rule{r("*", "*", "allow")}
	for tool, raw := range v.Permission {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			out = append(out, r(tool, "*", s))
			continue
		}
		var m map[string]string
		_ = json.Unmarshal(raw, &m)
		for p, a := range m {
			out = append(out, r(tool, p, a))
		}
	}
	return out
}

// userRules are what the user's and the project's own settings give every agent
// when no launch setting is set.
var userRules = []Rule{r("*", "*", "allow"), r("bash", "rm *", "deny"), r("bash", "*", "allow"), r("edit", "*", "allow")}

// world is a project: with no setting every agent has userRules; with one the
// agent has the setting's rules, then whatever its own settings add.
func world(extra map[string][]Rule) func(string) []Agent {
	return func(setting string) []Agent {
		var out []Agent
		for _, name := range []string{"build", "general", "x"} {
			rules := userRules
			if setting != "" {
				rules = settingRules(setting)
			}
			out = append(out, Agent{Name: name, Mode: "primary", Rules: append(append([]Rule(nil), rules...), extra[name]...)})
		}
		return out
	}
}

const plain = `{"permission":{"bash":"ask","edit":"ask"}}`

func check(f *fake) Result {
	return Check(context.Background(), f.run, "oc", []string{"agent", "list"}, "/w", []string{"A=1"}, "SETTING", plain, "build", "bash", "edit")
}

func TestCheckLaunchesWithTheDenialsKept(t *testing.T) {
	f := &fake{agents: world(nil)}
	res := check(f)
	if res.Unasked {
		t.Errorf("unasked with the setting: %+v", res)
	}
	if !strings.Contains(res.Inline, `"rm *":"deny"`) || !strings.Contains(res.Inline, `"edit":"ask"`) {
		t.Errorf("inline = %s", res.Inline)
	}
	if len(f.calls) != 2 {
		t.Fatalf("%d runs", len(f.calls))
	}
	// The first run has no setting and the second has it.
	for _, kv := range f.calls[0] {
		if strings.HasPrefix(kv, "SETTING=") {
			t.Error("the first run had the setting")
		}
	}
	if last := f.calls[1][len(f.calls[1])-1]; last != "SETTING="+res.Inline {
		t.Errorf("the second run had %q", last)
	}
}

func TestCheckSaysUnaskedWhenTheBaselineAgentOverridesTheSetting(t *testing.T) {
	res := check(&fake{agents: world(map[string][]Rule{"build": {r("bash", "*", "allow")}})})
	if !res.Unasked || strings.Join(res.Agents, ",") != "build" {
		t.Errorf("%+v", res)
	}
}

// A project can name a default agent, or give a subagent a task starts its own
// rules; neither is the baseline agent, and either can run a command unasked.
func TestCheckSaysUnaskedWhenAnyOtherAgentAllows(t *testing.T) {
	for name, extra := range map[string]map[string][]Rule{
		"a default agent":  {"x": {r("bash", "*", "allow")}},
		"a subagent":       {"general": {r("bash", "*", "allow")}},
		"an edit":          {"x": {r("edit", "*", "allow")}},
		"some commands":    {"general": {r("bash", "git *", "allow")}},
		"two of the three": {"x": {r("bash", "*", "allow")}, "general": {r("edit", "*", "allow")}},
	} {
		res := check(&fake{agents: world(extra)})
		if !res.Unasked || len(res.Agents) == 0 || res.Agents[0] == "build" {
			t.Errorf("%s: %+v", name, res)
		}
		if !strings.Contains(res.Note, "unasked in") {
			t.Errorf("%s: note %q", name, res.Note)
		}
	}
	// An agent that denies is not unasked.
	if res := check(&fake{agents: world(map[string][]Rule{"x": {r("bash", "*", "deny")}})}); res.Unasked {
		t.Errorf("a denying agent was flagged: %+v", res)
	}
}

func TestCheckFallsBackToThePlainSettingWhenTheRulesCannotBeRead(t *testing.T) {
	noBuild := func(string) []Agent { return []Agent{{Name: "x", Mode: "primary", Rules: userRules}} }
	for name, f := range map[string]*fake{
		"the first run fails":   {failAt: 1, agents: world(nil)},
		"the output is garbled": {garbled: true, agents: world(nil)},
		"an action not known":   {agents: func(string) []Agent { return []Agent{build(r("bash", "*", "permit"))} }},
		"no baseline agent":     {agents: noBuild},
	} {
		res := check(f)
		if res.Inline != plain {
			t.Errorf("%s: inline = %s", name, res.Inline)
		}
		if res.Note == "" {
			t.Errorf("%s: no note", name)
		}
		if name == "the output is garbled" && !res.Unasked {
			t.Errorf("%s: not unasked though nothing could be read", name)
		}
	}
}

func TestCheckIsUnaskedWhenTheResultCannotBeChecked(t *testing.T) {
	res := check(&fake{agents: world(nil), failAt: 2})
	if !res.Unasked || res.Inline == "" || !strings.Contains(res.Note, "could not be checked") {
		t.Errorf("%+v", res)
	}
}

// A rule for every tool is part of what a tool is held to.
func TestInlineKeepsARuleForEveryTool(t *testing.T) {
	for name, tc := range map[string]struct {
		rules []Rule
		want  string
	}{
		"deny everything":       {[]Rule{r("*", "*", "deny")}, `{"permission":{"bash":"deny"}}`},
		"ask everything":        {[]Rule{r("*", "*", "ask")}, `{"permission":{"bash":"ask"}}`},
		"deny a pattern of all": {[]Rule{r("*", "*", "allow"), r("*", "rm *", "deny")}, `{"permission":{"bash":{"*":"ask","rm *":"deny"}}}`},
		"deny all, then bash":   {[]Rule{r("*", "*", "deny"), r("bash", "git *", "allow")}, `{"permission":{"bash":{"*":"deny","git *":"ask"}}}`},
	} {
		got, ok := Inline(tc.rules, "bash")
		if !ok || got != tc.want {
			t.Errorf("%s: got %s (%v), want %s", name, got, ok, tc.want)
		}
	}
}

func TestInlineAcceptsExactlyTheMostRulesAndRefusesOneMore(t *testing.T) {
	list := func(n int) []Rule {
		out := []Rule{r("bash", "*", "ask")}
		for i := 1; i < n; i++ {
			out = append(out, r("bash", fmt.Sprintf("p%d *", i), "deny"))
		}
		return out
	}
	if _, ok := Inline(list(maxRules), "bash"); !ok {
		t.Errorf("%d rules refused", maxRules)
	}
	if got, ok := Inline(list(maxRules+1), "bash"); ok {
		t.Errorf("%d rules accepted: %.60s", maxRules+1, got)
	}
}
