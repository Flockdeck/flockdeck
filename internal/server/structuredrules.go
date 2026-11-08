package server

import (
	"errors"

	"github.com/jmwri/flockdeck/internal/acp"
	"github.com/jmwri/flockdeck/internal/record"
	"github.com/jmwri/flockdeck/internal/structured"
)

// Allow-always rules of a structured pane (API 1.29, cap structuredRules).
// "Always allow this command" is kept by Flockdeck's own Claude and Codex
// adapters, in memory, for the session. structuredRules lists what the adapter
// holds now and structuredRuleRevoke drops one. A rule's text is a whole
// command and can hold a secret, so it is redacted and then cut before it
// leaves the server; the window is never given more than structuredRuleText
// bytes of one. A rule is named by the id the adapter made for it (cmd.Entry),
// never by its place in the list, so a list that changed since the window drew
// it cannot make a revoke hit another rule. Both commands are for a window on
// this machine: structuredCommand refuses the relay and the table of commands
// refuses a viewer.

const (
	structuredRuleText   = 300
	structuredRuleDir    = 200
	structuredRulesMax   = 100
	structuredNoRules    = "This agent does not keep rules that can be listed"
	structuredRulesFail  = "The agent could not answer about its rules"
	structuredRuleBadID  = "Name the rule to remove"
	structuredRuleAbsent = "That rule is already gone"
)

// structuredRuleView is one rule as a window is told of it.
type structuredRuleView struct {
	ID   string `json:"id"`
	Tool string `json:"tool"`
	Text string `json:"text"`
	// Cut says Text is shorter than the command, because it was long.
	Cut bool   `json:"cut,omitempty"`
	Dir string `json:"dir,omitempty"`
}

// ruleViews redacts and clips the rules. The redactor runs on the whole text
// the adapter sent, before the cut, so that a secret that starts before the cut
// is not shown in part.
func ruleViews(in []acp.Rule) []structuredRuleView {
	out := make([]structuredRuleView, 0, len(in))
	for _, r := range in {
		if len(out) == structuredRulesMax {
			break
		}
		text, cut := clipLine(record.Redact(r.Text), structuredRuleText)
		tool, _ := clipLine(r.Tool, 40)
		dir, _ := clipLine(record.Redact(r.Dir), structuredRuleDir)
		out = append(out, structuredRuleView{ID: r.ID, Tool: tool, Text: text, Cut: cut || r.Cut, Dir: dir})
	}
	return out
}

func rulesRefusal(err error) string {
	switch {
	case errors.Is(err, structured.ErrRulesNotOffered):
		return structuredNoRules
	case errors.Is(err, structured.ErrNotRunning):
		return structuredNotPane
	}
	return structuredRulesFail
}

// structuredRules answers structuredRules and structuredRuleRevoke. It must run
// on the workspace goroutine; the agent is asked on another.
func (s *Server) structuredRules(c *controlClient, cmd command) {
	rt := s.ws.StructuredRuntime(cmd.ID)
	if rt == nil {
		refuse(c, cmd, errStructured(structuredNotPane))
		return
	}
	if cmd.Cmd == "structuredRuleRevoke" && !acp.ValidRuleID(cmd.Entry) {
		refuse(c, cmd, errStructured(structuredRuleBadID))
		return
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				s.reportPanic(c, "listing rules", r)
				refuse(c, cmd, errStructured(structuredRulesFail))
			}
		}()
		if cmd.Cmd == "structuredRuleRevoke" {
			ok, err := rt.RevokePermRule(cmd.Entry)
			switch {
			case err != nil:
				refuse(c, cmd, errStructured(rulesRefusal(err)))
			case !ok:
				refuse(c, cmd, errStructured(structuredRuleAbsent))
			default:
				c.sendJSON(teamResultMsg{Type: "teamResult", Req: cmd.Req, Op: cmd.Cmd, OK: true, Revoked: cmd.Entry})
			}
			return
		}
		rules, err := rt.PermRules()
		if err != nil {
			refuse(c, cmd, errStructured(rulesRefusal(err)))
			return
		}
		c.sendJSON(teamResultMsg{Type: "teamResult", Req: cmd.Req, Op: cmd.Cmd, OK: true, Rules: ruleViews(rules)})
	}()
}
