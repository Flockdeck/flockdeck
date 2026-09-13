package tool

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// A file inside a repository's own directory -- a hook, a config naming a
// pager -- runs code the next time git is used, so "always" for edits does not
// cover it: each change there is asked about on its own.
func TestStandingPermissionForEditsLeavesTheRepositoryAlone(t *testing.T) {
	root := newRoot(t)
	w, e := &writeFile{root: root}, &editFile{root: root}
	for _, c := range []struct{ path, want string }{
		{"src/main.go", editFamily},
		{".gitignore", editFamily},
		{".git/hooks/pre-commit", ""},
		{".GIT/config", ""},
		{"sub/.git/config", ""},
		{".hg/hgrc", ""},
	} {
		args := json.RawMessage(`{"path":"` + c.path + `"}`)
		if got := w.Prefix(args); got != c.want {
			t.Errorf("write %s: Prefix = %q, want %q", c.path, got, c.want)
		}
		if got := e.Prefix(args); got != c.want {
			t.Errorf("edit %s: Prefix = %q, want %q", c.path, got, c.want)
		}
	}
}

// Windows has other spellings for .git than its name -- with the trailing dot
// or space it drops, as the directory's own stream, or by its short name -- and
// a link may lead there under any name at all. None of them is covered by
// "always" for edits either.
func TestStandingPermissionForEditsKnowsTheRepositoryByItsOtherNames(t *testing.T) {
	root := newRoot(t)
	if err := os.MkdirAll(filepath.Join(root.Dir(), ".git", "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	var paths []string
	if runtime.GOOS == "windows" {
		paths = append(paths, ".git./config", ".git /config", ".git::$INDEX_ALLOCATION/config", `.git.\hooks\pre-commit`)
		// Short names can be turned off for a volume, and then GIT~1 is only
		// a name.
		if real, err := realPath(filepath.Join(root.Dir(), "GIT~1")); err == nil && strings.EqualFold(filepath.Base(real), ".git") {
			paths = append(paths, "GIT~1/config")
		} else {
			t.Log("no short names on this volume")
		}
	}
	link := filepath.Join(root.Dir(), "meta")
	if runtime.GOOS == "windows" {
		if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, filepath.Join(root.Dir(), ".git")).CombinedOutput(); err == nil {
			paths = append(paths, "meta/config")
		} else {
			t.Logf("cannot make a junction here: %v %s", err, out)
		}
	} else if err := os.Symlink(".git", link); err == nil {
		paths = append(paths, "meta/config")
	} else {
		t.Logf("cannot make a link here: %v", err)
	}
	w, e := &writeFile{root: root}, &editFile{root: root}
	for _, path := range paths {
		args := rawArgs(t, map[string]any{"path": path})
		if got := w.Prefix(args); got != "" {
			t.Errorf("write %s: Prefix = %q, want none", path, got)
		}
		if got := e.Prefix(args); got != "" {
			t.Errorf("edit %s: Prefix = %q, want none", path, got)
		}
	}
	if got := w.Prefix(rawArgs(t, map[string]any{"path": "src/main.go"})); got != editFamily {
		t.Errorf("an ordinary file: Prefix = %q, want %q", got, editFamily)
	}
}
