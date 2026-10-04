package helpers

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/store"
)

func TestInstallLayout(t *testing.T) {
	for _, goos := range []string{"linux", "windows"} {
		t.Run(goos, func(t *testing.T) {
			f := newFixture(t)
			f.goos = goos
			f.rebuild()
			f.publish("0.4.0")
			info, err := f.install("")
			if err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(f.store.Root, "lens", "versions", "0.4.0")
			for _, name := range []string{f.entry.BinaryName(goos), "README.md", "install.json"} {
				if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
					t.Errorf("%s: %v", name, err)
				}
			}
			if goos == "windows" {
				if _, err := os.Stat(filepath.Join(dir, "lens.exe")); err != nil {
					t.Error("no lens.exe on windows")
				}
			}
			var cur currentFile
			data, _ := os.ReadFile(filepath.Join(f.store.Root, "lens", "current.json"))
			if err := json.Unmarshal(data, &cur); err != nil || cur.Version != "0.4.0" {
				t.Errorf("current.json = %s", data)
			}
			var recorded InstallInfo
			data, _ = os.ReadFile(filepath.Join(dir, "install.json"))
			if err := json.Unmarshal(data, &recorded); err != nil {
				t.Fatal(err)
			}
			wantSource := f.site.URL + "/Flockdeck/lens/releases/download/v0.4.0/" + f.entry.ArchiveName("0.4.0", goos, "amd64")
			if recorded.Source != wantSource || recorded.SHA256 != info.SHA256 || !recorded.Signed || recorded.InstalledAt.IsZero() {
				t.Errorf("install.json = %+v", recorded)
			}
			if fi, err := os.Stat(f.store.DataDir("lens")); err != nil || !fi.IsDir() {
				t.Errorf("no data folder: %v", err)
			} else if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o700 {
				t.Errorf("data folder is %v", fi.Mode().Perm())
			}
			if runtime.GOOS != "windows" {
				bin, _ := os.Stat(filepath.Join(dir, f.entry.BinaryName(goos)))
				readme, _ := os.Stat(filepath.Join(dir, "README.md"))
				if bin.Mode().Perm() != 0o755 || readme.Mode().Perm() != 0o644 {
					t.Errorf("modes: binary %v, README %v", bin.Mode().Perm(), readme.Mode().Perm())
				}
			}
			assertNoStaging(t, f.store, "lens")
			got, ok := f.store.Info("lens")
			if !ok || got.Version != "0.4.0" {
				t.Errorf("Info = %+v, %v", got, ok)
			}
			path, err := f.store.BinaryPath(f.entry, goos)
			if err != nil || !strings.HasSuffix(path, f.entry.BinaryName(goos)) {
				t.Errorf("BinaryPath = %q, %v", path, err)
			}
		})
	}
}

func TestAlreadyInstalled(t *testing.T) {
	f := newFixture(t)
	f.publish("0.4.0")
	if _, err := f.install(""); err != nil {
		t.Fatal(err)
	}
	if _, err := f.install(""); !errors.Is(err, ErrAlreadyInstalled) {
		t.Fatalf("err = %v", err)
	}
	if _, err := f.install("0.4.0"); !errors.Is(err, ErrAlreadyInstalled) {
		t.Fatalf("err = %v", err)
	}
}

// An install that fails at any stage leaves the old version current.
func TestInterruptedInstallLeavesTheOldVersionCurrent(t *testing.T) {
	for _, stage := range []string{"downloaded", "extracted", "renamed", "current"} {
		t.Run(stage, func(t *testing.T) {
			var failAt string
			f := newFixture(t, func(o *Options) {
				o.Hook = func(s string) error {
					if s == failAt {
						return fmt.Errorf("simulated failure after %s", s)
					}
					return nil
				}
			})
			f.publish("0.4.0")
			f.publish("0.5.0")
			if _, err := f.install("0.4.0"); err != nil {
				t.Fatal(err)
			}
			failAt = stage
			if _, err := f.install("0.5.0"); err == nil {
				t.Fatal("the install did not fail")
			}
			if v, ok := f.store.Current("lens"); !ok || v != "0.4.0" {
				t.Fatalf("current = %q, %v; want 0.4.0", v, ok)
			}
			if _, err := os.Stat(filepath.Join(f.store.versionsDir("lens"), "0.5.0")); err == nil {
				t.Error("the half-installed version was left in versions/")
			}
			assertNoStaging(t, f.store, "lens")
			// And the next try works.
			failAt = ""
			if _, err := f.install("0.5.0"); err != nil {
				t.Fatalf("the install after a failure: %v", err)
			}
			if v, _ := f.store.Current("lens"); v != "0.5.0" {
				t.Fatalf("current = %q", v)
			}
		})
	}
}

func TestFailedFirstInstallLeavesNothingInstalled(t *testing.T) {
	f := newFixture(t, func(o *Options) {
		o.Hook = func(s string) error {
			if s == "current" {
				return errors.New("simulated")
			}
			return nil
		}
	})
	f.publish("0.4.0")
	if _, err := f.install(""); err == nil {
		t.Fatal("the install did not fail")
	}
	if _, ok := f.store.Current("lens"); ok {
		t.Fatal("something is installed")
	}
	if _, err := os.Stat(f.store.currentFile("lens")); err == nil {
		t.Fatal("current.json was left behind")
	}
}

