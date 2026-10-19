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

// The tails below are synthetic, built from the documented shape of each
// prompt. They are to be replaced by tails from real recordings, and Tested
// set, before any agent is typed into.
func TestClaudeWakeRule(t *testing.T) {
	box := "╭──────────────╮\n│ >            │\n╰──────────────╯\n  ? for shortcuts"
	cases := []struct {
		name string
		tail string
		want WakeVerdict
	}{
		{"idle empty prompt", "done with the task\n" + box, WakeOK},
		{"idle prompt without the footer", "done\n╭────╮\n│ >  │\n╰────╯", WakeOK},
		{"bare prompt character", "done\n❯", WakeOK},
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
		{"idle empty prompt", "assistant: done\n\nsonnet · 1.2k tokens\nyou >", WakeOK},
		{"with trailing space", "answer\nyou > ", WakeOK},
		{"draft typed", "answer\nyou > half a thought", WakeNo},
		{"continuation line", "you > first\n   …", WakeNo},
		{"tool question prompt", "Run `go test`?\n  [y] yes   [n] no (Enter)   or type what to do instead\n  >", WakeNo},
		{"answer streaming", "assistant: let me look", WakeNo},
		{"prompt only in earlier output", "you >\nassistant: thinking", WakeNo},
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
	idle := []string{"done", "you >"}
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
			}
		default:
			if s.Wake != nil {
				t.Errorf("%s has a wake rule it has no recording for", s.ID)
			}
		}
	}
}

func TestAUserEntryEditsAWakeRuleWithoutTouchingTheBuiltIn(t *testing.T) {
	f := &File{Agents: []json.RawMessage{
		json.RawMessage(`{"id": "claude", "wake": {"tested": true, "forbid": ["my own"]}}`),
	}}
	c := Merge(f)
	var got *WakeRule
	for _, s := range c.Specs {
		if s.ID == "claude" {
			got = s.Wake
		}
	}
	if got == nil || !got.Tested || len(got.Forbid) != 1 || got.Forbid[0] != "my own" {
		t.Fatalf("merged rule = %+v, want tested with the user's forbid list", got)
	}
	if len(got.Prompts) == 0 {
		t.Error("the user left prompts out and lost the built-in's")
	}
	for _, s := range Builtins() {
		if s.ID == "claude" && (s.Wake.Tested || slices.Contains(s.Wake.Forbid, "my own")) {
			t.Error("the entry reached into the built-in")
		}
	}
	if c.Notice != "" {
		t.Errorf("a wake entry produced a notice: %s", c.Notice)
	}
}

func TestANewAgentCanCarryAWakeRule(t *testing.T) {
	f := &File{Agents: []json.RawMessage{
		json.RawMessage(`{"id": "mine", "exe": "mine", "wake": {"version": "1", "prompts": ["mine>"]}}`),
	}}
	for _, s := range Merge(f).Specs {
		if s.ID == "mine" {
			if s.Wake == nil || s.Wake.Tested {
				t.Fatalf("rule = %+v, want present and untested by default", s.Wake)
			}
			return
		}
	}
	t.Fatal("agent not found")
}
