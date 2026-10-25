package session

import (
	"testing"

	"github.com/jmwri/flockdeck/internal/agent"
)

func TestWakeReadingUsesTheAgentsRule(t *testing.T) {
	rule := &agent.WakeRule{Version: "v1", Tested: true, Source: agent.RuleBuiltIn, Prompts: []string{"you >"}, Forbid: []string{"[y] yes"}}
	s := fakeSession(newFakePTY())
	s.wake = rule

	s.publish([]byte("answer\r\nyou > "))
	r := s.WakeReading()
	if r.Verdict != agent.WakeOK || r.Prompt != "you >" || r.Version != "v1" || r.Source != agent.RuleBuiltIn {
		t.Fatalf("reading = %+v, want ok at \"you >\" from v1", r)
	}
	if r.Tail.ChangedAt.IsZero() {
		t.Error("the reading carries no change time")
	}

	s.publish([]byte("draft"))
	if r := s.WakeReading(); r.Verdict != agent.WakeNo {
		t.Errorf("with a draft typed, verdict = %v, want no", r.Verdict)
	}
}

func TestWakeReadingWithoutARuleIsUnknown(t *testing.T) {
	s := fakeSession(newFakePTY())
	s.publish([]byte("you > "))
	r := s.WakeReading()
	if r.Verdict != agent.WakeUnknown || r.Version != "" {
		t.Fatalf("reading = %+v, want unknown with no version", r)
	}
}

func TestWakeReadingOfAnUntestedRuleIsUnknown(t *testing.T) {
	s := fakeSession(newFakePTY())
	s.wake = &agent.WakeRule{Version: "v1", Prompts: []string{"you >"}}
	s.publish([]byte("you > "))
	if r := s.WakeReading(); r.Verdict != agent.WakeUnknown {
		t.Fatalf("verdict = %v, want unknown for an untested rule", r.Verdict)
	}
}

func TestWakeReadingOfAUsersRuleIsUnknownEvenWhenTested(t *testing.T) {
	s := fakeSession(newFakePTY())
	s.wake = &agent.WakeRule{Version: "v1", Tested: true, Source: agent.RuleUser, Prompts: []string{"you >"}}
	s.publish([]byte("you > "))
	if r := s.WakeReading(); r.Verdict != agent.WakeUnknown || r.Source != agent.RuleUser {
		t.Fatalf("reading = %+v, want unknown from the user's rule", r)
	}
}

// The rule a real agent ships with is untested, so a session built from its spec
// never reads as allowing a wake, even on a screen that looks like its prompt.
func TestWakeReadingOfTheShippedClaudeRuleIsUnknown(t *testing.T) {
	var spec agent.Spec
	for _, sp := range agent.Builtins() {
		if sp.ID == "claude" {
			spec = sp
		}
	}
	if spec.Wake == nil {
		t.Fatal("the claude spec has no wake rule")
	}
	s := fakeSession(newFakePTY())
	s.wake = spec.Wake
	for _, rule := range spec.Wake.Prompts {
		s.publish([]byte("done\r\n" + rule + " "))
		if r := s.WakeReading(); r.Verdict == agent.WakeOK {
			t.Fatalf("the shipped rule allowed a wake at %q", rule)
		}
	}
}

// A dialog taller than the tail is still seen: its footer is the last line, so it is
// always inside the lines the rule reads, even when it is the only forbidden text.
func TestAFooterIsSeenOnADialogTallerThanTheTail(t *testing.T) {
	s := fakeSession(newFakePTY())
	s.wake = &agent.WakeRule{Version: "v1", Tested: true, Source: agent.RuleBuiltIn, Prompts: []string{">"}, Forbid: []string{"enter to select"}}
	var out []byte
	for i := 0; i < 3*ScreenTailLines; i++ {
		out = append(out, []byte("  option line\r\n")...)
	}
	out = append(out, []byte("Enter to select · up/down to move")...)
	s.publish(out)
	r := s.WakeReading()
	if len(r.Tail.Lines) > ScreenTailLines {
		t.Fatalf("tail holds %d lines, more than %d", len(r.Tail.Lines), ScreenTailLines)
	}
	if !r.Forbidden {
		t.Error("the footer of a tall dialog was not seen")
	}
}
