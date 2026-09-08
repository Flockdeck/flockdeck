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

// TestSweepRemovesAbandonedTemporaries checks the temporary of a write that
// was killed before its rename does not sit in the state directory forever,
// while one young enough to belong to a write still in flight is left alone.
func TestSweepRemovesAbandonedTemporaries(t *testing.T) {
	isolateConfig(t)
	state, err := Dir()
	if err != nil {
		t.Fatalf("dir: %v", err)
	}
	sessions, err := SessionsDir()
	if err != nil {
		t.Fatalf("sessions dir: %v", err)
	}

	write := func(dir, name string, age time.Duration) string {
		t.Helper()
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

	abandoned := write(state, "session.json.tmp1234", 48*time.Hour)
	inFlight := write(state, "session.json.tmp5678", time.Minute)
	kept := write(state, "session.json", 48*time.Hour)
	sessionTemp := write(sessions, "abcd.settings.json.tmp99", 48*time.Hour)

	if _, err := SweepSessions(24 * time.Hour); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if _, err := os.Stat(abandoned); !os.IsNotExist(err) {
		t.Error("an abandoned temporary should have been removed")
	}
	if _, err := os.Stat(sessionTemp); !os.IsNotExist(err) {
		t.Error("an abandoned temporary in the sessions directory should have been removed")
	}
	if _, err := os.Stat(inFlight); err != nil {
		t.Error("a fresh temporary may belong to a write in flight and must be kept")
	}
	if _, err := os.Stat(kept); err != nil {
		t.Error("the sweep must not touch the state files themselves")
	}
}

// TestSweepRefusesZeroAge checks the sweep will not treat every file as an
// orphan, which would delete the settings of whichever wrapper is running.
func TestSweepRefusesZeroAge(t *testing.T) {
	isolateConfig(t)
	dir, err := SessionsDir()
	if err != nil {
		t.Fatalf("sessions dir: %v", err)
	}
	live := filepath.Join(dir, "live.settings.json")
	if err := os.WriteFile(live, []byte("{}"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	for _, age := range []time.Duration{0, -time.Hour} {
		n, err := SweepSessions(age)
		if err == nil {
			t.Errorf("SweepSessions(%s) should have refused", age)
		}
		if n != 0 {
			t.Errorf("SweepSessions(%s) removed %d files", age, n)
		}
	}
	if _, err := os.Stat(live); err != nil {
		t.Error("a live settings file was swept away")
	}
}

// TestSessionNamesEachProjectOnce checks the restorer is handed one entry per
// directory. It reopens everything in Open and only skips what it already has,
// so a second spelling of the same root would restore its layout twice.
func TestSessionNamesEachProjectOnce(t *testing.T) {
	isolateConfig(t)

	a := filepath.Clean("/repo/a")
	if err := SaveSession(&Session{
		Open:   []string{a, a + string(filepath.Separator), "", filepath.Clean("/repo/b")},
		Active: a + string(filepath.Separator),
	}); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := LoadSession()
	if err != nil || got == nil {
		t.Fatalf("load: %v", err)
	}
	if len(got.Open) != 2 {
		t.Fatalf("restored %d projects, want 2: %v", len(got.Open), got.Open)
	}
	if got.Open[0] != a || got.Open[1] != filepath.Clean("/repo/b") {
		t.Errorf("open projects are %v, want tidy paths in their original order", got.Open)
	}
	if got.Active != a {
		t.Errorf("active project is %q, want %q", got.Active, a)
	}
}

// TestSessionDropsAnActiveProjectThatIsNotOpen checks the active project is
// only ever one of the open ones.
func TestSessionDropsAnActiveProjectThatIsNotOpen(t *testing.T) {
	isolateConfig(t)

	if err := SaveSession(&Session{Open: []string{"/repo/a"}, Active: "/repo/gone"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := LoadSession()
	if err != nil || got == nil {
		t.Fatalf("load: %v", err)
	}
	if got.Active != "" {
		t.Errorf("active project is %q, but it is not among the open ones", got.Active)
	}
}

// TestLoadRejectsAnotherProjectsLayout checks a layout file that turns out to
// describe a different directory restores nothing, rather than opening one
// project's tabs inside another.
func TestLoadRejectsAnotherProjectsLayout(t *testing.T) {
	isolateConfig(t)

	if err := Save("/repo/a", &State{Tabs: []Tab{{Title: "alpha"}}}); err != nil {
		t.Fatalf("save: %v", err)
	}
	p, err := path("/repo/a")
	if err != nil {
		t.Fatalf("path: %v", err)
	}
	data, err := json.MarshalIndent(&State{
		Version: Version,
		Root:    filepath.Clean("/repo/somewhere-else"),
		Tabs:    []Tab{{Title: "beta"}},
	}, "", "  ")
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := Load("/repo/a")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got != nil {
		t.Errorf("restored %q, which belongs to another project", got.Tabs[0].Title)
	}
}

// TestStateDirectoriesArePrivate checks no other account on the machine can
// list the wrapper's state, including a directory left wide open by an earlier
// version.
func TestStateDirectoriesArePrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not express directory permissions this way")
	}
	isolateConfig(t)

	dir, err := Dir()
	if err != nil {
		t.Fatalf("dir: %v", err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatalf("widen: %v", err)
	}

	for _, tc := range []struct {
		name string
		get  func() (string, error)
	}{
		{"state", Dir},
		{"sessions", SessionsDir},
		{"window profile", BrowserProfileDir},
	} {
		got, err := tc.get()
		if err != nil {
			t.Fatalf("%s dir: %v", tc.name, err)
		}
		fi, err := os.Stat(got)
		if err != nil {
			t.Fatalf("stat %s dir: %v", tc.name, err)
		}
		if perm := fi.Mode().Perm(); perm&0o077 != 0 {
			t.Errorf("%s directory is %v; group and other must not reach it", tc.name, perm)
		}
	}
}

// TestForgetUnknownProjectLeavesTheFileAlone checks forgetting something that
// was never remembered does not rewrite the list, which would give a wrapper
// saving at the same moment a needless chance to lose its own write.
func TestForgetUnknownProjectLeavesTheFileAlone(t *testing.T) {
	isolateConfig(t)
	dir, err := Dir()
	if err != nil {
		t.Fatalf("dir: %v", err)
	}
	if err := TouchRecent("/repo/a"); err != nil {
		t.Fatalf("touch: %v", err)
	}

	p := filepath.Join(dir, recentsFile)
	before, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	when := before.ModTime().Add(-time.Hour)
	if err := os.Chtimes(p, when, when); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	if err := ForgetRecent("/repo/never-opened"); err != nil {
		t.Fatalf("forget: %v", err)
	}
	after, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat after: %v", err)
	}
	if !after.ModTime().Equal(when) {
		t.Error("the projects file was rewritten although nothing was forgotten")
	}
	if list, _ := Recents(); len(list) != 1 {
		t.Errorf("remembered projects changed: %v", list)
	}
}

// TestStateSurvivesTheRoundTrip pins the shape a restored workspace arrives
// in. Everything here is something the restorer reads: a pane whose id or
// working directory is lost cannot be resumed, and a weight that does not come
// back moves every split the user had arranged.
func TestStateSurvivesTheRoundTrip(t *testing.T) {
	isolateConfig(t)

	want := &State{
		Active: 1,
		Tabs: []Tab{
			{
				Title: "api",
				Focus: "pane-1",
				Root: &Node{
					Dir:    "h",
					Weight: 0.5,
					Children: []*Node{
						{Pane: &Pane{ID: "pane-1", Kind: "claude", Cwd: "/repo/a", Name: "worker", Task: "fix the parser"}, Weight: 0.7},
						{
							Dir: "v",
							Children: []*Node{
								{Pane: &Pane{ID: "pane-2", Kind: "shell", Cwd: "/repo/a/sub"}},
							},
						},
					},
				},
			},
			{Title: "docs", Root: &Node{Pane: &Pane{ID: "pane-3", Kind: "claude", Cwd: "/repo/a"}}},
		},
	}

	if err := Save("/repo/a", want); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := Load("/repo/a")
	if err != nil || got == nil {
		t.Fatalf("load: %v", err)
	}

	if got.Version != Version {
		t.Errorf("version %d, want %d", got.Version, Version)
	}
	if !sameRoot(got.Root, "/repo/a") {
		t.Errorf("root %q, want /repo/a", got.Root)
	}
	if got.Active != 1 {
		t.Errorf("active tab %d, want 1", got.Active)
	}
	if len(got.Tabs) != 2 {
		t.Fatalf("restored %d tabs, want 2", len(got.Tabs))
	}
	if got.Tabs[0].Focus != "pane-1" {
		t.Errorf("focus %q, want pane-1", got.Tabs[0].Focus)
	}

	tree := got.Tabs[0].Root
	if tree.Dir != "h" || tree.Weight != 0.5 || len(tree.Children) != 2 {
		t.Fatalf("outer split came back as %+v", tree)
	}
	left := tree.Children[0]
	if left.Pane == nil {
		t.Fatal("the left leaf lost its pane")
	}
	if *left.Pane != (Pane{ID: "pane-1", Kind: "claude", Cwd: "/repo/a", Name: "worker", Task: "fix the parser"}) {
		t.Errorf("pane came back as %+v", *left.Pane)
	}
	if left.Weight != 0.7 {
		t.Errorf("pane weight %v, want 0.7", left.Weight)
	}
	nested := tree.Children[1]
	if nested.Dir != "v" || len(nested.Children) != 1 || nested.Children[0].Pane == nil {
		t.Fatalf("nested split came back as %+v", nested)
	}
	if nested.Children[0].Pane.Kind != "shell" {
		t.Errorf("nested pane kind %q, want shell", nested.Children[0].Pane.Kind)
	}
}

// TestLoadIgnoresAnotherSchemaVersion checks a layout written by a different
// version of the wrapper is passed over quietly. The fields it holds may mean
// something else entirely, and a start-up that fails is worse than one that
// opens a fresh tab.
func TestLoadIgnoresAnotherSchemaVersion(t *testing.T) {
	isolateConfig(t)

	p, err := path("/repo/a")
	if err != nil {
		t.Fatalf("path: %v", err)
	}
	for _, version := range []int{0, Version - 1, Version + 1} {
		data, err := json.MarshalIndent(&State{
			Version: version,
			Root:    filepath.Clean("/repo/a"),
			Tabs:    []Tab{{Title: "alpha", Root: &Node{Pane: &Pane{ID: "1", Kind: "claude", Cwd: "/repo/a"}}}},
		}, "", "  ")
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}

		got, err := Load("/repo/a")
		if err != nil {
			t.Errorf("version %d should be ignored, not reported: %v", version, err)
		}
		if got != nil {
			t.Errorf("version %d restored %d tabs, want none", version, len(got.Tabs))
		}
	}

	// The version this build writes is of course still read back.
	if err := Save("/repo/a", &State{Tabs: []Tab{{Title: "alpha"}}}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if got, err := Load("/repo/a"); err != nil || got == nil {
		t.Fatalf("the current version did not survive a round trip: %v", err)
	}
}

// TestInstanceRecordRoundTrip pins what a second launch reads before deciding
// whether to attach to a wrapper already running or start one of its own.
func TestInstanceRecordRoundTrip(t *testing.T) {
	isolateConfig(t)

	if inst, err := LoadInstance(); err != nil || inst != nil {
		t.Fatalf("with nothing recorded, got %v, %v; want nil, nil", inst, err)
	}

	started := time.Now().Round(time.Second)
	want := &Instance{PID: 4321, URL: "http://127.0.0.1:53124/", Token: "s3cret", Started: started}
	if err := SaveInstance(want); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := LoadInstance()
	if err != nil || got == nil {
		t.Fatalf("load: %v", err)
	}
	if got.PID != want.PID || got.URL != want.URL || got.Token != want.Token {
		t.Errorf("recorded %+v, want %+v", *got, *want)
	}
	if !got.Started.Equal(started) {
		t.Errorf("start time %v, want %v", got.Started, started)
	}

	p, err := instancePath()
	if err != nil {
		t.Fatalf("instance path: %v", err)
	}
	// The token in here is what authorises commands to the running wrapper.
	if fi, err := os.Stat(p); err != nil {
		t.Fatalf("stat: %v", err)
	} else if runtime.GOOS != "windows" && fi.Mode().Perm()&0o077 != 0 {
		t.Errorf("instance file is %v; the token it holds must not be readable by others", fi.Mode().Perm())
	}

	// A record that says nothing about where to connect is no record at all:
	// treating it as one would send the launch off to probe an empty address.
	for _, body := range []string{"{not json", "{}", `{"pid":1,"url":""}`} {
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		got, err := LoadInstance()
		if err != nil {
			t.Errorf("%s should be ignored, not reported: %v", body, err)
		}
		if got != nil {
			t.Errorf("%s was taken as a running instance: %+v", body, *got)
		}
	}
}

// TestRecentsAreCapped checks the remembered list stops growing, and that it
// is the projects the user has not touched in longest that fall off the end.
func TestRecentsAreCapped(t *testing.T) {
	isolateConfig(t)

	for i := 0; i < maxRecents+5; i++ {
		if err := TouchRecent(fmt.Sprintf("/repo/p%02d", i)); err != nil {
			t.Fatalf("touch %d: %v", i, err)
		}
	}

	list, err := Recents()
	if err != nil {
		t.Fatalf("recents: %v", err)
	}
	if len(list) != maxRecents {
		t.Fatalf("remembered %d projects, want %d", len(list), maxRecents)
	}
	if !sameRoot(list[0].Root, "/repo/p44") {
		t.Errorf("most recent is %q, want the last project opened", list[0].Root)
	}
	for _, p := range list {
		for i := 0; i < 5; i++ {
			if sameRoot(p.Root, fmt.Sprintf("/repo/p%02d", i)) {
				t.Errorf("%q should have fallen off the end", p.Root)
			}
		}
	}
}

// TestWriteAtomicReplacesRatherThanOverwrites checks a shorter write leaves
// none of the longer one it replaced, and that a write that cannot be done
// reports it and leaves no debris. Writing over a file in place would pass the
// first of those only by accident.
func TestWriteAtomicReplacesRatherThanOverwrites(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "state.json")

	long := []byte(`{"open":["/repo/a","/repo/b","/repo/c"]}`)
	short := []byte(`{"open":[]}`)
	if err := writeAtomic(p, long); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := writeAtomic(p, short); err != nil {
		t.Fatalf("second write: %v", err)
	}
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != string(short) {
		t.Errorf("file holds %q, want exactly %q", got, short)
	}

	if err := writeAtomic(filepath.Join(dir, "no-such-dir", "state.json"), short); err == nil {
		t.Error("writing into a directory that does not exist should have failed")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "state.json" {
		t.Errorf("directory holds %v, want only the state file", entries)
	}
}

// TestSaveFailureKeepsWhatWasThere checks a save that cannot complete leaves
// the workspace that was last saved intact, rather than destroying it on the
// way to failing.
func TestSaveFailureKeepsWhatWasThere(t *testing.T) {
	isolateConfig(t)

	if err := Save("/repo/a", &State{Tabs: []Tab{{Title: "alpha"}}}); err != nil {
		t.Fatalf("save: %v", err)
	}
	p, err := path("/repo/a")
	if err != nil {
		t.Fatalf("path: %v", err)
	}
	// A directory standing where the file goes is the portable way to make the
	// rename fail with the file itself still readable underneath.
	blocked := filepath.Join(filepath.Dir(p), "blocked.json")
	if err := os.Rename(p, blocked); err != nil {
		t.Fatalf("move aside: %v", err)
	}
	if err := os.Mkdir(p, 0o700); err != nil {
		t.Fatalf("block: %v", err)
	}

	if err := Save("/repo/a", &State{Tabs: []Tab{{Title: "beta"}}}); err == nil {
		t.Error("saving over a blocked path should have failed")
	}
	if err := os.Remove(p); err != nil {
		t.Fatalf("unblock: %v", err)
	}
	if err := os.Rename(blocked, p); err != nil {
		t.Fatalf("restore: %v", err)
	}

	got, err := Load("/repo/a")
	if err != nil || got == nil {
		t.Fatalf("load: %v", err)
	}
	if got.Tabs[0].Title != "alpha" {
		t.Errorf("restored %q, want the workspace that was last saved", got.Tabs[0].Title)
	}
}
