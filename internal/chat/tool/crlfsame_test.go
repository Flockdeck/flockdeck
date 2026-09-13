package tool

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A file with Windows line endings, written back with the same text read_file
// showed, is written back unchanged -- its endings are kept -- and the question
// says so rather than naming a change that is not there.
func TestRewritingACRLFFileWithItsOwnTextIsNoChange(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\r\ntwo\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	set, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	w, _ := set.Lookup("write_file")
	args, _ := json.Marshal(map[string]string{"path": "a.txt", "content": "one\ntwo\n"})
	if q := w.Approval(args); !strings.Contains(q, "the same as what the file holds now") {
		t.Errorf("question %q, want it to say nothing changes", q)
	}
}

// A rewrite that changes only line endings is still a change, and the question
// says where, in whichever direction the endings go: a file with bare newlines
// is written with the carriage returns it is given, and a file with Windows
// endings on only some lines is written with them on all.
func TestRewritingOnlyTheLineEndingsIsAChange(t *testing.T) {
	tests := []struct {
		name, file, content string
		line                int
	}{
		{"bare newlines given Windows endings", "one\ntwo\n", "one\r\ntwo\r\n", 1},
		{"mixed endings given bare newlines", "one\r\ntwo\n", "one\ntwo\n", 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte(tt.file), 0o644); err != nil {
				t.Fatal(err)
			}
			set, err := New(dir)
			if err != nil {
				t.Fatal(err)
			}
			w, _ := set.Lookup("write_file")
			args, _ := json.Marshal(map[string]string{"path": "a.txt", "content": tt.content})
			q := w.Approval(args)
			if want := fmt.Sprintf("the first change is at line %d", tt.line); !strings.Contains(q, want) {
				t.Errorf("question %q, want it to say %q", q, want)
			}
		})
	}
}
