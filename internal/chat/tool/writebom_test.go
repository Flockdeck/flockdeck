package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A file that starts with a byte-order mark keeps it when it is written out
// again, as it keeps its line endings, and the question does not call the
// mark a change.
func TestARewriteKeepsTheFilesByteOrderMark(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "build.ps1")
	if err := os.WriteFile(path, []byte(utf8BOM+"Write-Host 'héllo'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	set, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	w, _ := set.Lookup("write_file")

	same, _ := json.Marshal(map[string]string{"path": "build.ps1", "content": "Write-Host 'héllo'\n"})
	if q := w.Approval(same); !strings.Contains(q, "the same as what the file holds now") {
		t.Errorf("question %q, want it to say nothing changes", q)
	}

	args, _ := json.Marshal(map[string]string{"path": "build.ps1", "content": "Write-Host 'hé'\n"})
	if _, err := w.Run(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if want := utf8BOM + "Write-Host 'hé'\n"; string(got) != want {
		t.Errorf("wrote %q, want %q", got, want)
	}
}
