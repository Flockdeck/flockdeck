package chat

import (
	"strings"
	"testing"
)

// /history numbers each tool's output the way /output counts, and the number
// shown opens that output.
func TestHistoryNumbersToolOutputsTheWayOutputCounts(t *testing.T) {
	dir := t.TempDir()
	writeTranscript(t, dir,
		Entry{Type: "user", Text: "look around"},
		Entry{Type: "tool", Tool: "run_command", Call: "run_command go test ./...", Text: "=== RUN TestA\n--- FAIL: TestA\n[exit status 1]\n"},
		Entry{Type: "tool", Tool: "read_file", Call: "read_file a.go", Text: "1\tone line\n"},
		Entry{Type: "tool", Tool: "list_dir", Call: "list_dir", Text: "the working directory: 1 directory, 1 file\nsrc/\n"},
		Entry{Type: "assistant", Text: "found it", Model: "claude-opus-5"},
	)
	out := run(t, Options{Agent: "anthropic", Resume: true, Dir: dir}, "/history\n/output 3\n/exit\n", &scriptedWire{})
	said := strings.Join(strings.Fields(out), " ")

	for _, want := range []string{"(3 lines; /output 3)", "(2 lines; /output 1)"} {
		if !strings.Contains(said, want) {
			t.Errorf("the history lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(said, "/output 2)") {
		t.Errorf("a one-line output was given a number to open it by:\n%s", out)
	}
	// The number shown is the one /output answers to.
	if !strings.Contains(out, "· run_command go test ./...\n=== RUN TestA") {
		t.Errorf("/output 3 did not open the output numbered 3:\n%s", out)
	}
}
