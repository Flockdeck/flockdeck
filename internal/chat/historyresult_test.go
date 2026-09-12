package chat

import (
	"strings"
	"testing"
)

// The history draws a command by its result, the line the pane drew it by as
// it ran, so that what failed can be seen in it.
func TestHistoryDrawsACommandByItsResult(t *testing.T) {
	dir := t.TempDir()
	log, err := OpenLog(dir, "session-1", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range []Entry{
		{Type: "user", Text: "run the tests"},
		{Type: "tool", Tool: "run_command", Call: "run_command go test ./...",
			Text: "=== RUN   TestThing\n--- FAIL: TestThing\nFAIL\n[exit status 1]\n"},
		{Type: "assistant", Text: "one failed", Model: "claude-opus-5"},
	} {
		if err := log.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	log.Close()

	out := run(t, Options{Agent: "anthropic", Resume: true, Dir: dir}, "/history\n/exit\n", &scriptedWire{})
	if strings.Contains(out, "=== RUN") || !strings.Contains(out, "[exit status 1] (4 lines)") {
		t.Errorf("the command was not drawn by its result:\n%s", out)
	}
}
