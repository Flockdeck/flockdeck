package chat

import (
	"path/filepath"
	"strings"
	"testing"
)

// One paste of many megabytes used to end the read of the whole transcript,
// leaving a conversation resume could not put back. The entry is left out and
// the rest reads as before.
func TestAnEnormousEntryDoesNotCostTheRestOfTheTranscript(t *testing.T) {
	dir := t.TempDir()
	log, err := OpenLog(dir, "s", "/work")
	if err != nil {
		t.Fatal(err)
	}
	log.Append(Entry{Type: "user", Text: "before"})
	log.Append(Entry{Type: "user", Text: strings.Repeat("x", maxEntry+1)})
	log.Append(Entry{Type: "assistant", Text: "after"})
	log.Close()

	entries, err := ReadEntries(filepath.Join(dir, "s.jsonl"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(entries) != 2 || entries[0].Text != "before" || entries[1].Text != "after" {
		t.Errorf("entries = %d, want the two either side of the enormous one", len(entries))
	}
}
