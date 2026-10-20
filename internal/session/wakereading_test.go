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
