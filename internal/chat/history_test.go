package chat

import (
	"fmt"
	"strings"
	"testing"
)

// A resumed pane draws its conversation back the way it looked: a tool's
// output as the one line it was drawn as, not the whole file it read printed
// under "you".
func TestResumeDrawsToolOutputAsItWasDrawn(t *testing.T) {
	dir := t.TempDir()
	log, err := OpenLog(dir, "session-1", "/work")
	if err != nil {
		t.Fatal(err)
	}
	log.Append(Entry{Type: "user", Text: "what is in main.go?"})
	log.Append(Entry{Type: "tool", Tool: "read_file", Text: "1\tpackage main\n2\t\n3\tfunc main() {}\n"})
	log.Append(Entry{Type: "assistant", Text: "an empty main"})
	log.Close()

	out := run(t, Options{Agent: "anthropic", Dir: dir, Resume: true}, "/exit\n", &scriptedWire{})
	if !strings.Contains(out, "· read_file  1") || !strings.Contains(out, "(3 lines;") {
		t.Errorf("the tool's output was not drawn as one line:\n%s", out)
	}
	if strings.Contains(out, "func main() {}") {
		t.Errorf("the whole of the tool's output was drawn:\n%s", out)
	}
}

// Only the tail of a long conversation is drawn on resume, and the rest is one
// command away.
func TestHistoryShowsWhatResumeLeftOut(t *testing.T) {
	dir := t.TempDir()
	log, err := OpenLog(dir, "session-1", "/work")
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 20; i++ {
		log.Append(Entry{Type: "user", Text: fmt.Sprintf("question %d", i)})
		log.Append(Entry{Type: "assistant", Text: fmt.Sprintf("answer %d", i)})
	}
	log.Close()

	out := run(t, Options{Agent: "anthropic", Dir: dir, Resume: true}, "/history\n/exit\n", &scriptedWire{})
	before, after, found := strings.Cut(out, "/history shows the whole conversation")
	if !found {
		t.Fatalf("resume did not say how to see the rest:\n%s", out)
	}
	if strings.Contains(before, "question 1\n") {
		t.Error("resume drew the whole conversation")
	}
	if !strings.Contains(after, "question 1\n") {
		t.Errorf("/history did not reach the start of the conversation:\n%s", after)
	}
}
