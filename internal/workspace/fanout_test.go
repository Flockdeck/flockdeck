package workspace

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/layout"
	"github.com/jmwri/flockdeck/internal/session"
)

// TestExtractTasksFindsAPlan covers reading work items out of what an agent
// said, which is the input to a fan-out.
func TestExtractTasksFindsAPlan(t *testing.T) {
	output := `
I looked at the code. Here is what I would do:

1. Add a health endpoint to the HTTP server
2) Write tests for the config parser
- Replace the deprecated logging calls
* Update the README with the new flags
- [ ] Remove the unused helpers

Some prose that follows, which is not a task at all.
────────────────────────────────
│ ▸ ▸ ▸ │
Let me know which to start with.
`
	got := ExtractTasks(output)
	want := []string{
		"Add a health endpoint to the HTTP server",
		"Write tests for the config parser",
		"Replace the deprecated logging calls",
		"Update the README with the new flags",
		"Remove the unused helpers",
	}
	if len(got) != len(want) {
		t.Fatalf("extracted %d tasks, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("task %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestExtractTasksIgnoresNoise checks the things a terminal is full of do not
// become tasks.
func TestExtractTasksIgnoresNoise(t *testing.T) {
	noise := `
╭──────────────────────────────╮
│  ▸ ▸ ▸                       │
╰──────────────────────────────╯
- ok
- Add a real task here please
- Add a real task here please
Just a sentence.
`
	got := ExtractTasks(noise)
	if len(got) != 1 {
		t.Fatalf("extracted %#v, want only the one real task", got)
	}
	if got[0] != "Add a real task here please" {
		t.Errorf("task = %q", got[0])
	}
}

// TestExtractTasksIgnoresTheInterface covers the fallback that reads a pane's
// screen. Claude Code's own interface is drawn in the same column as the words
// it frames, and its status line begins with a glyph that reads exactly like a
// bullet, so all of it arrives looking like a plan.
func TestExtractTasksIgnoresTheInterface(t *testing.T) {
	screen := `
· Perambulating… (26s · ↓ 1.2k tokens · esc to interrupt)
· Perambulating… (38s · ↓ 2.0k tokens · thought for 2s · ctrl+o to expand
  thinking)
▸ ctrl+o to expand thinking
- Add a tooltip layer to the web interface
? for shortcuts
`
	got := ExtractTasks(screen)
	want := []string{"Add a tooltip layer to the web interface"}
	if len(got) != len(want) || got[0] != want[0] {
		t.Errorf("extracted %#v, want %#v", got, want)
	}
}

// TestExtractTasksKeepsWrappedItemsWhole covers the other half of reading a
// screen: a bullet too long for the pane is broken across rows, and keeping
// only the first row hands an agent a task that stops mid-sentence.
func TestExtractTasksKeepsWrappedItemsWhole(t *testing.T) {
	screen := `
  - One delegated pointerover listener on document. On hover of
    an element carrying data-tip, position the bubble and show it.
  - A second, shorter task

  Prose that is not part of either.
`
	got := ExtractTasks(screen)
	if len(got) != 2 {
		t.Fatalf("extracted %#v, want 2 tasks", got)
	}
	if !strings.HasSuffix(got[0], "position the bubble and show it.") {
		t.Errorf("the wrapped tail was lost: %q", got[0])
	}
	if strings.Contains(got[1], "Prose") {
		t.Errorf("prose was folded into a task: %q", got[1])
	}
}

// TestExtractTasksSkipsNestedDetail keeps a fan-out from starting one agent for
// a job and another for how to do it.
func TestExtractTasksSkipsNestedDetail(t *testing.T) {
	plan := `
1. Add a tooltip layer to the web interface
   - app.js: one delegated listener
   - app.css: the bubble itself
2. Write tests for the tooltip layer
`
	got := ExtractTasks(plan)
	want := []string{
		"Add a tooltip layer to the web interface",
		"Write tests for the tooltip layer",
	}
	if len(got) != len(want) {
		t.Fatalf("extracted %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("task %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestExtractTasksIgnoresCodeBlocks covers a plan that shows its work: a list
// inside a fence is sample text, not a set of jobs.
func TestExtractTasksIgnoresCodeBlocks(t *testing.T) {
	plan := "Here is what the config should look like:\n\n" +
		"```yaml\n" +
		"- name: not a task at all\n" +
		"- name: nor is this one\n" +
		"```\n\n" +
		"- Apply that config to the staging cluster\n"
	got := ExtractTasks(plan)
	if len(got) != 1 || got[0] != "Apply that config to the staging cluster" {
		t.Errorf("extracted %#v, want only the task outside the fence", got)
	}
}

// TestExtractTasksDropsMarkdownEmphasis keeps the markup out of the prompt an
// agent is handed.
func TestExtractTasksDropsMarkdownEmphasis(t *testing.T) {
	got := ExtractTasks("- **app.js** — add the delegated listener\n")
	if len(got) != 1 || got[0] != "app.js — add the delegated listener" {
		t.Errorf("extracted %#v", got)
	}
}

// TestExtractTasksPrefersThePlan covers the shape an agent's answer actually
// takes: it says what it found, and then says what it would do. Both halves are
// bulleted and only the second is work.
func TestExtractTasksPrefersThePlan(t *testing.T) {
	reply := `
I read the file. What is there today:

- The status vocabulary is waiting / working / idle
- Tooltips are set with title=, which screen readers ignore

Here's the plan:

- Add a delegated pointerover listener to app.js
- Style the bubble in app.css
`
	got := ExtractTasks(reply)
	want := []string{
		"Add a delegated pointerover listener to app.js",
		"Style the bubble in app.css",
	}
	if len(got) != len(want) {
		t.Fatalf("extracted %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("task %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestExtractTasksSkipsFindings covers the same answer without the heading that
// separates the halves. A finding opens by naming what it is about; an
// instruction opens with the doing.
func TestExtractTasksSkipsFindings(t *testing.T) {
	reply := `
- The Go process owns the panes, their processes and the layout tree
- Should the bubble follow the pointer?
- Add a delegated pointerover listener to app.js
- I'll style the bubble in app.css
`
	got := ExtractTasks(reply)
	want := []string{
		"Add a delegated pointerover listener to app.js",
		"I'll style the bubble in app.css",
	}
	if len(got) != len(want) {
		t.Fatalf("extracted %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("task %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestExtractTasksCaps keeps a runaway list from starting dozens of agents.
func TestExtractTasksCaps(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 40; i++ {
		b.WriteString("- task number ")
		b.WriteString(string(rune('a' + i%26)))
		b.WriteString(" of many\n")
	}
	if got := len(ExtractTasks(b.String())); got > maxTasks {
		t.Errorf("extracted %d tasks, want at most %d", got, maxTasks)
	}
}

// TestBranchNameFor pins the branch names children get.
func TestBranchNameFor(t *testing.T) {
	cases := map[string]string{
		"Add a health endpoint":     "agent/add-a-health-endpoint",
		"Fix the parser!! (urgent)": "agent/fix-the-parser-urgent",
		"   ":                       "agent/task",
		"a very long task description that keeps going and going": "agent/a-very-long-task-description",
		// Exactly the length the name is allowed, and every word of it whole.
		// Backing up from a cut that never happened dropped the last word.
		"abcd efgh ijkl mnop qrst uvwx yz": "agent/abcd-efgh-ijkl-mnop-qrst-uvwx-yz",
	}
	for in, want := range cases {
		got := BranchNameFor(in)
		if got != want {
			t.Errorf("BranchNameFor(%q) = %q, want %q", in, got, want)
		}
		if strings.HasSuffix(got, "-") || strings.Contains(got, "--") {
			t.Errorf("BranchNameFor(%q) = %q is not a tidy branch name", in, got)
		}
		// A branch name cut mid-word reads badly in `git branch`.
		if len(got) > 40 {
			t.Errorf("BranchNameFor(%q) = %q is too long", in, got)
		}
	}
}

// TestSpawnPlacesAChild covers starting a helper beside its parent and in a
// tab of its own.
func TestSpawnPlacesAChild(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	ws.NewTab(session.KindShell, root, "lead")

	parentTab := ws.CurrentTab()
	parent := parentTab.Focus

	// A child in a tab of its own.
	id, err := ws.Spawn(parent, SpawnOptions{Task: "do a thing", Kind: session.KindShell})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if ws.Pane(id) == nil {
		t.Fatal("the child pane was not registered")
	}
	if len(ws.VisibleTabs()) != 2 {
		t.Errorf("tabs = %d, want the child in its own tab", len(ws.VisibleTabs()))
	}

	// A child beside its parent.
	sibling, err := ws.Spawn(parent, SpawnOptions{Task: "another thing", Kind: session.KindShell, Split: true})
	if err != nil {
		t.Fatalf("spawn split: %v", err)
	}
	if parentTab.Tree.Find(sibling) == nil {
		t.Error("a split child should share its parent's tab")
	}
	if n := len(parentTab.Tree.Panes()); n != 2 {
		t.Errorf("parent tab holds %d panes, want 2", n)
	}
}

// TestSpawnGathersChildrenIntoOneTab covers the placement a fan-out uses: the
// first child makes a tab, the rest are told to join it, and the tab is a grid
// rather than each new pane halving the last.
func TestSpawnGathersChildrenIntoOneTab(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	ws.NewTab(session.KindShell, root, "lead")
	parent := ws.CurrentTab().Focus

	first, err := ws.Spawn(parent, SpawnOptions{Task: "task 1", Kind: session.KindShell, Title: "Fan out"})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	shared := ws.TabIDOf(first)
	if shared == "" {
		t.Fatal("the first child is not in a tab")
	}
	for i := 2; i <= 6; i++ {
		id, err := ws.Spawn(parent, SpawnOptions{
			Task: fmt.Sprintf("task %d", i),
			Kind: session.KindShell,
			Tab:  shared,
		})
		if err != nil {
			t.Fatalf("spawn %d: %v", i, err)
		}
		if got := ws.TabIDOf(id); got != shared {
			t.Fatalf("child %d landed in tab %q, want the one its siblings are in", i, got)
		}
	}

	if n := len(ws.VisibleTabs()); n != 2 {
		t.Errorf("tabs = %d, want the lead's and the one the children share", n)
	}
	tab := ws.Tab(shared)
	if n := len(tab.Tree.Panes()); n != 6 {
		t.Errorf("the shared tab holds %d panes, want all six", n)
	}
	if tab.Title != "Fan out" {
		t.Errorf("the shared tab is called %q, want the name the first child brought", tab.Title)
	}

	// Six panes are two rows of three, all the same size — the point of
	// gathering them being that you can watch them.
	tab.Tree.Compute(layout.Rect{W: 900, H: 900})
	for _, l := range tab.Tree.Leaves() {
		if r := l.Rect(); r.W < 290 || r.W > 300 || r.H != 450 {
			t.Errorf("a child came out %dx%d, want a sixth of the tab", r.W, r.H)
		}
	}
}

// TestSpawnFallsBackWhenTheTabIsGone covers a fan-out whose shared tab was
// closed while it was still starting agents: the child still gets a pane,
// in a tab of its own, rather than being lost.
func TestSpawnFallsBackWhenTheTabIsGone(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	ws.NewTab(session.KindShell, root, "lead")

	id, err := ws.Spawn(ws.CurrentTab().Focus, SpawnOptions{
		Task: "carry on regardless",
		Kind: session.KindShell,
		Tab:  "a tab that closed",
	})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if ws.TabIDOf(id) == "" {
		t.Error("the child is not on screen at all")
	}
	if n := len(ws.VisibleTabs()); n != 2 {
		t.Errorf("tabs = %d, want the child to have fallen back to one of its own", n)
	}
}

// TestSpawnRequiresATask guards the case where nothing was asked for.
func TestSpawnRequiresATask(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	ws.NewTab(session.KindShell, root, "lead")

	if _, err := ws.Spawn(ws.CurrentTab().Focus, SpawnOptions{Kind: session.KindClaude}); err == nil {
		t.Error("spawning an agent with no task should fail")
	}
}

// TestPrepareWorktreeIsolatesChildren is what makes parallel children safe:
// each gets its own checkout, and asking twice reuses the same one.
func TestPrepareWorktreeIsolatesChildren(t *testing.T) {
	isolateConfig(t)
	repo := t.TempDir()
	for _, args := range [][]string{
		{"init", "--initial-branch=main"},
		{"config", "user.email", "t@e.com"},
		{"config", "user.name", "T"},
		{"config", "commit.gpgsign", "false"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git %v: %v: %s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "f.txt"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-m", "init"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git %v: %v: %s", args, err, out)
		}
	}

	ws := newTestWorkspace(t, repo)
	branch := BranchNameFor("add a health endpoint")

	path, err := ws.PrepareWorktree(repo, branch)
	if err != nil {
		t.Fatalf("prepare worktree: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(path) })

	if fi, err := os.Stat(path); err != nil || !fi.IsDir() {
		t.Fatalf("worktree directory missing: %v", err)
	}
	if path == repo {
		t.Error("the child should not share the main checkout")
	}

	// Asking again for the same branch reuses the worktree rather than failing.
	again, err := ws.PrepareWorktree(repo, branch)
	if err != nil {
		t.Fatalf("second prepare: %v", err)
	}
	if filepath.Clean(again) != filepath.Clean(path) {
		t.Errorf("second call gave %q, want the existing %q", again, path)
	}

	// The branch the repository itself is on is the one case where reuse would
	// defeat the point: the child would land in the user's own checkout.
	shared, err := ws.PrepareWorktree(repo, "main")
	if err == nil {
		t.Errorf("preparing the checked-out branch gave %q, want a refusal", shared)
	} else if !strings.Contains(err.Error(), "main") {
		t.Errorf("error = %q, want it to name the branch", err)
	}
}

// TestExtractTasksReadsNumberedForms covers the ways an agent numbers a plan.
// A dash after the number is usually written with a space before it, which is
// the form a bare separator match misses.
func TestExtractTasksReadsNumberedForms(t *testing.T) {
	plan := `
1. Add a health endpoint to the server
2) Write tests for the config parser
3 - Replace the deprecated logging calls
4: Update the README with the new flags
`
	got := ExtractTasks(plan)
	want := []string{
		"Add a health endpoint to the server",
		"Write tests for the config parser",
		"Replace the deprecated logging calls",
		"Update the README with the new flags",
	}
	if len(got) != len(want) {
		t.Fatalf("extracted %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("task %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestExtractTasksKeepsCountsOutOfTasks guards the other side of that: a line
// that opens with a number and a space is a sentence, not a numbered entry.
func TestExtractTasksKeepsCountsOutOfTasks(t *testing.T) {
	if got := ExtractTasks("12 files still need the new header\n"); len(got) != 0 {
		t.Errorf("extracted %#v, want nothing", got)
	}
}

// TestExtractTasksKeepsLongWrappedItems covers a bullet long enough that its
// wrapped tail would carry it past the cap. isTask discards anything over the
// cap whole, so joining blindly cost the plan a real job instead of its tail.
func TestExtractTasksKeepsLongWrappedItems(t *testing.T) {
	head := "Rewrite the pane renderer " + strings.Repeat("and tidy it up ", 36)
	if n := len([]rune(head)); n > maxTaskRunes {
		t.Fatalf("the test's first row is %d runes, already over the cap", n)
	}
	screen := "- " + head + "\n  " + strings.Repeat("a continuation row ", 8) + "\n"

	got := ExtractTasks(screen)
	if len(got) != 1 {
		t.Fatalf("extracted %#v, want the one task", got)
	}
	if !strings.HasPrefix(got[0], "Rewrite the pane renderer") {
		t.Errorf("task = %q, want the bullet itself", got[0])
	}
	if strings.Contains(got[0], "a continuation row") {
		t.Errorf("the tail was joined past the cap: %q", got[0])
	}
}

// TestExtractTasksKeepsKeyNamesInTasks covers the cost of matching Claude
// Code's key hints anywhere in a line: this application's own plans are about
// keyboard shortcuts, and a task naming one is not a hint.
func TestExtractTasksKeepsKeyNamesInTasks(t *testing.T) {
	plan := `
- Add a ctrl+k command palette to the web interface
- Make shift+tab move focus backwards through the panes
• ctrl+o to expand thinking
• shift+tab to cycle permission modes
`
	got := ExtractTasks(plan)
	want := []string{
		"Add a ctrl+k command palette to the web interface",
		"Make shift+tab move focus backwards through the panes",
	}
	if len(got) != len(want) {
		t.Fatalf("extracted %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("task %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestExtractTasksReadsCheckboxes covers a task list written as checkboxes. The
// tick is written several ways, and an entry left holding its box opens on
// punctuation, which isTask reads as a fragment and drops.
func TestExtractTasksReadsCheckboxes(t *testing.T) {
	plan := `
- [ ] Remove the unused helpers
- [x] Add a health endpoint to the server
- [X] Write tests for the config parser
* [-] Update the README with the new flags
`
	got := ExtractTasks(plan)
	want := []string{
		"Remove the unused helpers",
		"Add a health endpoint to the server",
		"Write tests for the config parser",
		"Update the README with the new flags",
	}
	if len(got) != len(want) {
		t.Fatalf("extracted %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("task %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestExtractTasksSkipsTabIndentedDetail is the nesting rule again, written the
// other way a markdown list is indented. Counting bytes put a tabbed sub-item
// one column in, which reads as alignment drift rather than as nesting.
func TestExtractTasksSkipsTabIndentedDetail(t *testing.T) {
	plan := "1. Add a tooltip layer to the web interface\n" +
		"\t- app.js: one delegated listener\n" +
		"\t- app.css: the bubble itself\n" +
		"2. Write tests for the tooltip layer\n"
	got := ExtractTasks(plan)
	want := []string{
		"Add a tooltip layer to the web interface",
		"Write tests for the tooltip layer",
	}
	if len(got) != len(want) {
		t.Fatalf("extracted %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("task %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestExtractTasksDropsSingleMarkEmphasis covers emphasis written with one mark
// rather than two. Left on the front of an entry it opens the task on
// punctuation, which isTask reads as a fragment of a wrapped line.
func TestExtractTasksDropsSingleMarkEmphasis(t *testing.T) {
	plan := `
- *Add a delegated pointerover listener to app.js*
- _Style the tooltip bubble in app.css_
- __Write tests for the tooltip layer__
`
	got := ExtractTasks(plan)
	want := []string{
		"Add a delegated pointerover listener to app.js",
		"Style the tooltip bubble in app.css",
		"Write tests for the tooltip layer",
	}
	if len(got) != len(want) {
		t.Fatalf("extracted %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("task %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestExtractTasksIgnoresATrailingHeading covers the shape a reply usually ends
// on: the plan, and then one more heading over a closing sentence. Judging by
// the last cue alone fell back to every bullet in the answer, which handed the
// findings back as work.
func TestExtractTasksIgnoresATrailingHeading(t *testing.T) {
	reply := `
I read the file. What is there today:

- Tooltips are set with title=, which screen readers ignore
- Status colours are hard-coded in three places

Here's the plan:

- Add a delegated pointerover listener to app.js
- Style the tooltip bubble in app.css

Next steps:

Tell me which of those to start with.
`
	got := ExtractTasks(reply)
	want := []string{
		"Add a delegated pointerover listener to app.js",
		"Style the tooltip bubble in app.css",
	}
	if len(got) != len(want) {
		t.Fatalf("extracted %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("task %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestExtractTasksHandlesATruncatedCodeBlock covers the screen fallback. A
// pane's output is read from the tail, so it can begin part way through a code
// block; taking that block's closing fence for an opening one hid every task
// after it, which is the whole plan.
func TestExtractTasksHandlesATruncatedCodeBlock(t *testing.T) {
	screen := "  listen: 0.0.0.0\n" +
		"  port: 8080\n" +
		"```\n\n" +
		"- Apply that config to the staging cluster\n" +
		"- Roll the change forward to production\n"
	got := ExtractTasks(screen)
	want := []string{
		"Apply that config to the staging cluster",
		"Roll the change forward to production",
	}
	if len(got) != len(want) {
		t.Fatalf("extracted %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("task %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestExtractTasksReadsRedrawnRows covers the screen fallback on a real
// terminal. A row is updated in place with a carriage return rather than a
// newline, so the status line and the bullet that replaced it arrive as one
// line, and the bullet is invisible to anything splitting on newlines alone.
func TestExtractTasksReadsRedrawnRows(t *testing.T) {
	screen := "\u00b7 Perambulating\u2026 (2s\r\u00b7 Perambulating\u2026 (3s\r" +
		"- Add the delegated listener to app.js\r\n" +
		"- Style the tooltip bubble in app.css\r\n"
	got := ExtractTasks(screen)
	want := []string{
		"Add the delegated listener to app.js",
		"Style the tooltip bubble in app.css",
	}
	if len(got) != len(want) {
		t.Fatalf("extracted %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("task %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestExtractTasksKeepsWrappedItemsWholeOnCRLF is the wrapped-bullet case with
// the line endings a Windows terminal actually produces.
func TestExtractTasksKeepsWrappedItemsWholeOnCRLF(t *testing.T) {
	screen := "  - One delegated pointerover listener on document. On hover of\r\n" +
		"    an element carrying data-tip, position the bubble and show it.\r\n" +
		"  - A second, shorter task\r\n"
	got := ExtractTasks(screen)
	if len(got) != 2 {
		t.Fatalf("extracted %#v, want 2 tasks", got)
	}
	if !strings.HasSuffix(got[0], "position the bubble and show it.") {
		t.Errorf("the wrapped tail was lost: %q", got[0])
	}
}

// TestExtractTasksDropsBulletedHeadings covers a plan that bullets its own
// headings. "Next steps" reads as a list entry and clears every test isTask
// applies, but handed to an agent as its whole brief it says nothing.
func TestExtractTasksDropsBulletedHeadings(t *testing.T) {
	plan := `
- Next steps
- Add a delegated pointerover listener to app.js
- **Proposed work**
- Style the tooltip bubble in app.css
`
	got := ExtractTasks(plan)
	want := []string{
		"Add a delegated pointerover listener to app.js",
		"Style the tooltip bubble in app.css",
	}
	if len(got) != len(want) {
		t.Fatalf("extracted %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("task %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// Agents decorate their lists. A tick, a spanner, a warning triangle at the
// head of every entry is ornament rather than part of the job, and it used to
// cost the fan-out not some of the plan but all of it: the symbol makes the
// entry open on something that is neither a letter nor a digit, which reads as
// the middle of a wrapped line, and every item was thrown away.
func TestExtractTasksReadsDecoratedBullets(t *testing.T) {
	plan := `Here is the plan:

- ✅ Add the health endpoint to the HTTP server
- 🔧 Wire up the config loader
- ⚠️ Check the error path in the reconnect loop
`
	got := ExtractTasks(plan)
	want := []string{
		"Add the health endpoint to the HTTP server",
		"Wire up the config loader",
		"Check the error path in the reconnect loop",
	}
	if len(got) != len(want) {
		t.Fatalf("extracted %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("task %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// A heading is a heading whether or not it has been dressed up, and one handed
// to an agent as its whole brief says nothing at all.
func TestExtractTasksDropsDecoratedHeadings(t *testing.T) {
	plan := `
- 📋 Plan
- ✅ Rename the extractor and its tests
`
	got := ExtractTasks(plan)
	if len(got) != 1 || got[0] != "Rename the extractor and its tests" {
		t.Errorf("extracted %#v, want only the task", got)
	}
}

// The backtick is a symbol too, and an entry naming a function in backticks is
// a task like any other.
func TestExtractTasksKeepsBacktickedNames(t *testing.T) {
	plan := `
Here is the plan:
- ` + "`" + `listItem` + "`" + ` should take the whole line, not the body
`
	got := ExtractTasks(plan)
	want := "`listItem` should take the whole line, not the body"
	if len(got) != 1 || got[0] != want {
		t.Errorf("extracted %#v, want [%q]", got, want)
	}
}

// An agent numbering its plan writes "(1)" as readily as "1." or "1)", and
// numbers its steps in words as readily as with a marker at all. A plan
// written in a form the reader does not know goes through as no plan.
func TestExtractTasksReadsBracketedAndLabelledForms(t *testing.T) {
	cases := map[string]string{
		"bracketed": `Here is the plan:
(1) Split the router into three files
(2) Add a timeout to the control socket
(3) Cover the reconnect path with a test`,
		"labelled": `Here is the plan:
Task 1: Split the router into three files
Task 2: Add a timeout to the control socket
Step 3 — Cover the reconnect path with a test`,
	}
	want := []string{
		"Split the router into three files",
		"Add a timeout to the control socket",
		"Cover the reconnect path with a test",
	}
	for name, plan := range cases {
		got := ExtractTasks(plan)
		if len(got) != len(want) {
			t.Errorf("%s: extracted %#v, want %#v", name, got, want)
			continue
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("%s: task %d = %q, want %q", name, i, got[i], want[i])
			}
		}
	}
}

// The label has to carry a number. Without one, an ordinary sentence opening
// on the word would be read as an instruction with its first word missing.
func TestExtractTasksIgnoresAnUnnumberedLabel(t *testing.T) {
	plan := `Here is the plan:
Step through the reconnect path in the debugger
- Add a timeout to the control socket`
	got := ExtractTasks(plan)
	if len(got) != 1 || got[0] != "Add a timeout to the control socket" {
		t.Errorf("extracted %#v, want only the bulleted task", got)
	}
}

// A bracket is only a list marker when it closes the way a bracket does.
func TestExtractTasksIgnoresAHalfBracketedNumber(t *testing.T) {
	plan := `Here is the plan:
(1. this is not a numbered list entry at all
- Add a timeout to the control socket`
	got := ExtractTasks(plan)
	if len(got) != 1 || got[0] != "Add a timeout to the control socket" {
		t.Errorf("extracted %#v, want only the bulleted task", got)
	}
}

// A task written in any other script used to reduce to nothing, so every
// branch of the fan-out was the fallback name and the branches told the user
// which agent was which only by their numbering.
func TestBranchNameForOtherScripts(t *testing.T) {
	cases := []struct {
		task string
		want string
	}{
		{"Добавить проверку", "agent/добавить-проверку"},
		{"設定を読み込む", "agent/設定を読み込む"},
		{"Ajouter une vérification", "agent/ajouter-une-vérification"},
		// Only when there is genuinely nothing to name it after.
		{"!!! ??? ...", "agent/task"},
	}
	for _, c := range cases {
		if got := BranchNameFor(c.task); got != c.want {
			t.Errorf("BranchNameFor(%q) = %q, want %q", c.task, got, c.want)
		}
	}
}

// The length limit is in bytes, and a cut that lands inside a character hands
// git a ref that is not UTF-8 — which it refuses, taking the agent with it.
func TestBranchNameForCutsOnACharacter(t *testing.T) {
	for _, task := range []string{
		strings.Repeat("настройка", 8),
		strings.Repeat("設定", 20),
		strings.Repeat("é", 60),
		strings.Repeat("a", 60),
		"проверка настройки конфигурации приложения и его окружения",
	} {
		got := BranchNameFor(task)
		if !utf8.ValidString(got) {
			t.Errorf("BranchNameFor(%.20q…) = %q, which is not valid UTF-8", task, got)
		}
		if strings.HasSuffix(got, "-") || strings.Contains(got, "--") {
			t.Errorf("BranchNameFor(%.20q…) = %q is not a tidy branch name", task, got)
		}
	}
}

// Chinese and Japanese put no spaces between their words, so a whole sentence
// in either is one field. Reading that as a single word — a spinner frame, or
// the tail of a wrapped row — cost those plans not some of their items but all
// of them.
func TestExtractTasksReadsPlansWithoutSpaces(t *testing.T) {
	cases := map[string][]string{
		"japanese": {
			"ルーターを三つのファイルに分割する",
			"コントロールソケットにタイムアウトを追加する",
			"再接続経路のテストを追加する",
		},
		"chinese": {
			"把路由拆分成三个文件",
			"给控制套接字加上超时",
			"为重连路径补测试",
		},
	}
	plans := map[string]string{
		"japanese": "計画は次のとおりです:\n\n- ルーターを三つのファイルに分割する\n" +
			"- コントロールソケットにタイムアウトを追加する\n- 再接続経路のテストを追加する\n",
		"chinese": "计划如下:\n\n- 把路由拆分成三个文件\n" +
			"- 给控制套接字加上超时\n- 为重连路径补测试\n",
	}
	for name, want := range cases {
		got := ExtractTasks(plans[name])
		if len(got) != len(want) {
			t.Errorf("%s: extracted %#v, want %#v", name, got, want)
			continue
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("%s: task %d = %q, want %q", name, i, got[i], want[i])
			}
		}
	}
}

// The one-word rule still does its job everywhere it was doing it: a lone word
// in a language written with spaces is not a task.
func TestExtractTasksStillRefusesASingleWord(t *testing.T) {
	plan := "Here is the plan:\n- Perambulating\n- Add a timeout to the control socket\n"
	got := ExtractTasks(plan)
	if len(got) != 1 || got[0] != "Add a timeout to the control socket" {
		t.Errorf("extracted %#v, want only the real task", got)
	}
}

// A longer plan is often written as numbered headings, a paragraph under each,
// or as numbered lines in bold. Neither began with a list marker, so the
// extractor offered nothing for a plan it could have run as it stood.
func TestExtractTasksReadsNumberedHeadings(t *testing.T) {
	want := []string{"Split the router into its own package", "Add a timeout to every outbound request"}
	for _, plan := range []string{
		"## Plan\n\n### 1. Split the router into its own package\nIt has grown too big.\n\n### 2. Add a timeout to every outbound request\nNone has one.\n",
		"## Step 1: Split the router into its own package\n\n## Step 2: Add a timeout to every outbound request\n",
		"#### Task 1 — Split the router into its own package\n#### Task 2 — Add a timeout to every outbound request\n",
		"**1. Split the router into its own package**\n\n**2. Add a timeout to every outbound request**\n",
	} {
		got := ExtractTasks(plan)
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("ExtractTasks(%q) = %q, want %q", plan, got, want)
		}
	}
	// A heading with no number is still a heading and not a job.
	if got := ExtractTasks("## Overview of the router\n\n## Risks in the timeout work\n"); len(got) != 0 {
		t.Errorf("plain headings were read as tasks: %q", got)
	}
}

// The task becomes a command-line argument. Windows refuses a command line
// past about thirty-two thousand characters, and what it says about that names
// neither the task nor its length — so the length is checked here, where there
// is something useful to say about it.
func TestSpawnRefusesATaskTooLongToStartWith(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	ws.NewTab(session.KindShell, root, "lead")
	parent := ws.CurrentTab().Focus

	_, err := ws.Spawn(parent, SpawnOptions{
		Task: strings.Repeat("a", maxTaskBytes+1),
		Kind: session.KindShell,
	})
	if err == nil {
		t.Fatal("a task too long for a command line was accepted")
	}
	if !strings.Contains(err.Error(), "characters") {
		t.Errorf("err = %v, want it to name the length", err)
	}
	// The length is what the writer can count, which is characters rather
	// than bytes in any script but ASCII, and the refusal says what to do.
	accented := strings.Repeat("é", maxTaskBytes/2+1)
	_, err = ws.Spawn(parent, SpawnOptions{Task: accented, Kind: session.KindShell})
	if err == nil {
		t.Fatal("a task too long for a command line was accepted")
	}
	if want := fmt.Sprint(utf8.RuneCountInString(accented)); !strings.Contains(err.Error(), want+" characters") {
		t.Errorf("err = %v, want it to count %s characters", err, want)
	}
	if !strings.Contains(err.Error(), "file") {
		t.Errorf("err = %v, want it to say what to do with a brief this long", err)
	}
	// Nothing may be left behind by a spawn that was refused.
	if n := len(ws.VisibleTabs()); n != 1 {
		t.Errorf("tabs = %d, want the refused child to have opened none", n)
	}

	// A brief right up to the limit is still a brief.
	if _, err := ws.Spawn(parent, SpawnOptions{
		Task: strings.Repeat("a", maxTaskBytes),
		Kind: session.KindShell,
	}); err != nil {
		t.Errorf("a task of exactly the limit was refused: %v", err)
	}
}

// A pane names the agent it wants, and the empty name is the default agent
// rather than no agent at all — which is what keeps a fan-out that was never
// asked about agents running exactly what it always ran.
func TestAgentSpecResolvesByID(t *testing.T) {
	isolateConfig(t)
	ws := newTestWorkspace(t, t.TempDir())

	cases := []struct {
		name    string
		id      string
		want    string
		unknown bool
	}{
		{name: "the default", id: "", want: agent.DefaultAgentID},
		{name: "by name", id: "claude", want: "claude"},
		{name: "one nobody has heard of", id: "nosuch", unknown: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			spec, err := ws.AgentSpec(c.id)
			if c.unknown {
				// Whether the machine has an agent installed is a different
				// question from whether Flockdeck knows of it, and only the second
				// one leaves nothing to report about.
				if spec.ID != "" {
					t.Errorf("resolved %q to %q, want nothing", c.id, spec.ID)
				}
				if err == nil || !strings.Contains(err.Error(), c.id) {
					t.Errorf("err = %v, want it to name %q", err, c.id)
				}
				return
			}
			if spec.ID != c.want {
				t.Errorf("resolved %q to %q, want %q", c.id, spec.ID, c.want)
			}
			// The error here is only whether this machine has the CLI, which
			// is not what the resolution is being asked about.
			if err != nil && !strings.Contains(err.Error(), "not found") {
				t.Errorf("err = %v, want either nothing or a missing CLI", err)
			}
		})
	}
}

// The agent is checked before anything is created, so a name that is wrong
// costs a message rather than a tab with a dead pane in it.
func TestSpawnRefusesAnUnknownAgent(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	ws.NewTab(session.KindShell, root, "lead")
	parent := ws.CurrentTab().Focus

	_, err := ws.Spawn(parent, SpawnOptions{
		Task:  "split the router",
		Kind:  session.KindClaude,
		Agent: "nosuch",
	})
	if err == nil {
		t.Fatal("a spawn naming an agent that does not exist was accepted")
	}
	if !strings.Contains(err.Error(), "nosuch") {
		t.Errorf("err = %v, want it to name the agent", err)
	}
	if n := len(ws.VisibleTabs()); n != 1 {
		t.Errorf("tabs = %d, want the refused child to have opened none", n)
	}
}

// A shell pane runs no agent, so it is never held up over one.
func TestSpawnAShellIsNotAskedAboutAgents(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	ws.NewTab(session.KindShell, root, "lead")
	parent := ws.CurrentTab().Focus

	if _, err := ws.Spawn(parent, SpawnOptions{Kind: session.KindShell, Agent: "nosuch"}); err != nil {
		t.Errorf("a shell pane was refused over an agent it does not run: %v", err)
	}
}

// The catalog is what the fan-out dialog and the picker offer, so it has to
// hold the agent Flockdeck has always run and say which one is taken by default.
func TestAgentCatalogHoldsTheDefault(t *testing.T) {
	isolateConfig(t)
	ws := newTestWorkspace(t, t.TempDir())

	specs, def := ws.Agents()
	if len(specs) == 0 {
		t.Fatal("the catalog is empty")
	}
	found := false
	for _, spec := range specs {
		if spec.ID == def {
			found = true
		}
		if spec.Name == "" {
			t.Errorf("agent %q has no name to show", spec.ID)
		}
	}
	if !found {
		t.Errorf("the default agent %q is not in the catalog", def)
	}
}
