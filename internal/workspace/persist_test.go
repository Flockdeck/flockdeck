package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

// TestRestoreIgnoresDuplicatePaneIds covers a damaged layout that names one
// pane in two places. Starting it twice would leave a process nothing in the
// workspace points at, and for a Claude pane two agents on one transcript.
func TestRestoreIgnoresDuplicatePaneIds(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()

	saved := &store.State{Tabs: []store.Tab{{
		Title: "one",
		Root: &store.Node{Dir: "h", Children: []*store.Node{
			{Pane: &store.Pane{ID: "twice", Kind: "shell", Cwd: root}},
			{Pane: &store.Pane{ID: "twice", Kind: "shell", Cwd: root}},
			{Pane: &store.Pane{ID: "once", Kind: "shell", Cwd: root}},
		}},
	}}}
	if err := store.Save(root, saved); err != nil {
		t.Fatalf("save: %v", err)
	}

	ws := newTestWorkspace(t, root)
	if ok, err := ws.Restore(); err != nil || !ok {
		t.Fatalf("restore: ok=%v err=%v", ok, err)
	}
	if got := ws.VisibleTabs()[0].Tree.Panes(); !reflect.DeepEqual(got, []string{"twice", "once"}) {
		t.Errorf("panes = %v, want the repeat dropped", got)
	}
	// Closing the tab must be able to reach every process that was started.
	ws.CloseTab(ws.ActiveTabID())
	for _, id := range []string{"twice", "once"} {
		if ws.Pane(id) != nil {
			t.Errorf("pane %q outlived its tab", id)
		}
	}
}

// TestRestoredTabStillRenamesItself covers a tab created but never prompted
// before a restart. Its title is still the directory name, so the first thing
// asked of its agent should replace it, exactly as it would have before.
func TestRestoredTabStillRenamesItself(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	base := filepath.Base(root)

	saved := &store.State{Tabs: []store.Tab{
		{
			Title: base,
			Focus: "unprompted",
			Root:  &store.Node{Pane: &store.Pane{ID: "unprompted", Kind: "claude", Cwd: root}},
		},
		{
			Title: "already named by hand",
			Focus: "named",
			Root:  &store.Node{Pane: &store.Pane{ID: "named", Kind: "claude", Cwd: root}},
		},
	}}
	if err := store.Save(root, saved); err != nil {
		t.Fatalf("save: %v", err)
	}

	ws := newTestWorkspace(t, root)
	if ok, err := ws.Restore(); err != nil || !ok {
		t.Fatalf("restore: ok=%v err=%v", ok, err)
	}

	ws.nameTabAfterPrompt("unprompted", "rewrite the parser")
	ws.nameTabAfterPrompt("named", "rewrite the parser")

	tabs := ws.VisibleTabs()
	if tabs[0].Title == base {
		t.Errorf("tab title = %q, want it renamed after the first prompt", tabs[0].Title)
	}
	if tabs[1].Title != "already named by hand" {
		t.Errorf("tab title = %q, want a name the user chose left alone", tabs[1].Title)
	}
}

// TestRestoredShellTabKeepsItsName guards the other half: shell panes report
// no prompts, so a shell tab named after its directory is not waiting to be
// renamed and must not be treated as though it were.
func TestRestoredShellTabKeepsItsName(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()

	saved := &store.State{Tabs: []store.Tab{{
		Title: filepath.Base(root),
		Root:  &store.Node{Pane: &store.Pane{ID: "sh", Kind: "shell", Cwd: root}},
	}}}
	if err := store.Save(root, saved); err != nil {
		t.Fatalf("save: %v", err)
	}

	ws := newTestWorkspace(t, root)
	if ok, err := ws.Restore(); err != nil || !ok {
		t.Fatalf("restore: ok=%v err=%v", ok, err)
	}
	if ws.VisibleTabs()[0].AutoTitle {
		t.Error("a shell tab should not be marked as waiting for a prompt to name it")
	}
}

