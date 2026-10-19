package agent

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// tested returns a copy of a built-in rule with Tested set, so that the engine
// can be exercised before any rule has a real recording behind it.
func tested(r *WakeRule) *WakeRule {
	c := r.clone()
	c.Tested = true
	return c
}

func lines(s string) []string { return strings.Split(s, "\n") }

func builtinSpec(t *testing.T, c *Catalog, id string) Spec {
	t.Helper()
	for _, s := range c.Specs {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("no agent %q", id)
	return Spec{}
}

// The tails below are synthetic, built from the documented shape of each
// prompt. They are to be replaced by tails from real recordings, and Tested
// set, before any agent is typed into.
func TestClaudeWakeRule(t *testing.T) {
	box := "╭──────────────╮\n│ >            │\n╰──────────────╯\n  ? for shortcuts"
	rules := "──────────────\n❯\n──────────────\n  ? for shortcuts"
	cases := []struct {
		name string
		tail string
		want WakeVerdict
	}{
		{"idle empty prompt in a box", "done with the task\n" + box, WakeOK},
		{"idle empty prompt between rules", "done\n" + rules, WakeOK},
		{"no footer", "done\n╭────╮\n│ >  │\n╰────╯", WakeNo},
		{"bare prompt character", "done\n❯", WakeNo},
		{"draft text in the prompt", "done\n╭────────────╮\n│ > fix the  │\n╰────────────╯\n  ? for shortcuts", WakeNo},
		{"working, hint shown", "thinking\n╭────╮\n│ >  │\n╰────╯\n  esc to interrupt", WakeNo},
		{"permission question", "Do you want to proceed?\n❯ 1. Yes\n  2. No\n\n>", WakeNo},
		{"confirmation", "Overwrite file? (y/n) >", WakeNo},
		{"trust prompt", "Do you trust the files in this folder?\n> 1. Yes, proceed", WakeNo},
		{"login screen", "Select login method:\n>", WakeNo},
		{"editor open", "-- INSERT --\n>", WakeNo},
		{"mid output", "writing file src/main.go\nrunning tests", WakeNo},
		{"empty tail", "", WakeNo},
		{"blank lines only", "\n\n  \n", WakeNo},
		{"prompt-like line in earlier output", "> quoted line from a file\nstill printing", WakeNo},
		{"empty prompt earlier, output after", ">\nmore output arrived", WakeNo},
		{"markdown quote is not the prompt", "> some quoted text", WakeNo},

		// Lookalikes that must never pass.
		{"output line then a bare prompt", "step 3 running\n>", WakeNo},
		{"markdown table row", "| a | b |\n| > |\n  ? for shortcuts", WakeNo},
		{"printed box without its footer", "╭────╮\n│ > │\n╰────╯", WakeNo},
		{"prompt followed by a no-break space", "done\n──────\n> \n──────\n  ? for shortcuts", WakeNo},
		{"prompt followed by a tab", "done\n──────\n>\t\n──────\n  ? for shortcuts", WakeNo},
		{"prompt with no border above", "done\n>\n──────\n  ? for shortcuts", WakeNo},
		{"prompt with no border below", "──────\n>\n  ? for shortcuts", WakeNo},
		{"wrapped question", "Do you\nwant to proceed?\n──────\n>\n──────\n  ? for shortcuts", WakeNo},
	}
	rule := tested(claudeWake())
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, line := rule.Judge(lines(c.tail))
			if got != c.want {
				t.Fatalf("Judge = %v, want %v for tail %q", got, c.want, c.tail)
			}
			if got == WakeOK && !strings.ContainsAny(line, ">❯") {
				t.Errorf("matched line = %q, want the prompt line", line)
			}
			if got != WakeOK && line != "" {
				t.Errorf("matched line = %q on a verdict of %v", line, got)
			}
		})
	}
}

