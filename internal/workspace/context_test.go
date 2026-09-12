package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/help"
	"github.com/jmwri/flockdeck/internal/layout"
	"github.com/jmwri/flockdeck/internal/session"
)

// TestPaneContextIdentifiesThePane checks the basics an agent cannot work out
// for itself: which pane it is, which tab and project it sits in, and where it
// is working.
func TestPaneContextIdentifiesThePane(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	ws.NewTab(session.KindShell, root, "lead")

	tab := ws.CurrentTab()
	c, ok := ws.PaneContext(tab.Focus)
	if !ok {
		t.Fatal("no context for the focused pane")
	}
	if c.Tab != "lead" {
		t.Errorf("tab = %q, want lead", c.Tab)
	}
	if c.ProjectRoot != root {
		t.Errorf("project root = %q, want %q", c.ProjectRoot, root)
	}
	if c.Cwd != root {
		t.Errorf("cwd = %q, want %q", c.Cwd, root)
	}
	if c.Worktree {
		t.Error("a pane in the project root is not in a worktree of its own")
	}
	if len(c.Siblings) != 0 {
		t.Errorf("siblings = %#v, want none", c.Siblings)
	}

	text := c.Render()
	for _, want := range []string{"Flockdeck", `"lead"`, root} {
		if !strings.Contains(text, want) {
			t.Errorf("rendered context does not mention %q:\n%s", want, text)
		}
	}
}

// TestPaneContextListsSiblings covers the point of the whole thing: an agent
// is told which other agents are running beside it and where they are working,
// which is what stops two of them assuming they have the repository to
// themselves.
func TestPaneContextListsSiblings(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	ws.NewTab(session.KindShell, root, "lead")

	parent := ws.CurrentTab().Focus
	if _, err := ws.Spawn(parent, SpawnOptions{
		Task: "repair the token refresh",
		Kind: session.KindShell,
	}); err != nil {
		t.Fatalf("spawn: %v", err)
	}

	c, ok := ws.PaneContext(parent)
	if !ok {
		t.Fatal("no context for the parent pane")
	}
	if len(c.Siblings) != 1 {
		t.Fatalf("siblings = %d, want the spawned child", len(c.Siblings))
	}
	if got := c.Siblings[0].Task; got != "repair the token refresh" {
		t.Errorf("sibling task = %q", got)
	}

	text := c.Render()
	if !strings.Contains(text, "repair the token refresh") {
		t.Errorf("the child's task is missing from the context:\n%s", text)
	}
	if !strings.Contains(text, "cannot see") {
		t.Errorf("the context should say the conversations are separate:\n%s", text)
	}
}

// TestPaneContextExcludesItselfAndTheDead keeps the list to agents that are
// actually there to collide with.
func TestPaneContextExcludesItselfAndTheDead(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	ws.NewTab(session.KindShell, root, "lead")

	parent := ws.CurrentTab().Focus
	child, err := ws.Spawn(parent, SpawnOptions{Task: "a thing", Kind: session.KindShell})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if c, _ := ws.PaneContext(parent); len(c.Siblings) != 1 {
		t.Fatalf("siblings = %#v, want the live child", c.Siblings)
	}

	p := ws.Pane(child)
	if p == nil || p.Sess == nil {
		t.Fatal("the child has no session")
	}
	_ = p.Sess.Close()
	// The process is reaped on its own goroutine, so the status follows the
	// kill rather than accompanying it.
	deadline := time.Now().Add(5 * time.Second)
	for !p.Sess.Exited() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	c, _ := ws.PaneContext(parent)
	if len(c.Siblings) != 0 {
		t.Errorf("siblings = %#v, want the exited child left out", c.Siblings)
	}
	if _, ok := ws.PaneContext(parent); !ok {
		t.Error("the surviving pane should still have a context of its own")
	}
}

// TestPaneContextFlagsAWorktree checks that a fan-out child is told its
// directory is a checkout of its own, since that is what makes it safe for it
// to edit files while other agents do the same elsewhere.
func TestPaneContextFlagsAWorktree(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	ws.NewTab(session.KindShell, root, "lead")

	other := filepath.Join(t.TempDir(), "fix-auth")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatalf("make worktree dir: %v", err)
	}
	id, err := ws.Spawn(ws.CurrentTab().Focus, SpawnOptions{
		Task: "repair the token refresh",
		Cwd:  other,
		Kind: session.KindShell,
	})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}

	c, ok := ws.PaneContext(id)
	if !ok {
		t.Fatal("no context for the child")
	}
	if !c.Worktree {
		t.Fatalf("cwd %q under project %q should read as a separate checkout", c.Cwd, c.ProjectRoot)
	}
	text := c.Render()
	if !strings.Contains(text, "separate git worktree") {
		t.Errorf("the context should say the pane has its own checkout:\n%s", text)
	}
	if !strings.Contains(text, "repair the token refresh") {
		t.Errorf("a spawned pane should be told what it was started for:\n%s", text)
	}
}

// TestPaneContextUnknownPane guards the race where a hook arrives for a pane
// that has already been closed.
func TestPaneContextUnknownPane(t *testing.T) {
	isolateConfig(t)
	ws := newTestWorkspace(t, t.TempDir())
	if _, ok := ws.PaneContext("no-such-pane"); ok {
		t.Error("an unknown pane should not produce a context")
	}
}

// TestOneLineShortensAPastedWall keeps a long opening prompt from becoming the
// bulk of what an agent is told.
func TestOneLineShortensAPastedWall(t *testing.T) {
	got := oneLine("first line\n\n   second   line\t"+strings.Repeat("x", 400), siblingTaskLimit)
	if strings.ContainsAny(got, "\n\t") {
		t.Errorf("oneLine left whitespace in %q", got)
	}
	if len([]rune(got)) > 170 {
		t.Errorf("oneLine returned %d characters, want it shortened", len([]rune(got)))
	}
}

