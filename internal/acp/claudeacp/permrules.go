package claudeacp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/jmwri/flockdeck/internal/acp"
)

// The permission rules extension (internal/acp/permrules.go): list the
// allow-always rules of a session and drop one by id. The rule key is the tool
// and its canonical input (see ruleKey), so the text shown is the command in it,
// led by a marker for each other field the rule is tied to.

// maxRuleMarkers bounds the markers in front of a command.
const maxRuleMarkers = 6

// markerValue is a field's value for a marker, short and on one line.
func markerValue(v any) string {
	s := fmt.Sprint(v)
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 40 {
		s = strings.ToValidUTF8(s[:40], "") + "..."
	}
	return s
}

// ruleMarkers names the fields of a rule's input other than the command, so
// that two rules for the same command that differ in one of them read
// differently. dangerouslyDisableSandbox is the riskier grant and is first.
func ruleMarkers(m map[string]any) string {
	var out []string
	if v, ok := m["dangerouslyDisableSandbox"]; ok {
		if b, isBool := v.(bool); isBool && b {
			out = append(out, "(sandbox off)")
		} else if !isBool || b {
			out = append(out, "(dangerouslyDisableSandbox="+markerValue(v)+")")
		}
	}
	if v, ok := m["run_in_background"]; ok {
		if b, isBool := v.(bool); isBool && b {
			out = append(out, "(background)")
		} else if !isBool {
			out = append(out, "(run_in_background="+markerValue(v)+")")
		}
	}
	var rest []string
	for k := range m {
		if k != "command" && k != "dangerouslyDisableSandbox" && k != "run_in_background" {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	for _, k := range rest {
		out = append(out, "("+markerValue(k)+"="+markerValue(m[k])+")")
	}
	if len(out) > maxRuleMarkers {
		out = append(out[:maxRuleMarkers], "(...)")
	}
	return strings.Join(out, " ")
}

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
			if marks := ruleMarkers(m); marks != "" {
				text = marks + " " + c
			}
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
