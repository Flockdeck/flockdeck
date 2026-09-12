package workspace

import (
	"path/filepath"
	"testing"

	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/store"
)

// TestARenamedTabGoesBackToTheTitleItWouldHaveNow covers the way back from a
// name chosen by hand. Renaming used to clear AutoTitle for good, so a tab
// named once could never name itself again. Giving the name up now puts back
// the title the tab would have had it never been renamed — including what a
// prompt that arrived while the name was on show made of it.
func TestARenamedTabGoesBackToTheTitleItWouldHaveNow(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)

	tab := ws.NewTab(session.KindClaude, root, "")
	base := tab.Title
	ws.NameTab(tab.ID, "parser work")
	if !tab.Named || tab.AutoTitle {
		t.Fatalf("named = %v, auto = %v after a rename, want a name no prompt replaces", tab.Named, tab.AutoTitle)
	}
	ws.nameTabAfterPrompt(tab.Focus, "rewrite the parser")
	ws.Projects()
	if tab.Title != "parser work" {
		t.Fatalf("title = %q, want the name chosen by hand kept through a prompt", tab.Title)
	}

	if !ws.UseAutoTitle(tab.ID) {
		t.Fatal("giving up the name changed nothing")
	}
	if tab.Title != "rewrite the parser" || tab.Named || tab.AutoTitle {
		t.Errorf("title = %q, named = %v, auto = %v, want what the first prompt named it, left alone from then on",
			tab.Title, tab.Named, tab.AutoTitle)
	}
	if ws.UseAutoTitle(tab.ID) {
		t.Error("a tab that already has its automatic title was changed again")
	}

	// Named twice before any prompt, it goes back to its directory name and
	// to waiting for the first prompt, as a tab never named would be.
	fresh := ws.NewTab(session.KindClaude, root, "")
	ws.NameTab(fresh.ID, "later")
	ws.NameTab(fresh.ID, "later still")
	ws.UseAutoTitle(fresh.ID)
	if fresh.Title != base || !fresh.AutoTitle {
		t.Fatalf("title = %q, auto = %v, want %q waiting for a prompt", fresh.Title, fresh.AutoTitle, base)
	}
	ws.nameTabAfterPrompt(fresh.Focus, "add the tests")
	ws.Projects()
	if fresh.Title != "add the tests" {
		t.Errorf("title = %q, want the tab named after the prompt it was given", fresh.Title)
	}
}

// TestAHandNameIsKeptWithTheLayout covers a restart between naming a tab and
// giving the name up. The layout carries the name, that it was chosen by
// hand, and the title behind it; without them a restored tab either lost its
// way back or took its name for its automatic title.
func TestAHandNameIsKeptWithTheLayout(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)

	unprompted := ws.NewTab(session.KindClaude, root, "")
	base := unprompted.Title
	ws.NameTab(unprompted.ID, "mine")
	prompted := ws.NewTab(session.KindClaude, root, "")
	ws.nameTabAfterPrompt(prompted.Focus, "rewrite the parser")
	ws.Projects()
	ws.NameTab(prompted.ID, "also mine")
	ws.NewTab(session.KindClaude, root, "")
	if err := ws.SaveProject(root); err != nil {
		t.Fatalf("save: %v", err)
	}

	st, err := store.Load(root)
	if err != nil || st == nil {
		t.Fatalf("load: %v", err)
	}
	if !st.HandNames {
		t.Error("the layout does not say it marks names chosen by hand")
	}
	if got := st.Tabs[0]; !got.Named || got.Title != "mine" || got.Auto != base {
		t.Errorf("saved %+v, want the name, marked as chosen by hand, over %q", got, base)
	}
	if got := st.Tabs[2]; got.Named || got.Auto != "" {
		t.Errorf("saved %+v, want a tab that names itself saved as it always was", got)
	}

	again := newTestWorkspace(t, root)
	if ok, err := again.Restore(); err != nil || !ok {
		t.Fatalf("restore: ok=%v err=%v", ok, err)
	}
	tabs := again.VisibleTabs()
	if len(tabs) != 3 {
		t.Fatalf("restored %d tabs, want 3", len(tabs))
	}
	for _, tab := range tabs[:2] {
		if !tab.Named || tab.AutoTitle {
			t.Errorf("%q came back named = %v, auto = %v, want a name no prompt replaces", tab.Title, tab.Named, tab.AutoTitle)
		}
	}
	if tabs[2].Named || !tabs[2].AutoTitle {
		t.Errorf("an unnamed tab came back named = %v, auto = %v", tabs[2].Named, tabs[2].AutoTitle)
	}

	// The title behind the first is still waiting for a prompt, and follows
	// one without the name moving.
	again.nameTabAfterPrompt(tabs[0].Focus, "add the tests")
	again.Projects()
	if tabs[0].Title != "mine" {
		t.Fatalf("title = %q, want the name kept through a prompt", tabs[0].Title)
	}
	again.UseAutoTitle(tabs[0].ID)
	if tabs[0].Title != "add the tests" || tabs[0].AutoTitle {
		t.Errorf("title = %q, auto = %v, want what the prompt named it", tabs[0].Title, tabs[0].AutoTitle)
	}
	again.UseAutoTitle(tabs[1].ID)
	if tabs[1].Title != "rewrite the parser" || tabs[1].AutoTitle {
		t.Errorf("title = %q, auto = %v, want the title its first prompt gave it", tabs[1].Title, tabs[1].AutoTitle)
	}
}

// TestATabNamedInAnOlderLayoutCanGoBack covers a layout written before names
// chosen by hand were marked, which cannot say who chose a title. One that is
// not what the tab would be called now is taken for a name, so it can be given
// up; the title behind it is not known, so giving it up gives the tab the
// title a tab opened now would have.
func TestATabNamedInAnOlderLayoutCanGoBack(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	base := filepath.Base(root)

	saved := &store.State{Tabs: []store.Tab{
		{
			Title: "already named by hand",
			Focus: "named",
			Root:  &store.Node{Pane: &store.Pane{ID: "named", Kind: "claude", Cwd: root}},
		},
		{
			Title: base,
			Focus: "unprompted",
			Root:  &store.Node{Pane: &store.Pane{ID: "unprompted", Kind: "claude", Cwd: root}},
		},
		{
			Title: base,
			Focus: "sh",
			Root:  &store.Node{Pane: &store.Pane{ID: "sh", Kind: "shell", Cwd: root}},
		},
	}}
	if err := store.Save(root, saved); err != nil {
		t.Fatalf("save: %v", err)
	}

	ws := newTestWorkspace(t, root)
	if ok, err := ws.Restore(); err != nil || !ok {
		t.Fatalf("restore: ok=%v err=%v", ok, err)
	}
	tabs := ws.VisibleTabs()
	if !tabs[0].Named {
		t.Fatal("a title that is not the automatic one was not taken for a name, so it cannot be given up")
	}
	if tabs[1].Named || !tabs[1].AutoTitle {
		t.Errorf("a tab still under its directory name came back named = %v, auto = %v", tabs[1].Named, tabs[1].AutoTitle)
	}
	if tabs[2].Named {
		t.Error("a shell tab under the name it would be given anyway was taken for one named by hand")
	}

	if !ws.UseAutoTitle(tabs[0].ID) {
		t.Fatal("giving up the name changed nothing")
	}
	if tabs[0].Title != base || !tabs[0].AutoTitle {
		t.Errorf("title = %q, auto = %v, want %q waiting for a prompt", tabs[0].Title, tabs[0].AutoTitle, base)
	}
}
