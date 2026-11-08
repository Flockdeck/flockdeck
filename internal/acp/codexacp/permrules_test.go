package codexacp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/acp"
)

func TestPermRulesListAndRevokeByID(t *testing.T) {
	h := newHarness(t, []step{
		expect(`"method":"turn/start"`), startedTurn(),
		cmdApproval("ap-1", "c1", "go test ./...", ""),
		answered("ap-1", `{"decision":"accept"}`),
		endTurn("completed"),
	})
	h.onReq = func(rpcMsg) any { return outcome("allow_always") }
	sid := h.start()
	if stopReason(t, newText(h, sid)) != "end_turn" {
		t.Fatal("bad stop")
	}
	list := func() []acp.Rule {
		r := h.call("_flockdeck/permrules", map[string]any{"sessionId": sid})
		if r.Error != nil {
			t.Fatalf("permrules: %v", r.Error)
		}
		var out struct{ Rules []acp.Rule }
		_ = json.Unmarshal(r.Result, &out)
		return out.Rules
	}
	rules := list()
	if len(rules) != 1 || rules[0].Text != "go test ./..." || rules[0].Tool != "command" || !acp.ValidRuleID(rules[0].ID) {
		t.Fatalf("rules = %+v", rules)
	}
	r := h.call("_flockdeck/permrule_revoke", map[string]any{"sessionId": sid, "id": "r0000000000000000"})
	if r.Error != nil || strings.Contains(string(r.Result), "true") {
		t.Errorf("an id nobody holds: %s %v", r.Result, r.Error)
	}
	r = h.call("_flockdeck/permrule_revoke", map[string]any{"sessionId": sid, "id": rules[0].ID})
	if r.Error != nil || !strings.Contains(string(r.Result), `"revoked":true`) {
		t.Fatalf("revoke: %s %v", r.Result, r.Error)
	}
	if len(list()) != 0 {
		t.Error("the rule is still listed")
	}
	if r := h.call("_flockdeck/permrule_revoke", map[string]any{"sessionId": sid, "id": "x"}); r.Error == nil {
		t.Error("a malformed id was accepted")
	}
}
