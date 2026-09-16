package workspace

import (
	"testing"

	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/store"
)

// A pane with no parent -- one opened by hand, or a fresh fan-out row run
// from the window -- starts AutoReview from Prefs.AutoReviewDefault. See
// store.Prefs.AutoReviewDefault and Pane.AutoReview.
func TestNewPaneStartsFromTheAutoReviewDefault(t *testing.T) {
	isolateConfig(t)
	if err := store.SavePrefs(store.Prefs{AutoReviewDefault: true}); err != nil {
		t.Fatalf("SavePrefs: %v", err)
	}
	root := t.TempDir()
	ws, err := New(Options{Root: root})
	if err != nil {
		t.Fatalf("new workspace: %v", err)
	}
	t.Cleanup(ws.Close)

	tab := ws.NewTab(session.KindShell, root, "shell")
	if tab == nil {
		t.Fatal("no tab was opened")
	}
	id := tab.Tree.Panes()[0]
	p := ws.Pane(id)
	if p == nil {
		t.Fatal("the pane was not created")
	}
	if !p.AutoReview {
		t.Error("a pane opened by hand did not start with AutoReview on, though the default is on")
	}
}

// The default is off unless it has been turned on -- a restart, and a fresh
// pane, still ask about everything until it is.
func TestNewPaneAutoReviewIsOffByDefault(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws, err := New(Options{Root: root})
	if err != nil {
		t.Fatalf("new workspace: %v", err)
	}
	t.Cleanup(ws.Close)

	tab := ws.NewTab(session.KindShell, root, "shell")
	id := tab.Tree.Panes()[0]
	if p := ws.Pane(id); p == nil || p.AutoReview {
		t.Errorf("a fresh install's pane started with AutoReview on: %+v", p)
	}
}

// A fan-out run from the window gives its children no Parent, so they too
// start from the default -- not inherited from anything, since there is
// nothing to inherit from.
func TestSpawnWithNoParentStartsFromTheAutoReviewDefault(t *testing.T) {
	isolateConfig(t)
	if err := store.SavePrefs(store.Prefs{AutoReviewDefault: true}); err != nil {
		t.Fatalf("SavePrefs: %v", err)
	}
	root := t.TempDir()
	ws, err := New(Options{Root: root})
	if err != nil {
		t.Fatalf("new workspace: %v", err)
	}
	t.Cleanup(ws.Close)

	id, err := ws.Spawn("", SpawnOptions{Cwd: root, Kind: session.KindShell})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	p := ws.Pane(id)
	if p == nil {
		t.Fatal("the spawned pane was not created")
	}
	if p.Parent != "" {
		t.Fatalf("test fixture assumption broken: a fan-out run from the window should carry no Parent, got %q", p.Parent)
	}
	if !p.AutoReview {
		t.Error("a fan-out child with no parent did not start with AutoReview on, though the default is on")
	}
}
