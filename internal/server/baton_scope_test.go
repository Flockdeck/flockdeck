package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/workspace"
)

// An agent that spawns with -baton path:<file> has Flockdeck read the file as the
// user. A file inside the pane's project is read; one outside it, or reached
// through a link inside it, is not.
func TestABatonFileHasToBeInsideTheSpawningPanesProject(t *testing.T) {
	srv, ws := newTestServer(t)
	batonAgent(t, ws)
	pane := spawnGoPane(t, srv, ws)

	inside := writeNotesFor(t, pane)
	if _, err := srv.resolveBaton(pane, "path:"+inside); err != nil {
		t.Errorf("a file in the pane's project was refused: %v", err)
	}
	// A subfolder of the project is inside it too.
	sub := filepath.Join(paneDir(t, pane), "docs")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	deep := filepath.Join(sub, "plan.md")
	if err := os.WriteFile(deep, []byte("the plan"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.resolveBaton(pane, "path:"+deep); err != nil {
		t.Errorf("a file in a subfolder was refused: %v", err)
	}

	outside := filepath.Join(t.TempDir(), "elsewhere.md")
	if err := os.WriteFile(outside, []byte("not yours"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := srv.resolveBaton(pane, "path:"+outside)
	if err == nil || !strings.Contains(err.Error(), "outside this pane's project") {
		t.Errorf("a file outside the project: err = %v", err)
	}

	// A folder inside the project that is a link to somewhere else does not let a
	// file there through.
	link := filepath.Join(paneDir(t, pane), "linked")
	if err := os.Symlink(filepath.Dir(outside), link); err != nil {
		t.Logf("no symbolic links here, so that case is not run: %v", err)
		return
	}
	_, err = srv.resolveBaton(pane, "path:"+filepath.Join(link, "elsewhere.md"))
	if err == nil || !strings.Contains(err.Error(), "outside this pane's project") {
		t.Errorf("a file reached through a link out of the project: err = %v", err)
	}
}

// Naming a pane makes a baton from its conversation. Only a pane of the spawning
// pane's own project may be named.
func TestABatonFromANamedPaneHasToBeOfTheSameProject(t *testing.T) {
	srv, ws, parent := targetsFor(t, "gocli", "gocli")
	var first, second string
	var otherPane string
	ask(srv, func() struct{} {
		second = ws.RootOf(parent)
		first = ws.ActiveRoot()
		tab := ws.NewTabWith(workspace.Choice{Kind: session.KindClaude, Agent: "gocli"}, first, "other")
		otherPane = tab.Focus
		return struct{}{}
	})
	if otherPane == "" || first == second {
		t.Fatalf("setup: panes in %q and %q", first, second)
	}
	_, err := srv.resolveBaton(parent, otherPane)
	if err == nil || !strings.Contains(err.Error(), "another project") {
		t.Errorf("a pane of another project: err = %v", err)
	}
	// A pane of its own project is fine.
	var sibling string
	ask(srv, func() struct{} {
		sibling = ws.NewTabWith(workspace.Choice{Kind: session.KindClaude, Agent: "gocli"}, second, "sibling").Focus
		return struct{}{}
	})
	if _, err := srv.resolveBaton(parent, sibling); err != nil {
		t.Errorf("a pane of its own project was refused: %v", err)
	}
}