func TestChatWakeRule(t *testing.T) {
	cases := []struct {
		name string
		tail string
		want WakeVerdict
	}{
		{"idle empty prompt", "answer text\n\nsonnet · 1.2k in · 300 out\nyou >", WakeOK},
		{"with trailing space", "answer\n\nsonnet\nyou > ", WakeOK},
		{"first prompt", "\ndefault model\nyou >", WakeOK},
		{"draft typed", "answer\n\nsonnet\nyou > half a thought", WakeNo},
		{"continuation line", "you > first\n   …", WakeNo},
		{"tool question prompt", "Run `go test`?\n  [y] yes   [n] no (Enter)   or type what to do instead\n  >", WakeNo},
		{"answer streaming", "assistant: let me look", WakeNo},
		{"prompt only in earlier output", "you >\nassistant: thinking", WakeNo},
		{"working output then the prompt text", "assistant: reading files\nyou >", WakeNo},
		{"no blank line above the status line", "answer\nsonnet\nyou >", WakeNo},
		{"no status line", "\nyou >", WakeNo},
		{"no-break space after the prompt", "answer\n\nsonnet\nyou > ", WakeNo},
	}
	rule := tested(chatWake())
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got, _ := rule.Judge(lines(c.tail)); got != c.want {
				t.Fatalf("Judge = %v, want %v for tail %q", got, c.want, c.tail)
			}
		})
	}
}

func TestAnUntestedRuleNeverAllowsAWake(t *testing.T) {
	for name, rule := range map[string]*WakeRule{"claude": claudeWake(), "chat": chatWake()} {
		if rule.Tested {
			t.Fatalf("%s: the built-in rule is marked tested, and no recording exists", name)
		}
	}
	idle := []string{"done", "", "model", "you >"}
	if v, line := chatWake().Judge(idle); v != WakeUnknown || line != "" {
		t.Errorf("untested rule on an idle prompt = %v, %q, want unknown", v, line)
	}
	if v, _ := tested(chatWake()).Judge(idle); v != WakeOK {
		t.Errorf("the same rule once tested = %v, want ok", v)
	}
	// Saying no is safe, so an untested rule still does.
	if v, _ := chatWake().Judge([]string{"you > draft"}); v != WakeNo {
		t.Errorf("untested rule on a draft = %v, want no", v)
	}
}

func TestNoRuleIsUnknown(t *testing.T) {
	var none *WakeRule
	if v, _ := none.Judge([]string{"you >"}); v != WakeUnknown {
		t.Errorf("nil rule = %v, want unknown", v)
	}
	if v, _ := (&WakeRule{Tested: true}).Judge([]string{">"}); v != WakeUnknown {
		t.Errorf("rule with no prompts = %v, want unknown", v)
	}
	var zero WakeVerdict
	if zero != WakeUnknown {
		t.Error("the zero verdict must be unknown")
	}
}

// An entry left empty by a typo must not match everything.
func TestEmptyRuleStringsAreIgnored(t *testing.T) {
	r := &WakeRule{Tested: true, Prompts: []string{">", " "}, Forbid: []string{"", "  "}}
	if v, _ := r.Judge([]string{"some output", ">"}); v != WakeOK {
		t.Errorf("verdict = %v, want ok: an empty forbid string blocked it", v)
	}
	if v, _ := r.Judge([]string{"some output", "   "}); v != WakeNo {
		t.Errorf("verdict on a blank last line = %v, want no: an empty prompt matched", v)
	}
}

func TestOnlyClaudeAndTheChatClientHaveRules(t *testing.T) {
	for _, s := range Builtins() {
		switch {
		case s.ID == "claude", s.Runner == RunnerAPI:
			if s.Wake == nil {
				t.Errorf("%s has no wake rule", s.ID)
			} else if s.Wake.Source != RuleBuiltIn {
				t.Errorf("%s: source = %q, want %q", s.ID, s.Wake.Source, RuleBuiltIn)
			}
		default:
			if s.Wake != nil {
				t.Errorf("%s has a wake rule it has no recording for", s.ID)
			}
		}
	}
}

