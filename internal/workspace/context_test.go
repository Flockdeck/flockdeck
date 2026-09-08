package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/jmwri/agent-wrapper/internal/layout"
	"github.com/jmwri/agent-wrapper/internal/session"
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
	for _, want := range []string{"agent-wrapper", `"lead"`, root} {
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
	got := oneLine("first line\n\n   second   line\t" + strings.Repeat("x", 400))
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
	got := oneLine(strings.Repeat("こんにちは", 80))
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
