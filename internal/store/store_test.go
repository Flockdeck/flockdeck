package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
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
// out from under an instance that is still running.
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
		t.Error("a recent settings file may belong to a running instance and must be kept")
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

// TestClearInstanceKeepsALiveRivalsRecord checks an instance shutting down
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

	// Another instance, still running: its record must survive.
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
		t.Error("an instance shutting down must clear its own record")
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
// orphan, which would delete the settings of whichever instance is running.
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
// list Perch's state, including a directory left wide open by an earlier
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
// was never remembered does not rewrite the list, which would give an instance
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
// version of Perch is passed over quietly. The fields it holds may mean
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
// whether to attach to an instance already running or start one of its own.
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
	// The token in here is what authorises commands to the running instance.
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

// TestSweepContinuesPastAnUnreadableDirectory checks one directory it cannot
// read does not cost the other its clean-up. The two fill up independently, so
// giving up on both would leave the state directory's orphans there for good.
func TestSweepContinuesPastAnUnreadableDirectory(t *testing.T) {
	isolateConfig(t)
	state, err := Dir()
	if err != nil {
		t.Fatalf("dir: %v", err)
	}
	sessions, err := SessionsDir()
	if err != nil {
		t.Fatalf("sessions dir: %v", err)
	}

	orphan := filepath.Join(state, "session.json.tmp1234")
	if err := os.WriteFile(orphan, []byte("{}"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	when := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(orphan, when, when); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	// A file standing where the sessions directory belongs makes reading it
	// fail on every platform.
	if err := os.RemoveAll(sessions); err != nil {
		t.Fatalf("remove sessions dir: %v", err)
	}
	if err := os.WriteFile(sessions, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("block sessions dir: %v", err)
	}

	n, err := SweepSessions(24 * time.Hour)
	if err == nil {
		t.Error("the unreadable directory should have been reported")
	}
	if n != 1 {
		t.Errorf("removed %d files, want the one orphan in the state directory", n)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Error("the state directory was not swept")
	}
}

// TestSameRootDecidesWhatCountsAsOneProject pins the comparison the rest of
// the store is built on: it decides which layout file a workspace gets, which
// entries in the project list and the saved session are duplicates, and which
// project a recorded layout is allowed to be restored into.
func TestSameRootDecidesWhatCountsAsOneProject(t *testing.T) {
	a := filepath.Clean("/repo/app")
	sep := string(filepath.Separator)

	same := []string{a, a + sep, filepath.Join("/repo", "sub", "..", "app")}
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		// These file systems do not distinguish the two, so neither may we:
		// treating them as separate projects would give one directory two
		// layouts and list it twice in the picker.
		same = append(same, strings.ToUpper(a), strings.ToLower(a))
	}
	for _, root := range same {
		if !sameRoot(a, root) {
			t.Errorf("sameRoot(%q, %q) = false, want true", a, root)
		}
		if hashRoot(root) != hashRoot(a) {
			t.Errorf("%q is stored under a different layout file from %q", root, a)
		}
	}

	for _, root := range []string{
		filepath.Clean("/repo/app2"),
		filepath.Clean("/repo/app/sub"),
		filepath.Clean("/other/app"),
	} {
		if sameRoot(a, root) {
			t.Errorf("sameRoot(%q, %q) = true, want false", a, root)
		}
		if hashRoot(root) == hashRoot(a) {
			t.Errorf("%q shares a layout file with %q", root, a)
		}
	}

	// The name is what the layout file is found by, so its shape has to hold:
	// a change here loses every saved workspace at once.
	name := "layout-" + hashRoot(a) + ".json"
	if len(name) != len("layout-")+16+len(".json") {
		t.Errorf("layout file %q is not the expected layout-<16 hex>.json", name)
	}
}

// skipUnlessRootsFold skips a test on the platforms where a root is hashed
// exactly as it is spelled, and so where the old and new layout names can
// never differ.
func skipUnlessRootsFold(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "windows" && runtime.GOOS != "darwin" {
		t.Skip("roots are only folded where the filesystem folds case")
	}
}

// writeLayoutAt saves a state directly to a named file, standing in for a
// build that named layouts differently from the one under test.
func writeLayoutAt(t *testing.T, path string, s *State) {
	t.Helper()
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		t.Fatalf("encode layout: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write layout: %v", err)
	}
}

