package server

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// readUntil reads control messages until one of the given type arrives, and
// decodes it into out.
func readUntil(t *testing.T, conn *websocket.Conn, typ string, out any) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		_, data, err := conn.Read(ctx)
		cancel()
		if err != nil {
			t.Fatalf("read control: %v", err)
		}
		var probe struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(data, &probe) != nil || probe.Type != typ {
			continue
		}
		if err := json.Unmarshal(data, out); err != nil {
			t.Fatalf("decode %s: %v", typ, err)
		}
		return
	}
	t.Fatalf("timed out waiting for a %q message", typ)
}

// TestOpenAndSwitchProjects drives the picker the way the window does.
func TestOpenAndSwitchProjects(t *testing.T) {
	srv, ws := newTestServer(t)
	conn := dialControl(t, srv)

	st := nextState(t, conn, nil)
	if len(st.Projects) != 1 || !st.Projects[0].Active {
		t.Fatalf("expected one active project, got %+v", st.Projects)
	}
	first := st.Projects[0].Root

	second := t.TempDir()
	sendCmd(t, conn, command{Cmd: "openProject", Path: second})
	st = nextState(t, conn, func(s stateMsg) bool { return len(s.Projects) == 2 })

	// The newly opened project becomes active and shows only its own tabs.
	var active string
	for _, p := range st.Projects {
		if p.Active {
			active = p.Root
		}
	}
	if active == first {
		t.Error("opening a project should switch to it")
	}
	if len(st.Tabs) != 1 {
		t.Errorf("new project shows %d tabs, want 1", len(st.Tabs))
	}

	// Switching back shows the original project again, and the other project's
	// agents are still running.
	sendCmd(t, conn, command{Cmd: "selectProject", Root: first})
	nextState(t, conn, func(s stateMsg) bool {
		for _, p := range s.Projects {
			if p.Root == first && p.Active {
				return true
			}
		}
		return false
	})
	if len(ws.Tabs) != 2 {
		t.Errorf("expected both projects' tabs to survive, got %d", len(ws.Tabs))
	}

	// Closing a project removes it.
	sendCmd(t, conn, command{Cmd: "closeProject", Root: second})
	nextState(t, conn, func(s stateMsg) bool { return len(s.Projects) == 1 })
}

