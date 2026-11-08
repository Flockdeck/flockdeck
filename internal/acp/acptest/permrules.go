package acptest

import (
	"encoding/json"
	"strings"
)

// The permission rules extension (internal/acp/permrules.go), with the session
// option "permrules" (WithPermRules). The agent advertises it and holds three
// rules from the start:
//
//	r0000000000000001  Bash  "npm test"
//	r0000000000000002  Bash  a command with a secret in it
//	r0000000000000003  Bash  a command of 3000 bytes
//
// _flockdeck/permrules lists what is left; _flockdeck/permrule_revoke drops one
// by id and answers {revoked}. The ids are fixed, not made from the text.

// PermRuleSecret is the secret in the second rule's command, for a test that
// checks it never reaches a window.
const PermRuleSecret = "hunter2hunter2hunter2"

type permRule struct{ id, text string }

func (a *agent) permRulesCaps(caps map[string]any) {
	if !a.hasSessionOpt("permrules") {
		return
	}
	meta := cloneMap(nil)
	if m, ok := caps["_meta"].(map[string]any); ok {
		meta = cloneMap(m)
	}
	ext := cloneMap(nil)
	if e, ok := meta["flockdeck.dev"].(map[string]any); ok {
		ext = cloneMap(e)
	}
	ext["permRules"] = 1
	meta["flockdeck.dev"] = ext
	caps["_meta"] = meta
}

func (a *agent) permRulesCall(m msg) {
	var p struct {
		SessionID string `json:"sessionId"`
		ID        string `json:"id"`
	}
	_ = json.Unmarshal(m.Params, &p)
	a.mu.Lock()
	if !a.rulesSeeded {
		a.rulesSeeded = true
		a.rules = []permRule{
			{"r0000000000000001", "npm test"},
			{"r0000000000000002", "deploy --password=" + PermRuleSecret},
			{"r0000000000000003", strings.Repeat("x", 3000)},
		}
	}
	defer a.mu.Unlock()
	if m.Method == "_flockdeck/permrule_revoke" {
		revoked := false
		for i, r := range a.rules {
			if r.id == p.ID {
				a.rules = append(a.rules[:i:i], a.rules[i+1:]...)
				revoked = true
				break
			}
		}
		a.result(m.ID, map[string]any{"revoked": revoked})
		return
	}
	out := make([]map[string]any, 0, len(a.rules))
	for _, r := range a.rules {
		out = append(out, map[string]any{"id": r.id, "tool": "Bash", "text": r.text})
	}
	a.result(m.ID, map[string]any{"rules": out})
}
