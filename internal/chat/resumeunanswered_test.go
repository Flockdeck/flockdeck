package chat

import (
	"strings"
	"testing"
)

// writeTranscript puts entries where a resumed chat will read them.
func writeTranscript(t *testing.T, dir string, entries ...Entry) {
	t.Helper()
	log, err := OpenLog(dir, "session-1", "")
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	for _, e := range entries {
		if err := log.Append(e); err != nil {
			t.Fatal(err)
		}
	}
}

// A pane closed while the model was answering comes back with a prompt that
// has no answer; it says so, and /retry asks it.
func TestAResumedPromptWithNoAnswerCanBeRetried(t *testing.T) {
	dir := t.TempDir()
	writeTranscript(t, dir, Entry{Type: "user", Text: "do the thing"})
	wire := &scriptedWire{turns: []turnFunc{says("done now")}}
	out := run(t, Options{Agent: "anthropic", Model: "claude-opus-5", Resume: true, Dir: dir}, "/retry\n/exit\n", wire)

	if !strings.Contains(strings.Join(strings.Fields(out), " "), "the last prompt had no answer when the pane closed; /retry asks it again") {
		t.Errorf("the unanswered prompt was not said:\n%s", out)
	}
	if !strings.Contains(out, "done now") {
		t.Errorf("/retry did not ask it:\n%s", out)
	}
	if reqs := wire.requests(); len(reqs) != 1 || len(reqs[0].Messages) != 1 || reqs[0].Messages[0].Text != "do the thing" {
		t.Errorf("/retry sent %+v", reqs)
	}

	// A conversation that was answered says nothing of the kind.
	answered := t.TempDir()
	writeTranscript(t, answered, Entry{Type: "user", Text: "hi"}, Entry{Type: "assistant", Text: "hello"})
	out = run(t, Options{Agent: "anthropic", Model: "claude-opus-5", Resume: true, Dir: answered}, "/exit\n", &scriptedWire{})
	if strings.Contains(out, "had no answer") {
		t.Errorf("an answered conversation was said to have none:\n%s", out)
	}
}