// What a crash leaves, built by hand: a version folder that no current.json
// names, a stray staging folder and a half-written pointer.
func TestCrashLeftoversDoNotCountAsInstalled(t *testing.T) {
	f := newFixture(t)
	f.publish("0.4.0")
	if _, err := f.install("0.4.0"); err != nil {
		t.Fatal(err)
	}
	orphan := f.store.versionDir("lens", "0.5.0")
	if err := os.MkdirAll(orphan, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(orphan, "junk"), []byte("x"), 0o644)
	if v, _ := f.store.Current("lens"); v != "0.4.0" {
		t.Fatalf("an unreferenced folder changed what is current: %q", v)
	}
	// An install of that version replaces the orphan.
	f.publish("0.5.0")
	if _, err := f.install("0.5.0"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(orphan, "junk")); err == nil {
		t.Error("the orphan's contents survived")
	}
}

func TestCurrentIgnoresBrokenPointers(t *testing.T) {
	f := newFixture(t)
	f.publish("0.4.0")
	if _, err := f.install(""); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"not json":        "{",
		"empty":           "",
		"a path":          `{"version":"../../x"}`,
		"a missing one":   `{"version":"9.9.9"}`,
		"another shape":   `{"version":"v0.4.0"}`,
		"a separator":     `{"version":"0.4.0/.."}`,
		"a number":        `{"version":4}`,
		"no version":      `{}`,
		"a long version":  `{"version":"` + strings.Repeat("1", 100) + `"}`,
		"a nested folder": `{"version":"0.4.0\\.."}`,
	}
	for name, content := range cases {
		if err := os.WriteFile(f.store.currentFile("lens"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if v, ok := f.store.Current("lens"); ok {
			t.Errorf("%s: current = %q", name, v)
		}
	}
	if _, ok := f.store.Current("../lens"); ok {
		t.Error("an id with a path was accepted")
	}
}

func TestStagingSweep(t *testing.T) {
	f := newFixture(t)
	app := f.store.appDir("lens")
	old := filepath.Join(app, "staging.new-old")
	fresh := filepath.Join(app, "staging.new-fresh")
	tmp := filepath.Join(app, ".tmp-current.json-123")
	other := filepath.Join(app, "data")
	for _, d := range []string{old, fresh, other} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(tmp, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-2 * time.Hour)
	for _, p := range []string{old, tmp} {
		if err := os.Chtimes(p, past, past); err != nil {
			t.Fatal(err)
		}
	}
	if n := f.store.SweepStaging("lens", time.Now()); n != 2 {
		t.Errorf("swept %d, want 2", n)
	}
	for _, p := range []string{old, tmp} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s survived", p)
		}
	}
	for _, p := range []string{fresh, other} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s was removed: %v", p, err)
		}
	}
}

func TestOnlyPreviousVersionIsKept(t *testing.T) {
	f := newFixture(t)
	for _, v := range []string{"0.3.0", "0.4.0", "0.5.0"} {
		f.publish(v)
		if _, err := f.install(v); err != nil {
			t.Fatal(err)
		}
	}
	entries, _ := os.ReadDir(f.store.versionsDir("lens"))
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if len(names) != 2 || names[0] != "0.4.0" || names[1] != "0.5.0" {
		t.Fatalf("versions = %v, want [0.4.0 0.5.0]", names)
	}
}

