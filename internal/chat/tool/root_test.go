package tool

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// newRoot builds a root on a temporary directory, which is what almost every
// test in this package starts with.
func newRoot(t *testing.T) *Root {
	t.Helper()
	r, err := NewRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestResolveConfinesToTheRoot(t *testing.T) {
	root := newRoot(t)
	outside := filepath.Join(filepath.Dir(root.Dir()), "elsewhere", "secret.txt")

	tests := []struct {
		name    string
		path    string
		want    string // relative to the root; "." is the root itself
		refused bool
	}{
		{name: "empty means the root", path: "", want: "."},
		{name: "dot means the root", path: ".", want: "."},
		{name: "a relative path", path: "a/b.txt", want: "a/b.txt"},
		{name: "a relative path with a backtrack that stays inside", path: "a/../b.txt", want: "b.txt"},
		{name: "an absolute path inside", path: filepath.Join(root.Dir(), "a", "b.txt"), want: "a/b.txt"},
		{name: "a backtrack out of the root", path: "../elsewhere/secret.txt", refused: true},
		{name: "a backtrack out of the root from below", path: "a/../../elsewhere", refused: true},
		{name: "an absolute path outside", path: outside, refused: true},
		{name: "the parent itself", path: "..", refused: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := root.Resolve(tt.path)
			if tt.refused {
				if !errors.Is(err, ErrOutsideRoot) {
					t.Fatalf("Resolve(%q) = %q, %v; want a refusal", tt.path, got, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve(%q): %v", tt.path, err)
			}
			want := root.Dir()
			if tt.want != "." {
				want = filepath.Join(root.Dir(), filepath.FromSlash(tt.want))
			}
			if got != want {
				t.Errorf("Resolve(%q) = %q, want %q", tt.path, got, want)
			}
		})
	}
}

// TestResolveRefusesALinkOutOfTheRoot is the case a name-only check misses: the
// path is inside the root and the file it names is not.
func TestResolveRefusesALinkOutOfTheRoot(t *testing.T) {
	root := newRoot(t)
	away := t.TempDir()
	if err := os.WriteFile(filepath.Join(away, "secret.txt"), []byte("no"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root.Dir(), "away")
	if err := os.Symlink(away, link); err != nil {
		// Making a symbolic link on Windows needs a privilege the account
		// running the tests may not have, and the rest of the suite does not
		// depend on it.
		t.Skipf("cannot create a symbolic link here: %v", err)
	}
	if got, err := root.Resolve("away/secret.txt"); !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("Resolve through a link out of the root = %q, %v; want a refusal", got, err)
	}
	if _, err := root.Resolve("away"); !errors.Is(err, ErrOutsideRoot) {
		t.Error("the link itself leads out of the root and must be refused too")
	}
}

func TestNewRootRejectsWhatIsNotADirectory(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRoot(file); err == nil {
		t.Error("a file is not a working directory")
	}
	if _, err := NewRoot(""); err == nil {
		t.Error("an empty working directory must be refused")
	}
	if _, err := NewRoot(filepath.Join(dir, "missing")); err == nil {
		t.Error("a directory that does not exist must be refused")
	}
}

// TestResolveIgnoresCaseWhereTheFileSystemDoes guards the mistake that would
// otherwise show up only on Windows: a path that differs from the root only in
// case is the same path, and refusing it would refuse half the paths a model
// types.
func TestResolveIgnoresCaseWhereTheFileSystemDoes(t *testing.T) {
	if runtime.GOOS != "windows" && runtime.GOOS != "darwin" {
		t.Skip("only the case-insensitive platforms")
	}
	root := newRoot(t)
	upper := strings.ToUpper(root.Dir())
	if _, err := root.Resolve(filepath.Join(upper, "a.txt")); err != nil {
		t.Errorf("a path differing only in case must resolve: %v", err)
	}
}

func TestRelIsAlwaysSlashSeparated(t *testing.T) {
	root := newRoot(t)
	abs := filepath.Join(root.Dir(), "internal", "chat", "tool.go")
	if got := root.Rel(abs); got != "internal/chat/tool.go" {
		t.Errorf("Rel = %q, want internal/chat/tool.go", got)
	}
}

// A link whose target is not there yet cannot be followed to see where it
// leads, but writing through it creates that target: a link in the pane to a
// file outside it, not yet made, would have had it made there. The target is
// where the path leads, so one outside is refused and one inside is not.
func TestALinkToAFileNotYetMadeIsJudgedByWhereItLeads(t *testing.T) {
	root, outside := newRoot(t), t.TempDir()
	target := filepath.Join(outside, "made.txt")
	if err := os.Symlink(target, filepath.Join(root.Dir(), "out")); err != nil {
		t.Skipf("cannot make a link here: %v", err)
	}
	if err := os.Symlink("made.txt", filepath.Join(root.Dir(), "in")); err != nil {
		t.Fatal(err)
	}
	w := &writeFile{root: root}

	if _, err := root.Resolve("out"); !errors.Is(err, ErrOutsideRoot) {
		t.Errorf("Resolve(out) = %v; want a refusal", err)
	}
	if q := w.Approval(rawArgs(t, map[string]any{"path": "out", "content": "x"})); q != "" {
		t.Errorf("the write through the link out was put to the user: %q", q)
	}
	if _, err := call(t, w, map[string]any{"path": "out", "content": "x"}); !errors.Is(err, ErrOutsideRoot) {
		t.Errorf("writing through the link out: %v; want a refusal", err)
	}
	if _, err := os.Lstat(target); err == nil {
		t.Errorf("%s was made outside the working directory", target)
	}

	if _, err := call(t, w, map[string]any{"path": "in", "content": "x"}); err != nil {
		t.Errorf("writing through the link to a file inside: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(root.Dir(), "made.txt")); err != nil || string(got) != "x" {
		t.Errorf("the file inside holds %q, %v", got, err)
	}
}

// Unicode lower-cases the Kelvin sign to k and a dotted capital I to i, but a
// file system keeps them apart from those letters in a name. A folder named
// like the root with one of them in place of a k or an i is a sibling of the
// root, outside it, and a write to a file in it is refused.
func TestANameThatOnlyLowerCasesToTheRootsIsOutsideIt(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "work-in")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	root, err := NewRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	kelvin, dottedI := string(rune(0x212A)), string(rune(0x130))
	parent := filepath.Dir(root.Dir())
	for _, sibling := range []string{"wor" + kelvin + "-in", "work-" + dottedI + "n"} {
		p := filepath.Join(parent, sibling, "planted.txt")
		if got, err := root.Resolve(p); !errors.Is(err, ErrOutsideRoot) {
			t.Errorf("Resolve(%q) = %q, %v; want a refusal", p, got, err)
		}
		if _, err := call(t, &writeFile{root: root}, map[string]any{"path": p, "content": "x"}); !errors.Is(err, ErrOutsideRoot) {
			t.Errorf("writing %q: %v; want a refusal", p, err)
		}
		// On Windows the name is a folder of its own, and must not have been
		// made. On macOS, where APFS folds case the way Unicode does, the
		// Kelvin sign's name is the working directory itself: refusing it
		// there refuses a path that was inside, which is the safe mistake,
		// and finding the root by it is not a folder made beside it.
		info, err := os.Stat(filepath.Join(parent, sibling))
		if err != nil {
			continue
		}
		rootInfo, rerr := os.Stat(root.Dir())
		if rerr != nil {
			t.Fatal(rerr)
		}
		if !os.SameFile(info, rootInfo) {
			t.Errorf("%s was made beside the working directory", sibling)
		}
	}
}