// TestOneLineKeepsRunesWhole covers a task written in a non-ASCII script: the
// cut has to fall between runes, or the context ends in a mangled character.
func TestOneLineKeepsRunesWhole(t *testing.T) {
	got := oneLine(strings.Repeat("こんにちは", 80), siblingTaskLimit)
	if !utf8.ValidString(got) {
		t.Errorf("oneLine produced invalid UTF-8: %q", got)
	}
	if strings.ContainsRune(got, utf8.RuneError) {
		t.Errorf("oneLine cut a rune in half: %q", got)
	}
}

// TestPaneContextPutsTabMatesFirst checks the ordering the sibling limit
// relies on: panes sharing a tab usually share a checkout, so they are the
// ones that must survive when the list is cut short.
func TestPaneContextPutsTabMatesFirst(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	ws.NewTab(session.KindShell, root, "first")
	ws.NewTab(session.KindShell, root, "second")
	ws.SplitPane(layout.Vertical, session.KindShell)

	mine := ws.CurrentTab().Focus
	c, ok := ws.PaneContext(mine)
	if !ok {
		t.Fatal("no context for the focused pane")
	}
	if len(c.Siblings) != 2 {
		t.Fatalf("siblings = %#v, want the tab mate and the other tab", c.Siblings)
	}
	if c.Siblings[0].Tab != "second" {
		t.Errorf("first sibling is in tab %q, want the pane sharing this tab", c.Siblings[0].Tab)
	}
	if c.SiblingsOmitted != 0 {
		t.Errorf("omitted = %d, want none with only two siblings", c.SiblingsOmitted)
	}
}

// TestRenderSaysWhenSiblingsWereOmitted keeps a truncated list from reading as
// the whole picture, which would tell an agent it has the repository to
// itself when it does not.
func TestRenderSaysWhenSiblingsWereOmitted(t *testing.T) {
	c := PaneContext{
		PaneName:        "one",
		Siblings:        []Sibling{{Name: "two", Cwd: "/repo", Status: "working"}},
		SiblingsOmitted: 4,
	}
	if text := c.Render(); !strings.Contains(text, "4 more") {
		t.Errorf("the context does not say four panes were left out:\n%s", text)
	}
}

// TestTheBriefingSaysWhatBroadcastDoes covers what every agent is told about
// broadcast, and so what it tells a user who asks. It said broadcast mirrors
// what the user types, which it has never done: it decides where the prompt
// bar's one message goes, and typing into a pane reaches that pane alone. With
// it off the message still reaches the panes picked by hand, which stay picked.
func TestTheBriefingSaysWhatBroadcastDoes(t *testing.T) {
	text := PaneContext{PaneName: "one", Cwd: "/repo"}.Render()
	if strings.Contains(text, "mirrors what the user types") {
		t.Errorf("the briefing says broadcast mirrors typing:\n%s", text)
	}
	for _, want := range []string{"prompt bar", "added to the set by hand"} {
		if !strings.Contains(text, want) {
			t.Errorf("the briefing never says %q about broadcast:\n%s", want, text)
		}
	}
}

// TestTheBriefingDoesNotTellEveryAgentItHasClaudesSettings covers the agents
// briefed through a lifecycle hook, which are Claude Code and Flockdeck's own chat
// client. Both were told they had been started with a generated `--settings`
// file registering Claude Code's hooks, which the chat client never is.
func TestTheBriefingDoesNotTellEveryAgentItHasClaudesSettings(t *testing.T) {
	text := PaneContext{PaneName: "one", Cwd: "/repo"}.Render()
	if strings.Contains(text, "Every agent pane is started with a generated `--settings` file") {
		t.Errorf("the briefing tells every hooked agent it has Claude Code's settings file:\n%s", text)
	}
	for _, want := range []string{"Claude Code through the hooks", "chat client by itself"} {
		if !strings.Contains(text, want) {
			t.Errorf("the briefing never says %q:\n%s", want, text)
		}
	}
}

// TestBroadcastDefaultIsDroppedWhenBroadcastIsTurnedOff covers the selection
// nobody made: it describes the tab it was built from, so carrying it into
// the next tab would leave broadcast on with nothing to send to.
func TestBroadcastDefaultIsDroppedWhenBroadcastIsTurnedOff(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	tab := ws.NewTab(session.KindClaude, root, "first")

	ws.ToggleBroadcast()
	if !ws.InBroadcast(tab.Focus) {
		t.Fatal("turning broadcast on should select the Claude panes in the tab")
	}
	ws.ToggleBroadcast()
	if ws.InBroadcast(tab.Focus) {
		t.Error("a set filled in by default should not outlive broadcast being turned off")
	}
}

// TestBroadcastSelectionByHandSurvivesAToggle is the other half: once the user
// has picked the panes, they stay picked.
func TestBroadcastSelectionByHandSurvivesAToggle(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	tab := ws.NewTab(session.KindShell, root, "first")

	ws.ToggleBroadcastMember()
	ws.ToggleBroadcast()
	ws.ToggleBroadcast()
	if !ws.InBroadcast(tab.Focus) {
		t.Error("a pane the user added to the broadcast set should stay in it")
	}
}

// TestSummarisePromptStopsAtAWord checks the tab title a first prompt gives a
// tab: it is read at a glance, and a title that stops mid-word is harder to
// tell from its neighbours than one that stops after a word.
func TestSummarisePromptStopsAtAWord(t *testing.T) {
	got := summarisePrompt("I want you to spin off ten parallel agents")
	if strings.HasSuffix(got, "pa…") {
		t.Errorf("title %q breaks off inside a word", got)
	}
	if !strings.HasPrefix(got, "I want you to spin off") {
		t.Errorf("title %q lost the start of the prompt", got)
	}
	if n := len([]rune(got)); n > 29 {
		t.Errorf("title %q is %d characters, too wide for a tab", got, n)
	}

	// A single long word has no boundary to fall back on, so it is still cut.
	if got := summarisePrompt(strings.Repeat("x", 60)); len([]rune(got)) != 29 {
		t.Errorf("unbroken title = %q, want it cut at the limit", got)
	}
	if got := summarisePrompt("short enough"); got != "short enough" {
		t.Errorf("short title = %q, want it left alone", got)
	}
}

