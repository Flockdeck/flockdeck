package tool

import (
	"encoding/json"
	"strings"
	"testing"
)

// A write and an edit offer the same standing permission, so that agreeing to
// every edit at one agrees to them at the other; and it is a phrase no command
// prefix can be, so that it cannot agree to a command by accident.
func TestWritesAndEditsShareOneStandingPermission(t *testing.T) {
	root := newRoot(t)
	w, e := &writeFile{root: root}, &editFile{root: root}
	if w.Prefix(json.RawMessage(`{}`)) != e.Prefix(json.RawMessage(`{}`)) {
		t.Error("a write and an edit offer different standing permissions")
	}
	if n := len(strings.Fields(editFamily)); n <= 2 {
		t.Errorf("the edit family has %d words, and a command prefix can have two", n)
	}
}
