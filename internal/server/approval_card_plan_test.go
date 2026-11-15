package server

import (
	"errors"
	"testing"

	"github.com/jmwri/flockdeck/internal/acp"
	"github.com/jmwri/flockdeck/internal/acp/bridge"
)

// planOptions are the options the Claude adapter puts on an ExitPlanMode card.
var planOptions = []acp.PermissionOption{
	{OptionID: "allow", Name: "Approve, ask before edits", Kind: "allow_once"},
	{OptionID: "allow_accept_edits", Name: "Approve and accept edits", Kind: "allow_once"},
	{OptionID: "reject", Name: "Reject", Kind: "reject_once"},
}

// The phone is never offered an allow for a plan, and "approve and accept
// edits" is no exception: the card is for the desk.
func TestPhoneIsNotOfferedApproveAndAcceptEdits(t *testing.T) {
	plan := &bridge.Card{Seq: 4, RequestID: "p", Kind: "switch_mode", Body: "1. do things", Options: planOptions}
	for _, own := range []bool{true, false} {
		got := projectCard(plan, own, testRoot)
		if got.Answerable || got.CanAllow || got.Why != whyPlan {
			t.Errorf("own=%v: projectCard = %+v", own, got)
		}
	}
	const token = "3.0"
	l, _ := lockFor(plan, token)
	for _, decision := range []string{decisionAllow, decisionDeny, "allow_accept_edits", "allow_always"} {
		id, err := phoneAnswer(plan, token, true, testRoot, l, decision)
		if id != "" || err == nil {
			t.Errorf("%s answered a plan with %q, %v", decision, id, err)
		}
	}
	if _, err := phoneAnswer(plan, token, true, testRoot, l, decisionAllow); !errors.Is(err, errPhoneDeskOnly) {
		t.Errorf("allow a plan: %v", err)
	}
}

// Were the plan option's kind ever attached to another kind of card, a second
// allow_once option makes a plain Allow ambiguous, so the phone is refused
// rather than shown a guess.
func TestSecondAllowOnceOptionRefusesPhoneAllow(t *testing.T) {
	c := execCard("ls")
	c.Options = planOptions
	if id, ok := optionFor(c, decisionAllow); ok || id != "" {
		t.Errorf("optionFor allow = %q, %v", id, ok)
	}
}
