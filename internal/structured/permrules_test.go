package structured

import (
	"errors"
	"testing"

	"github.com/jmwri/flockdeck/internal/acp/acptest"
)

func TestRuntimeListsAndRevokesRules(t *testing.T) {
	r := begin(t, fakeOptions(t, acptest.WithPermRules()))
	until(t, r, "ready", idle)
	rules, err := r.PermRules()
	if err != nil || len(rules) != 3 {
		t.Fatalf("rules = %+v, %v", rules, err)
	}
	if ok, err := r.RevokePermRule(rules[0].ID); err != nil || !ok {
		t.Fatalf("revoke = %v, %v", ok, err)
	}
	if rules, _ = r.PermRules(); len(rules) != 2 {
		t.Errorf("%d left", len(rules))
	}
}

func TestRuntimeRulesNeedTheExtension(t *testing.T) {
	r := begin(t, fakeOptions(t))
	until(t, r, "ready", idle)
	if _, err := r.PermRules(); !errors.Is(err, ErrRulesNotOffered) {
		t.Errorf("list = %v", err)
	}
	if _, err := r.RevokePermRule("r0000000000000001"); !errors.Is(err, ErrRulesNotOffered) {
		t.Errorf("revoke = %v", err)
	}
}
