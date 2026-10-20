package iso

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func mkdirAged(t *testing.T, path string, age time.Duration) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(path, "config"), 0o700); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(-age)
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// Only an old directory with the test-home name goes; a recent one (a run in
// progress), another name, and a plain file with the name stay.
func TestSweepStaleRemovesOnlyOldTestHomes(t *testing.T) {
	root := t.TempDir()
	old := filepath.Join(root, homePrefix+"111")
	recent := filepath.Join(root, homePrefix+"222")
	other := filepath.Join(root, "flockdeck-state-333")
	similar := filepath.Join(root, "my-"+homePrefix+"444")
	mkdirAged(t, old, 48*time.Hour)
	mkdirAged(t, recent, time.Hour)
	mkdirAged(t, other, 48*time.Hour)
	mkdirAged(t, similar, 48*time.Hour)
	file := filepath.Join(root, homePrefix+"555")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(file, when, when); err != nil {
		t.Fatal(err)
	}

	sweepStale(root, time.Now(), staleAfter)

	if exists(old) {
		t.Error("the old test home was not removed")
	}
	for name, p := range map[string]string{"recent test home": recent, "other directory": other, "similar name": similar, "file": file} {
		if !exists(p) {
			t.Errorf("the %s was removed", name)
		}
	}
}

// A link with the name is not followed: its target and the link stay.
func TestSweepStaleDoesNotFollowLinks(t *testing.T) {
	root := t.TempDir()
	target := t.TempDir()
	keep := filepath.Join(target, "precious")
	if err := os.WriteFile(keep, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(-48 * time.Hour)
	_ = os.Chtimes(target, when, when)
	link := filepath.Join(root, homePrefix+"666")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks are not available here: %v", err)
	}

	sweepStale(root, time.Now(), staleAfter)

	if !exists(keep) {
		t.Error("the link's target was emptied")
	}
	if !exists(link) {
		t.Error("the link itself was removed")
	}
}

func TestSweepStaleIgnoresAMissingRoot(t *testing.T) {
	sweepStale(filepath.Join(t.TempDir(), "absent"), time.Now(), staleAfter)
}

func fakeEnv(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func cacheAt(dir string) func() (string, error) { return func() (string, error) { return dir, nil } }

func TestGoDirsDefaultsToWhereGoWouldPutThem(t *testing.T) {
	home := filepath.Join("h", "me")
	got := goDirs(fakeEnv(nil), cacheAt(filepath.Join("h", "cache")), cacheAt(home))
	want := [][2]string{
		{"GOCACHE", filepath.Join("h", "cache", "go-build")},
		{"GOMODCACHE", filepath.Join(home, "go", "pkg", "mod")},
	}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("goDirs = %v, want %v", got, want)
	}
}

func TestGoDirsKeepsWhatIsSetAndTakesTheFirstGOPATH(t *testing.T) {
	first := filepath.Join("a", "one")
	env := fakeEnv(map[string]string{"GOCACHE": "set", "GOPATH": first + string(os.PathListSeparator) + filepath.Join("b", "two")})
	got := goDirs(env, cacheAt("c"), cacheAt("h"))
	if len(got) != 1 || got[0] != [2]string{"GOMODCACHE", filepath.Join(first, "pkg", "mod")} {
		t.Errorf("goDirs = %v, want only GOMODCACHE under the first GOPATH", got)
	}
	if got := goDirs(fakeEnv(map[string]string{"GOCACHE": "x", "GOMODCACHE": "y"}), cacheAt("c"), cacheAt("h")); len(got) != 0 {
		t.Errorf("goDirs = %v, want nothing when both are set", got)
	}
	fail := func() (string, error) { return "", errors.New("no") }
	if got := goDirs(fakeEnv(nil), fail, fail); len(got) != 0 {
		t.Errorf("goDirs = %v, want nothing when the defaults cannot be found", got)
	}
}

