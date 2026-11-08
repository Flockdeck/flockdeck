package server

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/acp"
	"github.com/jmwri/flockdeck/internal/acp/acptest"
	"github.com/jmwri/flockdeck/internal/session"
)

func (e *structuredEnv) rulesOp(t *testing.T, cmd command) teamResultMsg {
	t.Helper()
	cmd.Req = "q1"
	raw := structuredReply(t, e.srv, e.c, cmd, "teamResult")
	var res teamResultMsg
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatal(err)
	}
	return res
}

func TestRuleViewsRedactBeforeTheyCutAndNeverShowMoreThanTheLimit(t *testing.T) {
	secret := "sk-ant-api03-" + strings.Repeat("A1b2C3d4", 8)
	// The secret starts before the cut: a cut-then-redact order would show its front.
	text := strings.Repeat("a", structuredRuleText-10) + " --token=" + secret
	got := ruleViews([]acp.Rule{
		{ID: "r0000000000000001", Tool: "Bash", Text: "export API_KEY=" + secret},
		{ID: "r0000000000000002", Tool: "Bash", Text: text},
		{ID: "r0000000000000003", Tool: "Bash", Text: "ls\x07\n-la", Dir: "/work"},
	})
	for _, v := range got {
		if strings.Contains(v.Text, secret) || strings.Contains(v.Text, secret[:20]) {
			t.Errorf("a secret reached the view: %q", v.Text)
		}
		if len(v.Text) > structuredRuleText+8 {
			t.Errorf("text of %d bytes", len(v.Text))
		}
	}
	if !got[1].Cut {
		t.Errorf("the long rule: %+v", got[1])
	}
	if got[2].Text != "ls -la" || got[2].Dir != "/work" {
		t.Errorf("control characters: %+v", got[2])
	}
	many := make([]acp.Rule, 150)
	for i := range many {
		many[i] = acp.Rule{ID: acp.RuleID(string(rune('a' + i%26))), Text: "x"}
	}
	if n := len(ruleViews(many)); n != structuredRulesMax {
		t.Errorf("%d rules, want %d", n, structuredRulesMax)
	}
}

func TestStructuredRulesListAndRevokeByID(t *testing.T) {
	e := newStructuredEnvWith(t, "plain", structuredEnvOpts{Args: []string{acptest.ArgSessions + "permrules"}})
	e.status(t, session.StatusIdle)
	res := e.rulesOp(t, command{Cmd: "structuredRules", ID: e.child})
	if !res.OK || res.Op != "structuredRules" || len(res.Rules) != 3 {
		t.Fatalf("list = %+v", res)
	}
	for _, r := range res.Rules {
		if strings.Contains(r.Text, acptest.PermRuleSecret) {
			t.Errorf("a secret reached the window: %q", r.Text)
		}
	}
	if long := res.Rules[2]; !long.Cut || len(long.Text) > structuredRuleText {
		t.Errorf("the long rule: cut=%v len=%d", long.Cut, len(long.Text))
	}

	// A revoke names the id, whatever the list looks like now.
	gone := e.rulesOp(t, command{Cmd: "structuredRuleRevoke", ID: e.child, Entry: "r0000000000000001"})
	if !gone.OK || gone.Revoked != "r0000000000000001" {
		t.Fatalf("revoke = %+v", gone)
	}
	left := e.rulesOp(t, command{Cmd: "structuredRules", ID: e.child})
	if len(left.Rules) != 2 || left.Rules[0].ID != "r0000000000000002" {
		t.Errorf("after the revoke: %+v", left.Rules)
	}
	if again := e.rulesOp(t, command{Cmd: "structuredRuleRevoke", ID: e.child, Entry: "r0000000000000001"}); again.OK || again.Error != structuredRuleAbsent {
		t.Errorf("a second revoke: %+v", again)
	}
	for _, bad := range []string{"", "1", "r1", "R0000000000000002"} {
		if r := e.rulesOp(t, command{Cmd: "structuredRuleRevoke", ID: e.child, Entry: bad}); r.OK || r.Error != structuredRuleBadID {
			t.Errorf("%q: %+v", bad, r)
		}
	}
	if r := e.rulesOp(t, command{Cmd: "structuredRules", ID: "nope"}); r.OK || r.Error != structuredNotPane {
		t.Errorf("not a pane: %+v", r)
	}
}

func TestStructuredRulesNeedAnAgentThatKeepsThem(t *testing.T) {
	e := newStructuredEnv(t, "plain")
	e.status(t, session.StatusIdle)
	if r := e.rulesOp(t, command{Cmd: "structuredRules", ID: e.child}); r.OK || r.Error != structuredNoRules {
		t.Errorf("list: %+v", r)
	}
	if r := e.rulesOp(t, command{Cmd: "structuredRuleRevoke", ID: e.child, Entry: "r0000000000000001"}); r.OK || r.Error != structuredNoRules {
		t.Errorf("revoke: %+v", r)
	}
}

// A private key block the adapter's cut left unterminated is still redacted, and
// the visible head of a long command is redacted with the text after it in view.
func TestRuleViewsRedactAnUnterminatedPrivateKey(t *testing.T) {
	body := strings.Repeat("MIIEvQIBADANBgkqhkiG9w0BAQEFAASC", 40)
	text := "echo '-----BEGIN PRIVATE KEY-----\n" + body // no END line: cut by the adapter
	got := ruleViews([]acp.Rule{{ID: "r0000000000000001", Tool: "Bash", Text: text, Cut: true}})
	if strings.Contains(got[0].Text, "MIIEvQ") || strings.Contains(got[0].Text, "BEGIN PRIVATE") && strings.Contains(got[0].Text, "MIIE") {
		t.Errorf("key material reached the view: %q", got[0].Text)
	}
	if !got[0].Cut {
		t.Error("the adapter's cut was lost")
	}
	// A key whose header the adapter cut in half shows nothing a reader could use.
	half := strings.Repeat("a", 50) + " -----BEGIN PRIV"
	if v := ruleViews([]acp.Rule{{ID: "r0000000000000001", Text: half, Cut: true}}); strings.Contains(v[0].Text, "MII") {
		t.Errorf("%q", v[0].Text)
	}
}
