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
