package workspace

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
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
	long := strings.Repeat("word ", 70) // 350 runes an entry
	plan := "1. Rework the parser:\n   - first " + long + "\n   - second " + long + "\n2. Write tests for the tooltip layer\n"
	got := ExtractTasks(plan)
	if len(got) != 2 {
		t.Fatalf("extracted %d tasks, want the two steps: %q", len(got), got)
	}
	if !strings.HasPrefix(got[0], "Rework the parser: first word") || strings.Contains(got[0], "second") {
		t.Errorf("first task = %q, want the step with its first entry and not its second", got[0])
	}
	if n := utf8.RuneCountInString(got[0]); n > maxTaskRunes {
		t.Errorf("first task is %d runes, past the limit of %d", n, maxTaskRunes)
	}
}
