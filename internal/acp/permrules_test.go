package acp

import (
	"errors"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/acp/acptest"
)

func TestPermRulesAreOnlyForAgentsThatSayTheyHaveThem(t *testing.T) {
	c := startInit(t, newRecorder(), acptest.WithSessionResume())
	if c.Capabilities().PermRules {
		t.Fatal("an agent that did not advertise the extension has it")
	}
	if _, err := c.PermRules(ctxT(t), "s"); !errors.Is(err, ErrNoPermRules) {
		t.Errorf("list = %v", err)
	}
	if _, err := c.RevokePermRule(ctxT(t), "s", "r0000000000000001"); !errors.Is(err, ErrNoPermRules) {
		t.Errorf("revoke = %v", err)
	}
}

func TestPermRulesListClipAndRevoke(t *testing.T) {
	c, sid := start(t, newRecorder(), acptest.WithPermRules())
	if !c.Capabilities().PermRules {
		t.Fatal("the extension was not read")
	}
	rules, err := c.PermRules(ctxT(t), sid)
	if err != nil || len(rules) != 3 {
		t.Fatalf("rules = %+v, %v", rules, err)
	}
	if long := rules[2]; len(long.Text) != MaxRuleText || !strings.HasPrefix(long.Text, "xxx") {
		t.Errorf("the 3000-byte rule is %d bytes", len(long.Text))
	}
	if _, err := c.RevokePermRule(ctxT(t), sid, "bad"); err == nil {
		t.Error("a malformed id was sent")
	}
	if ok, err := c.RevokePermRule(ctxT(t), sid, "r0000000000000002"); err != nil || !ok {
		t.Errorf("revoke = %v, %v", ok, err)
	}
	if ok, err := c.RevokePermRule(ctxT(t), sid, "r0000000000000002"); err != nil || ok {
		t.Errorf("revoke of a gone rule = %v, %v", ok, err)
	}
	if rules, _ = c.PermRules(ctxT(t), sid); len(rules) != 2 {
		t.Errorf("%d rules left, want 2", len(rules))
	}
}

func TestRuleIDIsStableAndShaped(t *testing.T) {
	a, b := RuleID("Bash\x00{}"), RuleID("Bash\x00{}")
	if a != b || !ValidRuleID(a) || RuleID("Bash\x00{ }") == a {
		t.Errorf("ids %s %s", a, b)
	}
}
