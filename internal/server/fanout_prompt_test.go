package server

import (
	"testing"

	"github.com/jmwri/flockdeck/internal/workspace"
)

// Fan out from a pane whose last output is Claude's permission prompt read the
// prompt's numbered choices as a plan, and offered "1. Yes", "2. Yes, and don't
// ask again" and "3. No" as three tasks. planTasks is what the preview reads the
// pane's plan with; given a screen that ends in the prompt it must find nothing.
func TestFanoutDoesNotOfferAPermissionPromptAsAPlan(t *testing.T) {
	const prompt = `
● Bash(go run ./cmd/migrate up)

  Bash command

    go run ./cmd/migrate up
    Apply migration 0042_order_refunds

  Do you want to proceed?
  ❯ 1. Yes
    2. Yes, and don't ask again for go run
    3. No, and tell Claude what to do differently (esc)
`
	got, fromReply := planTasks(workspace.PlanSource{Screen: prompt})
	if len(got) != 0 || fromReply {
		t.Fatalf("the preview offered %#v as the plan of a pane waiting on a permission prompt", got)
	}
}

// With no plan offered, the dialog says why: the pane is asking the person
// something. A pane with a plan, or with nothing at all, is not said to be.
func TestFanoutSaysAPermissionPromptIsAQuestion(t *testing.T) {
	const prompt = `
  Do you want to proceed?
  ❯ 1. Yes
    2. Yes, and don't ask again for go run
    3. No, and tell Claude what to do differently (esc)
`
	if tasks, _, question := readPlan(workspace.PlanSource{Screen: prompt}); len(tasks) != 0 || !question {
		t.Fatalf("a pane ending in a permission prompt: tasks %#v, question %v; want none, and a question", tasks, question)
	}
	const plan = `
Here is the plan:

1. Add a health endpoint
2. Write tests for the parser
`
	if tasks, _, question := readPlan(workspace.PlanSource{Screen: plan}); len(tasks) == 0 || question {
		t.Fatalf("a pane ending in a plan: tasks %#v, question %v; want the plan, and no question", tasks, question)
	}
	if _, _, question := readPlan(workspace.PlanSource{}); question {
		t.Fatal("an empty pane was said to be asking a question")
	}
}
