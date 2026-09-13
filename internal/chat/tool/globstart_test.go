package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A pattern that begins with a directory is looked for from that directory,
// and finds exactly what a walk of the whole tree would have: every match
// under it, and nothing through a name in another case or a version control
// store, which a walk of the tree would not have matched either.
func TestAPatternBeginningWithADirectoryFindsWhatTheWholeTreeWould(t *testing.T) {
	dir := t.TempDir()
	for _, rel := range []string{"src/a.go", "src/deep/er/b.go", "src/c.txt", "other/d.go", ".git/e.go"} {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("needle\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	set, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	glob, _ := set.Lookup("glob")
	grep, _ := set.Lookup("grep")
	run := func(tl Tool, args string) string {
		out, err := tl.Run(context.Background(), json.RawMessage(args))
		if err != nil {
			t.Fatal(err)
		}
		return out
	}

	if got, want := run(glob, `{"pattern": "src/**/*.go"}`), "src/a.go\nsrc/deep/er/b.go\n"; got != want {
		t.Errorf("glob src/**/*.go = %q, want %q", got, want)
	}
	if got, want := run(glob, `{"pattern": "./src/deep/**"}`), "src/deep/er/b.go\n"; got != want {
		t.Errorf("glob ./src/deep/** = %q, want %q", got, want)
	}
	for _, pattern := range []string{"Src/**/*.go", ".git/**", "missing/**"} {
		if got := run(glob, `{"pattern": "`+pattern+`"}`); !strings.HasPrefix(got, "No files match") {
			t.Errorf("glob %s = %q, want no match", pattern, got)
		}
	}
	got := run(grep, `{"pattern": "needle", "glob": "src/**/*.go"}`)
	if !strings.Contains(got, "src/a.go:1: needle") || !strings.Contains(got, "src/deep/er/b.go:1: needle") ||
		!strings.Contains(got, "2 matches in 2 files.") {
		t.Errorf("grep with glob src/**/*.go = %q, want the two Go files under src", got)
	}
}
