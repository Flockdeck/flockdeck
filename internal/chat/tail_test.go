package chat

import (
	"os"
	"path/filepath"
	"testing"
)

// Reading the tail of a transcript starts part-way through the file, and the
// first line read is dropped as a fragment -- but only a line that really was
// cut is a fragment. One that starts exactly where the reading does is whole.
func TestATailThatStartsOnALineKeepsIt(t *testing.T) {
	dir := t.TempDir()
	log, err := OpenLog(dir, "s", "/work")
	if err != nil {
		t.Fatal(err)
	}
	log.Append(Entry{Type: "user", Text: "first"})
	log.Append(Entry{Type: "assistant", Text: "second"})
	log.Append(Entry{Type: "user", Text: "third"})
	log.Close()
	path := filepath.Join(dir, "s.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	firstLine := 0
	for data[firstLine] != '\n' {
		firstLine++
	}
	// Exactly the second and third lines.
	entries, err := readTail(path, int64(len(data)-firstLine-1))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Text != "second" {
		t.Errorf("read %+v, want the two whole lines the tail began with", entries)
	}
	// One byte less, and the second line is cut and dropped.
	entries, _ = readTail(path, int64(len(data)-firstLine-2))
	if len(entries) != 1 || entries[0].Text != "third" {
		t.Errorf("read %+v, want only the line after the cut", entries)
	}
}