// TestSummarisePromptDropsMarkdown covers a first prompt written in markdown or
// opening on something pasted. A tab has room for a few words, and the marks
// that only mean anything rendered — a heading, a fence, a quote, a bullet and
// its box, bold — were spending them: "``` panic: runtime error:…".
func TestSummarisePromptDropsMarkdown(t *testing.T) {
	for prompt, want := range map[string]string{
		"## Task\nRefactor the router so handlers live in router.go": "Task Refactor the router so…",
		"```\npanic: runtime error: index out of range\n```\nwhy?":   "panic: runtime error: index…",
		"> the build fails on windows\ncan you look?":                "the build fails on windows…",
		"**Fix** the login bug on the settings page":                 "Fix the login bug on the…",
		"- [ ] add a timeout to the control socket":                  "add a timeout to the…",
		"* check the #123 regression":                                "check the #123 regression",
	} {
		if got := summarisePrompt(prompt); got != want {
			t.Errorf("summarisePrompt(%q) = %q, want %q", prompt, got, want)
		}
	}
}

// TestOpenProjectSaysWhatWentWrong keeps the two failures apart: a path that
// has been moved away and a path that is a file are fixed by different things.
func TestOpenProjectSaysWhatWentWrong(t *testing.T) {
	isolateConfig(t)
	dir := t.TempDir()
	ws := newTestWorkspace(t, dir)

	err := ws.OpenProject(filepath.Join(dir, "gone"))
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("opening a missing path gave %v, want it said so", err)
	}

	file := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(file, []byte("hi"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	err = ws.OpenProject(file)
	if err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("opening a file gave %v, want it said so", err)
	}
}

// TestTabTitleFromAPromptIsAppliedOnARead pins where the rename happens. The
// request arrives on the hook server's goroutine, which must not walk the tab
// list, so the tab changes when the interface next reads it instead.
func TestTabTitleFromAPromptIsAppliedOnARead(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	tab := ws.NewTab(session.KindClaude, root, "")
	if !tab.AutoTitle {
		t.Fatal("a tab named after its directory should be waiting for a prompt")
	}
	was := tab.Title

	ws.nameTabAfterPrompt(tab.Focus, "rewrite the parser")
	if tab.Title != was {
		t.Errorf("tab renamed to %q off the interface goroutine", tab.Title)
	}

	// Projects is the first thing the interface reads when it refreshes.
	ws.Projects()
	if tab.Title != "rewrite the parser" {
		t.Errorf("tab title = %q, want the prompt it was given", tab.Title)
	}
	if tab.AutoTitle {
		t.Error("a tab named after a prompt should not be renamed again")
	}
}

// TestSiblingsInTheSameCheckoutAreCalledOut checks the one line in the list
// that changes what an agent should do. Panes usually share a directory, so
// the path is the same on every line and the collision is easy to read past.
func TestSiblingsInTheSameCheckoutAreCalledOut(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	ws.NewTab(session.KindShell, root, "lead")

	other := filepath.Join(t.TempDir(), "fix-auth")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatalf("make worktree dir: %v", err)
	}
	parent := ws.CurrentTab().Focus
	if _, err := ws.Spawn(parent, SpawnOptions{Task: "beside you", Kind: session.KindShell}); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if _, err := ws.Spawn(parent, SpawnOptions{Task: "elsewhere", Cwd: other, Kind: session.KindShell}); err != nil {
		t.Fatalf("spawn: %v", err)
	}

	c, ok := ws.PaneContext(parent)
	if !ok || len(c.Siblings) != 2 {
		t.Fatalf("siblings = %#v, want both children", c.Siblings)
	}
	for _, sib := range c.Siblings {
		want := sib.Cwd == root
		if sib.SameCheckout != want {
			t.Errorf("sibling in %q: SameCheckout = %v, want %v", sib.Cwd, sib.SameCheckout, want)
		}
	}

	text := c.Render()
	if !strings.Contains(text, "same directory as you") {
		t.Errorf("the pane sharing the checkout is not called out:\n%s", text)
	}
	if !strings.Contains(text, other) {
		t.Errorf("the pane in its own checkout should still show its path:\n%s", text)
	}
}

// TestOpenProjectMatchesAnOpenProjectWhateverTheCase covers the same directory
// arriving spelled differently — from the command line, the recent list, or a
// drag onto the window — which used to open a second copy of a project that
// was already there.
func TestOpenProjectMatchesAnOpenProjectWhateverTheCase(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)

	shouted := strings.ToUpper(root)
	open, ok := ws.openRootFor(shouted)
	if !ok {
		t.Fatalf("openRootFor(%q) found nothing, want the open project", shouted)
	}
	if open != root {
		t.Errorf("openRootFor returned %q, want the spelling it was opened with", open)
	}

	// On a filesystem that ignores case, the whole path should behave.
	if _, err := os.Stat(shouted); err != nil {
		t.Skip("case-sensitive filesystem: nothing more to check")
	}
	if err := ws.OpenProject(shouted); err != nil {
		t.Fatalf("open project: %v", err)
	}
	if n := len(ws.Projects()); n != 1 {
		t.Errorf("projects = %d, want the one already open", n)
	}
	if ws.ActiveRoot() != root {
		t.Errorf("active root = %q, want %q", ws.ActiveRoot(), root)
	}
}

// TestPaneStartFailureNamesTheDirectory covers what the pane shows when there
// is no terminal to show: a worktree pulled out from under it is the usual
// cause, and the error from the process never says which directory it was.
func TestPaneStartFailureNamesTheDirectory(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)

	gone := filepath.Join(root, "worktree-that-went-away")
	tab := ws.NewTab(session.KindShell, gone, "")
	p := ws.Pane(tab.Focus)
	if p == nil {
		t.Fatal("no pane for the new tab")
	}
	if p.Err == nil {
		t.Skip("this platform starts a shell in a directory that is not there")
	}
	if !strings.Contains(p.Err.Error(), gone) {
		t.Errorf("pane error %q does not say which directory it tried", p.Err)
	}
}

