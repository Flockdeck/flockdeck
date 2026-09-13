package tool

import (
	"encoding/json"
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