// TestRecentsAndBrowse covers the picker's two data sources.
func TestRecentsAndBrowse(t *testing.T) {
	srv, ws := newTestServer(t)
	conn := dialControl(t, srv)
	nextState(t, conn, nil)

	// The project opened at startup should be remembered.
	var rec recentsMsg
	sendCmd(t, conn, command{Cmd: "recents"})
	readUntil(t, conn, "recents", &rec)
	found := false
	for _, r := range rec.Items {
		if r.Root == ws.ActiveRoot() {
			found = true
			if !r.Open {
				t.Error("the active project should be marked as open")
			}
		}
	}
	if !found {
		t.Errorf("active project missing from recents: %+v", rec.Items)
	}

	// Browsing lists sub-directories and flags repositories.
	parent := t.TempDir()
	if err := os.MkdirAll(filepath.Join(parent, "plain-dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(parent, "repo-dir", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	var br browseMsg
	sendCmd(t, conn, command{Cmd: "browse", Path: parent})
	readUntil(t, conn, "browse", &br)
	if br.Error != "" {
		t.Fatalf("browse error: %s", br.Error)
	}
	if len(br.Entries) != 2 {
		t.Fatalf("listed %d directories, want 2: %+v", len(br.Entries), br.Entries)
	}
	// Repositories sort first, since they are what a project usually is.
	if br.Entries[0].Name != "repo-dir" || !br.Entries[0].IsRepo {
		t.Errorf("expected the repository first and flagged, got %+v", br.Entries[0])
	}
	if br.Entries[1].IsRepo {
		t.Error("a plain directory must not be flagged as a repository")
	}
	if br.Parent == "" {
		t.Error("browse should offer a parent to navigate up to")
	}
	if len(br.Places) == 0 {
		t.Error("browse should offer shortcut places")
	}
}

// TestBrowseMissingDirectoryReportsError covers a path that does not exist.
func TestBrowseMissingDirectoryReportsError(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	nextState(t, conn, nil)

	parent := t.TempDir()
	var br browseMsg
	sendCmd(t, conn, command{Cmd: "browse", Path: filepath.Join(parent, "nope")})
	readUntil(t, conn, "browse", &br)
	if br.Error == "" {
		t.Error("expected an error for a missing directory")
	}
	// The way back out has to come with the error: without a parent the
	// picker's up control is disabled and whoever walked in here is stuck.
	if br.Parent != parent {
		t.Errorf("parent = %q, want %q", br.Parent, parent)
	}
}

// TestOpenProjectRejectsFiles checks the failure is reported to the window
// rather than silently ignored.
func TestOpenProjectRejectsFiles(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	nextState(t, conn, nil)

	file := filepath.Join(t.TempDir(), "a-file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	sendCmd(t, conn, command{Cmd: "openProject", Path: file})

	var note noticeMsg
	readUntil(t, conn, "notice", &note)
	if !note.Error {
		t.Errorf("expected an error notice, got %+v", note)
	}
}

// TestPaneFromATwinProjectIsNamedApart covers a tab holding agents from two
// projects whose folders are called the same thing: two checkouts of one
// service, say. The header names a borrowed pane's project so the two can be
// told apart, and naming it by its folder called both of them "app".
func TestPaneFromATwinProjectIsNamedApart(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	nextState(t, conn, nil)

	base := t.TempDir()
	first, second := filepath.Join(base, "work", "app"), filepath.Join(base, "home", "app")
	for _, dir := range []string{first, second} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	sendCmd(t, conn, command{Cmd: "openProject", Path: first})
	sendCmd(t, conn, command{Cmd: "openProject", Path: second})
	nextState(t, conn, func(s stateMsg) bool { return len(s.Projects) == 3 })
	sendCmd(t, conn, command{Cmd: "selectProject", Root: first})
	nextState(t, conn, func(s stateMsg) bool { return s.Root == first })
	sendCmd(t, conn, command{Cmd: "splitPane", Kind: "shell", Root: second})

	st := nextState(t, conn, func(s stateMsg) bool {
		for _, p := range s.Panes {
			if p.Project != "" {
				return true
			}
		}
		return false
	})
	var want string
	for _, p := range st.Projects {
		if p.Root == second {
			want = p.Name
		}
	}
	for _, p := range st.Panes {
		if p.Project != "" && (p.Project != want || p.Project == "app") {
			t.Errorf("the borrowed pane is labelled %q; the switcher calls its project %q", p.Project, want)
		}
	}
}

// TestUnreadableRecentsAreReported covers a config directory that cannot be
// read: an empty picker looks exactly like never having opened a project
// before, so the failure has to reach the window.
func TestUnreadableRecentsAreReported(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	nextState(t, conn, nil)

	// Point the config location at a file, so the store cannot resolve its
	// directory any more.
	blocker := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("APPDATA", blocker)
	t.Setenv("XDG_CONFIG_HOME", blocker)
	t.Setenv("HOME", blocker)

	sendCmd(t, conn, command{Cmd: "recents"})
	var note noticeMsg
	readUntil(t, conn, "notice", &note)
	if !note.Error {
		t.Errorf("expected an error notice, got %+v", note)
	}
	if !strings.Contains(note.Text, "recent projects") {
		t.Errorf("notice = %q, want it to name the recent projects", note.Text)
	}
}

// linkDir makes name point at target, falling back to a directory junction
// where creating a symlink needs a privilege the test run does not have.
// Windows reports a junction as neither a file nor a directory, which is
// exactly the case a listing has to follow before it can judge.
func linkDir(t *testing.T, target, name string) {
	t.Helper()
	if err := os.Symlink(target, name); err == nil {
		return
	} else if runtime.GOOS != "windows" {
		t.Skipf("cannot create a symlink here: %v", err)
	}
	out, err := exec.Command("cmd", "/c", "mklink", "/J", name, target).CombinedOutput()
	if err != nil {
		t.Skipf("cannot link a directory here: %v: %s", err, out)
	}
}

// TestBrowseListsLinkedDirectories covers a project reached through a link,
// which is how a checkout kept on another volume usually appears. The listing
// describes the link itself rather than what it leads to, so following it is
// the only way to tell that it is somewhere to open — and a plain file still
// must not be offered as one.
func TestBrowseListsLinkedDirectories(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	nextState(t, conn, nil)

	target := t.TempDir()
	if err := os.MkdirAll(filepath.Join(target, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	parent := t.TempDir()
	if err := os.WriteFile(filepath.Join(parent, "notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	linkDir(t, target, filepath.Join(parent, "linked-repo"))

	var br browseMsg
	sendCmd(t, conn, command{Cmd: "browse", Path: parent})
	readUntil(t, conn, "browse", &br)
	if br.Error != "" {
		t.Fatalf("browse error: %s", br.Error)
	}
	if len(br.Entries) != 1 || br.Entries[0].Name != "linked-repo" {
		t.Fatalf("listed %+v, want only linked-repo", br.Entries)
	}
	if !br.Entries[0].IsRepo {
		t.Error("a repository reached through a link should still be flagged as one")
	}
}
