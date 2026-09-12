package tool

import (
	"strings"
	"testing"
)

// A write to a path that is a directory will be refused, and asking the user
// first -- "Create src?" -- puts a question whose answer changes nothing.
func TestAWriteToADirectoryIsRefusedWithoutAsking(t *testing.T) {
	root := newRoot(t)
	write(t, root, "src/a.go", "package src\n")
	tl := &writeFile{root: root}
	args := map[string]any{"path": "src", "content": "x"}

	if q := tl.Approval(rawArgs(t, args)); q != "" {
		t.Errorf("a write that will be refused was put to the user: %q", q)
	}
	if _, err := call(t, tl, args); err == nil || !strings.Contains(err.Error(), "is a directory") {
		t.Errorf("error = %v, want the write refused", err)
	}
}
