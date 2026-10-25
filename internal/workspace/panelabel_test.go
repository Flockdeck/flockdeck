package workspace

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/hooks"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/team"
)

func TestLabelFromTaskIsTheFirstClauseWithoutMarkdown(t *testing.T) {
	long := "Refactor the rate limiter so that every handler shares one bucket per client address"
	for task, want := range map[string]string{
		"":                                     "",
		"   ":                                  "",
		"/review the branch":                   "",
		"<system-reminder>x":                   "",
		"Fix the flaky tabs test":              "Fix the flaky tabs test",
		"Fix the flaky tabs test. Then tidy.":  "Fix the flaky tabs test",
		"## Review PR 612: tabs per goal":      "Review PR 612",
		"- [ ] Add rate limiting; tests too":   "Add rate limiting",
		"```\n\nWrite limiter_test.go - cover": "Write limiter_test.go",
		"first line\nsecond line":              "first line",
		long:                                   "Refactor the rate limiter so that every",
	} {
		got := labelFromTask(task)
		if got != want {
			t.Errorf("labelFromTask(%q) = %q, want %q", task, got, want)
		}
		if n := len([]rune(got)); n > team.MaxPaneLabel {
			t.Errorf("labelFromTask(%q) has %d characters, the limit is %d", task, n, team.MaxPaneLabel)
		}
	}
}

func TestSpawnGivesAHelperTheLabelAskedForOrOneMadeFromItsTask(t *testing.T) {
	c := newCrew(t)
	spawn := func(opts SpawnOptions) *Pane {
		t.Helper()
		opts.Role, opts.Agent, opts.Kind, opts.SpawnedByAgent = "worker", "sleeper", session.KindClaude, true
		id, err := c.ws.Spawn(c.lead, opts)
		if err != nil {
			t.Fatal(err)
		}
		return c.ws.Pane(id)
	}
	named := spawn(SpawnOptions{Task: "Fix the flaky tabs test. Run it ten times.", Label: "tabs flake\x1b[31m\nPR 7"})
	if got := c.ws.LabelOf(named); strings.ContainsAny(got, "\x1b\n") || !strings.HasPrefix(got, "tabs flake") {
		t.Errorf("label asked for = %q, want one clean line starting with the name", got)
	}
	if c.ws.LabelAutoOf(named) {
		t.Error("a label asked for was marked as made from the task")
	}
	made := spawn(SpawnOptions{Task: "Fix the flaky tabs test. Run it ten times."})
	if got := c.ws.LabelOf(made); got != "Fix the flaky tabs test" {
		t.Errorf("label made from the task = %q", got)
	}
	if !c.ws.LabelAutoOf(made) {
		t.Error("a label made from the task was not marked")
	}
	if got := c.ws.LabelOf(c.ws.Pane(c.lead)); got != "" {
		t.Errorf("a pane started by hand has the label %q", got)
	}
}

func TestAPaneNamesItselfAndOnlyAManagerNamesAnother(t *testing.T) {
	c := newCrew(t)
	name := func(actor, target, text string) (string, error) {
		t.Helper()
		return c.ws.SetPaneLabel(actor, target, text)
	}
	if got, err := name(c.w1, c.w1, "  rate limiter PR  "); err != nil || got != "rate limiter PR" {
		t.Fatalf("a pane naming itself: %q, %v", got, err)
	}
	if c.ws.LabelAutoOf(c.ws.Pane(c.w1)) {
		t.Error("a label a pane chose is still marked as made from its task")
	}
	if _, err := name(c.w1, c.w2, "mine now"); err == nil || !strings.Contains(err.Error(), "only a manager") {
		t.Errorf("a worker naming another pane: %v", err)
	}
	if got := c.ws.LabelOf(c.ws.Pane(c.w2)); got == "mine now" {
		t.Error("the refused name was stored")
	}
	if got, err := name(c.lead, c.w2, "docs sweep"); err != nil || got != "docs sweep" {
		t.Fatalf("the manager naming a worker: %q, %v", got, err)
	}
	if _, err := name(c.lead, "no-such-pane", "x"); err == nil {
		t.Error("a manager named a pane that is not open")
	}
	// The person at the window may name any pane.
	if got, err := name("", c.reviewer, "review of 624"); err != nil || got != "review of 624" {
		t.Fatalf("the person naming a pane: %q, %v", got, err)
	}
	// Text is cut, stripped of control characters, and cannot pose as the user.
	got, _ := name(c.lead, c.w2, strings.Repeat("abcd ", 20))
	if n := len([]rune(got)); n == 0 || n > team.MaxPaneLabel || strings.HasSuffix(got, " ") {
		t.Errorf("a long label = %q (%d characters)", got, n)
	}
	if got, _ := name(c.lead, c.w2, "the user"); !strings.HasPrefix(got, "(agent) ") {
		t.Errorf("a label that poses as the user = %q", got)
	}
	// Empty clears it.
	if got, err := name(c.lead, c.w2, ""); err != nil || got != "" || c.ws.LabelOf(c.ws.Pane(c.w2)) != "" {
		t.Errorf("clearing: %q, %v", got, err)
	}
}

