package workspace

import "testing"

const claudePermissionPrompt = `
● Bash(go run ./cmd/migrate up)

 Bash command

   go run ./cmd/migrate up
   Apply migration 0042_order_refunds

 Do you want to proceed?
 ❯ 1. Yes
   2. Yes, and don't ask again for go run
   3. No, and tell Claude what to do differently (esc)
`

// A pane waiting on a permission prompt has its numbered choices as the last
// thing on the screen, and they were read as a plan: three agents to start, one
// of them "No, and tell Claude what to do differently".
func TestAPermissionPromptIsNotAPlan(t *testing.T) {
	for name, screen := range map[string]string{
		"as drawn":         claudePermissionPrompt,
		"with a hint":      claudePermissionPrompt + "\n Esc to cancel · Tab to amend\n",
		"in a frame":       "│ Do you want to proceed?\n│ ❯ 1. Yes\n│   2. Yes, allow all edits during this session\n│   3. No, keep planning\n",
		"without pointer":  "Do you want to make this edit to app.js?\n  1. Yes\n  2. Yes, allow all edits during this session (shift+tab)\n  3. No, and tell Claude what to do differently (esc)\n",
		"trailing padding": claudePermissionPrompt + "\n\n\n   \n",
	} {
		if !endsInPermissionPrompt(screen) {
			t.Errorf("%s: not recognised as a permission prompt", name)
		}
		got, fromReply := PlanSource{Screen: screen}.Tasks()
		if len(got) != 0 || fromReply {
			t.Errorf("%s: read %#v (fromReply %v) as a plan", name, got, fromReply)
		}
	}
}

// The guard is for a prompt that is still waiting. One answered has the agent's
// work after it, and a plan above that is still a plan; and a numbered plan
// that happens to begin with "Yes" is not a prompt.
func TestAnAnsweredPromptOrARealPlanIsStillRead(t *testing.T) {
	answered := claudePermissionPrompt + `
● Ran go run ./cmd/migrate up
  ok 42 migrations applied

Here is the plan:
1. Add GET /search with prefix matching
2. Handle Stripe refund webhooks
3. Document the public API
`
	plan := "Plan:\n1. Yes/no toggle for the search box\n2. Add GET /search with prefix matching\n3. Document the public API\n"
	for name, screen := range map[string]string{"answered": answered, "plan": plan} {
		if endsInPermissionPrompt(screen) {
			t.Errorf("%s: taken for a permission prompt", name)
		}
		if got, _ := (PlanSource{Screen: screen}).Tasks(); len(got) != 3 {
			t.Errorf("%s: read %#v, want the three steps of the plan", name, got)
		}
	}
}
