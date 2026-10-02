package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/store"
)

// isolateAutoReview is isolateConfig plus USERPROFILE, which Windows reads for
// the home directory.
func isolateAutoReview(t *testing.T, defaultOn bool) {
	t.Helper()
	isolateConfig(t)
	t.Setenv("USERPROFILE", stateTempDir(t))
	if err := store.SavePrefs(store.Prefs{AutoReviewDefault: defaultOn}); err != nil {
		t.Fatalf("SavePrefs: %v", err)
	}
}

// A pane's own switch survives save and restore, off as well as on, whatever
// the default is: a pane turned off while the default is on stays off.
func TestAutoReviewSurvivesSaveAndRestore(t *testing.T) {
	for _, defaultOn := range []bool{false, true} {
		isolateAutoReview(t, defaultOn)
		root := t.TempDir()
		ws := newTestWorkspace(t, root)
		on := ws.Pane(ws.NewTab(session.KindShell, root, "on").Tree.Panes()[0])
		off := ws.Pane(ws.NewTab(session.KindShell, root, "off").Tree.Panes()[0])
		ws.SetPaneAutoReview(on.ID, true)
		ws.SetPaneAutoReview(off.ID, false)
		if err := ws.SaveAll(); err != nil {
			t.Fatalf("save: %v", err)
		}
		ws.Close()

		restored := newTestWorkspace(t, root)
		if ok, err := restored.Restore(); err != nil || !ok {
			t.Fatalf("restore: ok=%v err=%v", ok, err)
		}
		if p := restored.Pane(on.ID); p == nil || !p.AutoReview {
			t.Errorf("default %v: a pane turned on came back off", defaultOn)
		}
		if p := restored.Pane(off.ID); p == nil || p.AutoReview {
			t.Errorf("default %v: a pane turned off came back on", defaultOn)
		}
		restored.Close()
	}
}

// A layout saved before the field was kept has none, and takes the default
// rather than off.
func TestRestoreWithoutAutoReviewFieldUsesTheDefault(t *testing.T) {
	for _, defaultOn := range []bool{false, true} {
		isolateAutoReview(t, defaultOn)
		root := t.TempDir()
		ws := newTestWorkspace(t, root)
		p := ws.Pane(ws.NewTab(session.KindShell, root, "old").Tree.Panes()[0])
		if err := ws.SaveAll(); err != nil {
			t.Fatalf("save: %v", err)
		}
		ws.Close()

		// Make the saved layout look like an old one.
		st, err := store.Load(root)
		if err != nil || st == nil {
			t.Fatalf("load layout: %v", err)
		}
		var strip func(n *store.Node)
		strip = func(n *store.Node) {
			if n == nil {
				return
			}
			if n.Pane != nil {
				n.Pane.AutoReview = nil
			}
			for _, c := range n.Children {
				strip(c)
			}
		}
		for i := range st.Tabs {
			strip(st.Tabs[i].Root)
		}
		if err := store.Save(root, st); err != nil {
			t.Fatalf("save layout: %v", err)
		}

		restored := newTestWorkspace(t, root)
		if ok, err := restored.Restore(); err != nil || !ok {
			t.Fatalf("restore: ok=%v err=%v", ok, err)
		}
		if got := restored.Pane(p.ID); got == nil || got.AutoReview != defaultOn {
			t.Errorf("default %v: an old layout's pane came back as %+v", defaultOn, got)
		}
		restored.Close()
	}
}

// A conversation resumed from the past starts from the default, like any
// pane opened by hand.
func TestResumedConversationStartsFromTheAutoReviewDefault(t *testing.T) {
	goExe, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go is not on PATH")
	}
	for _, defaultOn := range []bool{false, true} {
		isolateAutoReview(t, defaultOn)
		base, err := store.Dir()
		if err != nil {
			t.Fatal(err)
		}
		chats := filepath.Join(base, "chats")
		if err := os.MkdirAll(chats, 0o700); err != nil {
			t.Fatal(err)
		}
		const id = "55555555-5555-5555-5555-555555555555"
		if err := os.WriteFile(filepath.Join(chats, id+".jsonl"),
			[]byte(`{"type":"user","ts":1,"text":"hello"}`+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		root := t.TempDir()
		ws, err := New(Options{Root: root, HookBinary: goExe})
		if err != nil {
			t.Fatal(err)
		}
		if err := ws.OpenConversation(id, root, "chat"); err != nil {
			t.Fatalf("open: %v", err)
		}
		if p := ws.Pane(id); p == nil || p.AutoReview != defaultOn {
			t.Errorf("default %v: resumed pane was %+v", defaultOn, p)
		}
		ws.Close()
	}
}
