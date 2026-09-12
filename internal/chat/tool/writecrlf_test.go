package tool

import (
	"os"
	"testing"
)

// A file with Windows line endings, rewritten from what read_file showed, keeps
// its endings: changed to bare newlines, every line of it would show as changed.
func TestARewriteKeepsTheFilesLineEndings(t *testing.T) {
	root := newRoot(t)
	path := write(t, root, "a.txt", "one\r\ntwo\r\n")
	tl := &writeFile{root: root}
	if _, err := call(t, tl, map[string]any{"path": "a.txt", "content": "one\nTWO\nthree\n"}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "one\r\nTWO\r\nthree\r\n" {
		t.Errorf("file = %q, want the new text in the file's own line endings", got)
	}

	// A file that had bare newlines keeps them.
	path = write(t, root, "b.txt", "one\ntwo\n")
	if _, err := call(t, tl, map[string]any{"path": "b.txt", "content": "uno\ndos\n"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != "uno\ndos\n" {
		t.Errorf("file = %q, want bare newlines kept", got)
	}
}
