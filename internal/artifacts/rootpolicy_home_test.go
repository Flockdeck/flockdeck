package artifacts

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// This file keeps the first implementation of the check on the folders below
// a home that keep secrets, which held them as a list of text, and holds the
// shipped one to it. The list is read from internal/secretname/testdata when a
// test runs, so this file, like the shipped program, holds no list of names as
// text.

var (
	homeListOnce sync.Once
	homeList     []string
	homeListErr  error
)

func listedHomePaths(t testing.TB) []string {
	t.Helper()
	homeListOnce.Do(func() {
		b, err := os.ReadFile(filepath.Join("..", "secretname", "testdata", "names.txt"))
		if err != nil {
			homeListErr = err
			return
		}
		in := false
		for _, line := range strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n") {
			line = strings.TrimSpace(line)
			switch {
			case line == "" || strings.HasPrefix(line, "#"):
			case strings.HasPrefix(line, "["):
				in = line == "[home-paths]"
			case in:
				homeList = append(homeList, line)
			}
		}
	})
	if homeListErr != nil {
		t.Fatal(homeListErr)
	}
	if len(homeList) == 0 {
		t.Fatal("no [home-paths] in secretname/testdata/names.txt")
	}
	return homeList
}

// oldSecretUnderHome is what forbiddenRoot asked of each home before.
func oldSecretUnderHome(t testing.TB, h, real string) string {
	for _, sub := range listedHomePaths(t) {
		if sameOrUnder(h+"/"+sub, real) {
			return "a folder that keeps secrets (" + sub + ")"
		}
	}
	return ""
}

// oldRestForbidden is restForbidden as it was.
func oldRestForbidden(t testing.TB, rest []string) string {
	if strings.HasPrefix(rest[0], "onedrive") {
		if len(rest) == 1 {
			return "a cloud folder that holds the user's folders"
		}
		rest = rest[1:]
	}
	joined := strings.Join(rest, "/")
	for _, sub := range listedHomePaths(t) {
		s := strings.ToLower(sub)
		if joined == s || strings.HasPrefix(joined, s+"/") {
			return "a folder that keeps secrets (" + sub + ")"
		}
	}
	for _, sub := range settingsUnderHome {
		if joined == strings.ToLower(sub) {
			return "a folder that holds every program's settings (" + sub + ")"
		}
	}
	return ""
}

func sameRest(t testing.TB, parts []string) {
	t.Helper()
	if len(parts) == 0 {
		return
	}
	lower := make([]string, len(parts))
	for i, p := range parts {
		lower[i] = strings.ToLower(p)
	}
	want, got := oldRestForbidden(t, lower), restForbidden(lower, parts)
	// The reason names the folder as the path spells it, where it named it as
	// the list did: the same words in other case.
	if (want == "") != (got == "") || !strings.EqualFold(want, got) && !strings.Contains(want, "every program's") {
		t.Errorf("restForbidden(%q) = %q, was %q", parts, got, want)
	}
	if strings.Contains(want, "every program's") && want != got {
		t.Errorf("restForbidden(%q) = %q, was %q", parts, got, want)
	}
}

func TestRestForbiddenAnswersAsTheListDid(t *testing.T) {
	for _, p := range listedHomePaths(t) {
		parts := strings.Split(p, "/")
		sameRest(t, parts)
		sameRest(t, strings.Split(strings.ToUpper(p), "/"))
		sameRest(t, append(append([]string{}, parts...), "project", "src"))
		sameRest(t, parts[:len(parts)-1])
		sameRest(t, append([]string{"OneDrive"}, parts...))
		sameRest(t, append([]string{"onedrive - work"}, parts[:len(parts)-1]...))
		sameRest(t, append([]string{"x"}, parts...))
	}
	for _, p := range settingsUnderHome {
		sameRest(t, strings.Split(p, "/"))
	}
	sameRest(t, []string{"onedrive"})
}

func FuzzRestForbiddenAnswersAsTheListDid(f *testing.F) {
	f.Add(".ssh/id")
	f.Add("AppData/Local/Google/Chrome/Default")
	f.Add("onedrive/.mozilla")
	f.Add("library/application support/firefox")
	f.Add("appdata/roaming")
	f.Add(".config/gh")
	f.Fuzz(func(t *testing.T, s string) {
		var parts []string
		for _, p := range strings.FieldsFunc(s, func(r rune) bool { return r == '/' || r == 0x5c }) {
			parts = append(parts, p)
		}
		sameRest(t, parts)
	})
}