// TestRestoreSessionDoesNotReopenTheActiveProject covers a saved session that
// spells the project the window was started on differently — a trailing
// separator here, but on Windows and macOS it is as likely to be the case of a
// drive letter. Reopening it would show the same directory twice, each copy
// resuming the other's conversations.
func TestRestoreSessionDoesNotReopenTheActiveProject(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()

	ws := newTestWorkspace(t, root)
	ws.NewTab(session.KindShell, root, "alpha")
	if err := ws.SaveAll(); err != nil {
		t.Fatalf("save: %v", err)
	}
	ws.Close()

	// Rewrite the session the way an earlier run might have left it.
	if err := store.SaveSession(&store.Session{
		Open:   []string{root + string(os.PathSeparator)},
		Active: root,
	}); err != nil {
		t.Fatalf("save session: %v", err)
	}

	again := newTestWorkspace(t, root)
	if _, err := again.Restore(); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if n := again.RestoreSession(); n != 0 {
		t.Errorf("reopened %d projects, want none: it is already open", n)
	}
	if got := again.Projects(); len(got) != 1 {
		t.Errorf("projects = %d, want the one directory listed once", len(got))
	}
}

// TestRestoreSessionKeepsTheTabTheUserLeftOn checks that bringing the other
// projects back does not disturb which tab of the active project is on screen.
func TestRestoreSessionKeepsTheTabTheUserLeftOn(t *testing.T) {
	isolateConfig(t)
	first, second := t.TempDir(), t.TempDir()

	ws := newTestWorkspace(t, first)
	ws.NewTab(session.KindShell, first, "alpha")
	ws.NewTab(session.KindShell, first, "beta")
	beta := ws.ActiveTabID()
	if err := ws.OpenProject(second); err != nil {
		t.Fatalf("open second: %v", err)
	}
	ws.NewTab(session.KindShell, second, "theirs")
	// Quit while looking at the second of the first project's two tabs.
	ws.SelectTab(beta)
	if err := ws.SaveAll(); err != nil {
		t.Fatalf("save: %v", err)
	}
	ws.Close()

	again := newTestWorkspace(t, first)
	if _, err := again.Restore(); err != nil {
		t.Fatalf("restore: %v", err)
	}
	again.RestoreSession()

	current := again.CurrentTab()
	if current == nil {
		t.Fatal("no tab is on screen after restoring")
	}
	if current.Title != "beta" {
		t.Errorf("tab on screen = %q, want the one the window was left on", current.Title)
	}
}

// TestRestoreSessionKeepsReopenedProjectsRecent checks that a project brought
// back by the saved session counts as used. The recent list is capped, so a
// project that is always open but never opened by hand would otherwise be the
// first to fall out of the picker.
func TestRestoreSessionKeepsReopenedProjectsRecent(t *testing.T) {
	isolateConfig(t)
	first, second := t.TempDir(), t.TempDir()

	ws := newTestWorkspace(t, first)
	if err := ws.OpenProject(second); err != nil {
		t.Fatalf("open second: %v", err)
	}
	if err := ws.SaveAll(); err != nil {
		t.Fatalf("save: %v", err)
	}
	ws.Close()

	// Push the second project out of living memory, as a run that never opened
	// it by hand would leave it.
	if err := store.ForgetRecent(second); err != nil {
		t.Fatalf("forget: %v", err)
	}

	again := newTestWorkspace(t, first)
	if n := again.RestoreSession(); n != 1 {
		t.Fatalf("reopened %d projects, want 1", n)
	}
	recents, err := store.Recents()
	if err != nil {
		t.Fatalf("recents: %v", err)
	}
	found := false
	for _, p := range recents {
		if p.Root == second {
			found = true
		}
	}
	if !found {
		t.Errorf("recents = %v, want the reopened project among them", recents)
	}
}

