package server

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jmwri/flockdeck/internal/session/transcript"
)

// The other tests of the phone answer use a project root that is not on disk, so
// they read the file system as one with no links in it: every path exists and is
// its own resolution. Tests of links replace this with their own, and the real
// one with realFS.
func init() {
	evalLinks = func(p string) (string, error) { return p, nil }
	lstatPath = func(string) (os.FileInfo, error) { return nil, nil }
}

func realFS(t *testing.T) {
	t.Helper()
	was, wasL := evalLinks, lstatPath
	evalLinks, lstatPath = filepath.EvalSymlinks, os.Lstat
	t.Cleanup(func() { evalLinks, lstatPath = was, wasL })
}

// fakeLinks is a file system with some links: a map from a path to where it
// leads ("" for a link that cannot be read), and the paths that exist.
func fakeLinks(t *testing.T, links map[string]string, exist ...string) {
	t.Helper()
	was, wasL := evalLinks, lstatPath
	t.Cleanup(func() { evalLinks, lstatPath = was, wasL })
	have := map[string]bool{}
	for _, p := range exist {
		have[p] = true
	}
	// below says p is under from, and where it is below it.
	below := func(from, p string) (string, bool) {
		rel, err := filepath.Rel(from, p)
		if err != nil || rel == "." || !filepath.IsLocal(rel) {
			return "", false
		}
		return rel, true
	}
	evalLinks = func(p string) (string, error) {
		for from, to := range links {
			if p == from {
				if to == "" {
					return "", errors.New("a link that cannot be read")
				}
				return to, nil
			}
			if rel, ok := below(from, p); ok {
				return filepath.Join(to, rel), nil
			}
		}
		if !have[p] {
			return "", fs.ErrNotExist
		}
		return p, nil
	}
	lstatPath = func(p string) (os.FileInfo, error) {
		if _, ok := links[p]; ok {
			return fakeInfo{mode: fs.ModeSymlink}, nil
		}
		if have[p] {
			return fakeInfo{}, nil
		}
		return nil, fs.ErrNotExist
	}
}

// fakeInfo is the one thing of a file's information that resolvedPathOK reads.
type fakeInfo struct {
	fs.FileInfo
	mode fs.FileMode
}

func (f fakeInfo) Mode() fs.FileMode { return f.mode }

