package chat

import (
	"fmt"
	"strings"
	"testing"
)

// A long conversation drawn whole scrolls away the part somebody wanted to
// see; /history with a number draws only the latest entries.
func TestHistoryTakesHowManyEntriesToDraw(t *testing.T) {
	dir := t.TempDir()
	log, err := OpenLog(dir, "session-1", "/work")
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 10; i++ {
		log.Append(Entry{Type: "user", Text: fmt.Sprintf("question %d", i)})
	}
	log.Close()

	out := run(t, Options{Agent: "anthropic", Dir: dir}, "/history 3\n/history x\n/exit\n", &scriptedWire{})
	if !strings.Contains(out, "question 10") || strings.Contains(out, "question 7\n") {
		t.Errorf("/history 3 did not draw just the last three:\n%s", out)
	}
	for _, want := range []string{"7 earlier entries", "/history takes a number"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}
