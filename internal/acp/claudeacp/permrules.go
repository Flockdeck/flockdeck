package claudeacp

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"

	"github.com/jmwri/flockdeck/internal/acp"
)

// The permission rules extension (internal/acp/permrules.go): list the
// allow-always rules of a session and drop one by id. The rule key is the tool
// and its canonical input (see ruleKey), so the text shown is the command in it.

// ruleView is the rule kept under key as the client is shown it. The text is
// not redacted here; the host does that.
func ruleView(key string) acp.Rule {
	tool, in, _ := strings.Cut(key, "\x00")
	text := in
	var m map[string]any
	dec := json.NewDecoder(bytes.NewReader([]byte(in)))
	dec.UseNumber()
	if dec.Decode(&m) == nil {
		if c, ok := m["command"].(string); ok {
			text = c
		}
	}
	text, cut := acp.ClipRuleText(text)
	return acp.Rule{ID: acp.RuleID(key), Tool: tool, Text: text, Cut: cut}
}

func (s *server) permRules(m rpcMsg) {
	var p struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(m.Params, &p); err != nil {
		s.replyErr(m.ID, &rpcErr{Code: codeInvalidParams, Message: "bad parameters"})
		return
	}
	ss := s.session(p.SessionID)
	if ss == nil {
		s.replyErr(m.ID, &rpcErr{Code: codeInvalidParams, Message: "unknown session"})
		return
	}
	ss.mu.Lock()
	rules := make([]acp.Rule, 0, len(ss.allowed))
	for k, on := range ss.allowed {
		if on {
			rules = append(rules, ruleView(k))
		}
	}
	ss.mu.Unlock()
	sort.Slice(rules, func(i, j int) bool { return rules[i].ID < rules[j].ID })
	if len(rules) > acp.MaxRules {
		rules = rules[:acp.MaxRules]
	}
	s.reply(m.ID, map[string]any{"rules": rules})
}

func (s *server) permRuleRevoke(m rpcMsg) {
	var p struct {
		SessionID string `json:"sessionId"`
		ID        string `json:"id"`
	}
	if err := json.Unmarshal(m.Params, &p); err != nil || !acp.ValidRuleID(p.ID) {
		s.replyErr(m.ID, &rpcErr{Code: codeInvalidParams, Message: "bad parameters"})
		return
	}
	ss := s.session(p.SessionID)
	if ss == nil {
		s.replyErr(m.ID, &rpcErr{Code: codeInvalidParams, Message: "unknown session"})
		return
	}
	revoked := false
	ss.mu.Lock()
	for k := range ss.allowed {
		if acp.RuleID(k) == p.ID {
			delete(ss.allowed, k)
			revoked = true
		}
	}
	ss.mu.Unlock()
	s.reply(m.ID, map[string]any{"revoked": revoked})
}