// TestLoadAdoptsALayoutSavedUnderTheOldName covers the upgrade that started
// normalizing roots before hashing them. That changed the file name every
// layout was stored under, so without this the first run of the new build
// opens an empty workspace and the real one is never looked at again.
func TestLoadAdoptsALayoutSavedUnderTheOldName(t *testing.T) {
	isolateConfig(t)
	skipUnlessRootsFold(t)

	root := filepath.Join(t.TempDir(), "Repo", "App")
	old, err := legacyPath(root)
	if err != nil {
		t.Fatalf("legacy path: %v", err)
	}
	if old == "" {
		t.Fatal("root is already in normal form, so it cannot exercise the old name")
	}
	current, err := path(root)
	if err != nil {
		t.Fatalf("path: %v", err)
	}
	if old == current {
		t.Fatal("old and new names match, so there is no upgrade to test")
	}
	writeLayoutAt(t, old, &State{
		Version: Version,
		Root:    filepath.Clean(root),
		Tabs:    []Tab{{Title: "last night"}},
	})

	got, err := Load(root)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got == nil {
		t.Fatal("upgrading lost the saved layout")
	}
	if len(got.Tabs) != 1 || got.Tabs[0].Title != "last night" {
		t.Errorf("restored %+v, want the single tab that was saved", got.Tabs)
	}

	// The layout is moved rather than copied, so the next save writes to one
	// file and a later run does not have two to choose between.
	if _, err := os.Stat(current); err != nil {
		t.Errorf("layout was not moved to the name in use now: %v", err)
	}
	if _, err := os.Stat(old); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("layout under the old name = %v, want it moved away", err)
	}
}

// TestLoadPrefersTheNameInUseNow checks the migration cannot undo later work:
// once the new build has saved, that is the layout, and a stale file left
// under the old name must not come back.
func TestLoadPrefersTheNameInUseNow(t *testing.T) {
	isolateConfig(t)
	skipUnlessRootsFold(t)

	root := filepath.Join(t.TempDir(), "Repo", "App")
	old, err := legacyPath(root)
	if err != nil {
		t.Fatalf("legacy path: %v", err)
	}
	writeLayoutAt(t, old, &State{
		Version: Version,
		Root:    filepath.Clean(root),
		Tabs:    []Tab{{Title: "stale"}},
	})
	if err := Save(root, &State{Tabs: []Tab{{Title: "current"}}}); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := Load(root)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got == nil || len(got.Tabs) != 1 || got.Tabs[0].Title != "current" {
		t.Fatalf("restored %+v, want the layout saved under the name in use now", got)
	}
	if _, err := os.Stat(old); err != nil {
		t.Errorf("file under the old name = %v, want it left untouched", err)
	}
}

// TestLoadLeavesAnotherProjectsLegacyLayoutAlone checks the root recorded in a
// layout is still what decides whether it is this project's, on the migration
// path too. A file reached only through a hash collision must be neither
// restored nor renamed over this project's name, which would lose it for the
// project it really belongs to.
func TestLoadLeavesAnotherProjectsLegacyLayoutAlone(t *testing.T) {
	isolateConfig(t)
	skipUnlessRootsFold(t)

	root := filepath.Join(t.TempDir(), "Repo", "App")
	old, err := legacyPath(root)
	if err != nil {
		t.Fatalf("legacy path: %v", err)
	}
	writeLayoutAt(t, old, &State{
		Version: Version,
		Root:    filepath.Clean(filepath.Join(t.TempDir(), "Other", "Project")),
		Tabs:    []Tab{{Title: "someone else's"}},
	})

	got, err := Load(root)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got != nil {
		t.Errorf("restored %+v, want nothing for a layout belonging to another root", got)
	}
	if _, err := os.Stat(old); err != nil {
		t.Errorf("other project's layout = %v, want it left where it was", err)
	}
}

// TestDirAdoptsStateSavedUnderTheOldName checks that renaming the program does
// not strand what the previous name saved. Everything is below one directory —
// every layout, the auth token, the generated per-session settings — so the
// upgrade has to bring the directory with it or the first run after it opens
// an empty workspace beside a full one nobody will look in again.
func TestDirAdoptsStateSavedUnderTheOldName(t *testing.T) {
	isolateConfig(t)
	base, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("UserConfigDir: %v", err)
	}
	old := filepath.Join(base, legacyDirName)
	if err := os.MkdirAll(old, 0o700); err != nil {
		t.Fatalf("create the old directory: %v", err)
	}
	want := `{"version":1,"root":"/repo"}`
	if err := os.WriteFile(filepath.Join(old, "layout-abc.json"), []byte(want), 0o600); err != nil {
		t.Fatalf("seed a layout: %v", err)
	}

	dir, err := Dir()
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}
	if filepath.Base(dir) != "perch" {
		t.Errorf("Dir() = %q, want the directory named for the program in use now", dir)
	}
	got, err := os.ReadFile(filepath.Join(dir, "layout-abc.json"))
	if err != nil {
		t.Fatalf("the saved layout did not come across: %v", err)
	}
	if string(got) != want {
		t.Errorf("layout = %q, want %q", got, want)
	}
	if _, err := os.Stat(old); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the old directory was left behind: Stat = %v, want it moved", err)
	}
}

