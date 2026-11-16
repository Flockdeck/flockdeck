package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/coder/websocket"
)

// What a window reached through the relay is shown when it browses: Places,
// the parent, a link out of a project, and Windows' case folding.

func dialPhone(t *testing.T, srv *Server) *websocket.Conn {
	t.Helper()
	ts := remoteServer(t, srv)
	phone, err := dialRemoteControl(ts, ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { phone.CloseNow() })
	return phone
}

func projectRoots(srv *Server) []string {
	roots, _ := ask(srv, func() []string {
		var out []string
		for _, p := range srv.ws.Projects() {
			out = append(out, p.Root)
		}
		return out
	})
	return roots
}

func tabCount(srv *Server) int {
	n, _ := ask(srv, func() int {
		total := 0
		for _, p := range srv.ws.Projects() {
			total += p.Tabs
		}
		return total
	})
	return n
}

func browseThrough(t *testing.T, phone *websocket.Conn, path string) browseMsg {
	t.Helper()
	sendCmd(t, phone, command{Cmd: "browse", Path: path, Req: "b"})
	var m browseMsg
	readUntil(t, phone, "browse", &m)
	return m
}

func wantRefusal(t *testing.T, phone *websocket.Conn, what string) {
	t.Helper()
	var note noticeMsg
	readQuick(t, phone, "notice", &note)
	if !note.Error || !strings.Contains(note.Text, "machine") {
		t.Errorf("%s through the relay was answered %+v, want it refused", what, note)
	}
}

// Places are the machine's home, Documents, drive letters and root: nothing a
// window that may see only known projects needs.
func TestARelayWindowBrowseCarriesNoPlaces(t *testing.T) {
	srv, ws := newTestServer(t)
	known := ws.ActiveRoot()
	phone := dialPhone(t, srv)
	m := browseThrough(t, phone, known)
	if m.Error != "" {
		t.Fatalf("browse of a known project: %+v", m)
	}
	if len(m.Places) != 0 {
		t.Errorf("a relay window was sent %d places: %+v", len(m.Places), m.Places)
	}
	desk := dialControl(t, srv)
	sendCmd(t, desk, command{Cmd: "browse", Path: known, Req: "d"})
	var dm browseMsg
	readUntil(t, desk, "browse", &dm)
	if len(dm.Places) == 0 {
		t.Error("the desk's own browse lost its places")
	}
}

// The way up from a project's folder is withheld once it leaves the known
// projects, and offered inside them.
func TestARelayWindowBrowseWithholdsAnUnknownParent(t *testing.T) {
	srv, ws := newTestServer(t)
	known := ws.ActiveRoot()
	sub := filepath.Join(known, "inside-folder")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	phone := dialPhone(t, srv)
	if m := browseThrough(t, phone, known); m.Parent != "" {
		t.Errorf("the parent of a project root was offered to a relay window: %q", m.Parent)
	}
	if m := browseThrough(t, phone, sub); m.Parent != known {
		t.Errorf("the parent inside a project was %q, want %q", m.Parent, known)
	}
}

// A link inside a project that leads out of it is not a way out. On Windows
// the link is a directory junction, which needs no privilege.
func TestARelayWindowBrowseDoesNotFollowALinkOutOfAProject(t *testing.T) {
	srv, ws := newTestServer(t)
	known := ws.ActiveRoot()
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(outside, "secret-folder"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(known, "way-out")
	if runtime.GOOS == "windows" {
		if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, outside).CombinedOutput(); err != nil {
			t.Skipf("mklink /J: %v: %s", err, out)
		}
	} else if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink: %v", err)
	}
	phone := dialPhone(t, srv)
	for _, path := range []string{link, filepath.Join(link, "secret-folder")} {
		if m := browseThrough(t, phone, path); m.Error == "" || len(m.Entries) != 0 {
			t.Errorf("browse of %s listed %d folders (error %q), want a refusal", path, len(m.Entries), m.Error)
		}
	}
}

// Windows paths are not case sensitive: a known project spelt in other case
// is the same folder, and an unknown one stays unknown.
func TestARelayWindowBrowseFoldsCaseOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("case folding is Windows' rule")
	}
	srv, ws := newTestServer(t)
	known := ws.ActiveRoot()
	if err := os.MkdirAll(filepath.Join(known, "inside-folder"), 0o755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	phone := dialPhone(t, srv)
	if m := browseThrough(t, phone, strings.ToUpper(known)); m.Error != "" {
		t.Errorf("browse of the known project in upper case was refused: %q", m.Error)
	}
	if m := browseThrough(t, phone, strings.ToUpper(outside)); m.Error == "" || len(m.Entries) != 0 {
		t.Errorf("browse of an unknown folder in upper case was answered %+v", m)
	}
}
