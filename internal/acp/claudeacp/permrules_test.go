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
