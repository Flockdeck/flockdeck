package help

import (
	"flag"
	"os"
	"strings"
	"testing"
)

// The README's shortcut table is the one copy of the key table that is not
// rendered at run time, so it is the one that can drift. This test compares it
// against the table and, with -update, rewrites it.
//
//	go test ./internal/help -run TestREADMEShortcuts -update
var update = flag.Bool("update", false, "rewrite the README's shortcut table from the key table")

const (
	readmePath  = "../../README.md"
	startMarker = "<!-- shortcuts:start -->"
	endMarker   = "<!-- shortcuts:end -->"
)

func TestREADMEShortcuts(t *testing.T) {
	data, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatalf("read README: %v", err)
	}
	readme := string(data)

	start := strings.Index(readme, startMarker)
	end := strings.Index(readme, endMarker)
	if start < 0 || end < 0 || end < start {
		t.Fatalf("README is missing the %s / %s markers around its shortcut table",
			startMarker, endMarker)
	}

	got := readme[start+len(startMarker) : end]
	want := "\n\n" + ShortcutsMarkdown() + "\n"

	if got == want {
		return
	}
	if *update {
		out := readme[:start+len(startMarker)] + want + readme[end:]
		if err := os.WriteFile(readmePath, []byte(out), 0o644); err != nil {
			t.Fatalf("write README: %v", err)
		}
		t.Log("README shortcut table rewritten")
		return
	}
	t.Errorf("the README's shortcut table does not match the key table.\n"+
		"Run: go test ./internal/help -run TestREADMEShortcuts -update\n\ngot:\n%s\nwant:\n%s", got, want)
}
