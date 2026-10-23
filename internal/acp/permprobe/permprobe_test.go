package permprobe

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func r(tool, pattern, action string) Rule {
	return Rule{Permission: tool, Pattern: pattern, Action: action}
}

func agentJSON(rules ...Rule) []byte {
	b, _ := json.Marshal(map[string]any{"name": "build", "permission": rules})
	return b
}

func TestUnaskedFollowsTheLastMatchingRule(t *testing.T) {
	for name, tc := range map[string]struct {
		rules []Rule
		want  bool
	}{
		"the default allows":        {[]Rule{r("*", "*", "allow")}, true},
		"asked after the default":   {[]Rule{r("*", "*", "allow"), r("bash", "*", "ask")}, false},
		"denied":                    {[]Rule{r("*", "*", "allow"), r("bash", "*", "deny")}, false},
		"an allow after an ask":     {[]Rule{r("bash", "*", "ask"), r("bash", "*", "allow")}, true},
		"an ask after an allow":     {[]Rule{r("bash", "*", "allow"), r("bash", "*", "ask")}, false},
		"allowed for some commands": {[]Rule{r("bash", "*", "ask"), r("bash", "git *", "allow")}, true},
		"allowed before the ask":    {[]Rule{r("bash", "git *", "allow"), r("bash", "*", "ask")}, false},
		"a deny for some commands":  {[]Rule{r("bash", "*", "ask"), r("bash", "rm *", "deny")}, false},
		"no rule at all":            {[]Rule{r("read", "*", "allow")}, true},
		"another tool's allow":      {[]Rule{r("bash", "*", "ask"), r("edit", "*", "allow")}, false},
		"an action not known":       {[]Rule{r("bash", "*", "permit")}, true},
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
	// The last rule for every command replaces the default one and moves to the
	// end, where it was, so what comes before it no longer applies... except that
	// it is an ask and the others before it are shadowed. That is what the user's
	// own list did with an allow there.
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

func TestParseRulesReadsOpencodesOutput(t *testing.T) {
	out := append([]byte("loading config...\n"), agentJSON(r("bash", "*", "ask"))...)
	rules, err := ParseRules(out)
	if err != nil || len(rules) != 1 || rules[0].Action != "ask" {
		t.Fatalf("%v, %v", rules, err)
	}
	for name, bad := range map[string]string{"no object": "nothing", "junk": "{not json", "no rules": `{"name":"build"}`} {
		if _, err := ParseRules([]byte(bad)); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

// fake is a probe whose answer depends on the setting it was started with.
type fake struct {
	calls   [][]string
	base    []Rule                      // what the agent resolves with no setting
	apply   func(setting string) []Rule // what it resolves with one
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
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "SETTING="); ok {
			return agentJSON(f.apply(v)...), nil
		}
	}
	return agentJSON(f.base...), nil
}

// plainApply is how an agent with no agent-level settings resolves: the setting
// replaces the tool's rules. Granular maps come out sorted, which is enough for
// these fixtures.
func plainApply(setting string) []Rule {
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

const plain = `{"permission":{"bash":"ask","edit":"ask"}}`

func TestCheckLaunchesWithTheDenialsKept(t *testing.T) {
	f := &fake{base: []Rule{r("*", "*", "allow"), r("bash", "rm *", "deny"), r("bash", "*", "allow"), r("edit", "*", "allow")}, apply: plainApply}
	res := Check(context.Background(), f.run, "oc", []string{"debug"}, "/w", []string{"A=1"}, "SETTING", plain, "bash", "edit")
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

func TestCheckSaysUnaskedWhenAnAgentOverridesTheSetting(t *testing.T) {
	f := &fake{base: []Rule{r("*", "*", "allow")}}
	f.apply = func(s string) []Rule { return append(plainApply(s), r("bash", "*", "allow")) }
	res := Check(context.Background(), f.run, "oc", nil, "/w", nil, "SETTING", plain, "bash", "edit")
	if !res.Unasked {
		t.Errorf("%+v", res)
	}
}

func TestCheckFallsBackToThePlainSettingWhenTheRulesCannotBeRead(t *testing.T) {
	for name, f := range map[string]*fake{
		"the first run fails":   {failAt: 1, apply: plainApply},
		"the output is garbled": {garbled: true, apply: plainApply},
		"an action not known":   {base: []Rule{r("bash", "*", "permit")}, apply: plainApply},
	} {
		res := Check(context.Background(), f.run, "oc", nil, "/w", nil, "SETTING", plain, "bash", "edit")
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
	f := &fake{base: []Rule{r("*", "*", "allow")}, apply: plainApply, failAt: 2}
	res := Check(context.Background(), f.run, "oc", nil, "/w", nil, "SETTING", plain, "bash", "edit")
	if !res.Unasked || res.Inline == "" || !strings.Contains(res.Note, "could not be checked") {
		t.Errorf("%+v", res)
	}
}