// The path is resolved through the links of the file system at the moment of the
// decision, and held to the same rules.
func TestResolvedPathOKFollowsLinks(t *testing.T) {
	elsewhere, _ := filepath.Abs(string(filepath.Separator) + "elsewhere")
	for name, tc := range map[string]struct {
		links map[string]string
		exist []string
		path  string
		want  string
	}{
		"no links, a new file":       {nil, []string{testRoot}, pnew, ""},
		"no links, a file":           {nil, []string{testRoot, pa}, pa, ""},
		"a folder that leads out":    {map[string]string{inRoot("out"): elsewhere}, []string{testRoot}, inRoot("out", "a.go"), whyPath},
		"a file that leads out":      {map[string]string{pa: filepath.Join(elsewhere, "a.go")}, []string{testRoot}, pa, whyPath},
		"a new file in a link out":   {map[string]string{inRoot("out"): elsewhere}, []string{testRoot}, inRoot("out", "deep", "new.go"), whyPath},
		"a link inside the project":  {map[string]string{inRoot("alias"): inRoot("src")}, []string{testRoot, inRoot("src")}, inRoot("alias", "a.go"), whyPath},
		"the root is itself a link":  {map[string]string{testRoot: elsewhere}, nil, pa, ""},
		"a link into .git":           {map[string]string{inRoot("alias"): inRoot(".git")}, []string{testRoot}, inRoot("alias", "config"), whyPath},
		"a link to package.json":     {map[string]string{pa: inRoot("package.json")}, []string{testRoot}, pa, whyPath},
		"a link that cannot be read": {map[string]string{pa: ""}, []string{testRoot}, pa, whyPath},
		"a root that does not exist": {nil, nil, pnew, whyPath},
		"nothing exists up the path": {nil, []string{}, pnew, whyPath},
		"a path the floor refuses":   {nil, []string{testRoot}, inRoot(".git", "config"), whyPath},
	} {
		t.Run(name, func(t *testing.T) {
			fakeLinks(t, tc.links, tc.exist...)
			if got := resolvedPathOK(tc.path, testRoot); got != tc.want {
				t.Errorf("resolvedPathOK(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

// Go does not resolve a Windows junction: EvalSymlinks returns it as it is, and
// Lstat reports it as irregular. A part of the path that reads so is refused.
func TestResolvedPathOKRefusesIrregularParts(t *testing.T) {
	fakeLinks(t, nil, testRoot, inRoot("junction"), pa)
	lstatPath = func(p string) (os.FileInfo, error) {
		switch p {
		case inRoot("junction"):
			return fakeInfo{mode: fs.ModeIrregular}, nil
		case testRoot, pa:
			return fakeInfo{}, nil
		}
		return nil, fs.ErrNotExist
	}
	if got := resolvedPathOK(inRoot("junction", "a.go"), testRoot); got != whyPath {
		t.Errorf("through a junction: %q", got)
	}
	if got := resolvedPathOK(pa, testRoot); got != "" {
		t.Errorf("an ordinary file: %q", got)
	}
	lstatPath = func(p string) (os.FileInfo, error) { return nil, errors.New("access denied") }
	if got := resolvedPathOK(pa, testRoot); got != whyPath {
		t.Errorf("a part that cannot be read: %q", got)
	}
}

// A part of the path that cannot be read is a refusal of its own, and so is a path
// that is not clean, before anything is resolved.
func TestNoLinksBelowAndTheLexicalCheck(t *testing.T) {
	was := lstatPath
	t.Cleanup(func() { lstatPath = was })
	lstatPath = func(p string) (os.FileInfo, error) {
		if p == pa {
			return nil, errors.New("access denied")
		}
		return fakeInfo{}, nil
	}
	if noLinksBelow(pa, testRoot) {
		t.Error("a part that cannot be read passed")
	}
	if !noLinksBelow(pb, testRoot) {
		t.Error("an ordinary part was refused")
	}
	fakeLinks(t, nil, testRoot)
	unclean := testRoot + string(filepath.Separator) + "src" + string(filepath.Separator) + ".." + string(filepath.Separator) + "a.go"
	if got := resolvedPathOK(unclean, testRoot); got != whyPath {
		t.Errorf("an unclean path: %q", got)
	}
}

// A part that reads as ordinary but resolves elsewhere (a reparse point Lstat does
// not show as one) is caught by the resolved path being held to the rules.
func TestResolvedPathOKChecksWhereItResolves(t *testing.T) {
	elsewhere, _ := filepath.Abs(string(filepath.Separator) + "elsewhere")
	fakeLinks(t, nil, testRoot, inRoot("odd"), inRoot("odd", "a.go"), inRoot("tame"), inRoot("tame", "a.go"))
	plain := evalLinks
	evalLinks = func(p string) (string, error) {
		switch p {
		case inRoot("odd", "a.go"):
			return filepath.Join(elsewhere, "a.go"), nil
		case inRoot("tame", "a.go"):
			return inRoot("tame", "b.go"), nil
		}
		return plain(p)
	}
	if got := resolvedPathOK(inRoot("odd", "a.go"), testRoot); got != whyPath {
		t.Errorf("resolves outside the project: %q", got)
	}
	if got := resolvedPathOK(inRoot("tame", "a.go"), testRoot); got != "" {
		t.Errorf("resolves inside the project: %q", got)
	}
}

// A link that leads out is refused when the phone answers, even though the card
// shows a path inside the project.
func TestPhoneAnswerChecksLinksAtDecisionTime(t *testing.T) {
	elsewhere, _ := filepath.Abs(string(filepath.Separator) + "elsewhere")
	c := editCard(inRoot("out", "a.go"), transcript.EditDiff(inRoot("out", "a.go"), "a", "b"))
	l, _ := lockFor(c, "t")
	if p := projectCard(c, true, testRoot); !p.CanAllow {
		t.Fatalf("the card is not allowed by its text: %+v", p)
	}
	fakeLinks(t, map[string]string{inRoot("out"): elsewhere}, testRoot)
	if _, err := phoneAnswer(c, "t", true, testRoot, l, decisionAllow); err == nil {
		t.Error("allowed an edit through a link that leads out")
	}
	if id, err := phoneAnswer(c, "t", true, testRoot, l, decisionDeny); err != nil || id != "reject" {
		t.Errorf("deny = %q %v: a link must not stop a deny", id, err)
	}
	fakeLinks(t, nil, testRoot)
	if id, err := phoneAnswer(c, "t", true, testRoot, l, decisionAllow); err != nil || id != "allow" {
		t.Errorf("allow without the link = %q %v", id, err)
	}
	// A command has no path to resolve.
	e := execCard("ls")
	le, _ := lockFor(e, "t")
	fakeLinks(t, map[string]string{testRoot: ""})
	if _, err := phoneAnswer(e, "t", true, testRoot, le, decisionAllow); err != nil {
		t.Errorf("a command is refused for a file system error: %v", err)
	}
}

// The same with a real link on disk. A symlink needs a privilege on Windows that
// a test machine may not have; a junction does not, and is tried then.
func TestResolvedPathOKOnTheRealFileSystem(t *testing.T) {
	realFS(t)
	root := t.TempDir()
	out := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "out")
	if err := os.Symlink(out, link); err != nil {
		if runtime.GOOS != "windows" {
			t.Skipf("no symlinks here: %v", err)
		}
		if b, err := exec.Command("cmd", "/c", "mklink", "/J", link, out).CombinedOutput(); err != nil {
			t.Skipf("no symlinks or junctions here: %v %s", err, b)
		}
	}
	if got := resolvedPathOK(filepath.Join(root, "src", "a.go"), root); got != "" {
		t.Errorf("a new file in the project: %q", got)
	}
	if got := resolvedPathOK(filepath.Join(link, "a.go"), root); got != whyPath {
		t.Errorf("a new file through a link that leads out: %q", got)
	}
	if err := os.WriteFile(filepath.Join(out, "x.go"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := resolvedPathOK(filepath.Join(link, "x.go"), root); got != whyPath {
		t.Errorf("a file through a link that leads out: %q", got)
	}
}
