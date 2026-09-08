package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
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

// TestConcurrentSavesLeaveValidState checks a second writer can never rename a
// half-written file into place, which a shared "<name>.tmp" would allow, and
// that no temporary files are left lying in the state directory.
func TestConcurrentSavesLeaveValidState(t *testing.T) {
	isolateConfig(t)
	dir, err := Dir()
	if err != nil {
		t.Fatalf("dir: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			open := make([]string, 0, 60)
			for j := 0; j < 50+i; j++ {
				open = append(open, fmt.Sprintf("/repo/%d/%d", i, j))
			}
			if err := SaveSession(&Session{Open: open, Active: open[0]}); err != nil {
				t.Errorf("save session: %v", err)
			}
		}(i)
	}
	wg.Wait()

	got, err := LoadSession()
	if err != nil {
		t.Fatalf("load session: %v", err)
	}
	if got == nil {
		t.Fatal("concurrent writers left the session file unreadable")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Errorf("temporary file %s was left behind", e.Name())
		}
	}
}

// TestSaveLeavesNoTemporaryFiles checks the write-then-rename dance cleans up
// after itself, so the state directory does not silently fill with debris.
func TestSaveLeavesNoTemporaryFiles(t *testing.T) {
	isolateConfig(t)
	dir, err := Dir()
	if err != nil {
		t.Fatalf("dir: %v", err)
	}

	for i := 0; i < 3; i++ {
		if err := Save("/repo/a", &State{Tabs: []Tab{{Title: "alpha"}}}); err != nil {
			t.Fatalf("save layout: %v", err)
		}
		if err := SaveSession(&Session{Open: []string{"/repo/a"}, Active: "/repo/a"}); err != nil {
			t.Fatalf("save session: %v", err)
		}
		if err := TouchRecent("/repo/a"); err != nil {
			t.Fatalf("touch recent: %v", err)
		}
		if err := SaveInstance(&Instance{PID: 1, URL: "http://127.0.0.1:1/"}); err != nil {
			t.Fatalf("save instance: %v", err)
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Errorf("temporary file %s was left behind", e.Name())
		}
	}
}

// TestTouchRecentKeepsListWhenUnreadable checks an unreadable projects file
// stops the rewrite rather than replacing every remembered project with the
// one being opened.
func TestTouchRecentKeepsListWhenUnreadable(t *testing.T) {
	isolateConfig(t)
	dir, err := Dir()
	if err != nil {
		t.Fatalf("dir: %v", err)
	}

	// A directory where the file belongs is the portable way to make a read
	// fail with something other than "does not exist".
	if err := os.Mkdir(filepath.Join(dir, recentsFile), 0o755); err != nil {
		t.Fatalf("block projects file: %v", err)
	}

	if err := TouchRecent("/repo/a"); err == nil {
		t.Error("TouchRecent reported success while it could not read the existing list")
	}
	if err := ForgetRecent("/repo/a"); err == nil {
		t.Error("ForgetRecent reported success while it could not read the existing list")
	}
}