// homeFixture makes a home with some of the listed folders in it (every other
// one), a few of them links to folders outside it where the system allows,
// and returns the home and the spellings of paths to try.
func homeFixture(t testing.TB, dir string) (home string, reals []string) {
	t.Helper()
	home = filepath.Join(dir, "home")
	outside := filepath.Join(dir, "outside")
	for _, d := range []string{home, outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for i, sub := range listedHomePaths(t) {
		p := filepath.Join(home, filepath.FromSlash(sub))
		switch {
		case i%2 == 1:
			// not there
		case i%7 == 0:
			target := filepath.Join(outside, strings.ReplaceAll(sub, "/", "_"))
			if err := os.MkdirAll(filepath.Join(target, "inner"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := linkDir(target, p); err != nil {
				_ = os.MkdirAll(p, 0o755) // no links here
			}
			reals = append(reals, target, filepath.Join(target, "inner"))
		default:
			if err := os.MkdirAll(filepath.Join(p, "child"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		reals = append(reals, p, filepath.Join(p, "child"), filepath.Join(p, "missing", "deeper"),
			filepath.Dir(p), filepath.Join(filepath.Dir(p), "sibling"),
			filepath.Join(home, strings.ToUpper(filepath.FromSlash(sub))),
			filepath.Join(home, strings.ToLower(filepath.FromSlash(sub)), "child"),
			strings.TrimSuffix(p, sub[len(sub)-1:])) // a name that is a letter short
	}
	reals = append(reals, home, filepath.Join(home, "Documents"), filepath.Join(home, "repos", "app"),
		filepath.Join(home, ".config"), filepath.Join(home, "AppData", "Local"), outside, dir)
	return home, reals
}

func sameHome(t testing.TB, home, real string) {
	t.Helper()
	want, got := oldSecretUnderHome(t, home, real), secretUnderHome(home, real)
	if (want == "") != (got == "") || !strings.EqualFold(want, got) {
		t.Errorf("secretUnderHome(%q) = %q, was %q", real, got, want)
	}
}

func TestSecretUnderHomeAnswersAsTheListDid(t *testing.T) {
	home, reals := homeFixture(t, t.TempDir())
	hits := 0
	for _, r := range reals {
		sameHome(t, home, r)
		if secretUnderHome(home, r) != "" {
			hits++
		}
	}
	if hits < len(listedHomePaths(t)) {
		t.Errorf("only %d of %d paths tried were refused: the test is not looking", hits, len(reals))
	}
}

func FuzzSecretUnderHomeAnswersAsTheListDid(f *testing.F) {
	home, _ := homeFixture(f, f.TempDir())
	for _, p := range listedHomePaths(f) {
		f.Add(p)
		f.Add(strings.ToUpper(p) + "/x")
	}
	f.Add(".config")
	f.Add("")
	f.Fuzz(func(t *testing.T, s string) {
		if len(s) > 300 || strings.ContainsRune(s, 0) {
			return
		}
		sameHome(t, home, filepath.Join(home, filepath.FromSlash(s)))
	})
}

// A home with too many entries to look through is refused, not skipped.
func TestSecretUnderHomeRefusesWhenItCannotLook(t *testing.T) {
	home := t.TempDir()
	budget := 2
	for _, n := range []string{"a", "b", "c"} {
		if err := os.Mkdir(filepath.Join(home, n), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if why := walkHome(home, "", filepath.Join(home, "repos"), &budget); why == "" {
		t.Error("a walk that ran out of budget refused nothing")
	}
}

// linkDir makes p a link to the folder target: a symbolic link, or on Windows,
// where that needs a privilege, a junction.
func linkDir(target, p string) error {
	err := os.Symlink(target, p)
	if err != nil && runtime.GOOS == "windows" {
		return exec.Command("cmd", "/c", "mklink", "/J", p, target).Run()
	}
	return err
}

// The links the fixture makes are the case that text cannot answer, so the test
// says if it could not make one.
func TestHomeFixtureHasLinks(t *testing.T) {
	home, _ := homeFixture(t, t.TempDir())
	n := 0
	for _, sub := range listedHomePaths(t) {
		if fi, err := os.Lstat(filepath.Join(home, filepath.FromSlash(sub))); err == nil && fi.Mode()&os.ModeType != 0 {
			n++
		}
	}
	if n == 0 {
		t.Skip("this system cannot make links here: the equivalence test does not cover them")
	}
}

// A link on the way to a listed path that points back up the home is not
// walked: .config -> home would otherwise read the whole home a second time.
func TestWalkHomeDoesNotFollowALinkUp(t *testing.T) {
	home := t.TempDir()
	const others = 40
	for i := 0; i < others; i++ {
		if err := os.Mkdir(filepath.Join(home, fmt.Sprintf("d%02d", i)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := linkDir(home, filepath.Join(home, ".config")); err != nil {
		t.Skip("this system cannot make links here")
	}
	budget := 1000
	if why := walkHome(home, "", filepath.Join(home, "repos"), &budget); why != "" {
		t.Errorf("walk refused an unrelated root: %q", why)
	}
	// The home is read once: its folders and the .config link. Following the
	// link would read them all again.
	if spent := 1000 - budget; spent > others+5 {
		t.Errorf("walk looked at %d entries of a home with %d; the link up was followed", spent, others+1)
	}
}