// TestProjectsWithTheSameNameAreToldApart covers the project switcher, which
// shows the name and nothing else: two checkouts of one repository are both
// called the same thing, and picking between two identical labels is guesswork.
func TestProjectsWithTheSameNameAreToldApart(t *testing.T) {
	isolateConfig(t)
	first := filepath.Join(t.TempDir(), "checkout-a", "service")
	second := filepath.Join(t.TempDir(), "checkout-b", "service")
	for _, dir := range []string{first, second} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("make %s: %v", dir, err)
		}
	}

	ws := newTestWorkspace(t, first)
	if err := ws.OpenProject(second); err != nil {
		t.Fatalf("open: %v", err)
	}

	projects := ws.Projects()
	if len(projects) != 2 {
		t.Fatalf("projects = %d, want two", len(projects))
	}
	if projects[0].Name == projects[1].Name {
		t.Errorf("both projects are called %q", projects[0].Name)
	}
	for _, p := range projects {
		if !strings.HasSuffix(p.Name, "service") {
			t.Errorf("name %q no longer ends in the project directory", p.Name)
		}
	}

	// A name that stands on its own is left alone.
	lone := newTestWorkspace(t, first).Projects()
	if lone[0].Name != "service" {
		t.Errorf("name = %q, want the directory name on its own", lone[0].Name)
	}
}

// TestRenderDocumentsEverySpawnFlag keeps the one place an agent learns this
// command in step with the command itself: a flag it is never shown is a flag
// it will not use.
func TestRenderDocumentsEverySpawnFlag(t *testing.T) {
	text := PaneContext{PaneName: "one", CanSpawn: true}.Render()
	for _, flag := range []string{"--worktree", "--split", "--shell"} {
		if !strings.Contains(text, flag) {
			t.Errorf("the spawn section does not mention %s", flag)
		}
	}
	// Naming a flag is not the same as saying what it does. Where a spawned
	// pane lands is the part an agent cannot discover without reading this
	// repository's source, which the agent in another project cannot do.
	for _, said := range []string{"new tab", "beside you"} {
		if !strings.Contains(text, said) {
			t.Errorf("the spawn section never says where a pane lands: no %q", said)
		}
	}
	if shell := (PaneContext{PaneName: "one", CanSpawn: true, Shell: true}).Render(); strings.Contains(shell, "flockdeck spawn") {
		t.Error("a shell pane should not be told to spawn agents")
	}
}

// TestOtherProjectsAreNamedApart checks the list of projects an agent is told
// about: two checkouts of one repository reaching it as the same word says
// nothing at all.
func TestOtherProjectsAreNamedApart(t *testing.T) {
	isolateConfig(t)
	first := filepath.Join(t.TempDir(), "checkout-a", "service")
	second := filepath.Join(t.TempDir(), "checkout-b", "service")
	for _, dir := range []string{first, second} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("make %s: %v", dir, err)
		}
	}

	ws := newTestWorkspace(t, first)
	ws.NewTab(session.KindShell, first, "lead")
	if err := ws.OpenProject(second); err != nil {
		t.Fatalf("open: %v", err)
	}
	ws.SelectProject(first)

	c, ok := ws.PaneContext(ws.CurrentTab().Focus)
	if !ok {
		t.Fatal("no context for the focused pane")
	}
	if len(c.OtherProjects) != 1 {
		t.Fatalf("other projects = %#v, want the one next door", c.OtherProjects)
	}
	if c.OtherProjects[0] == "service" {
		t.Errorf("the other project is named %q, the same as this one", c.OtherProjects[0])
	}
	if !strings.Contains(c.OtherProjects[0], "checkout-b") {
		t.Errorf("name %q does not say which checkout it is", c.OtherProjects[0])
	}

	// The pane's own project is named the same way, so the agent and the user
	// looking at the switcher call it the same thing.
	if !strings.Contains(c.ProjectName, "checkout-a") {
		t.Errorf("this project is named %q, which does not say which checkout it is", c.ProjectName)
	}
	if text := c.Render(); !strings.Contains(text, c.ProjectName) {
		t.Errorf("the rendered context does not name the project:\n%s", text)
	}
}

// TestPaneContextFollowsThePanesOwnProject covers an agent borrowed by another
// project's tab: it works in the project it was started in, and the context has
// to say so. Reading the project off the tab told it that it was working in a
// project it has never touched, that its own checkout was a worktree of that
// project, and that its real project was somebody else's.
func TestPaneContextFollowsThePanesOwnProject(t *testing.T) {
	isolateConfig(t)
	ws, first, second := twoProjects(t)

	// A tab of the first project showing an agent of the second, which is what
	// working on two projects at once looks like.
	ws.SplitPaneInProject(layout.Horizontal, session.KindShell, second)
	tab := ws.CurrentTab()
	if tab.Root != first {
		t.Fatalf("tab root = %q, want the first project %q", tab.Root, first)
	}
	borrowed := borrowedPane(t, ws, tab)
	if borrowed == nil {
		t.Fatal("no pane of the second project on the first project's tab")
	}

	c, ok := ws.PaneContext(borrowed.ID)
	if !ok {
		t.Fatal("no context for the borrowed pane")
	}
	if c.ProjectRoot != second {
		t.Errorf("project root = %q, want the pane's own project %q", c.ProjectRoot, second)
	}
	if c.Worktree {
		t.Error("a pane sitting in its own project root is not in a worktree")
	}
	// The other agent of its own project — sitting on that project's own tab —
	// is a sibling; the pane it shares a tab with belongs to the first project
	// and is not.
	if len(c.Siblings) != 1 {
		t.Fatalf("siblings = %#v, want only the second project's own pane", c.Siblings)
	}
	if !sameDir(c.Siblings[0].Cwd, second) {
		t.Errorf("sibling works in %q, want the second project %q", c.Siblings[0].Cwd, second)
	}
	if len(c.OtherProjects) != 1 {
		t.Errorf("other projects = %v, want just the first project", c.OtherProjects)
	}

	// And the pane that lent the tab still sees its own project, with the
	// borrowed agent left out of its siblings.
	host := ""
	for _, id := range tab.Tree.Panes() {
		if id != borrowed.ID {
			host = id
		}
	}
	hc, ok := ws.PaneContext(host)
	if !ok {
		t.Fatal("no context for the host pane")
	}
	if hc.ProjectRoot != first {
		t.Errorf("host project root = %q, want %q", hc.ProjectRoot, first)
	}
	for _, s := range hc.Siblings {
		if s.Cwd == borrowed.Cwd {
			t.Errorf("the borrowed pane is listed as a sibling of the project it is only shown in")
		}
	}
}