// TestRestoreSessionCanonicalisesRoots checks that a project brought back from
// the saved session is held under the same spelling every other route into the
// workspace uses, so opening it again from the picker finds it rather than
// starting a second copy of it.
func TestRestoreSessionCanonicalisesRoots(t *testing.T) {
	isolateConfig(t)
	first, second := t.TempDir(), t.TempDir()

	ws := newTestWorkspace(t, first)
	if err := ws.OpenProject(second); err != nil {
		t.Fatalf("open second: %v", err)
	}
	if err := ws.SaveAll(); err != nil {
		t.Fatalf("save: %v", err)
	}
	ws.Close()

	if err := store.SaveSession(&store.Session{
		Open: []string{first, second + string(os.PathSeparator)},
	}); err != nil {
		t.Fatalf("save session: %v", err)
	}

	again := newTestWorkspace(t, first)
	if n := again.RestoreSession(); n != 1 {
		t.Fatalf("reopened %d projects, want 1", n)
	}
	// Opening the same directory again must select the project, not add one.
	if err := again.OpenProject(second); err != nil {
		t.Fatalf("open: %v", err)
	}
	if got := again.Projects(); len(got) != 2 {
		t.Fatalf("projects = %d, want the two distinct directories", len(got))
	}
	if again.ActiveRoot() != second {
		t.Errorf("active root = %q, want %q", again.ActiveRoot(), second)
	}
}

// TestRestoreLandsBesideATabThatIsGone covers the tab the window was left on
// being one that cannot be restored — its pane's directory deleted since, as a
// finished worktree would be. The window should come up on the tab beside it,
// the way closing a tab lands on a neighbour, rather than at the front of the
// bar.
func TestRestoreLandsBesideATabThatIsGone(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()

	tab := func(title, id string, cwd string) store.Tab {
		return store.Tab{
			Title: title,
			Focus: id,
			Root:  &store.Node{Pane: &store.Pane{ID: id, Kind: "shell", Cwd: cwd}},
		}
	}
	saved := &store.State{
		Active: 2,
		Tabs: []store.Tab{
			tab("one", "a", root),
			tab("two", "b", root),
			tab("gone", "c", ""), // no directory: cannot be restored
			tab("four", "d", root),
		},
	}
	if err := store.Save(root, saved); err != nil {
		t.Fatalf("save: %v", err)
	}

	ws := newTestWorkspace(t, root)
	if ok, err := ws.Restore(); err != nil || !ok {
		t.Fatalf("restore: ok=%v err=%v", ok, err)
	}
	current := ws.CurrentTab()
	if current == nil {
		t.Fatal("no tab is on screen after restoring")
	}
	if current.Title != "two" {
		t.Errorf("tab on screen = %q, want the neighbour of the one that is gone", current.Title)
	}
}

// layoutFileFor finds the layout file a project's state was written to.
func layoutFileFor(t *testing.T, root string) string {
	t.Helper()
	dir, err := store.Dir()
	if err != nil {
		t.Fatalf("state dir: %v", err)
	}
	files, err := filepath.Glob(filepath.Join(dir, "layout-*.json"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var st store.State
		if json.Unmarshal(data, &st) == nil && st.Root == root {
			return f
		}
	}
	t.Fatalf("no layout file was written for %s", root)
	return ""
}

// TestSaveAllNamesTheProjectThatFailed covers the message printed on the way
// out. It is the only thing the user gets, and with several projects open
// "could not save layout" does not say which one to go and look at.
func TestSaveAllNamesTheProjectThatFailed(t *testing.T) {
	isolateConfig(t)
	first, second := t.TempDir(), t.TempDir()

	ws := newTestWorkspace(t, first)
	ws.NewTab(session.KindShell, first, "alpha")
	if err := ws.OpenProject(second); err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := ws.SaveAll(); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Put a directory where the second project's layout file belongs, so that
	// one save cannot be written and the other still goes through.
	blocked := layoutFileFor(t, second)
	if err := os.Remove(blocked); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(blocked, 0o755); err != nil {
		t.Fatal(err)
	}

	err := ws.SaveAll()
	if err == nil {
		t.Fatal("expected the blocked project to report a failure")
	}
	if !strings.Contains(err.Error(), second) {
		t.Errorf("error = %q, want it to name the project %q", err, second)
	}
	if strings.Contains(err.Error(), first) {
		t.Errorf("error = %q, want only the project that failed named", err)
	}
}