// TestRecentsRoundTrip checks the remembered list is ordered most recent first
// and that reopening a project moves it to the front rather than duplicating
// it, however its path is spelled.
func TestRecentsRoundTrip(t *testing.T) {
	isolateConfig(t)

	for _, root := range []string{"/repo/a", "/repo/b", "/repo/c"} {
		if err := TouchRecent(root); err != nil {
			t.Fatalf("touch %s: %v", root, err)
		}
	}
	if err := TouchRecent(filepath.Clean("/repo/a") + string(filepath.Separator)); err != nil {
		t.Fatalf("re-touch a: %v", err)
	}

	list, err := Recents()
	if err != nil {
		t.Fatalf("recents: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("remembered %d projects, want 3: %v", len(list), list)
	}
	if !sameRoot(list[0].Root, "/repo/a") {
		t.Errorf("most recent is %q, want /repo/a", list[0].Root)
	}

	if err := ForgetRecent("/repo/b"); err != nil {
		t.Fatalf("forget: %v", err)
	}
	list, err = Recents()
	if err != nil {
		t.Fatalf("recents after forget: %v", err)
	}
	for _, p := range list {
		if sameRoot(p.Root, "/repo/b") {
			t.Error("a forgotten project came back")
		}
	}
}

// TestTouchRecentStoresTidyPaths checks the picker is offered a clean path
// rather than whichever spelling the caller happened to have, and that an
// empty path is refused instead of being remembered as ".".
func TestTouchRecentStoresTidyPaths(t *testing.T) {
	isolateConfig(t)

	messy := filepath.Join("/repo", "a", "b", "..") + string(filepath.Separator)
	if err := TouchRecent(messy); err != nil {
		t.Fatalf("touch: %v", err)
	}
	list, err := Recents()
	if err != nil {
		t.Fatalf("recents: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("remembered %d projects, want 1", len(list))
	}
	if want := filepath.Clean("/repo/a"); list[0].Root != want {
		t.Errorf("remembered %q, want %q", list[0].Root, want)
	}

	if err := TouchRecent("   "); err == nil {
		t.Error("an empty path should not be remembered as a project")
	}
	if list, _ := Recents(); len(list) != 1 {
		t.Errorf("the empty path changed the list: %v", list)
	}
}

// TestRecentsCollapsesStaleDuplicates checks a projects file written before
// two spellings of a path counted as one still lists each project once.
func TestRecentsCollapsesStaleDuplicates(t *testing.T) {
	isolateConfig(t)
	dir, err := Dir()
	if err != nil {
		t.Fatalf("dir: %v", err)
	}

	now := time.Now()
	raw := []Project{
		{Root: filepath.Clean("/repo/a"), LastUsed: now.Add(-time.Hour)},
		{Root: filepath.Clean("/repo/a") + string(filepath.Separator), LastUsed: now},
		{Root: "", LastUsed: now.Add(-2 * time.Hour)},
		{Root: filepath.Clean("/repo/b"), LastUsed: now.Add(-3 * time.Hour)},
	}
	data, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, recentsFile), data, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	list, err := Recents()
	if err != nil {
		t.Fatalf("recents: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("listed %d projects, want 2: %v", len(list), list)
	}
	// The newest spelling wins, so the entry keeps its real last-used time.
	if !list[0].LastUsed.Equal(now) {
		t.Errorf("kept the older duplicate: %v", list[0])
	}
	if !sameRoot(list[1].Root, "/repo/b") {
		t.Errorf("second entry is %q, want /repo/b", list[1].Root)
	}
}

// TestClearInstanceKeepsALiveRivalsRecord checks a wrapper shutting down
// cannot delete the record of another one that is still running, which would
// leave a live instance no later launch could attach to.
func TestClearInstanceKeepsALiveRivalsRecord(t *testing.T) {
	isolateConfig(t)

	alive := map[int]bool{}
	restore := processAlive
	processAlive = func(pid int) bool { return alive[pid] }
	t.Cleanup(func() { processAlive = restore })

	save := func(pid int) {
		t.Helper()
		if err := SaveInstance(&Instance{PID: pid, URL: "http://127.0.0.1:1/", Token: "t"}); err != nil {
			t.Fatalf("save instance: %v", err)
		}
	}

	// Another wrapper, still running: its record must survive.
	const rival = 4242
	alive[rival] = true
	save(rival)
	if err := ClearInstance(); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if inst, _ := LoadInstance(); inst == nil || inst.PID != rival {
		t.Error("a live instance's record was deleted by another process shutting down")
	}

	// The same record once that process is gone: now it is ours to remove.
	alive[rival] = false
	if err := ClearInstance(); err != nil {
		t.Fatalf("clear stale: %v", err)
	}
	if inst, _ := LoadInstance(); inst != nil {
		t.Error("a record left by a dead process should be cleared")
	}

	// Our own record always goes, alive or not.
	save(os.Getpid())
	alive[os.Getpid()] = true
	if err := ClearInstance(); err != nil {
		t.Fatalf("clear own: %v", err)
	}
	if inst, _ := LoadInstance(); inst != nil {
		t.Error("a wrapper shutting down must clear its own record")
	}

	// Nothing recorded at all is not an error.
	if err := ClearInstance(); err != nil {
		t.Errorf("clearing an absent record: %v", err)
	}
}
