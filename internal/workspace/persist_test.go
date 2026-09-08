package workspace

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/jmwri/agent-wrapper/internal/layout"
	"github.com/jmwri/agent-wrapper/internal/session"
	"github.com/jmwri/agent-wrapper/internal/store"
)

// isolateConfig points the state directory at a temporary location so tests
// never touch the developer's real saved layouts or recent projects.
func isolateConfig(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)         // Windows
	t.Setenv("XDG_CONFIG_HOME", dir) // Linux
	t.Setenv("HOME", dir)            // macOS and fallback
}

// newTestWorkspace builds a workspace rooted at a directory.
func newTestWorkspace(t *testing.T, root string) *Workspace {
	t.Helper()
	ws, err := New(Options{Root: root})
	if err != nil {
		t.Fatalf("new workspace: %v", err)
	}
	t.Cleanup(ws.Close)
	return ws
}

// TestSaveRestoreRoundTrip checks that the tab and split structure survives a
// restart, and that panes keep their ids so Claude conversations can resume.
func TestSaveRestoreRoundTrip(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()

	ws := newTestWorkspace(t, root)
	ws.NewTab(session.KindShell, root, "first")
	ws.SplitPane(layout.Horizontal, session.KindShell)
	ws.SplitPane(layout.Vertical, session.KindShell)
	ws.NewTab(session.KindShell, root, "second")
	ws.SelectTab(ws.VisibleTabs()[0].ID)

	wantPanes := ws.VisibleTabs()[0].Tree.Panes()
	wantFocus := ws.VisibleTabs()[0].Focus
	if len(wantPanes) != 3 {
		t.Fatalf("expected 3 panes before saving, got %d", len(wantPanes))
	}

	if err := ws.SaveAll(); err != nil {
		t.Fatalf("save: %v", err)
	}
	ws.Close()

	restored := newTestWorkspace(t, root)
	ok, err := restored.Restore()
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if !ok {
		t.Fatal("expected the layout to be restored")
	}

	tabs := restored.VisibleTabs()
	if len(tabs) != 2 {
		t.Fatalf("restored %d tabs, want 2", len(tabs))
	}
	if tabs[0].Title != "first" {
		t.Errorf("tab title = %q, want first", tabs[0].Title)
	}
	// Pane ids must survive verbatim: they are the Claude session UUIDs that
	// `--resume` reattaches to.
	if got := tabs[0].Tree.Panes(); !reflect.DeepEqual(got, wantPanes) {
		t.Errorf("restored panes = %v, want %v", got, wantPanes)
	}
	if tabs[0].Focus != wantFocus {
		t.Errorf("restored focus = %q, want %q", tabs[0].Focus, wantFocus)
	}
	if restored.ActiveTabID() != tabs[0].ID {
		t.Error("the tab that was active when saved should be active again")
	}
	if n := len(tabs[0].Tree.Children); n != 2 {
		t.Errorf("restored root has %d children, want 2", n)
	}
}

// TestProjectsAreIndependent is the point of having projects: opening a second
// one must not disturb the first, and each keeps its own tabs.
func TestProjectsAreIndependent(t *testing.T) {
	isolateConfig(t)
	first, second := t.TempDir(), t.TempDir()

	ws := newTestWorkspace(t, first)
	ws.NewTab(session.KindShell, first, "alpha")
	firstTabID := ws.ActiveTabID()

	if err := ws.OpenProject(second); err != nil {
		t.Fatalf("open second project: %v", err)
	}
	if ws.ActiveRoot() != second {
		t.Fatalf("active root = %q, want the newly opened project", ws.ActiveRoot())
	}

	// The second project starts with its own tab, and only its tabs show.
	visible := ws.VisibleTabs()
	if len(visible) != 1 {
		t.Fatalf("second project shows %d tabs, want 1", len(visible))
	}
	if visible[0].Root != second {
		t.Errorf("visible tab belongs to %q, want %q", visible[0].Root, second)
	}

	// The first project's tab still exists, and its session is still running.
	firstTab := ws.Tab(firstTabID)
	if firstTab == nil {
		t.Fatal("the first project's tab was discarded when switching")
	}
	for _, id := range firstTab.Tree.Panes() {
		if p := ws.Pane(id); p == nil || !p.Alive() {
			t.Error("switching projects must not kill the other project's agents")
		}
	}

	// Switching back shows the first project's tabs again.
	ws.SelectProject(first)
	if ws.ActiveRoot() != first {
		t.Fatalf("active root = %q, want %q", ws.ActiveRoot(), first)
	}
	for _, tab := range ws.VisibleTabs() {
		if tab.Root != first {
			t.Errorf("tab from %q shown while %q is active", tab.Root, first)
		}
	}

	if got := ws.Projects(); len(got) != 2 {
		t.Fatalf("expected 2 open projects, got %d", len(got))
	}
}