// TestDirNeverWritesOverStateAlreadySaved checks the adoption cannot destroy
// this build's own state. Once anything has been saved under the name in use
// now, that is the state to use and the old directory is only a leftover: a
// migration that ran a second time and moved it over the top would replace a
// live layout with a stale one.
func TestDirNeverWritesOverStateAlreadySaved(t *testing.T) {
	isolateConfig(t)
	base, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("UserConfigDir: %v", err)
	}
	old := filepath.Join(base, legacyDirName)
	if err := os.MkdirAll(old, 0o700); err != nil {
		t.Fatalf("create the old directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(old, "layout-abc.json"), []byte("stale"), 0o600); err != nil {
		t.Fatalf("seed the old layout: %v", err)
	}
	cur := filepath.Join(base, "perch")
	if err := os.MkdirAll(cur, 0o700); err != nil {
		t.Fatalf("create the current directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cur, "layout-abc.json"), []byte("current"), 0o600); err != nil {
		t.Fatalf("seed the current layout: %v", err)
	}

	dir, err := Dir()
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "layout-abc.json"))
	if err != nil {
		t.Fatalf("read the layout: %v", err)
	}
	if string(got) != "current" {
		t.Errorf("layout = %q, want the state already saved under the name in use now", got)
	}
	if _, err := os.Stat(old); err != nil {
		t.Errorf("the old directory should be left alone, not consumed: %v", err)
	}
}

// TestDamagedLayoutIsKeptRatherThanOverwritten checks a layout that will not
// parse survives the run that could not read it. Starting empty is fine;
// starting empty and then saving over the only copy of the user's tabs is how
// a single hand-edited typo turns into a workspace nobody can get back.
func TestDamagedLayoutIsKeptRatherThanOverwritten(t *testing.T) {
	isolateConfig(t)

	p, err := path("/repo/damaged")
	if err != nil {
		t.Fatalf("path: %v", err)
	}
	broken := []byte(`{"version":1,"tabs":[{"title":"work in progress"`)
	if err := os.WriteFile(p, broken, 0o600); err != nil {
		t.Fatalf("write layout: %v", err)
	}

	if got, err := Load("/repo/damaged"); err != nil || got != nil {
		t.Fatalf("Load = %v, %v; a damaged layout should restore nothing without failing", got, err)
	}

	// The run carries on and saves its empty workspace on the way out.
	if err := Save("/repo/damaged", &State{}); err != nil {
		t.Fatalf("save: %v", err)
	}

	kept, err := os.ReadFile(p + damagedSuffix)
	if err != nil {
		t.Fatalf("the damaged layout was not kept: %v", err)
	}
	if string(kept) != string(broken) {
		t.Errorf("kept copy is %q, want the file exactly as it was: %q", kept, broken)
	}
}

// TestDamagedLegacyLayoutIsKeptUnderItsOwnName checks the copy is made of the
// file that was actually read. A layout still under the pre-normalization name
// is read from there, so quarantining the name in use now would move nothing
// and leave the damaged file to be found and rejected again every run.
func TestDamagedLegacyLayoutIsKeptUnderItsOwnName(t *testing.T) {
	if runtime.GOOS != "windows" && runtime.GOOS != "darwin" {
		t.Skip("roots are only respelled on case-folding platforms")
	}
	isolateConfig(t)

	root := filepath.Clean("/Repo/Legacy")
	old, err := legacyPath(root)
	if err != nil {
		t.Fatalf("legacy path: %v", err)
	}
	if old == "" {
		t.Fatal("this root should have a distinct pre-normalization name")
	}
	if err := os.WriteFile(old, []byte("{ truncated"), 0o600); err != nil {
		t.Fatalf("write legacy layout: %v", err)
	}

	if got, err := Load(root); err != nil || got != nil {
		t.Fatalf("Load = %v, %v; want nothing restored and no error", got, err)
	}
	if _, err := os.Stat(old); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the damaged legacy layout is still in place; it will be rejected again every run")
	}
	if _, err := os.Stat(old + damagedSuffix); err != nil {
		t.Errorf("the damaged legacy layout was not kept: %v", err)
	}
}

