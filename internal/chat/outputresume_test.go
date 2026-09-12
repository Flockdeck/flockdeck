package chat

import (
	"strings"
	"testing"
)

// A resumed conversation's tool output is there to be read back with /output,
// as /history has just listed it: the pane was restarted, not the work.
func TestOutputReadsBackToolsFromBeforeAResume(t *testing.T) {
	dir := t.TempDir()
	log, err := OpenLog(dir, "session-1", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range []Entry{
		{Type: "user", Text: "read the notes"},
		{Type: "tool", Tool: "read_file", Call: "read_file notes.txt", Text: "line one\nline two\n"},
		{Type: "assistant", Text: "done", Model: "claude-opus-5"},
	} {
		if err := log.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	log.Close()

	out := run(t, Options{Agent: "anthropic", Resume: true, Dir: dir}, "/output\n/exit\n", &scriptedWire{})
	if !strings.Contains(out, "· read_file notes.txt") || !strings.Contains(out, "line two") {
		t.Errorf("/output did not show the tool's output from before the resume:\n%s", out)
	}
}