// TestEachProjectPersistsSeparately checks a project's layout is restored on
// its own, not mixed with another's.
func TestEachProjectPersistsSeparately(t *testing.T) {
	isolateConfig(t)
	first, second := t.TempDir(), t.TempDir()

	ws := newTestWorkspace(t, first)
	ws.NewTab(session.KindShell, first, "alpha")
	if err := ws.OpenProject(second); err != nil {
		t.Fatalf("open: %v", err)
	}
	ws.NewTab(session.KindShell, second, "beta")
	ws.NewTab(session.KindShell, second, "gamma")
	if err := ws.SaveAll(); err != nil {
		t.Fatalf("save: %v", err)
	}
	ws.Close()

	// Reopening the second project restores exactly its own tabs.
	again := newTestWorkspace(t, second)
	if _, err := again.Restore(); err != nil {
		t.Fatalf("restore: %v", err)
	}
	var titles []string
	for _, tab := range again.VisibleTabs() {
		titles = append(titles, tab.Title)
	}
	if len(titles) != 3 {
		t.Fatalf("second project restored %v, want 3 tabs", titles)
	}
	for _, title := range titles {
		if title == "alpha" {
			t.Error("the other project's tabs leaked into this one")
		}
	}
}

// TestCloseProjectKeepsTheLastOne guards against ending up with an empty
// window.
func TestCloseProjectKeepsTheLastOne(t *testing.T) {
	isolateConfig(t)
	first, second := t.TempDir(), t.TempDir()

	ws := newTestWorkspace(t, first)
	ws.NewTab(session.KindShell, first, "alpha")
	if err := ws.OpenProject(second); err != nil {
		t.Fatalf("open: %v", err)
	}

	ws.CloseProject(second)
	if len(ws.Projects()) != 1 {
		t.Fatalf("expected 1 project after closing, got %d", len(ws.Projects()))
	}
	if ws.ActiveRoot() != first {
		t.Errorf("active root = %q, want %q", ws.ActiveRoot(), first)
	}
	if len(ws.VisibleTabs()) == 0 {
		t.Error("the surviving project should still have its tabs")
	}

	// The final project cannot be closed.
	ws.CloseProject(first)
	if len(ws.Projects()) != 1 {
		t.Error("closing the last project should be refused")
	}
}

// TestRestoreWithNoSavedLayout covers a first run.
func TestRestoreWithNoSavedLayout(t *testing.T) {
	isolateConfig(t)
	ws := newTestWorkspace(t, t.TempDir())

	ok, err := ws.Restore()
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if ok {
		t.Error("expected no layout to restore on a first run")
	}
}

// TestClosePaneKeepsTabUsable checks focus lands somewhere sensible after a
// pane is closed.
func TestClosePaneKeepsTabUsable(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)

	ws.NewTab(session.KindShell, root, "t")
	ws.SplitPane(layout.Horizontal, session.KindShell)
	tab := ws.CurrentTab()
	closed := tab.Focus

	ws.ClosePane()

	if len(ws.VisibleTabs()) != 1 {
		t.Fatalf("expected the tab to survive, got %d tabs", len(ws.VisibleTabs()))
	}
	panes := tab.Tree.Panes()
	if len(panes) != 1 {
		t.Fatalf("expected 1 remaining pane, got %d", len(panes))
	}
	if tab.Focus == closed {
		t.Error("focus is still on the closed pane")
	}
	if ws.Pane(closed) != nil {
		t.Error("the closed pane should have been forgotten")
	}
}

// TestClosingLastPaneClosesTab covers the tab teardown path.
func TestClosingLastPaneClosesTab(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)

	ws.NewTab(session.KindShell, root, "only")
	ws.ClosePane()

	if len(ws.VisibleTabs()) != 0 {
		t.Fatalf("expected the tab to close, got %d tabs", len(ws.VisibleTabs()))
	}
}

// TestOpenProjectRejectsNonDirectories covers the picker's error path.
func TestOpenProjectRejectsNonDirectories(t *testing.T) {
	isolateConfig(t)
	ws := newTestWorkspace(t, t.TempDir())

	if err := ws.OpenProject(t.TempDir() + "/nope"); err == nil {
		t.Error("expected opening a missing directory to fail")
	}
	if len(ws.Projects()) != 1 {
		t.Error("a failed open must not add a project")
	}
}

