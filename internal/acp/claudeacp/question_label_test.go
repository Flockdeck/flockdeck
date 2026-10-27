package claudeacp

import (
	"strings"
	"testing"
	"time"
)

func askWithLabels(labels ...string) string {
	var os []string
	for _, l := range labels {
		os = append(os, `{"label":"`+l+`","description":""}`)
	}
	return `{"questions":[{"question":"Which?","header":"h","multiSelect":false,"options":[` + strings.Join(os, ",") + `]}]}`
}

// What claude is told the person chose is the label as the card showed it.
func TestAnswerGivesBackTheLabelAsShown(t *testing.T) {
	// zero-width space, a bidi isolate, an escape sequence and a line break
	// inside the label of the first option.
	in := askWithLabels(`Post​gres⁦`, `SQ\u001b[31mLite`, `two\nlines`)
	for i, want := range []string{"Postgres", "SQLite", "two"} {
		log, reqs := runAsk(t, in, answered("answer", `[{"question":0,"selected":[`+string(rune('0'+i))+`]}]`))
		if len(reqs) != 1 {
			t.Fatalf("%d requests", len(reqs))
		}
		if !logHas(log, `"answers":{"Which?":"`+want+`"}`) {
			t.Errorf("option %d: claude was not told %q: %v", i, want, log)
		}
	}
}

func TestLabelsThatCleanToNothingOrToTheSameAreDenied(t *testing.T) {
	for name, in := range map[string]string{
		"nothing":        askWithLabels(`​⁦`, `ok`),
		"the same":       askWithLabels(`Red`, `R​ed`),
		"separator":      askWithLabels(`a​, b`, `c`),
		"only an escape": askWithLabels(`\u001b[31m`, `ok`),
	} {
		log, reqs := runAsk(t, in, answered("answer", `[{"question":0,"selected":[0]}]`))
		if len(reqs) != 0 || logHas(log, `"behavior":"allow"`) || !logHas(log, `"behavior":"deny"`) {
			t.Errorf("%s: %d requests, log %v", name, len(reqs), log)
		}
	}
}

// A turn cancelled with a question open retracts it: claude takes the retraction
// as the answer and nothing is written for the request.
func TestCancelWithAQuestionOpen(t *testing.T) {
	h := newHarness(t, []step{expect(`"role":"user"`), emit(askRequest(askInput)),
		expect(`"subtype":"interrupt"`), emit(`{"type":"control_cancel_request","request_id":"ask-1"}`), emit(lResultAborted)})
	release := make(chan struct{})
	h.onReq = func(rpcMsg) any { <-release; return answered("answer", `[{"question":0,"selected":[0]},{"question":1,"selected":[0]}]`) }
	defer close(release)
	sid := h.start()
	id := h.promptText(sid, "ask")
	h.waitFor(func(m rpcMsg) bool { return m.Method == "session/request_permission" }, 5*time.Second)
	h.notify("session/cancel", map[string]any{"sessionId": sid})
	if got := stopReason(t, h.await(id)); got != "cancelled" {
		t.Errorf("stop reason = %q", got)
	}
	if log := readLog(h.log); logHas(log, `"request_id":"ask-1","response"`) || logHas(log, `"behavior"`) {
		t.Errorf("a retracted question was answered: %v", log)
	}
}