// TestTheSharedCheckoutSurvivesTheSiblingCap covers a busy project, which is
// where the list an agent is given stops being complete. What has to survive
// being cut is the agent editing the reader's own files: dropping it says
// nobody else is in this checkout, which is worse than saying nothing.
func TestTheSharedCheckoutSurvivesTheSiblingCap(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	worktree := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatalf("make worktree dir: %v", err)
	}

	ws := newTestWorkspace(t, root)
	ws.NewTab(session.KindShell, root, "lead")
	reader := ws.CurrentTab().Focus

	// Enough panes on the reader's own tab to fill the list on their own, none
	// of them in its checkout.
	for i := 0; i <= maxSiblings; i++ {
		ws.SplitPaneIn(layout.Vertical, session.KindShell, worktree)
	}
	// And one agent in the reader's checkout, on a tab of its own.
	ws.NewTab(session.KindShell, root, "next door")
	ws.SelectTab(ws.VisibleTabs()[0].ID)

	c, ok := ws.PaneContext(reader)
	if !ok {
		t.Fatal("no context for the reader")
	}
	if c.SiblingsOmitted == 0 {
		t.Fatalf("siblings = %d with none omitted; the test needs a list long enough to be cut",
			len(c.Siblings))
	}
	shared := 0
	for _, s := range c.Siblings {
		if s.SameCheckout {
			shared++
		}
	}
	if shared != 1 {
		t.Errorf("%d of the listed siblings share the reader's checkout, want the one that does", shared)
	}
	if len(c.Siblings) > 0 && !c.Siblings[0].SameCheckout {
		t.Errorf("first sibling is %q in %q, want the one sharing the checkout",
			c.Siblings[0].Name, c.Siblings[0].Cwd)
	}
}

// TestSpawnExamplesNameACommandThatExists covers a build that has not been
// installed. `flockdeck` is only on PATH once it has been, and an agent handed
// `flockdeck spawn` on a machine without it is handed a command that cannot run,
// with nothing but the failure to say why.
func TestSpawnExamplesNameACommandThatExists(t *testing.T) {
	exe := filepath.Join("C:", "Program Files", "flockdeck", "flockdeck.exe")
	text := PaneContext{PaneName: "one", CanSpawn: true, SpawnCommand: exe}.Render()
	quoted := `"` + exe + `" spawn "add tests for the parser"`
	if !strings.Contains(text, quoted) {
		t.Errorf("the examples do not run %s, quoted for a path with a space:\n%s", exe, text)
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "flockdeck spawn") {
			t.Errorf("an example still runs a bare flockdeck: %q", line)
		}
	}

	// A name that needs no quoting is left as it is, and a context built
	// without one still documents something.
	plain := PaneContext{PaneName: "one", CanSpawn: true, SpawnCommand: "flockdeck"}.Render()
	if !strings.Contains(plain, `flockdeck spawn "add tests for the parser"`) {
		t.Errorf("an installed copy should be run by name:\n%s", plain)
	}
	if bare := (PaneContext{PaneName: "one", CanSpawn: true}).Render(); !strings.Contains(bare, "flockdeck spawn") {
		t.Errorf("a context with no command recorded should still name one:\n%s", bare)
	}
}

// TestTheSpawnCommandIsResolvedOnce checks the workspace hands the context a
// command at all, since the text is only as good as what reaches it.
func TestTheSpawnCommandIsResolvedOnce(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	ws.NewTab(session.KindShell, root, "lead")

	c, ok := ws.PaneContext(ws.CurrentTab().Focus)
	if !ok {
		t.Fatal("no context for the focused pane")
	}
	if c.SpawnCommand == "" {
		t.Error("the context carries no way of running flockdeck")
	}
}

// TestTheOwnTaskIsKeptWholeWhereASiblingsIsSummarised covers what an agent is told it was
// asked, which after a compaction is the only copy of it left. A long
// instruction cut at the length used for other agents' one-line summaries
// reads as though it were the whole of it.
func TestTheOwnTaskIsKeptWholeWhereASiblingsIsSummarised(t *testing.T) {
	task := "rewrite the importer: " + strings.Repeat("keep every clause of this; ", 12) + "and stop at the marker"
	own := PaneContext{PaneName: "one", Task: task}.Render()
	tail := "and stop at the marker"
	if !strings.Contains(own, tail) {
		t.Errorf("the end of the pane's own task is missing from its context:\n%s", own)
	}

	// The same task belonging to somebody else is still a summary.
	sib := PaneContext{PaneName: "one", Siblings: []Sibling{{Name: "two", Cwd: "/repo", Status: "working", Task: task}}}.Render()
	if strings.Contains(sib, tail) {
		t.Error("a sibling's task is written out in full; it should be cut to a line")
	}
	if !strings.Contains(sib, "rewrite the importer") {
		t.Errorf("a sibling's task is missing entirely:\n%s", sib)
	}
}

