package claudeacp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/acp"
)

func rulesOf(t *testing.T, h *harness, sid string) []acp.Rule {
	t.Helper()
	r := h.call("_flockdeck/permrules", map[string]any{"sessionId": sid})
	if r.Error != nil {
		t.Fatalf("permrules: %v", r.Error)
	}
	var out struct {
		Rules []acp.Rule `json:"rules"`
	}
	if err := json.Unmarshal(r.Result, &out); err != nil {
		t.Fatal(err)
	}
	return out.Rules
}

// An allow-always answer is listed with an id and the command, and revoking it
// by id makes the next call ask again.
func TestPermRulesListAndRevokeByID(t *testing.T) {
	h := newHarness(t, []step{
		expect(`"role":"user"`),
		emit(bashRequest("p1", "toolu_1", `{"command":"deploy --token=abc123","description":"d"}`)), expect(`"request_id":"p1"`),
		emit(lResultOK),
	})
	h.onReq = func(rpcMsg) any { return outcome("allow_always") }
	sid := h.start()
	stopReason(t, h.await(h.promptText(sid, "go")))

	rules := rulesOf(t, h, sid)
	if len(rules) != 1 || rules[0].Tool != "Bash" || rules[0].Text != "deploy --token=abc123" || !acp.ValidRuleID(rules[0].ID) {
		t.Fatalf("rules = %+v", rules)
	}
	if want := acp.RuleID(ruleKey("Bash", json.RawMessage(`{"command":"deploy --token=abc123"}`))); rules[0].ID != want {
		t.Errorf("id %s, want %s (the description is not part of the rule)", rules[0].ID, want)
	}

	revoke := func(id string) bool {
		r := h.call("_flockdeck/permrule_revoke", map[string]any{"sessionId": sid, "id": id})
		if r.Error != nil {
			t.Fatalf("revoke: %v", r.Error)
		}
		var out struct{ Revoked bool }
		_ = json.Unmarshal(r.Result, &out)
		return out.Revoked
	}
	if revoke("r0000000000000000") {
		t.Error("an id nobody holds was revoked")
	}
	if len(rulesOf(t, h, sid)) != 1 {
		t.Error("a revoke of another id dropped the rule")
	}
	if !revoke(rules[0].ID) {
		t.Fatal("the rule was not revoked")
	}
	if len(rulesOf(t, h, sid)) != 0 {
		t.Error("the rule is still listed")
	}
	if r := h.call("_flockdeck/permrule_revoke", map[string]any{"sessionId": sid, "id": "not an id"}); r.Error == nil {
		t.Error("a malformed id was accepted")
	}
	if r := h.call("_flockdeck/permrules", map[string]any{"sessionId": "nope"}); r.Error == nil {
		t.Error("an unknown session was answered")
	}
}

func TestRuleViewCutsALongCommandAndKeepsTheToolName(t *testing.T) {
	long := strings.Repeat("é", acp.MaxRuleText)
	in, _ := json.Marshal(map[string]any{"command": long})
	v := ruleView(ruleKey("PowerShell", in))
	if !v.Cut || len(v.Text) > acp.MaxRuleText || v.Tool != "PowerShell" {
		t.Errorf("cut=%v len=%d tool=%q", v.Cut, len(v.Text), v.Tool)
	}
	if !strings.HasPrefix(long, v.Text) {
		t.Error("the cut broke a character")
	}
}

// After a revoke the same command asks again; before it, it does not.
func TestARevokedRuleAsksAgain(t *testing.T) {
	const input = `{"command":"make deploy"}`
	h := newHarness(t, []step{
		expect(`"role":"user"`), emit(bashRequest("p1", "toolu_1", input)), expect(`"request_id":"p1"`), emit(lResultOK),
		expect(`"role":"user"`), emit(bashRequest("p2", "toolu_2", input)), expect(`"request_id":"p2"`), emit(lResultOK),
		expect(`"role":"user"`), emit(bashRequest("p3", "toolu_3", input)), expect(`"request_id":"p3"`), emit(lResultOK),
	})
	h.onReq = func(rpcMsg) any { return outcome("allow_always") }
	sid := h.start()
	stopReason(t, h.await(h.promptText(sid, "one")))
	if n := len(h.requests("session/request_permission")); n != 1 {
		t.Fatalf("%d cards after the first call, want 1", n)
	}
	stopReason(t, h.await(h.promptText(sid, "two")))
	if n := len(h.requests("session/request_permission")); n != 1 {
		t.Fatalf("%d cards after a repeat, want 1: the rule should have allowed it", n)
	}
	rules := rulesOf(t, h, sid)
	if len(rules) != 1 {
		t.Fatalf("rules = %+v", rules)
	}
	if r := h.call("_flockdeck/permrule_revoke", map[string]any{"sessionId": sid, "id": rules[0].ID}); r.Error != nil {
		t.Fatal(r.Error)
	}
	stopReason(t, h.await(h.promptText(sid, "three")))
	if n := len(h.requests("session/request_permission")); n != 2 {
		t.Errorf("%d cards after the revoke, want 2: the command was not asked about again", n)
	}
}

// Rules for the same command that differ in a field the rule covers read
// differently, and the riskier one says so.
func TestRulesThatDifferInExtraFieldsReadDifferently(t *testing.T) {
	in := func(s string) string { return ruleView(ruleKey("Bash", json.RawMessage(s))).Text }
	plain := in(`{"command":"npm test"}`)
	sandbox := in(`{"command":"npm test","dangerouslyDisableSandbox":true}`)
	bg := in(`{"command":"npm test","run_in_background":true}`)
	timeout := in(`{"command":"npm test","timeout":600000}`)
	described := in(`{"command":"npm test","description":"anything"}`)
	seen := map[string]bool{}
	for _, s := range []string{plain, sandbox, bg, timeout} {
		if seen[s] {
			t.Errorf("two rules read alike: %q", s)
		}
		seen[s] = true
	}
	if plain != "npm test" || described != plain {
		t.Errorf("a plain rule reads %q, with a description %q", plain, described)
	}
	if !strings.HasPrefix(sandbox, "(sandbox off) ") || !strings.HasPrefix(bg, "(background) ") || !strings.Contains(timeout, "timeout=600000") {
		t.Errorf("markers: %q %q %q", sandbox, bg, timeout)
	}
	// The marker survives a long command: it is in front.
	long := ruleView(ruleKey("Bash", json.RawMessage(`{"command":"`+strings.Repeat("z", 5000)+`","dangerouslyDisableSandbox":true}`)))
	if !strings.HasPrefix(long.Text, "(sandbox off) ") || !long.Cut {
		t.Errorf("long rule: cut=%v starts %q", long.Cut, long.Text[:20])
	}
}
