package help

import (
	"flag"
	"os"
	"strings"
	"testing"
)

// docs/keys.md carries the one copy of the key table that is not rendered at
// run time, so it is the one that can drift. This test compares it against the
// table and, with -update, rewrites it.
//
//	go test ./internal/help -run TestKeysDocShortcuts -update
var update = flag.Bool("update", false, "rewrite docs/keys.md's shortcut table from the key table")

const (
	keysDocPath = "../../docs/keys.md"
	startMarker = "<!-- shortcuts:start -->"
	endMarker   = "<!-- shortcuts:end -->"
)

func TestKeysDocShortcuts(t *testing.T) {
	data, err := os.ReadFile(keysDocPath)
	if err != nil {
		t.Fatalf("read docs/keys.md: %v", err)
	}
	doc := string(data)

	start := strings.Index(doc, startMarker)
	end := strings.Index(doc, endMarker)
	if start < 0 || end < 0 || end < start {
		t.Fatalf("docs/keys.md is missing the %s / %s markers around its shortcut table",
			startMarker, endMarker)
	}

	got := doc[start+len(startMarker) : end]
	want := "\n\n" + ShortcutsMarkdown() + "\n"

	// git checks the file out with whichever line ending the platform uses,
	// so on a machine with core.autocrlf the file holds CRLF while
	// ShortcutsMarkdown writes LF. Compared literally that reports every row as
	// drifted, and a check that is always red is one nobody reads — including
	// on the day the key table really does change. The words are the drift; the
	// line endings are the checkout's business.
	if unixLines(got) == want {
		return
	}
	if *update {
		out := doc[:start+len(startMarker)] + matchLines(want, doc) + doc[end:]
		if err := os.WriteFile(keysDocPath, []byte(out), 0o644); err != nil {
			t.Fatalf("write docs/keys.md: %v", err)
		}
		t.Log("docs/keys.md shortcut table rewritten")
		return
	}
	t.Errorf("the shortcut table in docs/keys.md does not match the key table.\n"+
		"Run: go test ./internal/help -run TestKeysDocShortcuts -update\n\ngot:\n%s\nwant:\n%s", got, want)
}

// unixLines drops the carriage returns of a CRLF checkout.
func unixLines(s string) string { return strings.ReplaceAll(s, "\r\n", "\n") }

// matchLines rewrites a block with whichever line ending the file already uses,
// so -update does not leave one LF section in the middle of a CRLF file.
func matchLines(block, file string) string {
	if strings.Contains(file, "\r\n") {
		return strings.ReplaceAll(block, "\n", "\r\n")
	}
	return block
}