// TestAPaneBelowTheProjectRootIsNotAWorktree covers a project opened above the
// checkout an agent works in — a directory of repositories opened as one
// project, or a tab opened on a subdirectory. The agent shares the project's
// checkout; being told it has a worktree of its own tells it to keep out of
// the rest of its own repository.
func TestAPaneBelowTheProjectRootIsNotAWorktree(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	below := filepath.Join(root, "services", "api")
	if err := os.MkdirAll(below, 0o755); err != nil {
		t.Fatalf("make subdirectory: %v", err)
	}

	ws := newTestWorkspace(t, root)
	ws.NewTab(session.KindShell, below, "api")

	c, ok := ws.PaneContext(ws.CurrentTab().Focus)
	if !ok {
		t.Fatal("no context for the focused pane")
	}
	if c.ProjectRoot != root {
		t.Fatalf("project root = %q, want %q", c.ProjectRoot, root)
	}
	if c.Worktree {
		t.Error("a directory inside the project is not a checkout of its own")
	}
	if !c.Subdirectory {
		t.Error("the context does not record that the pane works below the project root")
	}

	text := c.Render()
	if strings.Contains(text, "separate git worktree") {
		t.Errorf("the agent is told it has a worktree it does not have:\n%s", text)
	}
	if !strings.Contains(text, "in the same checkout") {
		t.Errorf("the agent is not told where the project root is:\n%s", text)
	}
}

// TestTheSpawnCommandIsQuotedWhenItHasToBe covers the paths an uninstalled
// build actually sits at. Quoting on a space alone leaves the brackets in
// "Program Files (x86)" for the shell to read as syntax, and an agent copying
// the example gets a parse error rather than a helper.
func TestTheSpawnCommandIsQuotedWhenItHasToBe(t *testing.T) {
	cases := map[string]string{
		"flockdeck":                            "flockdeck",
		`C:\Program Files (x86)\flockdeck.exe`: `"C:\Program Files (x86)\flockdeck.exe"`,
		`C:\tools\flockdeck-2.1.exe`:           `C:\tools\flockdeck-2.1.exe`,
		`/usr/local/bin/flockdeck`:             `/usr/local/bin/flockdeck`,
		`C:\build\flockdeck(1).exe`:            `"C:\build\flockdeck(1).exe"`,
	}
	for in, want := range cases {
		if got := shellWord(in); got != want {
			t.Errorf("shellWord(%q) = %q, want %q", in, got, want)
		}
		text := PaneContext{PaneName: "one", CanSpawn: true, SpawnCommand: in}.Render()
		if !strings.Contains(text, want+" spawn ") {
			t.Errorf("the examples do not run %s:\n%s", want, text)
		}
	}
}

// TestTheSpawnCommandSurvivesTheShell covers the characters sh still reads
// inside double quotes. The examples an agent is given are run by a shell, and
// a path through a folder with a $ in its name, or a build run from a Windows
// share, was quoted into a command for some other path.
func TestTheSpawnCommandSurvivesTheShell(t *testing.T) {
	paths := []string{
		`/home/me/$work/flockdeck`,
		`C:\Users\me\a$b\flockdeck.exe`,
		`\\server\share\flockdeck.exe`,
		"/opt/odd`dir/flockdeck",
		`/opt/it's $here/flockdeck`,
	}
	// Reading the word back through a real sh proves it, but only where sh is
	// given its argument as it stands: on Windows the one to hand is Git's,
	// whose runtime rewrites backslashes in the command line it is started
	// with before sh ever sees the quotes.
	sh, err := exec.LookPath("sh")
	for _, p := range paths {
		word := shellWord(p)
		if !strings.HasPrefix(word, `'`) {
			t.Errorf("shellWord(%q) = %s, which sh would expand", p, word)
			continue
		}
		if err != nil || runtime.GOOS == "windows" {
			continue
		}
		out, runErr := exec.Command(sh, "-c", "printf %s "+word).Output()
		if runErr != nil {
			t.Errorf("sh could not read %s: %v", word, runErr)
			continue
		}
		if string(out) != p {
			t.Errorf("sh read %s as %q, want %q", word, out, p)
		}
	}
}

// TestTheBriefingDoesNotOfferDetachFromThePane covers the shell section of the
// briefing, which offered `flockdeck -detach` as the way to close the window
// and leave the agents running. Run from inside a pane it joins the instance
// already running, where -detach is one of the flags that only shape a fresh
// start: it is ignored, and another window opens onto everything instead.
func TestTheBriefingDoesNotOfferDetachFromThePane(t *testing.T) {
	text := PaneContext{PaneName: "one", CanSpawn: true}.Render()
	if strings.Contains(text, "-detach         # close the window") {
		t.Errorf("the briefing offers -detach as a way to close the window:\n%s", text)
	}
	for _, want := range []string{"`-agent`, `-detach`", how("detach")} {
		if !strings.Contains(text, want) {
			t.Errorf("the briefing never says %q:\n%s", want, text)
		}
	}
}

// TestTheBriefingNamesEveryVariableAPaneCarries covers the table of what a
// pane has in its environment. FLOCKDECK_AGENT and FLOCKDECK_MODEL are set on
// every agent pane, and are how a script or a prompt says what it is sitting
// in, but the table stopped at five, so an agent asked which model it runs
// had nowhere to point.
func TestTheBriefingNamesEveryVariableAPaneCarries(t *testing.T) {
	text := PaneContext{PaneName: "one", CanSpawn: true}.Render()
	for _, name := range []string{"FLOCKDECK_API", "FLOCKDECK_TOKEN", "FLOCKDECK_PANE", "FLOCKDECK_PANE_NAME",
		"FLOCKDECK_PROJECT", "FLOCKDECK_AGENT", "FLOCKDECK_MODEL"} {
		if !strings.Contains(text, "`"+name+"`") {
			t.Errorf("the briefing never names %s:\n%s", name, text)
		}
	}
}

// TestRenderNamesEveryActionTheInterfaceHas keeps the description of the
// application whole as the application grows. An action added to the palette
// and left out of here is one the agent beside the user will never mention,
// which is the failure this section exists to prevent.
func TestRenderNamesEveryActionTheInterfaceHas(t *testing.T) {
	text := PaneContext{PaneName: "one", CanSpawn: true}.Render()
	for _, k := range help.Keys {
		if !strings.Contains(text, k.Name()) {
			t.Errorf("the context never mentions %q", k.Name())
		}
		if k.Keys != "" && !strings.Contains(text, k.Keys) {
			t.Errorf("the context gives no binding for %q; the table says %s", k.Name(), k.Keys)
		}
	}
}