// The file that edits a built-in rule is not what proves it, so it cannot
// switch the gate off.
func TestAUserEntryCannotMarkABuiltInRuleTested(t *testing.T) {
	for _, entry := range []string{
		`{"id": "claude", "wake": {"tested": true}}`,
		`{"id": "claude", "wake": {"tested": true, "prompts": [">"], "forbid": []}}`,
		`{"id": "anthropic", "wake": {"tested": true}}`,
	} {
		c := Merge(&File{Agents: []json.RawMessage{json.RawMessage(entry)}})
		for _, id := range []string{"claude", "anthropic"} {
			if r := builtinSpec(t, c, id).Wake; r.Tested {
				t.Errorf("%s: %s marked the rule tested", id, entry)
			}
		}
	}
	for _, s := range Builtins() {
		if s.Wake != nil && s.Wake.Tested {
			t.Errorf("%s: the built-in itself was changed", s.ID)
		}
	}
}

func TestAUserForbidListAddsToTheBuiltInListAndNeverReplacesIt(t *testing.T) {
	c := Merge(&File{Agents: []json.RawMessage{
		json.RawMessage(`{"id": "claude", "wake": {"forbid": ["my own", "esc to interrupt"]}}`),
	}})
	got := builtinSpec(t, c, "claude").Wake
	for _, want := range claudeWake().Forbid {
		if !slices.Contains(got.Forbid, want) {
			t.Errorf("built-in forbid %q was lost", want)
		}
	}
	if !slices.Contains(got.Forbid, "my own") {
		t.Error("the user's forbid string was not added")
	}
	if n := strings.Count(strings.Join(got.Forbid, "|"), "esc to interrupt"); n != 1 {
		t.Errorf("a repeated string appears %d times", n)
	}
	if len(got.Prompts) == 0 {
		t.Error("the user left prompts out and lost the built-in's")
	}
	if got.Source != RuleBuiltIn {
		t.Errorf("source = %q after only adding a forbid string", got.Source)
	}
	if slices.Contains(claudeWake().Forbid, "my own") {
		t.Error("the entry reached into the built-in")
	}
	if c.Notice != "" {
		t.Errorf("a wake entry produced a notice: %s", c.Notice)
	}
}

func TestAnEditedBuiltInRuleSaysSo(t *testing.T) {
	c := Merge(&File{Agents: []json.RawMessage{
		json.RawMessage(`{"id": "claude", "wake": {"prompts": [">", "$"]}}`),
	}})
	if got := builtinSpec(t, c, "claude").Wake; got.Source != RuleEdited || got.Tested {
		t.Errorf("source = %q, tested = %v, want edited and untested", got.Source, got.Tested)
	}
}

func TestANewAgentCanCarryAWakeRuleAndItIsMarkedAsTheUsers(t *testing.T) {
	f := &File{Agents: []json.RawMessage{
		json.RawMessage(`{"id": "mine", "exe": "mine", "wake": {"version": "1", "prompts": ["mine>"]}}`),
		json.RawMessage(`{"id": "mine2", "exe": "mine2", "wake": {"tested": true, "prompts": ["m2>"]}}`),
	}}
	c := Merge(f)
	if r := builtinSpec(t, c, "mine").Wake; r == nil || r.Tested || r.Source != RuleUser {
		t.Fatalf("rule = %+v, want present, untested by default and the user's", r)
	}
	if r := builtinSpec(t, c, "mine2").Wake; r == nil || !r.Tested || r.Source != RuleUser {
		t.Fatalf("rule = %+v, want the user's own tested flag kept for their own agent", r)
	}
}

func TestAWakeRuleForABuiltInAgentWithoutOneIsTheUsers(t *testing.T) {
	c := Merge(&File{Agents: []json.RawMessage{
		json.RawMessage(`{"id": "codex", "wake": {"prompts": ["codex>"]}}`),
	}})
	if r := builtinSpec(t, c, "codex").Wake; r == nil || r.Source != RuleUser {
		t.Fatalf("rule = %+v, want the user's", r)
	}
}
