package chat

import (
	"strings"
	"testing"
)

// An agent with no model named has the user pick one with /model; a pane
// restarted without it had them pick again every time. A resumed conversation
// takes up the model it last answered with -- unless the pane names one.
func TestAResumedConversationKeepsTheModelItWasUsing(t *testing.T) {
	transcript := func(t *testing.T) string {
		dir := t.TempDir()
		log, err := OpenLog(dir, "session-1", "/work")
		if err != nil {
			t.Fatal(err)
		}
		log.Append(Entry{Type: "user", Text: "hello"})
		log.Append(Entry{Type: "assistant", Text: "hi", Model: "qwen2.5-coder:7b"})
		log.Close()
		return dir
	}

	wire := &scriptedWire{turns: []turnFunc{says("still here")}}
	out := run(t, Options{Agent: "openai-compatible", Dir: transcript(t), Resume: true, Task: "go on"}, "", wire)
	if reqs := wire.requests(); len(reqs) != 1 || reqs[0].Model != "qwen2.5-coder:7b" {
		t.Errorf("asked %+v, want the model the conversation was using", reqs)
	}
	if !strings.Contains(out, "as this conversation last did") || strings.Contains(out, "no model is named") {
		t.Errorf("the resume did not say which model it took up:\n%s", out)
	}

	wire = &scriptedWire{turns: []turnFunc{says("still here")}}
	run(t, Options{Agent: "openai-compatible", Model: "llama3.2", Dir: transcript(t), Resume: true, Task: "go on"}, "", wire)
	if reqs := wire.requests(); len(reqs) != 1 || reqs[0].Model != "llama3.2" {
		t.Errorf("asked %+v, want the model the pane names", reqs)
	}
}