// TestRenderExplainsTheFeaturesThatChangeHowAnAgentWorks covers the other half
// of that section: naming a feature is not saying what it means for the agent
// reading about it. Each of these is a fact that changes what an agent should
// do, not a key for it to pass on.
func TestRenderExplainsTheFeaturesThatChangeHowAnAgentWorks(t *testing.T) {
	text := PaneContext{PaneName: "one", CanSpawn: true}.Render()
	for _, want := range []string{
		"amber",                     // the status the user is watching for
		"one agent per line",        // what fan-out does with a plan
		"working tree as it stands", // what the commit button takes
		"resumes this conversation", // what restarting a pane keeps
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the capabilities section does not say %q:\n%s", want, text)
		}
	}
}

// TestRenderDocumentsThePaneEnvironment covers what a pane carries for the
// processes inside it. A shell pane has no lifecycle hooks and these variables
// are all it has to go on, so the agent that is asked how to read one has to
// have been told they exist.
func TestRenderDocumentsThePaneEnvironment(t *testing.T) {
	text := PaneContext{PaneName: "one", CanSpawn: true}.Render()
	for _, want := range []string{
		"FLOCKDECK_API", "FLOCKDECK_TOKEN", "FLOCKDECK_PANE", "FLOCKDECK_PANE_NAME", "FLOCKDECK_PROJECT",
		"PERCH_",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the context does not document %s", want)
		}
	}
	// The command that stops every agent in every project is the one an agent
	// has to recognise before it runs something that reads like a way to end
	// its own pane.
	if !strings.Contains(text, "-quit") {
		t.Errorf("the context never says what -quit does:\n%s", text)
	}

	// A pane with nothing to call back into has none of this in its
	// environment, and being told otherwise sends it looking for an address
	// that was never set.
	if plain := (PaneContext{PaneName: "one"}).Render(); strings.Contains(plain, "FLOCKDECK_TOKEN") {
		t.Error("a pane with no hook server should not be told about a token it does not carry")
	}
	if shell := (PaneContext{PaneName: "one", CanSpawn: true, Shell: true}).Render(); strings.Contains(shell, "FLOCKDECK_TOKEN") {
		t.Error("a shell pane has no agent to read the environment table")
	}
}

// TestOpeningPromptFencesTheBriefingAheadOfTheTask covers the delivery an
// agent with no lifecycle hooks gets. Both halves are in one string by then,
// and the fence is the only thing telling the agent which half the user wrote.
func TestOpeningPromptFencesTheBriefingAheadOfTheTask(t *testing.T) {
	task := "rewrite the importer: " + strings.Repeat("keep every clause of this; ", 12) + "and stop at the marker"
	text := PaneContext{PaneName: "one", Cwd: "/repo", Task: task}.OpeningPrompt()

	open := strings.Index(text, "<flockdeck-context>")
	closed := strings.Index(text, "</flockdeck-context>")
	if open != 0 || closed < 0 {
		t.Fatalf("the briefing is not fenced from the start:\n%s", text)
	}
	if !strings.Contains(text[open:closed], "Where you are running") {
		t.Errorf("the fence does not hold the briefing:\n%s", text)
	}
	// The task follows the block whole. Cut to the length a sibling's summary
	// is cut to, it would read as the whole of an instruction it is not.
	if !strings.Contains(text[closed:], task) {
		t.Errorf("the task does not follow the briefing in full:\n%s", text)
	}
	// And it is not summarised inside the block as well, where the agent would
	// find a second, shorter copy of what it has just been asked.
	if strings.Contains(text[:closed], "You were started with this task") {
		t.Errorf("the task is repeated inside the briefing as well:\n%s", text)
	}
}

// TestOpeningPromptWithNoTaskIsTheBlockAlone covers a pane opened by hand: the
// agent is still told which checkout it has and who else is in the repository,
// because that changes what it does with the first thing the user types.
func TestOpeningPromptWithNoTaskIsTheBlockAlone(t *testing.T) {
	text := PaneContext{PaneName: "one", Cwd: "/repo"}.OpeningPrompt()
	if !strings.HasPrefix(text, "<flockdeck-context>") {
		t.Errorf("a pane with no task got no briefing:\n%s", text)
	}
	if tail := strings.TrimSpace(text[strings.Index(text, "</flockdeck-context>"):]); tail != "</flockdeck-context>" {
		t.Errorf("something follows the block for a pane with no task: %q", tail)
	}
}

// TestOpeningPromptSaysWhenItWasTaken is the honesty the once-per-launch
// delivery depends on. A hooked agent is briefed again whenever it starts,
// resumes or compacts; this one is told a list of other agents that begins
// going stale the moment it is read, so it has to be told when it was true.
func TestOpeningPromptSaysWhenItWasTaken(t *testing.T) {
	taken := time.Date(2026, 9, 9, 15, 4, 0, 0, time.UTC)
	c := PaneContext{PaneName: "one", Cwd: "/repo", Taken: taken}

	prompt := c.OpeningPrompt()
	if !strings.Contains(prompt, "2026-09-09 15:04") {
		t.Errorf("the briefing does not say when it was taken:\n%s", prompt)
	}
	if !strings.Contains(prompt, "briefed once") {
		t.Errorf("the briefing does not say it will not be refreshed:\n%s", prompt)
	}

	// The hook reply is asked for again every time, so the same sentence there
	// would warn about a staleness that never happens.
	if hook := c.Render(); strings.Contains(hook, "briefed once") {
		t.Errorf("the hook briefing claims it is never refreshed:\n%s", hook)
	}

	// A context assembled without a clock still has to say what it is; only
	// the time drops out.
	if plain := (PaneContext{PaneName: "one"}).OpeningPrompt(); !strings.Contains(plain, "briefed once") {
		t.Errorf("a briefing with no timestamp forgot to say it is a snapshot:\n%s", plain)
	}
}

