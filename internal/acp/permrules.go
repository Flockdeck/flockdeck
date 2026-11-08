package acp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"
)

// The permission rules extension. An allow-always answer is kept by Flockdeck's
// own adapters, in memory, per session (see claudeacp/perm.go and
// codexacp/perm.go). ACP has no call to list or drop one, so the adapters add
// two, with the capability agentCapabilities._meta["flockdeck.dev"] =
// {"permRules": 1}:
//
//   - _flockdeck/permrules {sessionId}: answer {rules: [{id, tool, text, cut}]}.
//     text is the command the rule covers, cut to MaxRuleText bytes by the
//     adapter (cut says it was). id names the rule: RuleID of the adapter's key.
//   - _flockdeck/permrule_revoke {sessionId, id}: forget the rule with that id.
//     Answers {revoked: bool}; an id nobody holds answers false, which is not an
//     error (the rule may have been dropped by a rewind or a restart).
//
// The text can hold a secret (a token in a command line). The adapter does not
// redact it; the host does before anything is shown (see the server's
// structuredRules).
const (
	// MaxRuleText bounds the text of one rule as an adapter sends it.
	MaxRuleText = 2000
	// MaxRules bounds how many rules an adapter sends and the client reads.
	MaxRules = 200
)

var ruleIDPattern = regexp.MustCompile(`^r[0-9a-f]{16}$`)

// ErrNoPermRules is returned when the agent does not have the extension.
var ErrNoPermRules = errors.New("acp: the agent cannot list its allow-always rules")

// Rule is one allow-always grant.
type Rule struct {
	ID   string `json:"id"`
	Tool string `json:"tool"`
	Text string `json:"text"`
	Cut  bool   `json:"cut,omitempty"`
	// Dir is the directory a rule is tied to, when the adapter ties one.
	Dir string `json:"dir,omitempty"`
}

// RuleID is the id of the rule an adapter keeps under key: "r" and 16 hex
// digits of the key's SHA-256. It does not change while the rule exists and
// does not depend on the order rules were made in.
func RuleID(key string) string {
	sum := sha256.Sum256([]byte(key))
	return "r" + hex.EncodeToString(sum[:8])
}

// ValidRuleID says s has the shape of RuleID's answer.
func ValidRuleID(s string) bool { return ruleIDPattern.MatchString(s) }

// ClipRuleText cuts s to MaxRuleText bytes on a rune boundary and says whether
// it cut.
func ClipRuleText(s string) (string, bool) {
	if len(s) <= MaxRuleText {
		return s, false
	}
	n := MaxRuleText
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return strings.ToValidUTF8(s[:n], ""), true
}

// PermRules lists the allow-always rules of the session. At most MaxRules are
// returned; an entry with a malformed id or an empty text is left out.
func (c *Client) PermRules(ctx context.Context, sessionID string) ([]Rule, error) {
	if !c.Capabilities().PermRules {
		return nil, ErrNoPermRules
	}
	if sessionID == "" {
		return nil, errors.New("acp: listing rules needs a session")
	}
	ctx, cancel := c.setup(ctx)
	defer cancel()
	var r struct {
		Rules []Rule `json:"rules"`
	}
	if err := c.call(ctx, "_flockdeck/permrules", map[string]any{"sessionId": sessionID}, &r); err != nil {
		return nil, err
	}
	out := make([]Rule, 0, len(r.Rules))
	for _, x := range r.Rules {
		if len(out) == MaxRules {
			break
		}
		if !ValidRuleID(x.ID) || x.Text == "" {
			continue
		}
		x.Text, _ = ClipRuleText(x.Text)
		out = append(out, x)
	}
	return out, nil
}

// RevokePermRule drops the rule with the id and says whether the adapter held
// it.
func (c *Client) RevokePermRule(ctx context.Context, sessionID, id string) (bool, error) {
	if !c.Capabilities().PermRules {
		return false, ErrNoPermRules
	}
	if sessionID == "" || !ValidRuleID(id) {
		return false, errors.New("acp: revoking a rule needs a session and a rule id")
	}
	ctx, cancel := c.setup(ctx)
	defer cancel()
	var r struct {
		Revoked bool `json:"revoked"`
	}
	if err := c.call(ctx, "_flockdeck/permrule_revoke", map[string]any{"sessionId": sessionID, "id": id}, &r); err != nil {
		return false, err
	}
	return r.Revoked, nil
}
