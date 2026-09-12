package tool

import (
	"strings"
	"testing"
)

// write_file makes the directories a new file needs, and agreeing to the file
// is agreeing to them, so the question says which.
func TestWriteFileSaysWhichDirectoriesItWillMake(t *testing.T) {
	root := newRoot(t)
	write(t, root, "src/existing.go", "package src\n")
	tl := &writeFile{root: root}

	q := tl.Approval(rawArgs(t, map[string]any{"path": "src/new/pkg/a.go", "content": "package pkg\n"}))
	if !strings.Contains(q, "making the directory src/new/") {
		t.Errorf("the question does not say which directory it makes:\n%s", q)
	}
	q = tl.Approval(rawArgs(t, map[string]any{"path": "src/b.go", "content": "package src\n"}))
	if strings.Contains(q, "making the directory") {
		t.Errorf("a file in a directory that exists claims to make one:\n%s", q)
	}
}