// TestOpeningPromptClaimsNoHooksItDoesNotHave covers the paragraph about the
// user watching the pane. Told its state is read from lifecycle hooks and a
// settings file, an agent that fires neither would take a question left on the
// screen for one the user had been shown.
func TestOpeningPromptClaimsNoHooksItDoesNotHave(t *testing.T) {
	prompt := PaneContext{PaneName: "one", Cwd: "/repo"}.OpeningPrompt()
	for _, unwanted := range []string{"--settings", "lifecycle hooks — your own settings"} {
		if strings.Contains(prompt, unwanted) {
			t.Errorf("the briefing describes %q, which this agent does not have:\n%s", unwanted, prompt)
		}
	}
	// It still has to describe what the user sees, because the reason to ask
	// on a line of its own is that the pane goes amber and the user is told.
	for _, want := range []string{"amber", "desktop notification"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the briefing never says %q:\n%s", want, prompt)
		}
	}
}

// TestSiblingsAreDescribedWithTheirAgentAndModel covers what is worth asking
// of the pane next door, which depends on what is running in it.
func TestSiblingsAreDescribedWithTheirAgentAndModel(t *testing.T) {
	for _, tc := range []struct {
		name  string
		sib   Sibling
		want  string
		avoid string
	}{
		{
			name: "agent and model",
			sib:  Sibling{Name: "two", Cwd: "/repo", Status: "working", Agent: "codex", Model: "gpt-5"},
			want: "running codex · gpt-5",
		},
		{
			// An empty model means whatever the CLI is configured with, which
			// is not Flockdeck's to report as a choice somebody made.
			name:  "no model asked for",
			sib:   Sibling{Name: "two", Cwd: "/repo", Status: "working", Agent: "claude"},
			want:  "running claude",
			avoid: "·",
		},
		{
			name:  "nothing recorded",
			sib:   Sibling{Name: "two", Cwd: "/repo", Status: "working"},
			avoid: "running",
		},
		{
			name:  "a shell runs no agent",
			sib:   Sibling{Name: "two", Cwd: "/repo", Shell: true, Agent: "claude"},
			want:  "a shell, not an agent",
			avoid: "claude",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.sib.describe()
			if tc.want != "" && !strings.Contains(got, tc.want) {
				t.Errorf("describe() = %q, want it to mention %q", got, tc.want)
			}
			if tc.avoid != "" && strings.Contains(got, tc.avoid) {
				t.Errorf("describe() = %q, want nothing about %q", got, tc.avoid)
			}
		})
	}
}

// TestPaneContextNamesTheAgentASiblingRuns checks the label reaches the
// briefing from the pane itself. The describing was all in place while the
// lookup behind it still answered nothing for every pane, so no agent was ever
// told what was running next door.
func TestPaneContextNamesTheAgentASiblingRuns(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	ws.NewTab(session.KindShell, root, "lead")
	parent := ws.CurrentTab().Focus

	child, err := ws.Spawn(parent, SpawnOptions{Task: "port the parser", Kind: session.KindShell, Split: true})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	// A live process is what keeps a sibling in the list; which agent it is
	// recorded as is what is under test, so the shell is relabelled as one.
	p := ws.Pane(child)
	p.Kind, p.Agent, p.Model = session.KindClaude, "codex", "gpt-5"

	c, _ := ws.PaneContext(parent)
	if len(c.Siblings) != 1 {
		t.Fatalf("siblings = %#v, want the child", c.Siblings)
	}
	if got := c.Siblings[0]; got.Agent != "codex" || got.Model != "gpt-5" {
		t.Errorf("sibling runs %q · %q, want codex · gpt-5", got.Agent, got.Model)
	}
	if text := c.Render(); !strings.Contains(text, "running codex · gpt-5") {
		t.Errorf("the briefing never says what the sibling runs:\n%s", text)
	}
}

// TestTheAgentLabelsAreExplainedWhereThereAreAny keeps the note about them out
// of a window where every pane is a shell or an agent nobody recorded.
func TestTheAgentLabelsAreExplainedWhereThereAreAny(t *testing.T) {
	named := PaneContext{PaneName: "one", Siblings: []Sibling{
		{Name: "two", Cwd: "/repo", Status: "working", Agent: "codex", Model: "gpt-5"},
	}}.Render()
	if !strings.Contains(named, "Where an agent and a model are named") {
		t.Errorf("the labels are never explained:\n%s", named)
	}

	plain := PaneContext{PaneName: "one", Siblings: []Sibling{
		{Name: "two", Cwd: "/repo", Status: "working"},
	}}.Render()
	if strings.Contains(plain, "Where an agent and a model are named") {
		t.Errorf("a list with no labels explains labels anyway:\n%s", plain)
	}
}

// TestOpeningPromptOnlyBriefsAnAgentThatCannotBeAsked covers the choice made
// when a pane starts. An agent with hooks is briefed when it fires the first
// one, and putting the same text in front of its task as well would spend a
// large part of its first turn on something it is about to be told anyway.
func TestOpeningPromptOnlyBriefsAnAgentThatCannotBeAsked(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	ws.NewTab(session.KindShell, root, "lead")
	pane := ws.CurrentTab().Focus

	if got := ws.OpeningPrompt(pane, "fix the parser", agent.ContextHook); got != "fix the parser" {
		t.Errorf("a hooked agent's prompt = %q, want the task untouched", got)
	}
	if got := ws.OpeningPrompt(pane, "fix the parser", agent.ContextNone); got != "fix the parser" {
		t.Errorf("an unbriefed agent's prompt = %q, want the task untouched", got)
	}

	briefed := ws.OpeningPrompt(pane, "fix the parser", agent.ContextPrompt)
	if !strings.HasPrefix(briefed, "<flockdeck-context>") || !strings.Contains(briefed, root) {
		t.Errorf("an agent with no hooks was not briefed:\n%s", briefed)
	}
	if !strings.HasSuffix(strings.TrimSpace(briefed), "fix the parser") {
		t.Errorf("the task is not the last thing the agent reads:\n%s", briefed)
	}

	// A pane closed between the decision to start it and this call still has
	// work to do; a task with no briefing beats a briefing with no task.
	if got := ws.OpeningPrompt("no-such-pane", "fix the parser", agent.ContextPrompt); got != "fix the parser" {
		t.Errorf("an unknown pane's prompt = %q, want the task alone", got)
	}
}
