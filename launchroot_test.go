package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"

	"github.com/jmwri/flockdeck/internal/store"
)

// A launch that names no project from somewhere nobody works — the home
// directory the Start menu shortcut starts in, or System32 — goes back to the
// projects that were open last time, and opens the home directory only when
// there were none. -C, and a start from inside a project, open what they say
// without looking at what was open before.
func TestLaunchRoot(t *testing.T) {
	home, a, b, project, install := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	sys := "/"
	if runtime.GOOS == "windows" {
		root := t.TempDir()
		t.Setenv("SystemRoot", root)
		sys = filepath.Join(root, "System32")
	}
	exe := filepath.Join(install, "flockdeck.exe")
	deleted := filepath.Join(a, "deleted")
	last := &store.Session{Open: []string{a, b}, Active: b}
	gone := &store.Session{Open: []string{deleted, a}, Active: deleted}
	unread := errors.New("session.json could not be read")

	defer func(wd func() (string, error), ls func() (*store.Session, error)) {
		workingDir, loadSession = wd, ls
	}(workingDir, loadSession)

	cases := []struct {
		name    string
		opts    options
		cwd     string
		saved   *store.Session
		loadErr error
		want    string
		chosen  bool
		asked   bool // whether the saved session should have been read
	}{
		{"Start menu, with projects open last time", options{}, home, last, nil, b, false, true},
		{"Start menu, nothing saved", options{}, home, nil, nil, home, false, true},
		{"Start menu, the session cannot be read", options{}, home, nil, unread, home, false, true},
		{"System32, with projects open last time", options{}, sys, last, nil, b, false, true},
		{"System32, the last project has gone", options{}, sys, gone, nil, a, false, true},
		{"System32, nothing saved", options{}, sys, nil, nil, home, false, true},
		{"its own folder, nothing saved", options{}, install, nil, nil, install, false, true},
		{"inside a project", options{}, project, last, nil, project, true, false},
		{"-C, from the home directory", options{dir: project, dirGiven: true}, home, last, nil, project, true, false},
		{"-C naming the home directory", options{dir: home, dirGiven: true}, sys, last, nil, home, true, false},
	}
	for _, c := range cases {
		asked := false
		workingDir = func() (string, error) { return c.cwd, nil }
		loadSession = func() (*store.Session, error) {
			asked = true
			return c.saved, c.loadErr
		}
		got, chosen, err := launchRoot(c.opts, exe)
		if err != nil {
			t.Errorf("%s: launchRoot failed: %v", c.name, err)
			continue
		}
		if got != c.want || chosen != c.chosen {
			t.Errorf("%s: launchRoot = %s, chosen %v; want %s, chosen %v", c.name, got, chosen, c.want, c.chosen)
		}
		if asked != c.asked {
			t.Errorf("%s: read the saved session %v, want %v", c.name, asked, c.asked)
		}
	}
}

// The home directory is recognised however it is spelled, since the Start
// menu's working directory and the one the environment names need not match
// letter for letter.
func TestHomeDirIsAskedOfTheFileSystem(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "home")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("cannot make a symlink here: %v", err)
	}
	t.Setenv("HOME", link)
	t.Setenv("USERPROFILE", link)
	if !homeDir(real) {
		t.Errorf("%s is the home directory %s reached through a link, but was not recognised", real, link)
	}
	if homeDir(t.TempDir()) {
		t.Error("a directory that is not the home directory was taken for it")
	}
}

// Joining a running instance with no project named shows the window onto the
// projects it already has, rather than asking it to open the one the last
// saved session was left on; a project that was named still goes to it.
func TestAttachWithNoRootOpensNothing(t *testing.T) {
	var opens atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/open" {
			opens.Add(1)
		}
	}))
	defer srv.Close()
	inst := &store.Instance{URL: srv.URL, Token: "token"}

	if err := attach(inst, srv.URL, "", true); err != nil {
		t.Fatalf("attach with no project: %v", err)
	}
	if n := opens.Load(); n != 0 {
		t.Errorf("attach with no project asked the instance to open one %d times", n)
	}
	if err := attach(inst, srv.URL, t.TempDir(), true); err != nil {
		t.Fatalf("attach with a project: %v", err)
	}
	if n := opens.Load(); n != 1 {
		t.Errorf("attach with a project asked the instance to open it %d times, want once", n)
	}
}