// Under Main the go command's caches are not in the test home, so a test that
// runs `go build` uses the real ones.
func TestMainKeepsTheGoCachesOutOfTheTestHome(t *testing.T) {
	home := os.Getenv("HOME")
	for _, name := range []string{"GOCACHE", "GOMODCACHE"} {
		v := os.Getenv(name)
		if v == "" {
			t.Errorf("%s is not set after Main", name)
			continue
		}
		if home != "" && strings.HasPrefix(v, home) {
			t.Errorf("%s = %q is inside the test home %q", name, v, home)
		}
	}
}

// A home whose run is still going has its modification time renewed, so the
// sweep of another run does not take it for one left by a run that died.
func TestKeepFreshRenewsTheHomesTime(t *testing.T) {
	dir := t.TempDir()
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(dir, old, old); err != nil {
		t.Fatal(err)
	}
	stop := keepFresh(dir, 5*time.Millisecond)
	defer stop()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if fi, err := os.Stat(dir); err == nil && time.Since(fi.ModTime()) < time.Hour {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Error("the modification time was not renewed")
}

// The renewal has to come several times inside the age a home is swept at, or
// one missed beat would let a live home go.
func TestTouchEveryIsWellInsideStaleAfter(t *testing.T) {
	if touchEvery > staleAfter/4 {
		t.Errorf("touchEvery = %v, want at most %v (a quarter of staleAfter)", touchEvery, staleAfter/4)
	}
	if touchInterval != touchEvery {
		t.Errorf("touchInterval = %v outside the child test, want touchEvery %v", touchInterval, touchEvery)
	}
}

// sweepProbeEnv names the aged home the parent planted, in the scratch temp
// directory the child was given. It is set only for the child that checks
// Main's wiring, and has no FLOCKDECK_ prefix, which Main would clear.
const sweepProbeEnv = "ISO_PROBE_SWEEP"

func init() {
	// Runs before TestMain, so Main in the child test binary renews its home
	// in milliseconds, not hourly.
	if os.Getenv(sweepProbeEnv) != "" {
		touchInterval = 20 * time.Millisecond
	}
}

// Main itself has to sweep and to keep its home fresh; the pieces tested alone
// would pass if Main stopped calling them. The test starts this binary with
// TMP and TEMP in a scratch directory holding an aged fake home, so nothing in
// the real temp directory is touched, and the child reports what Main did.
func TestMainSweepsAndRenewsItsHome(t *testing.T) {
	if os.Getenv(sweepProbeEnv) != "" || os.Getenv(probeEnv) != "" {
		return
	}
	scratch := t.TempDir()
	aged := filepath.Join(scratch, homePrefix+"aged")
	mkdirAged(t, aged, 48*time.Hour)
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		switch strings.ToUpper(name) {
		case "TMP", "TEMP", "TMPDIR", strings.ToUpper(realConfigEnv), strings.ToUpper(realHomeEnv):
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "TMP="+scratch, "TEMP="+scratch, "TMPDIR="+scratch, sweepProbeEnv+"="+aged)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestProbeSweepAndRenew$", "-test.v")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "PASS: TestProbeSweepAndRenew") {
		t.Fatalf("the child did not see Main sweep and renew: %v\n%s", err, out)
	}
	if exists(aged) {
		t.Error("the aged home is still there after the child's Main")
	}
}

// TestProbeSweepAndRenew is the child's half. Run on its own it checks nothing.
func TestProbeSweepAndRenew(t *testing.T) {
	aged := os.Getenv(sweepProbeEnv)
	if aged == "" {
		t.Skip("run by TestMainSweepsAndRenewsItsHome")
	}
	if exists(aged) {
		t.Errorf("Main did not sweep %s", aged)
	}
	home := os.Getenv("HOME")
	if !strings.HasPrefix(filepath.Base(home), homePrefix) {
		t.Fatalf("HOME %q is not a test home", home)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(home, old, old); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if fi, err := os.Stat(home); err == nil && time.Since(fi.ModTime()) < time.Hour {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Error("Main did not renew its home's modification time")
}
