package store

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// isolateConfig points the state directory at a temporary location.
func isolateConfig(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)         // Windows
	t.Setenv("XDG_CONFIG_HOME", dir) // Linux
	t.Setenv("HOME", dir)            // macOS and fallback
}

// TestSweepSessionsOnlyRemovesOldOrphans checks the sweep cannot pull settings
// out from under a wrapper that is still running.
func TestSweepSessionsOnlyRemovesOldOrphans(t *testing.T) {
	isolateConfig(t)
	dir, err := SessionsDir()
	if err != nil {
		t.Fatalf("sessions dir: %v", err)
	}

	write := func(name string, age time.Duration) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("{}"), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		when := time.Now().Add(-age)
		if err := os.Chtimes(p, when, when); err != nil {
			t.Fatalf("chtimes %s: %v", name, err)
		}
		return p
	}

	old := write("aaaa.settings.json", 48*time.Hour)
	recent := write("bbbb.settings.json", 5*time.Minute)
	unrelated := write("notes.txt", 48*time.Hour)

	n, err := SweepSessions(24 * time.Hour)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if n != 1 {
		t.Errorf("removed %d files, want 1", n)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Error("an orphaned settings file should have been removed")
	}
	if _, err := os.Stat(recent); err != nil {
		t.Error("a recent settings file may belong to a running wrapper and must be kept")
	}
	if _, err := os.Stat(unrelated); err != nil {
		t.Error("unrelated files must not be touched")
	}
}

// TestStatePerRootIsIsolated checks two workspaces do not clobber each other's
// saved layout.
func TestStatePerRootIsIsolated(t *testing.T) {
	isolateConfig(t)

	a := &State{Tabs: []Tab{{Title: "alpha", Root: &Node{Pane: &Pane{ID: "1", Kind: "claude", Cwd: "/a"}}}}}
	b := &State{Tabs: []Tab{{Title: "beta", Root: &Node{Pane: &Pane{ID: "2", Kind: "shell", Cwd: "/b"}}}}}

	if err := Save("/repo/a", a); err != nil {
		t.Fatalf("save a: %v", err)
	}
	if err := Save("/repo/b", b); err != nil {
		t.Fatalf("save b: %v", err)
	}

	gotA, err := Load("/repo/a")
	if err != nil || gotA == nil {
		t.Fatalf("load a: %v", err)
	}
	if gotA.Tabs[0].Title != "alpha" {
		t.Errorf("workspace a restored %q", gotA.Tabs[0].Title)
	}
	gotB, err := Load("/repo/b")
	if err != nil || gotB == nil {
		t.Fatalf("load b: %v", err)
	}
	if gotB.Tabs[0].Title != "beta" {
		t.Errorf("workspace b restored %q", gotB.Tabs[0].Title)
	}
}

// TestLoadIgnoresCorruptState checks a damaged layout cannot stop the app from
// starting.
func TestLoadIgnoresCorruptState(t *testing.T) {
	isolateConfig(t)

	if err := Save("/repo/c", &State{Tabs: []Tab{{Title: "x"}}}); err != nil {
		t.Fatalf("save: %v", err)
	}
	p, err := path("/repo/c")
	if err != nil {
		t.Fatalf("path: %v", err)
	}
	if err := os.WriteFile(p, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("corrupt: %v", err)
	}

	got, err := Load("/repo/c")
	if err != nil {
		t.Errorf("a corrupt layout should be ignored, not returned as an error: %v", err)
	}
	if got != nil {
		t.Error("a corrupt layout should restore nothing")
	}
}

// TestLayoutFollowsRootSpelling checks the same directory typed differently
// still finds its own layout. Roots arrive from a picker, the command line and
// saved session state, so trailing separators and casing vary between runs.
func TestLayoutFollowsRootSpelling(t *testing.T) {
	isolateConfig(t)

	want := &State{Tabs: []Tab{{Title: "alpha", Root: &Node{Pane: &Pane{ID: "1", Kind: "claude", Cwd: "/repo/a"}}}}}
	if err := Save(filepath.Clean("/repo/a"), want); err != nil {
		t.Fatalf("save: %v", err)
	}

	spellings := []string{"/repo/a/", "/repo/./a"}
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		spellings = append(spellings, "/Repo/A")
	}
	for _, root := range spellings {
		got, err := Load(root)
		if err != nil {
			t.Fatalf("load %q: %v", root, err)
		}
		if got == nil {
			t.Errorf("load %q restored nothing; it names the same directory as the saved root", root)
			continue
		}
		if got.Tabs[0].Title != "alpha" {
			t.Errorf("load %q restored %q, want alpha", root, got.Tabs[0].Title)
		}
	}
}
