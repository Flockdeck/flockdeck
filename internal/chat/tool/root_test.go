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
