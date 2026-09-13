package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A line is searched whole up to its ceiling however it arrives: longer than
// the reader holds at once, with Windows line endings, or last in a file with
// no newline after it.
func TestGrepSearchesALineLongerThanOneRead(t *testing.T) {
	dir := t.TempDir()
	long := strings.Repeat("x", 200<<10) + "needle" + strings.Repeat("y", 10)
	files := map[string]string{
		"long.txt": "first\n" + long + "\nlast needle\n",
		"crlf.txt": "one\r\ntwo needle\r\nthree\r\n",
		"end.txt":  "no newline after this needle",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	set, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	grep, _ := set.Lookup("grep")
	out, err := grep.Run(context.Background(), json.RawMessage(`{"pattern": "needle"}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"long.txt:2: xxx", "long.txt:3: last needle", "crlf.txt:2: two needle\n", "end.txt:1: no newline after this needle", "4 matches in 3 files."} {
		if !strings.Contains(out, want) {
			t.Errorf("grep found:\n%s\nwant %q in it", out, want)
		}
	}
	// And a line past the ceiling is searched as far as the ceiling.
	past := strings.Repeat("z", grepScanLine) + "needle"
	if err := os.WriteFile(filepath.Join(dir, "long.txt"), []byte(past+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _ = grep.Run(context.Background(), json.RawMessage(`{"pattern": "needle", "path": "long.txt"}`))
	if !strings.HasPrefix(out, "No matches") {
		t.Errorf("grep found %q past the ceiling on one line", out)
	}
}
