package tool

import (
	"encoding/json"
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