// TestRestoreSessionReopensProjects covers bringing the whole workspace back,
// not just the directory named on the command line.
func TestRestoreSessionReopensProjects(t *testing.T) {
	isolateConfig(t)
	first, second, third := t.TempDir(), t.TempDir(), t.TempDir()

	ws := newTestWorkspace(t, first)
	ws.NewTab(session.KindShell, first, "alpha")
	if err := ws.OpenProject(second); err != nil {
		t.Fatalf("open second: %v", err)
	}
	ws.NewTab(session.KindShell, second, "beta")
	if err := ws.OpenProject(third); err != nil {
		t.Fatalf("open third: %v", err)
	}
	if err := ws.SaveAll(); err != nil {
		t.Fatalf("save: %v", err)
	}
	ws.Close()

	// A fresh run started on the first project should bring the others back.
	again := newTestWorkspace(t, first)
	if _, err := again.Restore(); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if n := again.RestoreSession(); n != 2 {
		t.Errorf("reopened %d extra projects, want 2", n)
	}
	if len(again.Projects()) != 3 {
		t.Fatalf("expected 3 open projects, got %d", len(again.Projects()))
	}
	// The project asked for on the command line stays the active one.
	if again.ActiveRoot() != first {
		t.Errorf("active root = %q, want %q", again.ActiveRoot(), first)
	}
	for _, tab := range again.VisibleTabs() {
		if tab.Root != first {
			t.Errorf("tab from %q shown while %q is active", tab.Root, first)
		}
	}
}

// TestRestoreSessionSkipsMissingProjects covers a project directory that has
// since been deleted.
func TestRestoreSessionSkipsMissingProjects(t *testing.T) {
	isolateConfig(t)
	first := t.TempDir()
	gone := filepath.Join(t.TempDir(), "deleted-since")
	if err := os.MkdirAll(gone, 0o755); err != nil {
		t.Fatal(err)
	}

	ws := newTestWorkspace(t, first)
	if err := ws.OpenProject(gone); err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := ws.SaveAll(); err != nil {
		t.Fatalf("save: %v", err)
	}
	ws.Close()
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}

	again := newTestWorkspace(t, first)
	if n := again.RestoreSession(); n != 0 {
		t.Errorf("reopened %d projects, want none: the directory is gone", n)
	}
	if len(again.Projects()) != 1 {
		t.Errorf("expected only the surviving project, got %d", len(again.Projects()))
	}
}

// TestOpenConversationResumesById covers resuming a stored conversation from
// the history panel.
func TestOpenConversationResumesById(t *testing.T) {
	isolateConfig(t)
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)

	root := t.TempDir()
	ws := newTestWorkspace(t, root)

	// A conversation with no stored transcript cannot be resumed: `claude
	// --resume` would exit immediately.
	if err := ws.OpenConversation("11111111-2222-3333-4444-555555555555", root, "x"); err == nil {
		t.Error("expected resuming an unknown conversation to fail")
	}

	// With a transcript present it opens as its own tab, keyed by the
	// conversation id so the layout remembers it.
	id := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	dir := filepath.Join(home, "projects", "any-folder")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	before := len(ws.VisibleTabs())
	if err := ws.OpenConversation(id, root, "resumed work"); err != nil {
		t.Fatalf("open conversation: %v", err)
	}
	if got := len(ws.VisibleTabs()); got != before+1 {
		t.Fatalf("tabs = %d, want %d", got, before+1)
	}
	if ws.Pane(id) == nil {
		t.Error("the pane should be keyed by the conversation id")
	}

	// Opening it twice focuses the existing pane instead of starting a rival
	// process on the same transcript.
	tabs := len(ws.VisibleTabs())
	if err := ws.OpenConversation(id, root, "resumed work"); err != nil {
		t.Fatalf("second open: %v", err)
	}
	if got := len(ws.VisibleTabs()); got != tabs {
		t.Errorf("tabs = %d, want it to focus the existing one (%d)", got, tabs)
	}
}

// TestRestoreCollapsedSplitKeepsItsShare covers a saved split that loses all
// but one pane — a directory that has gone, say. The survivor should take over
// the room the split had, not shrink back to the share it held inside it.
func TestRestoreCollapsedSplitKeepsItsShare(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()

	// A narrow column beside a wide one, the wide one holding two panes stacked
	// vertically. The lower of the two cannot be restored: it has no directory.
	saved := &store.State{Tabs: []store.Tab{{
		Title: "one",
		Root: &store.Node{Dir: "h", Children: []*store.Node{
			{Weight: 1, Pane: &store.Pane{ID: "narrow", Kind: "shell", Cwd: root}},
			{Weight: 4, Dir: "v", Children: []*store.Node{
				{Weight: 1, Pane: &store.Pane{ID: "wide", Kind: "shell", Cwd: root}},
				{Weight: 1, Pane: &store.Pane{ID: "gone", Kind: "shell"}},
			}},
		}},
	}}}
	if err := store.Save(root, saved); err != nil {
		t.Fatalf("save: %v", err)
	}

	ws := newTestWorkspace(t, root)
	if ok, err := ws.Restore(); err != nil || !ok {
		t.Fatalf("restore: ok=%v err=%v", ok, err)
	}
	tree := ws.VisibleTabs()[0].Tree
	if got := tree.Panes(); !reflect.DeepEqual(got, []string{"narrow", "wide"}) {
		t.Fatalf("panes = %v, want the two that could be restored", got)
	}
	if got := tree.Find("wide").Weight; got != 4 {
		t.Errorf("surviving pane weight = %v, want the collapsed split's 4", got)
	}
}
