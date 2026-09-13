package workspace

import (
	"reflect"
	"strings"
	"testing"
)

// TestExtractTasksKeepsTheDetailAColonIntroduces covers a step that ends on a
// colon over the specifics of the job. Nested entries are detail and not jobs
// of their own, so they were dropped, and the agent was handed "Fix the login
// bug:" — a heading, with nothing under it saying which bug.
func TestExtractTasksKeepsTheDetailAColonIntroduces(t *testing.T) {
	for name, tc := range map[string]struct {
		plan string
		want []string
	}{
		"a colon hands over to the detail": {
			"1. Fix the login bug:\n" +
				"   - the token refresh races with logout\n" +
				"   - add a regression test for it\n" +
				"2. Write tests for the tooltip layer\n",
			[]string{
				"Fix the login bug: the token refresh races with logout; add a regression test for it",
				"Write tests for the tooltip layer",
			},
		},
		"a step without a colon is whole already": {
			"1. Add a tooltip layer to the web interface\n" +
				"   - app.js: one delegated listener\n" +
				"2. Write tests for the tooltip layer\n",
			[]string{
				"Add a tooltip layer to the web interface",
				"Write tests for the tooltip layer",
			},
		},
		"a plan heading still gives way to its steps": {
			"- Next steps:\n  - Split the router into its own package\n  - Add a timeout to the control socket\n",
			[]string{"Split the router into its own package", "Add a timeout to the control socket"},
		},
	} {
		if got := ExtractTasks(tc.plan); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: extracted %q, want %q", name, got, tc.want)
		}
	}
}

// TestExtractTasksKeepsAColonsDetailWithinTheLimit checks that detail too long
// to join whole is cut at the last entry that fits, rather than costing the
// plan the step: a task past the limit is thrown away entire.
func TestExtractTasksKeepsAColonsDetailWithinTheLimit(t *testing.T) {
	long := strings.Repeat("word ", maxTaskBytes/10) // over half the limit an entry
	plan := "1. Rework the parser:\n   - first " + long + "\n   - second " + long + "\n2. Write tests for the tooltip layer\n"
	got := ExtractTasks(plan)
	if len(got) != 2 {
		t.Fatalf("extracted %d tasks, want the two steps", len(got))
	}
	if !strings.HasPrefix(got[0], "Rework the parser: first word") || strings.Contains(got[0], "second") {
		t.Errorf("first task = %.60q, want the step with its first entry and not its second", got[0])
	}
	if !strings.HasSuffix(got[0], "…") {
		t.Error("the first task was cut short without saying so")
	}
	if n := len(got[0]); n > maxTaskBytes {
		t.Errorf("first task is %d bytes, past the limit of %d", n, maxTaskBytes)
	}
}

// TestExtractTasksFindsTheStepsUnderGroupLabels covers a plan grouped under
// labels, with the real steps nested beneath. Only the shallowest entries were
// taken as tasks, the labels are a word each and no task, and so a plan made of
// nothing but work offered nothing at all.
func TestExtractTasksFindsTheStepsUnderGroupLabels(t *testing.T) {
	for name, tc := range map[string]struct {
		plan string
		want []string
	}{
		"numbered labels": {
			"1. Backend\n" +
				"   - Add the /health endpoint\n" +
				"   - Add tests for it\n" +
				"2. Frontend\n" +
				"   - Add a status button\n",
			[]string{"Add the /health endpoint", "Add tests for it", "Add a status button"},
		},
		"a step's own detail stays with it": {
			"- **Backend**\n" +
				"  - Add the /health endpoint\n" +
				"    - report the version and the uptime\n" +
				"  - Add tests for it\n",
			[]string{"Add the /health endpoint", "Add tests for it"},
		},
		"labels inside labels": {
			"1. Server\n" +
				"   - API\n" +
				"     - Add the /health endpoint\n" +
				"   - Add a timeout to the control socket\n",
			[]string{"Add the /health endpoint", "Add a timeout to the control socket"},
		},
	} {
		if got := ExtractTasks(tc.plan); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: extracted %q, want %q", name, got, tc.want)
		}
	}
}

// TestExtractTasksLeavesWhatAQuestionOrAFindingHeads covers entries that are
// no task and no label either. The steps under a label are the work, but the
// entries under a question are its options, and those under a finding its
// evidence: lifted to the depth of the steps, the options to a question the
// agent was still asking were offered as tasks to start.
func TestExtractTasksLeavesWhatAQuestionOrAFindingHeads(t *testing.T) {
	for name, tc := range map[string]struct {
		plan string
		want []string
	}{
		"a question's options": {
			"- Which approach do you prefer?\n" +
				"  - Rewrite the parser from scratch\n" +
				"  - Patch the existing tokenizer\n" +
				"- Add tests for the parser\n",
			[]string{"Add tests for the parser"},
		},
		"a numbered question's options": {
			"1. Should we keep the old API?\n" +
				"   - Keep it behind a flag\n" +
				"   - Remove it outright\n" +
				"2. Add the new endpoint\n",
			[]string{"Add the new endpoint"},
		},
		"a finding's evidence": {
			"- The login flow has two problems\n" +
				"  - Tokens refresh after logout\n" +
				"  - Retries never back off\n" +
				"- Fix the retry backoff in the client\n",
			[]string{"Fix the retry backoff in the client"},
		},
	} {
		for _, screen := range []bool{true, false} {
			if got := extractTasks(tc.plan, screen); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("%s (screen %v): extracted %q, want %q", name, screen, got, tc.want)
			}
		}
	}
}