func TestAManagerCannotNameAPaneOfAnotherProject(t *testing.T) {
	c := newCrew(t)
	other := t.TempDir()
	if err := c.ws.OpenProject(other); err != nil {
		t.Fatal(err)
	}
	c.ws.NewTabWith(Choice{Kind: session.KindClaude, Agent: "sleeper"}, other, "")
	foreign := c.ws.FocusedPane().ID
	if _, err := c.ws.SetPaneLabel(c.lead, foreign, "mine"); err == nil || !strings.Contains(err.Error(), "not in your project") {
		t.Errorf("a manager naming a pane of another project: %v", err)
	}
}

func TestPaneNamedNamesTheCallerWithNoTarget(t *testing.T) {
	c := newCrew(t)
	res, err := c.ws.PaneNamed(hooks.PaneNameRequest{Auth: hooks.Auth{Pane: c.w1}, Text: "tabs per goal"})
	if err != nil || res.Pane != c.w1 || res.Label != "tabs per goal" {
		t.Fatalf("PaneNamed = %+v, %v", res, err)
	}
	if _, err := c.ws.PaneNamed(hooks.PaneNameRequest{Auth: hooks.Auth{Pane: "gone"}, Text: "x"}); err == nil || !hooks.IsNoPane(err) {
		t.Errorf("a pane that is not open: %v", err)
	}
	roster, _ := c.ws.Roster(c.w1)
	for _, m := range roster {
		if m.Pane == c.w1 && m.Label() != "tabs per goal" {
			t.Errorf("the roster calls the pane %q", m.Label())
		}
	}
}

func TestALabelSurvivesSaveAndRestore(t *testing.T) {
	isolateAutoReview(t, false)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	chosen := ws.Pane(ws.NewTab(session.KindShell, root, "a").Tree.Panes()[0])
	made := ws.Pane(ws.NewTab(session.KindShell, root, "b").Tree.Panes()[0])
	plain := ws.Pane(ws.NewTab(session.KindShell, root, "c").Tree.Panes()[0])
	if _, err := ws.SetPaneLabel("", chosen.ID, "limiter PR"); err != nil {
		t.Fatal(err)
	}
	ws.mu.Lock()
	made.Label, made.LabelAuto = "Fix the tabs", true
	ws.mu.Unlock()
	if err := ws.SaveAll(); err != nil {
		t.Fatal(err)
	}
	ws.Close()

	restored := newTestWorkspace(t, root)
	if ok, err := restored.Restore(); err != nil || !ok {
		t.Fatalf("restore: ok=%v err=%v", ok, err)
	}
	defer restored.Close()
	if p := restored.Pane(chosen.ID); p == nil || restored.LabelOf(p) != "limiter PR" || restored.LabelAutoOf(p) {
		t.Errorf("a chosen label came back as %+v", p)
	}
	if p := restored.Pane(made.ID); p == nil || restored.LabelOf(p) != "Fix the tabs" || !restored.LabelAutoOf(p) {
		t.Errorf("a label made from the task came back as %+v", p)
	}
	if p := restored.Pane(plain.ID); p == nil || restored.LabelOf(p) != "" {
		t.Errorf("a pane with no label came back with one: %+v", p)
	}
}

