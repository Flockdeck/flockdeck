package tool

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// The pane's directory can be spelled another way than the file system's own
// name for it -- a junction or a link to it, a subst or mapped drive, a short
// 8.3 name -- and that spelling is what the model is told and copies into the
// absolute paths it writes. Such a path leads inside the root, and is taken
// there; one that leads out of it is still refused.
func TestAnAbsolutePathThroughAnotherNameForTheRootIsInsideIt(t *testing.T) {
	real, parent := t.TempDir(), t.TempDir()
	alias := filepath.Join(parent, "alias")
	if runtime.GOOS == "windows" {
		if out, err := exec.Command("cmd", "/c", "mklink", "/J", alias, real).CombinedOutput(); err != nil {
			t.Skipf("cannot make a junction here: %v %s", err, out)
		}
	} else if err := os.Symlink(real, alias); err != nil {
		t.Skipf("cannot make a link here: %v", err)
	}
	root, err := NewRoot(alias)
	if err != nil {
		t.Fatal(err)
	}

	p := filepath.Join(alias, "sub", "a.txt")
	got, err := root.Resolve(p)
	if err != nil {
		t.Fatalf("Resolve(%q): %v", p, err)
	}
	if rel := root.Rel(got); rel != "sub/a.txt" {
		t.Errorf("Resolve(%q) = %q, shown as %q; want sub/a.txt", p, got, rel)
	}
	if _, err := call(t, &writeFile{root: root}, map[string]any{"path": p, "content": "x"}); err != nil {
		t.Fatalf("writing %q: %v", p, err)
	}
	if data, err := os.ReadFile(filepath.Join(real, "sub", "a.txt")); err != nil || string(data) != "x" {
		t.Errorf("the file in the working directory holds %q, %v", data, err)
	}

	for _, out := range []string{filepath.Join(alias, "..", "elsewhere.txt"), filepath.Join(parent, "elsewhere.txt")} {
		if got, err := root.Resolve(out); !errors.Is(err, ErrOutsideRoot) {
			t.Errorf("Resolve(%q) = %q, %v; want a refusal", out, got, err)
		}
	}
}
