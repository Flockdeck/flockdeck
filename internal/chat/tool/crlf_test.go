package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// read_file shows a file with Windows line endings without its carriage
// returns, so every multi-line edit the model then asks for is written with
// bare newlines. The edit has to land all the same, and leave the file's own
// line endings as they were.
func TestAnEditLandsInAFileWithWindowsLineEndings(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "a.txt")
	if err := os.WriteFile(path, []byte("one\r\ntwo\r\nthree\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	set, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	edit, _ := set.Lookup("edit_file")
	args, _ := json.Marshal(map[string]any{"path": "a.txt", "old_string": "one\ntwo", "new_string": "uno\ndos"})
	if q := edit.Approval(args); q == "" {
		t.Error("the edit was not offered for approval, so it would be refused")
	}
	if _, err := edit.Run(context.Background(), args); err != nil {
		t.Fatalf("edit: %v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "uno\r\ndos\r\nthree\r\n" {
		t.Errorf("file = %q, want the edit made with the file's own line endings", got)
	}
}