func TestInstallRefusedWhileRunning(t *testing.T) {
	busy := false
	f := newFixture(t, func(o *Options) { o.Busy = func(string) bool { return busy } })
	f.publish("0.4.0")
	f.publish("0.5.0")
	if _, err := f.install("0.4.0"); err != nil {
		t.Fatal(err)
	}
	busy = true
	if _, err := f.install("0.5.0"); !errors.Is(err, ErrBusy) {
		t.Fatalf("err = %v, want ErrBusy", err)
	}
	busy = false
	// A run.json naming this very process, with its real start time.
	started, _ := store.ProcessStartedAt(os.Getpid())
	if err := f.store.writeRun("lens", RunInfo{PID: os.Getpid(), Started: started, Port: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.install("0.5.0"); !errors.Is(err, ErrBusy) {
		t.Fatalf("err = %v, want ErrBusy from run.json", err)
	}
	// A recycled pid is not a running helper.
	if !started.IsZero() {
		if err := f.store.writeRun("lens", RunInfo{PID: os.Getpid(), Started: started.Add(-time.Hour), Port: 1}); err != nil {
			t.Fatal(err)
		}
		if _, err := f.install("0.5.0"); err != nil {
			t.Fatalf("a stale run.json blocked an install: %v", err)
		}
	}
}

func TestUninstallKeepsDataUnlessPurged(t *testing.T) {
	f := newFixture(t)
	f.publish("0.4.0")
	if _, err := f.install(""); err != nil {
		t.Fatal(err)
	}
	app := f.store.appDir("lens")
	note := filepath.Join(f.store.DataDir("lens"), "notes.db")
	if err := os.WriteFile(note, []byte("precious"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(f.store.logsDir("lens"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(f.store.logFile("lens"), []byte("log"), 0o600)
	_ = os.WriteFile(filepath.Join(app, "run.json"), []byte(`{"pid":0}`), 0o600)

	if err := f.store.Uninstall("lens", false); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(note); err != nil || string(got) != "precious" {
		t.Fatalf("data was touched by an uninstall: %q, %v", got, err)
	}
	for _, gone := range []string{"versions", "current.json", "logs", "run.json"} {
		if _, err := os.Stat(filepath.Join(app, gone)); err == nil {
			t.Errorf("%s survived an uninstall", gone)
		}
	}
	if _, ok := f.store.Current("lens"); ok {
		t.Error("still installed")
	}
	if !f.store.HasData("lens") {
		t.Error("HasData is false with data present")
	}

	if err := f.store.PurgeData("lens"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(note); err == nil {
		t.Error("purge left the data")
	}
}

func TestUninstallWithPurgeRemovesData(t *testing.T) {
	f := newFixture(t)
	f.publish("0.4.0")
	if _, err := f.install(""); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(f.store.Root, "other-helper")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Uninstall("lens", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(f.store.appDir("lens")); err == nil {
		t.Error("the app folder survived a purge")
	}
	if _, err := os.Stat(other); err != nil {
		t.Errorf("a purge reached a sibling: %v", err)
	}
}

func TestPurgeRefusesADataFolderThatIsALink(t *testing.T) {
	f := newFixture(t)
	f.publish("0.4.0")
	if _, err := f.install(""); err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	keep := filepath.Join(target, "keep.txt")
	if err := os.WriteFile(keep, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(f.store.DataDir("lens")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, f.store.DataDir("lens")); err != nil {
		t.Skipf("cannot make links here: %v", err)
	}
	if err := f.store.PurgeData("lens"); err == nil {
		t.Fatal("purged through a link")
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("the link's target was touched: %v", err)
	}
}

func TestUninstallRefusedWhileRunning(t *testing.T) {
	f := newFixture(t)
	f.publish("0.4.0")
	if _, err := f.install(""); err != nil {
		t.Fatal(err)
	}
	started, _ := store.ProcessStartedAt(os.Getpid())
	if err := f.store.writeRun("lens", RunInfo{PID: os.Getpid(), Started: started, Port: 1}); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Uninstall("lens", false); err == nil || !strings.Contains(err.Error(), "running") {
		t.Fatalf("err = %v", err)
	}
	if _, ok := f.store.Current("lens"); !ok {
		t.Fatal("a running helper was uninstalled")
	}
	if err := f.store.PurgeData("lens"); err == nil {
		t.Fatal("data was purged under a running helper")
	}
}

func TestUninstallNotInstalled(t *testing.T) {
	s := &Store{Root: t.TempDir()}
	if err := s.Uninstall("lens", false); err != nil {
		t.Fatal(err)
	}
	if err := s.Uninstall("../x", false); err == nil {
		t.Fatal("an id with a path was accepted")
	}
}

func TestCatalogue(t *testing.T) {
	e, ok := Lookup("lens")
	if !ok || e.Repo != "Flockdeck/lens" || e.Name != "lens" {
		t.Fatalf("lens = %+v, %v", e, ok)
	}
	if _, ok := Lookup("nope"); ok {
		t.Fatal("found a helper that is not there")
	}
	if !validVersion(e.MinVersion) {
		t.Fatalf("the catalogue's minimum version %q is not a version", e.MinVersion)
	}
	if got := e.ArchiveName("0.4.0", "windows", "arm64"); got != "lens_0.4.0_windows_arm64.zip" {
		t.Errorf("windows archive = %s", got)
	}
	if got := e.ArchiveName("0.4.0", "darwin", "arm64"); got != "lens_0.4.0_darwin_arm64.tar.gz" {
		t.Errorf("darwin archive = %s", got)
	}
	if e.BinaryName("windows") != "lens.exe" || e.BinaryName("linux") != "lens" {
		t.Error("binary names")
	}
	m := e.Banner.FindStringSubmatch("lens 0.4.0 at http://localhost:8123/")
	if m == nil || m[1] != "8123" {
		t.Errorf("banner does not read the port: %v", m)
	}
	for _, bad := range []string{"lens at http://localhost:1/", "Lens 1.0 at http://localhost:1/", "lens 1.0 at http://evil.example:1/",
		"x\nlens 1.0 at http://localhost:1/", "lens 1.0 at http://localhost:1/ extra"} {
		if e.Banner.MatchString(bad) {
			t.Errorf("banner matched %q", bad)
		}
	}
	if len(e.Allows) == 0 || e.Ready == "" || e.Health == "" {
		t.Error("the entry is missing its permissions or probes")
	}
	if len(Catalogue()) != 1 {
		t.Error("the first slice has one helper")
	}
}