func TestALabelThatPassesForARoleOfTheProjectIsMarkedAsAnAgents(t *testing.T) {
	c := newCrew(t)
	for _, text := range []string{"manager", "Reviewer", "w-o-r-k-e-r"} {
		got, err := c.ws.SetPaneLabel(c.w1, c.w1, text)
		if err != nil || !strings.HasPrefix(got, "(agent) ") {
			t.Errorf("SetPaneLabel(%q) = %q, %v, want it marked as an agent's", text, got, err)
		}
	}
	if got, _ := c.ws.SetPaneLabel(c.w1, c.w1, "reviewer PR 12"); got != "reviewer PR 12" {
		t.Errorf("a label that only contains a role word was changed: %q", got)
	}
}

func TestARenameByAnotherPaneTellsTheNamedPaneAndIsLimited(t *testing.T) {
	c := newCrew(t)
	if _, err := c.ws.SetPaneLabel(c.lead, c.w1, "docs sweep"); err != nil {
		t.Fatal(err)
	}
	var told bool
	for _, m := range c.inbox(t, c.w1, true) {
		if strings.Contains(m.Text, `now "docs sweep"`) && strings.Contains(m.Text, "pane "+c.lead) {
			told = true
		}
	}
	if !told {
		t.Errorf("the named pane was not told who named it: %+v", c.inbox(t, c.w1, true))
	}
	before := len(c.inbox(t, c.w1, true))
	if _, err := c.ws.SetPaneLabel(c.w1, c.w1, "my own name"); err != nil {
		t.Fatal(err)
	}
	if got := len(c.inbox(t, c.w1, true)); got != before {
		t.Errorf("a pane naming itself sent itself a message (%d, was %d)", got, before)
	}
	var err error
	for i := 0; i < maxRenames+1 && err == nil; i++ {
		_, err = c.ws.SetPaneLabel(c.lead, c.w1, fmt.Sprintf("name %d", i))
	}
	if err == nil || !strings.Contains(err.Error(), "wait before naming it again") {
		t.Errorf("renames in a loop were not limited: %v", err)
	}
	if _, err := c.ws.SetPaneLabel("", c.w1, "the person may"); err != nil {
		t.Errorf("the person at the window was limited: %v", err)
	}
	notices := 0
	for _, m := range c.inbox(t, c.w1, true) {
		if strings.Contains(m.Text, "Pane name changed") || strings.Contains(m.Text, "name was changed") {
			notices++
		}
	}
	if notices > 2 {
		t.Errorf("a burst of renames sent %d notices, want the first and one summary", notices)
	}
}

func TestOnePaneIsLimitedInRenamingOthersAcrossTargets(t *testing.T) {
	c := newCrew(t)
	targets := []string{c.w1, c.w2, c.reviewer}
	var err error
	n := 0
	for i := 0; i < maxActorRenames+1 && err == nil; i++ {
		_, err = c.ws.SetPaneLabel(c.lead, targets[i%len(targets)], fmt.Sprintf("name %d", i))
		if err == nil {
			n++
		}
	}
	if err == nil || !strings.Contains(err.Error(), "wait before naming another") || n != maxActorRenames {
		t.Errorf("renames across panes were not limited: %d done, %v", n, err)
	}
	c.ws.mu.Lock()
	c.ws.forgetRenames(c.w1)
	_, left := c.ws.labelRenames[c.w1]
	c.ws.mu.Unlock()
	if left {
		t.Error("a closed pane's renames were remembered")
	}
}

func TestALabelMadeFromATaskIsNotAWordForARole(t *testing.T) {
	if got := labelFromTask("Reviewer", "docs-bot"); !strings.HasPrefix(got, "(agent) ") {
		t.Errorf("a task that is a role word gave %q", got)
	}
	if got := labelFromTask("docs-bot"); got != "docs-bot" {
		t.Errorf("not a role without the project's roles: %q", got)
	}
	if got := labelFromTask("docs-bot", "docs-bot"); !strings.HasPrefix(got, "(agent) ") {
		t.Errorf("a custom role id was not rewritten: %q", got)
	}
}
