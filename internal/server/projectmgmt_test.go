package server

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/jmwri/flockdeck/internal/store"
)

// TestRenameProjectSavesAndAppliesTheName checks the renameProject command
// both writes the name to the recent-projects list, so the picker's Recent
// section can show it, and applies it to the open project's own summary at
// once, so the switcher and the rail need no restart to catch up.
func TestRenameProjectSavesAndAppliesTheName(t *testing.T) {
	srv, ws := newTestServer(t)
	c := &controlClient{out: make(chan []byte, 8)}
	root := ws.ActiveRoot()

	srv.handleCommand(c, command{Cmd: "renameProject", Root: root, Text: "  My Project  "})

	list, err := store.Recents()
	if err != nil {
		t.Fatalf("recents: %v", err)
	}
	found := false
	for _, p := range list {
		if p.Root == filepath.Clean(root) {
			found = true
			if p.Name != "My Project" {
				t.Errorf("stored name = %q, want %q (trimmed)", p.Name, "My Project")
			}
		}
	}
	if !found {
		t.Fatal("the project was not in the recent list after renaming it")
	}

	projects, ok := ask(srv, ws.Projects)
	if !ok {
		t.Fatal("could not read Projects back")
	}
	if len(projects) != 1 || projects[0].Name != "My Project" {
		t.Fatalf("projects = %+v, want the open project's own name to follow the rename", projects)
	}

	// Renaming to nothing goes back to the automatic name.
	srv.handleCommand(c, command{Cmd: "renameProject", Root: root, Text: ""})
	projects, ok = ask(srv, ws.Projects)
	if !ok {
		t.Fatal("could not read Projects back")
	}
	if projects[0].Name == "My Project" {
		t.Error("an empty rename did not go back to the automatic name")
	}
}

// TestArchiveProjectCommandTogglesTheFlag checks archiving and unarchiving
// through the control socket, and that an archived project stays open.
func TestArchiveProjectCommandTogglesTheFlag(t *testing.T) {
	srv, ws := newTestServer(t)
	c := &controlClient{out: make(chan []byte, 8)}
	root := ws.ActiveRoot()

	srv.handleCommand(c, command{Cmd: "archiveProject", Root: root, Archived: true})
	projects, ok := ask(srv, ws.Projects)
	if !ok {
		t.Fatal("could not read Projects back")
	}
	if len(projects) != 1 || !projects[0].Archived {
		t.Fatalf("projects = %+v, want the project archived", projects)
	}
	if got, _ := ask(srv, ws.ActiveRoot); got != root {
		t.Error("archiving the active project closed it")
	}

	srv.handleCommand(c, command{Cmd: "archiveProject", Root: root, Archived: false})
	if projects, ok = ask(srv, ws.Projects); !ok || projects[0].Archived {
		t.Fatalf("projects = %+v, ok=%v, want the project unarchived", projects, ok)
	}
}

// TestReorderProjectsCommandSortsRecents checks the reorderProjects command
// gives the recent list the order sent, ahead of recency.
func TestReorderProjectsCommandSortsRecents(t *testing.T) {
	srv, _ := newTestServer(t)
	c := &controlClient{out: make(chan []byte, 8)}
	a, b := t.TempDir(), t.TempDir()
	if err := store.TouchRecents(a, b); err != nil {
		t.Fatalf("touch: %v", err)
	}

	srv.handleCommand(c, command{Cmd: "reorderProjects", Roots: []string{b, a}})

	list, err := store.Recents()
	if err != nil {
		t.Fatalf("recents: %v", err)
	}
	var got []string
	for _, p := range list {
		got = append(got, p.Root)
	}
	want := []string{filepath.Clean(b), filepath.Clean(a)}
	if len(got) < 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("recents order = %v, want %v first", got, want)
	}
}

// TestRemoveProjectRefusesToLeaveNothingOpen checks the guard on the picker's
// × control for an open project: removing the only project open must fail
// with a clear reason, closing nothing and forgetting nothing, rather than
// leave the picker with no project open at all.
func TestRemoveProjectRefusesToLeaveNothingOpen(t *testing.T) {
	srv, ws := newTestServer(t)
	c := &controlClient{out: make(chan []byte, 8)}
	root := ws.ActiveRoot()

	srv.handleCommand(c, command{Cmd: "removeProject", Root: root})

	sawError := false
drain:
	for {
		select {
		case data := <-c.out:
			var note noticeMsg
			if json.Unmarshal(data, &note) == nil && note.Type == "notice" && note.Error {
				sawError = true
			}
		default:
			break drain
		}
	}
	if !sawError {
		t.Fatal("removing the last open project did not report an error")
	}

	projects, ok := ask(srv, ws.Projects)
	if !ok || len(projects) != 1 || projects[0].Root != root {
		t.Fatalf("projects = %+v, ok=%v, want the last project to stay open", projects, ok)
	}
	list, err := store.Recents()
	if err != nil {
		t.Fatalf("recents: %v", err)
	}
	found := false
	for _, p := range list {
		if p.Root == filepath.Clean(root) {
			found = true
		}
	}
	if !found {
		t.Error("the last open project was forgotten despite the guard refusing to remove it")
	}
}

// TestRemoveProjectClosesAndForgetsAnOpenProject checks that removing a
// second open project, through the same command the picker's × sends for a
// not-open row, both closes it and drops it from the recent list in one
// step -- the case removeProject exists for instead of the plain
// forgetRecent.
func TestRemoveProjectClosesAndForgetsAnOpenProject(t *testing.T) {
	srv, ws := newTestServer(t)
	c := &controlClient{out: make(chan []byte, 8)}
	first := ws.ActiveRoot()
	second := t.TempDir()

	if _, ok := ask(srv, func() bool {
		if err := ws.OpenProject(second); err != nil {
			t.Error(err)
		}
		return true
	}); !ok {
		t.Fatal("server closed")
	}

	srv.handleCommand(c, command{Cmd: "removeProject", Root: second})

	projects, ok := ask(srv, ws.Projects)
	if !ok {
		t.Fatal("could not read Projects back")
	}
	if len(projects) != 1 || projects[0].Root != first {
		t.Fatalf("projects = %+v, want only %s left open", projects, first)
	}
	list, err := store.Recents()
	if err != nil {
		t.Fatalf("recents: %v", err)
	}
	for _, p := range list {
		if p.Root == filepath.Clean(second) {
			t.Error("the removed project was still in the recent list")
		}
	}
}

// TestRemoveProjectForgetsANotOpenRecentProject checks removeProject also
// covers the ordinary case, a recent project that is not open at all: it
// simply drops from the list, the way forgetRecent always has.
func TestRemoveProjectForgetsANotOpenRecentProject(t *testing.T) {
	srv, _ := newTestServer(t)
	c := &controlClient{out: make(chan []byte, 8)}
	gone := t.TempDir()
	if err := store.TouchRecent(gone); err != nil {
		t.Fatalf("touch: %v", err)
	}

	srv.handleCommand(c, command{Cmd: "removeProject", Root: gone})

	list, err := store.Recents()
	if err != nil {
		t.Fatalf("recents: %v", err)
	}
	for _, p := range list {
		if p.Root == filepath.Clean(gone) {
			t.Fatal("the not-open project was still in the recent list after removeProject")
		}
	}
}
