package workspace

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmwri/agent-wrapper/internal/session"
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

// TestFanOutCapCountsStartedAgents covers the cap over a list a user has been
// editing. Blank rows are left behind by that editing, and they must not be
// counted against the agents the cap is there to limit.
func TestFanOutCapCountsStartedAgents(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	ws.NewTab(session.KindShell, root, "lead")
	parent := ws.CurrentTab().Focus

	var tasks []string
	for i := 0; i < maxTasks; i++ {
		tasks = append(tasks, "", fmt.Sprintf("do the %dth thing", i))
	}
	made, errs := ws.FanOut(parent, tasks, SpawnOptions{Kind: session.KindShell})
	if len(made) != maxTasks {
		t.Errorf("started %d agents, want the full %d", len(made), maxTasks)
	}
	if len(errs) != 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
}

// TestFanOutStopsAtTheCap is the other half: past the cap the extra tasks are
// refused, and the caller is told rather than left to count panes.
func TestFanOutStopsAtTheCap(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	ws.NewTab(session.KindShell, root, "lead")
	parent := ws.CurrentTab().Focus

	var tasks []string
	for i := 0; i < maxTasks+3; i++ {
		tasks = append(tasks, fmt.Sprintf("do the %dth thing", i))
	}
	made, errs := ws.FanOut(parent, tasks, SpawnOptions{Kind: session.KindShell})
	if len(made) != maxTasks {
		t.Errorf("started %d agents, want %d", len(made), maxTasks)
	}
	if len(errs) != 1 {
		t.Fatalf("errors = %v, want the one that says it stopped", errs)
	}
	if !strings.Contains(errs[0].Error(), "stopped after") {
		t.Errorf("error = %q, want it to say the fan-out stopped", errs[0])
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
