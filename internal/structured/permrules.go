package structured

import (
	"errors"

	"github.com/jmwri/flockdeck/internal/acp"
)

// The allow-always rules of the pane. Flockdeck's own adapters keep them, in
// memory, per session (see internal/acp/permrules.go); the runtime asks for the
// list and passes a revoke on by id. It holds no copy, so what it answers is
// what the adapter holds now. The text is the agent's own command and is not
// cleaned here: the server redacts and clips it before a window sees it.

var (
	// ErrRulesNotOffered: the agent is not one that keeps rules it can list.
	ErrRulesNotOffered = errors.New("structured: this agent cannot list its allow-always rules")
	// ErrRulesFailed: the agent did not answer, or refused.
	ErrRulesFailed = errors.New("structured: the agent could not answer about its rules")
)

// PermRules lists the allow-always rules of the current session.
func (r *Runtime) PermRules() ([]acp.Rule, error) {
	c, session, err := r.rulesClient()
	if err != nil {
		return nil, err
	}
	rules, err := c.PermRules(r.ctx, session)
	if err != nil {
		return nil, r.rulesErr(err)
	}
	return rules, nil
}

// RevokePermRule drops the rule with the id and says whether the agent held it.
func (r *Runtime) RevokePermRule(id string) (bool, error) {
	c, session, err := r.rulesClient()
	if err != nil {
		return false, err
	}
	ok, err := c.RevokePermRule(r.ctx, session, id)
	if err != nil {
		return false, r.rulesErr(err)
	}
	return ok, nil
}

func (r *Runtime) rulesClient() (*acp.Client, string, error) {
	r.mu.Lock()
	c, session, phase := r.client, r.session, r.phase
	r.mu.Unlock()
	if c == nil || phase != PhaseReady || session == "" {
		return nil, "", ErrNotRunning
	}
	if !c.Capabilities().PermRules {
		return nil, "", ErrRulesNotOffered
	}
	return c, session, nil
}

func (r *Runtime) rulesErr(err error) error {
	if r.ctx.Err() != nil {
		return ErrNotRunning
	}
	if errors.Is(err, acp.ErrNoPermRules) {
		return ErrRulesNotOffered
	}
	return ErrRulesFailed
}
