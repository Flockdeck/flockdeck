package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
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

// TestSessionLanding checks a launch that named no project goes back to the
// one the user was last in, and to another that was open when that one has
// gone, rather than to the directory it happened to start in.
func TestSessionLanding(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	gone := filepath.Join(a, "deleted")

	cases := []struct {
		name string
		s    *Session
		want string
	}{
		{"nothing saved", nil, ""},
		{"the project last in", &Session{Open: []string{a, b}, Active: b}, b},
		{"the project last in has gone", &Session{Open: []string{gone, b}, Active: gone}, b},
		{"no project was active", &Session{Open: []string{gone, a, b}}, a},
		{"every project has gone", &Session{Open: []string{gone}, Active: gone}, ""},
	}
	for _, c := range cases {
		if got := c.s.Landing(); got != c.want {
			t.Errorf("%s: Landing = %q, want %q", c.name, got, c.want)
		}
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
// list Flockdeck's state, including a directory left wide open by an earlier
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
						{Pane: &Pane{ID: "pane-1", Kind: "agent", Cwd: "/repo/a", Name: "worker", Task: "fix the parser", Agent: "codex", Model: "gpt-5",
							Routed: "rename or move", RoutedFrom: "gpt-5.6-sol"}, Weight: 0.7},
						{
							Dir: "v",
							Children: []*Node{
								{Pane: &Pane{ID: "pane-2", Kind: "shell", Cwd: "/repo/a/sub"}},
							},
						},
					},
				},
			},
			{Title: "docs", Root: &Node{Pane: &Pane{ID: "pane-3", Kind: "agent", Cwd: "/repo/a"}}},
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
	if *left.Pane != (Pane{ID: "pane-1", Kind: "agent", Cwd: "/repo/a", Name: "worker", Task: "fix the parser", Agent: "codex", Model: "gpt-5",
		Routed: "rename or move", RoutedFrom: "gpt-5.6-sol"}) {
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
// version of Flockdeck is passed over quietly. The fields it holds may mean
// something else entirely, and a start-up that fails is worse than one that
// opens a fresh tab.
func TestLoadIgnoresAnotherSchemaVersion(t *testing.T) {
	isolateConfig(t)

	p, err := path("/repo/a")
	if err != nil {
		t.Fatalf("path: %v", err)
	}
	// Version 1 is left out: it is not another schema but the one this build
	// migrates, and it has a test of its own below.
	for _, version := range []int{0, Version + 1} {
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

// TestVersion1LayoutIsMigrated covers the upgrade a user gets for nothing: a
// layout saved by a build that knew only Claude comes back with every tab and
// every pane, each agent pane now saying which agent it runs.
//
// This is the promise that somebody who only ever runs Claude notices nothing.
// Getting it wrong quarantines the file, and the first they would know of it
// is an empty workspace where their tabs were.
func TestVersion1LayoutIsMigrated(t *testing.T) {
	isolateConfig(t)

	p, err := path("/repo/a")
	if err != nil {
		t.Fatalf("path: %v", err)
	}
	// Written out by hand rather than through Save, because Save stamps the
	// version this build writes and there is no way back to version 1.
	v1 := []byte(`{
	  "version": 1,
	  "root": ` + strconv.Quote(filepath.Clean("/repo/a")) + `,
	  "active": 1,
	  "tabs": [
	    {"title": "api", "focus": "pane-1", "root": {"dir": "h", "children": [
	      {"pane": {"id": "pane-1", "kind": "claude", "cwd": "/repo/a", "name": "worker"}},
	      {"pane": {"id": "pane-2", "kind": "shell", "cwd": "/repo/a/sub"}}
	    ]}},
	    {"title": "docs", "root": {"pane": {"id": "pane-3", "kind": "claude", "cwd": "/repo/a"}}}
	  ]
	}`)
	if err := os.WriteFile(p, v1, 0o600); err != nil {
		t.Fatalf("write layout: %v", err)
	}

	got, err := Load("/repo/a")
	if err != nil || got == nil {
		t.Fatalf("Load = %v, %v; a version 1 layout must still restore", got, err)
	}
	if got.Version != Version {
		t.Errorf("version %d, want %d", got.Version, Version)
	}
	if len(got.Tabs) != 2 || got.Active != 1 {
		t.Fatalf("restored %d tabs, active %d; want 2 and 1", len(got.Tabs), got.Active)
	}

	tests := []struct {
		name  string
		pane  *Pane
		kind  string
		agent string
	}{
		{"a claude pane becomes an agent pane running claude",
			got.Tabs[0].Root.Children[0].Pane, "agent", "claude"},
		{"a shell pane stays a shell with no agent",
			got.Tabs[0].Root.Children[1].Pane, "shell", ""},
		{"panes in later tabs are migrated too",
			got.Tabs[1].Root.Pane, "agent", "claude"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.pane == nil {
				t.Fatal("the leaf lost its pane")
			}
			if tc.pane.Kind != tc.kind {
				t.Errorf("kind %q, want %q", tc.pane.Kind, tc.kind)
			}
			if tc.pane.Agent != tc.agent {
				t.Errorf("agent %q, want %q", tc.pane.Agent, tc.agent)
			}
			// Version 1 never recorded a model, and inventing one here would
			// pin a pane to a model the user never chose.
			if tc.pane.Model != "" {
				t.Errorf("model %q, want none", tc.pane.Model)
			}
		})
	}

	// The migration happens in memory: the file on disk is left as it was
	// until something saves over it, so a run that only looks does not rewrite
	// the user's layout and a step back to the older build still finds one it
	// can read.
	after, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(after) != string(v1) {
		t.Errorf("Load rewrote the layout on disk: %s", after)
	}
	if _, err := os.Stat(p + damagedSuffix); err == nil {
		t.Error("a version 1 layout was quarantined; it should have been migrated")
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
	if filepath.Base(dir) != "flockdeck" {
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
	cur := filepath.Join(base, "flockdeck")
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
// flockdeck that exited long ago is still openable by whatever started it. Calling
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

// TestConfigDirFollowsTheInstanceThatWonTheUpgrade checks the run that loses
// the race to rename the old state directory ends up in the same place as the
// run that won it.
//
// Two instances starting together on the first run after the rename both find
// nothing under the name in use now and both try the move. One succeeds. The
// loser's rename fails with the source already gone, and if it carries on
// under the old name it creates that directory again, empty, and writes its
// layouts, its recent projects and its instance record into somewhere nothing
// will ever adopt — including the other instance, which is looking for a
// record it will not find.
func TestConfigDirFollowsTheInstanceThatWonTheUpgrade(t *testing.T) {
	isolateConfig(t)
	base, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("user config dir: %v", err)
	}
	old := filepath.Join(base, legacyDirName)
	if err := os.MkdirAll(old, 0o700); err != nil {
		t.Fatalf("create legacy dir: %v", err)
	}
	saved := []byte(`[{"root":"/repo/kept"}]`)
	if err := os.WriteFile(filepath.Join(old, recentsFile), saved, 0o600); err != nil {
		t.Fatalf("write legacy state: %v", err)
	}

	// The other instance, getting its move in between our look and our rename.
	real := renameDir
	t.Cleanup(func() { renameDir = real })
	renameDir = func(from, to string) error {
		if err := real(from, to); err != nil {
			return err
		}
		return errors.New("the other instance moved it first")
	}

	dir, err := Dir()
	if err != nil {
		t.Fatalf("dir: %v", err)
	}
	want := filepath.Join(base, "flockdeck")
	if dir != want {
		t.Fatalf("state dir is %s, want %s: this run would write where nothing looks", dir, want)
	}
	got, err := os.ReadFile(filepath.Join(dir, recentsFile))
	if err != nil {
		t.Fatalf("the moved state is not in the directory being used: %v", err)
	}
	if string(got) != string(saved) {
		t.Errorf("recents = %q, want %q", got, saved)
	}
}

// modTime is the file's own record of when it was last written, used below to
// tell a write that did not happen from one that wrote the same bytes again.
func modTime(t *testing.T, path string) time.Time {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return fi.ModTime()
}

// TestSaveSkipsAFileThatAlreadyHoldsIt checks a save with nothing to say does
// not touch the file. Quitting saves every open project, and all but the one
// the user was working in are unchanged; each of those was a file creation, a
// flush to the device and a rename, and each was also a chance for a second
// instance's save to be the one that lost.
func TestSaveSkipsAFileThatAlreadyHoldsIt(t *testing.T) {
	isolateConfig(t)

	st := &State{Tabs: []Tab{{Title: "alpha", Root: &Node{Pane: &Pane{ID: "1", Kind: "claude", Cwd: "/repo/a"}}}}}
	if err := Save("/repo/a", st); err != nil {
		t.Fatalf("save: %v", err)
	}
	p, err := path("/repo/a")
	if err != nil {
		t.Fatalf("path: %v", err)
	}
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	if err := Save("/repo/a", st); err != nil {
		t.Fatalf("save again: %v", err)
	}
	if got := modTime(t, p); !got.Equal(old) {
		t.Errorf("the file was written again (mtime %v, want %v); nothing about it had changed", got, old)
	}
}

// TestSaveStillWritesWhenTheLengthIsUnchanged checks the shortcut is a
// shortcut and not a way to lose a save. The length is only a filter on
// whether the bytes are worth comparing; a tab switch changes an index and not
// a single character of the file's size.
func TestSaveStillWritesWhenTheLengthIsUnchanged(t *testing.T) {
	isolateConfig(t)

	st := &State{Tabs: []Tab{{Title: "alpha"}, {Title: "bravo"}, {Title: "charlie"}}}
	if err := Save("/repo/b", st); err != nil {
		t.Fatalf("save: %v", err)
	}
	p, err := path("/repo/b")
	if err != nil {
		t.Fatalf("path: %v", err)
	}
	before, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	st.Active = 2
	if err := Save("/repo/b", st); err != nil {
		t.Fatalf("save again: %v", err)
	}
	after, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(before) != len(after) {
		t.Fatalf("this case is meant to keep the file the same length: %d then %d", len(before), len(after))
	}
	got, err := Load("/repo/b")
	if err != nil || got == nil {
		t.Fatalf("Load = %v, %v", got, err)
	}
	if got.Active != 2 {
		t.Errorf("active tab is %d, want 2: the save was skipped over a file of the same length", got.Active)
	}
}

// TestSaveReplacesAFileOfTheSameLengthThatIsNotOurs is the same guard from the
// other side: a file left by something else that happens to be the same size
// must still be replaced, not mistaken for what we were about to write.
func TestSaveReplacesAFileOfTheSameLengthThatIsNotOurs(t *testing.T) {
	isolateConfig(t)
	dir, err := Dir()
	if err != nil {
		t.Fatalf("dir: %v", err)
	}
	p := filepath.Join(dir, sessionFile)

	want := &Session{Open: []string{"/repo/a"}, Active: "/repo/a"}
	if err := SaveSession(want); err != nil {
		t.Fatalf("save session: %v", err)
	}
	good, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	// Same number of bytes, different ones: a run of spaces where the file was.
	if err := os.WriteFile(p, []byte(strings.Repeat(" ", len(good))), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := SaveSession(want); err != nil {
		t.Fatalf("save session again: %v", err)
	}
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != string(good) {
		t.Errorf("the file was left as %q; a save must replace what it did not write", got)
	}
}

// benchState builds a layout the size of a workspace someone actually runs:
// eight tabs of six agents each.
func benchState(tabs, panes int) *State {
	st := &State{}
	for t := 0; t < tabs; t++ {
		root := &Node{Dir: "h"}
		for p := 0; p < panes; p++ {
			root.Children = append(root.Children, &Node{Pane: &Pane{
				ID:   fmt.Sprintf("%d-%d-9f6a1c34-2b7e-4d51-8a0c", t, p),
				Kind: "claude", Cwd: "/repo/project/sub", Name: "agent",
				Task: "keep the build green",
			}})
		}
		st.Tabs = append(st.Tabs, Tab{Title: fmt.Sprintf("tab %d", t), Root: root})
	}
	return st
}

func benchIsolate(b *testing.B) {
	dir := b.TempDir()
	b.Setenv("APPDATA", dir)
	b.Setenv("XDG_CONFIG_HOME", dir)
	b.Setenv("HOME", dir)
}

// BenchmarkRunSavesUnchangedLayout is what a run does to a project the user
// did not touch: read the layout at startup, write it on the way out. Quitting
// with several projects open does this for every one of them.
func BenchmarkRunSavesUnchangedLayout(b *testing.B) {
	benchIsolate(b)
	st := benchState(8, 6)
	if err := Save("/repo/project", st); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Load("/repo/project"); err != nil {
			b.Fatal(err)
		}
		if err := Save("/repo/project", st); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkRunSavesChangedLayout is the same for the project that did change,
// and is here to show what the comparison costs when it cannot save the write.
func BenchmarkRunSavesChangedLayout(b *testing.B) {
	benchIsolate(b)
	st := benchState(8, 6)
	if err := Save("/repo/project", st); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Load("/repo/project"); err != nil {
			b.Fatal(err)
		}
		st.Active = 1 + i%7
		if err := Save("/repo/project", st); err != nil {
			b.Fatal(err)
		}
	}
}

// TestLayoutFileNamesAreFixed pins the name a project's layout is saved under.
//
// The name is a hash of the root, so every part of taking it — cleaning the
// path, folding its case, the hash itself, the prefix and extension around it
// — is load-bearing in a way nothing else in the package is. Change any of
// them and the application looks up a name nothing was ever written to, finds
// nothing, and opens an empty workspace beside the layout it should have
// restored. That has happened once already; legacyPath exists to carry that
// change's victims across.
//
// So these values are not an implementation detail to be updated when the test
// goes red. A failure here means every saved layout in the world has just been
// orphaned, and the change needs a migration beside it.
func TestLayoutFileNamesAreFixed(t *testing.T) {
	// The hash on its own, checked everywhere so a platform that never sees
	// the paths below still catches a change to it.
	for _, c := range []struct{ in, want string }{
		{"", "cbf29ce484222325"},
		{".", "af63a34c86018bb1"},
		{"/home/user/repo", "96e5ae60e8caf52a"},
		{`c:\users\jim\repo`, "194e567d812290d0"},
		{"/users/jim/repo", "ed058ea066c6eec6"},
		{"/Users/Jim/repo", "1fb07467504dc246"},
		{`C:\Users\Jim\repo`, "baed5f9e244c96f0"},
	} {
		if got := hashString(c.in); got != c.want {
			t.Errorf("hashString(%q) = %s, want %s: every saved layout has just been orphaned", c.in, got, c.want)
		}
	}

	isolateConfig(t)
	dir, err := Dir()
	if err != nil {
		t.Fatalf("dir: %v", err)
	}

	// The whole name, for roots spelled the way that platform spells them.
	// The alternative spellings are the ones the picker, the command line and
	// the saved session actually produce, and every one of them has to reach
	// the same file. The filesystem root is in there because it is the one
	// path where tidying a trailing separator away changes what is left.
	var root, want string
	var also []string
	switch runtime.GOOS {
	case "windows":
		root, want = `C:\Users\Jim\repo`, "layout-194e567d812290d0.json"
		also = []string{`c:\users\jim\repo\`, `C:/Users/Jim/./repo`}
	case "darwin":
		root, want = "/Users/Jim/repo", "layout-ed058ea066c6eec6.json"
		also = []string{"/users/jim/repo/", "/Users/Jim/./repo"}
	default:
		root, want = "/home/user/repo", "layout-96e5ae60e8caf52a.json"
		also = []string{"/home/user/repo/", "/home/user/./repo"}
	}
	for _, spelling := range append([]string{root}, also...) {
		got, err := path(spelling)
		if err != nil {
			t.Fatalf("path: %v", err)
		}
		if got != filepath.Join(dir, want) {
			t.Errorf("path(%q) = %s, want %s", spelling, got, filepath.Join(dir, want))
		}
	}

	// The filesystem root, whose separator is not a trailing one to be tidied
	// away: strip it and the name changes.
	fsRoot, fsWant := "/", "layout-af63a24c860189fe.json"
	if runtime.GOOS == "windows" {
		fsRoot, fsWant = `C:`+`\`, "layout-f696dd190d7d304c.json"
	}
	if got, err := path(fsRoot); err != nil || got != filepath.Join(dir, fsWant) {
		t.Errorf("path(%q) = %s, %v, want %s", fsRoot, got, err, filepath.Join(dir, fsWant))
	}

	// And the name the same root was saved under before it was folded, which
	// is what Load falls back to. Where case is not folded there is no second
	// name to fall back to and there never was.
	old, err := legacyPath(root)
	if err != nil {
		t.Fatalf("legacy path: %v", err)
	}
	switch runtime.GOOS {
	case "windows":
		if old != filepath.Join(dir, "layout-baed5f9e244c96f0.json") {
			t.Errorf("legacyPath(%q) = %s, want layout-baed5f9e244c96f0.json", root, old)
		}
	case "darwin":
		if old != filepath.Join(dir, "layout-1fb07467504dc246.json") {
			t.Errorf("legacyPath(%q) = %s, want layout-1fb07467504dc246.json", root, old)
		}
	default:
		if old != "" {
			t.Errorf("legacyPath(%q) = %s, want no fallback name on a platform that does not fold case", root, old)
		}
	}
}

// TestStateFileNamesAreFixed pins the names of the files that are not per
// project. They are looked up by name across releases in exactly the same way
// a layout is, and renaming one silently starts the user again from nothing.
func TestStateFileNamesAreFixed(t *testing.T) {
	for _, c := range [][2]string{
		{recentsFile, "projects.json"},
		{sessionFile, "session.json"},
		{instanceFile, "instance.json"},
		{prefsFile, "prefs.json"},
	} {
		if c[0] != c[1] {
			t.Errorf("state file is named %q, want %q", c[0], c[1])
		}
	}
	isolateConfig(t)
	dir, err := Dir()
	if err != nil {
		t.Fatalf("dir: %v", err)
	}
	base, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("user config dir: %v", err)
	}
	if dir != filepath.Join(base, "flockdeck") {
		t.Errorf("state dir is %s, want %s", dir, filepath.Join(base, "flockdeck"))
	}
}

// TestEverythingAtOnceLeavesEveryFileReadable runs the whole package against
// itself: layouts being saved and loaded, the project list being touched and
// forgotten, the session and the instance record being rewritten, and the
// sweep walking the same directory while all of it happens.
//
// One instance already does several of these at once — the interface saves on
// its own goroutine while the hook server touches projects on another — and
// two instances do all of them. Four writers is more than that on purpose:
// this is the corner where the retries have to hold up. Nothing here may leave a file that the next
// start cannot read, and nothing may leave debris in the state directory: a
// half-written layout is a workspace the user does not get back.
func TestEverythingAtOnceLeavesEveryFileReadable(t *testing.T) {
	isolateConfig(t)
	dir, err := Dir()
	if err != nil {
		t.Fatalf("dir: %v", err)
	}
	roots := []string{"/repo/one", "/repo/two", "/repo/three"}
	for _, root := range roots {
		if err := Save(root, &State{Tabs: []Tab{{Title: "seed"}}}); err != nil {
			t.Fatalf("seed %s: %v", root, err)
		}
	}

	const rounds = 40
	var wg sync.WaitGroup
	fail := func(what string, err error) {
		if err != nil {
			t.Errorf("%s: %v", what, err)
		}
	}
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				root := roots[(w+i)%len(roots)]
				st := &State{Active: i % 3}
				for tab := 0; tab <= i%4; tab++ {
					st.Tabs = append(st.Tabs, Tab{
						Title: fmt.Sprintf("w%d-%d", w, tab),
						Root:  &Node{Pane: &Pane{ID: fmt.Sprintf("%d-%d", w, tab), Kind: "claude", Cwd: root}},
					})
				}
				fail("save layout", Save(root, st))
				if _, err := Load(root); err != nil {
					t.Errorf("load layout: %v", err)
				}
				fail("touch recent", TouchRecent(fmt.Sprintf("/repo/p%d", i%7)))
				fail("forget recent", ForgetRecent(fmt.Sprintf("/repo/p%d", (i+3)%7)))
				if _, err := Recents(); err != nil {
					t.Errorf("recents: %v", err)
				}
				fail("save session", SaveSession(&Session{Open: roots, Active: root}))
				if _, err := LoadSession(); err != nil {
					t.Errorf("load session: %v", err)
				}
				fail("save instance", SaveInstance(&Instance{PID: os.Getpid(), URL: "http://127.0.0.1:1/", Started: time.Now()}))
				if _, err := LoadInstance(); err != nil {
					t.Errorf("load instance: %v", err)
				}
				// The sweep another instance runs at startup, walking the same
				// directory these writes are landing in.
				if _, err := SweepSessions(time.Hour); err != nil {
					t.Errorf("sweep: %v", err)
				}
			}
		}(w)
	}
	wg.Wait()

	for _, root := range roots {
		st, err := Load(root)
		if err != nil {
			t.Errorf("load %s afterwards: %v", root, err)
			continue
		}
		if st == nil {
			t.Errorf("layout for %s is no longer readable", root)
		}
	}
	if _, err := Recents(); err != nil {
		t.Errorf("recents afterwards: %v", err)
	}
	if sess, err := LoadSession(); err != nil || sess == nil {
		t.Errorf("session afterwards: %v, %v", sess, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Errorf("temporary file %s was left behind", e.Name())
		}
		if strings.Contains(e.Name(), damagedSuffix) {
			t.Errorf("%s was written badly enough that it had to be quarantined", e.Name())
		}
	}
}

// TestASaveWaitsOutAHoldOnTheFileItReplaces checks a save survives another
// process keeping the file for longer than a moment.
//
// On Windows a rename cannot replace a file anything else has open, whatever
// sharing either side asked for, so every read the other instance makes of a
// layout stops this one saving it. Those holds are usually gone by the first
// retry, but the tail is long, and past the end of the retries the save is not
// slow — it is lost, and with it the tabs it was carrying.
func TestASaveWaitsOutAHoldOnTheFileItReplaces(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("only Windows lets an open file stand in the way of the rename that replaces it")
	}
	dir := t.TempDir()
	dst := filepath.Join(dir, "layout.json")
	if err := os.WriteFile(dst, []byte("old"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	src := filepath.Join(dir, "layout.json.tmp1")
	if err := os.WriteFile(src, []byte("new"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	// The other instance, reading the layout it is about to restore from.
	held, err := os.Open(dst)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	const hold = 400 * time.Millisecond
	go func() {
		time.Sleep(hold)
		held.Close()
	}()

	start := time.Now()
	if err := renameWithRetry(src, dst); err != nil {
		t.Fatalf("the save was lost to a %v hold on the file: %v", hold, err)
	}
	if waited := time.Since(start); waited < hold {
		t.Errorf("the rename reported success after %v, but the file was held for %v", waited, hold)
	}
	got, err := os.ReadFile(dst)
	if err != nil || string(got) != "new" {
		t.Errorf("file holds %q, %v; want the saved contents", got, err)
	}
}

// TestARenameThatCannotWorkIsNotWaitedOut is the other side of the budget: a
// failure that is not contention has to come back at once, or every save in a
// broken state directory would sit out the whole budget before saying so.
func TestARenameThatCannotWorkIsNotWaitedOut(t *testing.T) {
	dir := t.TempDir()
	start := time.Now()
	err := renameWithRetry(filepath.Join(dir, "was-never-written"), filepath.Join(dir, "dst"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("renaming a source that is not there gave %v, want a not-exist error", err)
	}
	if waited := time.Since(start); waited > contentionBudget/2 {
		t.Errorf("it waited %v before reporting a failure that was never going to change", waited)
	}
}

// TestTheStateDirectoryIsDecidedOnceARun checks a run that had to fall back to
// the directory the old name points at stays there.
//
// The move to the name in use now can fail for a reason that goes away —
// a file a detached instance still holds open, on Windows — and looking again
// later would then succeed and rename the directory out from under everything
// that had already been handed its old path. The settings directory each agent
// is launched with is worked out once at startup and kept; move it half way
// through the run and every agent afterwards writes into a tree nothing looks
// at again.
func TestTheStateDirectoryIsDecidedOnceARun(t *testing.T) {
	isolateConfig(t)
	base, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("user config dir: %v", err)
	}
	old := filepath.Join(base, legacyDirName)
	if err := os.MkdirAll(old, 0o700); err != nil {
		t.Fatalf("create legacy dir: %v", err)
	}

	real := renameDir
	t.Cleanup(func() { renameDir = real })
	refused := false
	renameDir = func(from, to string) error {
		if !refused {
			refused = true
			return errors.New("held open by a detached instance")
		}
		return real(from, to)
	}

	first, err := Dir()
	if err != nil {
		t.Fatalf("dir: %v", err)
	}
	if first != old {
		t.Fatalf("state dir is %s, want the old name %s while the move cannot be made", first, old)
	}
	// Whatever the run hands out now is held for as long as it runs.
	sessions, err := SessionsDir()
	if err != nil {
		t.Fatalf("sessions dir: %v", err)
	}

	again, err := Dir()
	if err != nil {
		t.Fatalf("dir again: %v", err)
	}
	if again != first {
		t.Errorf("the run moved from %s to %s part way through; every path already handed out points into the first", first, again)
	}
	if _, err := os.Stat(sessions); err != nil {
		t.Errorf("the settings directory handed out earlier is gone: %v", err)
	}
}

// TestSweepLeavesARunningInstancesSettingsAlone checks the sweep does not pull
// the hook settings out from under agents that are still going.
//
// A pane's settings are written when it starts and never touched again, so an
// agent working since yesterday has a file that looks a day abandoned. `flockdeck
// -solo` starts a second instance beside a first that is still answering — the
// one left detached with its agents running among them — and its sweep was
// deleting exactly those files. Temporaries are a different matter: no write
// is a day long, so an old one is nobody's.
func TestSweepLeavesARunningInstancesSettingsAlone(t *testing.T) {
	isolateConfig(t)
	sessions, err := SessionsDir()
	if err != nil {
		t.Fatalf("sessions dir: %v", err)
	}
	state, err := Dir()
	if err != nil {
		t.Fatalf("dir: %v", err)
	}

	settings := filepath.Join(sessions, "9f6a1c34-2b7e.settings.json")
	temp := filepath.Join(state, "layout-abc.json.tmp99")
	old := time.Now().Add(-48 * time.Hour)
	for _, p := range []string{settings, temp} {
		if err := os.WriteFile(p, []byte("{}"), 0o600); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatalf("age %s: %v", p, err)
		}
	}

	// Another instance, running, that is not us.
	if err := SaveInstance(&Instance{PID: os.Getpid() + 1, URL: "http://127.0.0.1:1/", Started: old}); err != nil {
		t.Fatalf("save instance: %v", err)
	}
	alive := processAlive
	t.Cleanup(func() { processAlive = alive })
	processAlive = func(pid int) bool { return pid == os.Getpid()+1 }

	if _, err := SweepSessions(24 * time.Hour); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if _, err := os.Stat(settings); err != nil {
		t.Errorf("the running instance's pane lost its hook settings: %v", err)
	}
	if _, err := os.Stat(temp); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("an abandoned temporary was kept; no write is a day long")
	}

	// Once nothing else is running, the settings go too.
	processAlive = func(int) bool { return false }
	if _, err := SweepSessions(24 * time.Hour); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if _, err := os.Stat(settings); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("settings with nobody left to want them were kept")
	}
}

// TestEveryFieldSurvivesTheRoundTrip checks a layout comes back exactly as it
// went in, field for field.
//
// The tests beside this one pick out the parts they are about, which is what
// lets a field be added to the schema, written by the workspace, and quietly
// dropped on the way through here without anything going red. This one
// compares the whole thing, so a field that stops surviving is a failure
// whether or not anybody thought to check it: the tab holding an agent
// borrowed from another project, the prompt a spawned pane was given, the
// exact share of the window a split was dragged to.
func TestEveryFieldSurvivesTheRoundTrip(t *testing.T) {
	isolateConfig(t)

	want := &State{
		Active: 2,
		Tabs: []Tab{
			{
				Title: "api & \"docs\" <\\>",
				Focus: "pane-2",
				Root: &Node{
					Dir:    "h",
					Weight: 1.0 / 3.0, // needs every digit of a float64 to come back
					Children: []*Node{
						{Pane: &Pane{
							ID: "pane-1", Kind: "claude", Cwd: `C:\repo\ünïcode`,
							Name: "worker", Task: "fix the parser\nthen the lexer",
							Root: `C:\other\project`, Cols: 211, Rows: 57,
						}, Weight: 0.7},
						{
							Dir:    "v",
							Weight: 2.0 / 3.0,
							Children: []*Node{
								{Pane: &Pane{ID: "pane-2", Kind: "shell", Cwd: "/repo/a/sub"}},
								{Dir: "h", Children: []*Node{
									{Pane: &Pane{ID: "pane-3", Kind: "claude", Cwd: "/repo/a"}, Weight: 0.25},
								}},
							},
						},
					},
				},
			},
			{Title: "docs", Root: &Node{Pane: &Pane{ID: "pane-4", Kind: "claude", Cwd: "/repo/a"}}},
			{Root: &Node{Pane: &Pane{ID: "pane-5", Kind: "shell", Cwd: "/repo/a"}}},
		},
	}
	// Save fills in the version and the root on the struct it is handed, so
	// what goes in is noted before it does.
	before := *want

	if err := Save("/repo/a", want); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := Load("/repo/a")
	if err != nil || got == nil {
		t.Fatalf("load: %v", err)
	}
	if !reflect.DeepEqual(got.Tabs, before.Tabs) {
		t.Errorf("tabs came back different\n got %s\nwant %s", showTabs(got.Tabs), showTabs(before.Tabs))
	}
	if got.Active != before.Active {
		t.Errorf("active tab %d, want %d", got.Active, before.Active)
	}
	if got.Version != Version {
		t.Errorf("version %d, want %d", got.Version, Version)
	}
	if !sameRoot(got.Root, "/repo/a") {
		t.Errorf("root %q, want /repo/a", got.Root)
	}
}

// showTabs renders tabs for a failure message; the structs are trees of
// pointers and print as addresses otherwise.
func showTabs(tabs []Tab) string {
	b, err := json.MarshalIndent(tabs, "", "  ")
	if err != nil {
		return fmt.Sprintf("%+v", tabs)
	}
	return string(b)
}

// TestAReadThatCannotWorkIsNotWaitedOut checks the retry budget is spent only
// on contention.
//
// The recent list is read every time the project picker opens. A state
// directory somebody has mangled — a directory standing where a file belongs —
// would otherwise stall each of those reads for the whole budget, which is a
// picker that feels broken rather than one that fails.
func TestAReadThatCannotWorkIsNotWaitedOut(t *testing.T) {
	dir := t.TempDir()
	notAFile := filepath.Join(dir, "layout.json")
	if err := os.Mkdir(notAFile, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	start := time.Now()
	if _, err := readState(notAFile); err == nil {
		t.Fatal("reading a directory as a state file should have failed")
	}
	if waited := time.Since(start); waited > contentionBudget/2 {
		t.Errorf("it waited %v before reporting a failure that was never going to change", waited)
	}

	start = time.Now()
	if _, err := readState(filepath.Join(dir, "was-never-written")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("reading a file that is not there gave %v, want a not-exist error", err)
	}
	if waited := time.Since(start); waited > contentionBudget/2 {
		t.Errorf("it waited %v for a file that is simply not there", waited)
	}
}