// TestDamagedRecentsAreKeptBeforeTheListIsRebuilt checks the projects file
// survives the open that could not read it. TouchRecent rewrites the whole
// list from what it read, and what it read is nothing, so without this the
// first project opened after the damage is the only one the user has ever
// opened as far as the picker is concerned.
func TestDamagedRecentsAreKeptBeforeTheListIsRebuilt(t *testing.T) {
	isolateConfig(t)
	dir, err := Dir()
	if err != nil {
		t.Fatalf("dir: %v", err)
	}
	p := filepath.Join(dir, recentsFile)
	broken := []byte(`[{"root":"/repo/one"},{"root":"/repo/two"}`)
	if err := os.WriteFile(p, broken, 0o600); err != nil {
		t.Fatalf("write projects: %v", err)
	}

	if got, err := Recents(); err != nil || got != nil {
		t.Fatalf("Recents = %v, %v; a damaged list should read as empty without failing", got, err)
	}
	if err := TouchRecent("/repo/three"); err != nil {
		t.Fatalf("touch recent: %v", err)
	}

	kept, err := os.ReadFile(p + damagedSuffix)
	if err != nil {
		t.Fatalf("the damaged project list was not kept: %v", err)
	}
	if string(kept) != string(broken) {
		t.Errorf("kept copy is %q, want %q", kept, broken)
	}
	// The list itself carries on from empty, as it always has.
	list, err := Recents()
	if err != nil {
		t.Fatalf("recents: %v", err)
	}
	if len(list) != 1 || !sameRoot(list[0].Root, "/repo/three") {
		t.Errorf("recents = %v, want just the project that was opened", list)
	}
}

// TestDamagedSessionIsKept checks the record of which projects were open
// survives a run that could not read it, since the save on the way out
// replaces it with whatever this run happened to have open.
func TestDamagedSessionIsKept(t *testing.T) {
	isolateConfig(t)
	dir, err := Dir()
	if err != nil {
		t.Fatalf("dir: %v", err)
	}
	p := filepath.Join(dir, sessionFile)
	broken := []byte(`{"open":["/repo/a","/repo/b"`)
	if err := os.WriteFile(p, broken, 0o600); err != nil {
		t.Fatalf("write session: %v", err)
	}

	if got, err := LoadSession(); err != nil || got != nil {
		t.Fatalf("LoadSession = %v, %v; want nothing restored and no error", got, err)
	}
	kept, err := os.ReadFile(p + damagedSuffix)
	if err != nil {
		t.Fatalf("the damaged session was not kept: %v", err)
	}
	if string(kept) != string(broken) {
		t.Errorf("kept copy is %q, want %q", kept, broken)
	}
}

// TestLayoutFromAnotherSchemaVersionIsKept checks stepping back to an older
// build does not destroy the layout the newer one saved. The older build
// cannot read it and must not restore from it, but the save on the way out
// lands on the same name, so ignoring it in place means deleting it.
func TestLayoutFromAnotherSchemaVersionIsKept(t *testing.T) {
	isolateConfig(t)

	p, err := path("/repo/newer")
	if err != nil {
		t.Fatalf("path: %v", err)
	}
	future := []byte(`{"version":` + fmt.Sprint(Version+1) + `,"tabs":[{"title":"from a later build"}]}`)
	if err := os.WriteFile(p, future, 0o600); err != nil {
		t.Fatalf("write layout: %v", err)
	}

	if got, err := Load("/repo/newer"); err != nil || got != nil {
		t.Fatalf("Load = %v, %v; a layout from another schema version should restore nothing", got, err)
	}
	if err := Save("/repo/newer", &State{}); err != nil {
		t.Fatalf("save: %v", err)
	}
	kept, err := os.ReadFile(p + damagedSuffix)
	if err != nil {
		t.Fatalf("the newer layout was not kept: %v", err)
	}
	if string(kept) != string(future) {
		t.Errorf("kept copy is %q, want %q", kept, future)
	}
}

// TestProcessAliveSeesPastAHandleToAnExitedProcess checks the liveness test is
// about the process and not about whether a handle to it can be opened.
//
// Windows keeps a process object for as long as anything holds a handle, so a
// perch that exited long ago is still openable by whatever started it. Calling
// that "running" is what leaves a stale instance record in place, and while it
// is there every launch tries to attach to a server that is not listening and
// then declines to clear the record standing in its way.
func TestProcessAliveSeesPastAHandleToAnExitedProcess(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("a handle held open past exit is a Windows lifetime rule")
	}
	cmd := exec.Command("cmd", "/c", "exit")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	// Deliberately not waited for: Wait is what closes the handle, and the
	// handle is the whole point.
	defer func() { _ = cmd.Wait() }()
	pid := cmd.Process.Pid

	deadline := time.Now().Add(10 * time.Second)
	for processAlive(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("pid %d still reads as running; it exited and only the open handle is keeping it answerable", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
	runtime.KeepAlive(cmd)
}

// TestProcessAliveKnowsThisProcess is the other half: the test above would
// pass just as well if liveness always said no.
func TestProcessAliveKnowsThisProcess(t *testing.T) {
	if !processAlive(os.Getpid()) {
		t.Error("this process reads as not running")
	}
	if processAlive(0) || processAlive(-1) {
		t.Error("a process id that cannot name a process reads as running")
	}
}
