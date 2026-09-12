package chat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A transcript is mostly code and is read by people as well as programs, so
// the code in it is written as it is rather than with every < and & escaped.
func TestATranscriptKeepsCodeReadable(t *testing.T) {
	dir := t.TempDir()
	log, err := OpenLog(dir, "s", "/work")
	if err != nil {
		t.Fatal(err)
	}
	log.Append(Entry{Type: "assistant", Text: "if a < b && c > d {}"})
	log.Close()

	data, err := os.ReadFile(filepath.Join(dir, "s.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "if a < b && c > d {}") {
		t.Errorf("the code was escaped: %s", data)
	}
	entries, _ := ReadEntries(filepath.Join(dir, "s.jsonl"))
	if len(entries) != 1 || entries[0].Text != "if a < b && c > d {}" {
		t.Errorf("read back as %+v", entries)
	}
}
