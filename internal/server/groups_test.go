package server

import (
	"testing"
)

// TestGroupProjectsCommandMergesTheSwitcher covers "Group open projects…"
// end to end over the control socket: two projects opened separately merge
// into one switcher entry once grouped.
func TestGroupProjectsCommandMergesTheSwitcher(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	nextState(t, conn, nil)
	first := srv.activeRoot()
	second := t.TempDir()
	srv.openProjectOnOwner(t, second)
	srv.selectProjectOnOwner(t, first)

	sendCmd(t, conn, command{Cmd: "groupProjects", Roots: []string{first, second}, Text: "platform"})
	// len(Projects) == 1 is also true of the very first state, before second
	// is even opened, so the predicate has to name what only a completed
	// merge produces.
	msg := nextState(t, conn, func(s stateMsg) bool {
		return len(s.Projects) == 1 && len(s.Projects[0].Members) == 2
	})
	if len(msg.Projects) != 1 {
		t.Fatalf("projects = %d, want 1 merged entry", len(msg.Projects))
	}
	if msg.Projects[0].Name != "platform" {
		t.Errorf("name = %q, want platform", msg.Projects[0].Name)
	}
	if len(msg.Projects[0].Members) != 2 {
		t.Errorf("members = %d, want 2", len(msg.Projects[0].Members))
	}
}

// TestAddRepoToGroupCommand covers "Add Repo to This Project…": a folder
// opened this way joins the active project's group rather than becoming a
// switcher entry of its own.
func TestAddRepoToGroupCommand(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	nextState(t, conn, nil)
	first := srv.activeRoot()
	second := t.TempDir()
	srv.openProjectOnOwner(t, second)
	srv.groupOnOwner(t, []string{first, second}, "platform")
	srv.selectProjectOnOwner(t, first)

	third := t.TempDir()
	sendCmd(t, conn, command{Cmd: "addRepoToGroup", Root: first, Path: third})
	msg := nextState(t, conn, func(s stateMsg) bool {
		return len(s.Projects) == 1 && len(s.Projects[0].Members) == 3
	})
	if len(msg.Projects) != 1 || len(msg.Projects[0].Members) != 3 {
		t.Fatalf("projects = %+v, want one project with 3 members", msg.Projects)
	}
}

// TestRemoveRepoFromGroupCommand covers ungrouping one repo from its
// project over the control socket, without closing it: it should read as
// its own project again, with the group left holding the other member.
func TestRemoveRepoFromGroupCommand(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	nextState(t, conn, nil)
	first := srv.activeRoot()
	second := t.TempDir()
	srv.openProjectOnOwner(t, second)
	srv.groupOnOwner(t, []string{first, second}, "platform")

	sendCmd(t, conn, command{Cmd: "removeRepoFromGroup", Root: second})
	msg := nextState(t, conn, func(s stateMsg) bool { return len(s.Projects) == 2 })
	if len(msg.Projects) != 2 {
		t.Fatalf("projects = %d, want the group and the split-out repo as two entries", len(msg.Projects))
	}
}

// TestRenameGroupCommand covers the picker's rename dialog reaching a
// multi-repo project by naming any one of its members.
func TestRenameGroupCommand(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	nextState(t, conn, nil)
	first := srv.activeRoot()

	sendCmd(t, conn, command{Cmd: "renameGroup", Root: first, Text: "renamed"})
	msg := nextState(t, conn, func(s stateMsg) bool {
		return len(s.Projects) == 1 && s.Projects[0].Name == "renamed"
	})
	if msg.Projects[0].Name != "renamed" {
		t.Fatalf("name = %q, want renamed", msg.Projects[0].Name)
	}
}

// selectProjectOnOwner runs SelectProject on the workspace goroutine.
func (s *Server) selectProjectOnOwner(t *testing.T, root string) {
	t.Helper()
	if _, ok := ask(s, func() bool { s.ws.SelectProject(root); return true }); !ok {
		t.Fatal("the server closed before the project was selected")
	}
}

// groupOnOwner runs NewGroupFrom on the workspace goroutine.
func (s *Server) groupOnOwner(t *testing.T, roots []string, name string) {
	t.Helper()
	err, ok := ask(s, func() error { _, err := s.ws.NewGroupFrom(roots, name); return err })
	if !ok {
		t.Fatal("the server closed before the group was made")
	}
	if err != nil {
		t.Fatalf("group: %v", err)
	}
}
