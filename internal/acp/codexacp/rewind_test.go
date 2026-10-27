package codexacp

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

const (
	tok1 = "11111111-1111-4111-8111-111111111111"
	tok2 = "22222222-2222-4222-8222-222222222222"
	tok3 = "33333333-3333-4333-8333-333333333333"
)

func (h *harness) promptTurn(sid, text, turn string) int {
	params := map[string]any{"sessionId": sid, "prompt": []any{map[string]any{"type": "text", "text": text}}}
	if turn != "" {
		params["_meta"] = map[string]any{"flockdeck.dev": map[string]any{"turn": turn}}
	}
	return h.send("session/prompt", params)
}

// threeTurns is a codex that finishes three turns.
func threeTurns() []step {
	var steps []step
	for i := 0; i < 3; i++ {
		steps = append(steps, expect(`"method":"turn/start"`), startedTurn(), endTurn("completed"))
	}
	return steps
}

func runThree(t *testing.T, h *harness) string {
	t.Helper()
	sid := h.start()
	for _, tok := range []string{tok1, tok2, tok3} {
		if got := stopReason(t, h.await(h.promptTurn(sid, "go", tok))); got != "end_turn" {
			t.Fatalf("stop = %q", got)
		}
	}
	return sid
}

func TestInitializeAdvertisesTheExtension(t *testing.T) {
	h := newHarness(t, nil)
	r := h.call("initialize", map[string]any{"protocolVersion": 1})
	var init struct {
		C struct {
			Meta map[string]map[string]any  `json:"_meta"`
			SC   map[string]json.RawMessage `json:"sessionCapabilities"`
		} `json:"agentCapabilities"`
	}
	_ = json.Unmarshal(r.Result, &init)
	if init.C.Meta["flockdeck.dev"]["rewind"] != float64(1) {
		t.Errorf("no rewind in %s", r.Result)
	}
	if _, ok := init.C.SC["fork"]; !ok {
		t.Errorf("no fork in %s", r.Result)
	}
}

func TestForkPointCountsTheTurnsFromThePointToTheEnd(t *testing.T) {
	h := newHarness(t, threeTurns())
	sid := runThree(t, h)
	for turn, want := range map[string]string{tok3: "rb1", tok2: "rb2"} {
		r := h.call("_flockdeck/forkpoint", map[string]any{"sessionId": sid, "beforeTurn": turn})
		if r.Error != nil || !strings.Contains(string(r.Result), `"at":"`+want+`"`) {
			t.Errorf("fork point of %s = %s %+v, want %s", turn, r.Result, r.Error, want)
		}
	}
	for _, turn := range []string{tok1, "x", "", strings.Replace(tok2, "2", "A", 1)} {
		if r := h.call("_flockdeck/forkpoint", map[string]any{"sessionId": sid, "beforeTurn": turn}); r.Error == nil || r.Error.Code != codeInvalidParams {
			t.Errorf("fork point of %q = %+v", turn, r)
		}
	}
	if r := h.call("_flockdeck/forkpoint", map[string]any{"sessionId": "nope", "beforeTurn": tok2}); r.Error == nil {
		t.Error("a fork point of an unknown session was given")
	}
}

func TestRewindRollsTheThreadBack(t *testing.T) {
	h := newHarness(t, threeTurns())
	sid := runThree(t, h)
	if r := h.call("_flockdeck/rewind", map[string]any{"sessionId": sid, "beforeTurn": tok2}); r.Error != nil {
		t.Fatalf("rewind = %+v", r.Error)
	}
	rb := logFind(readLog(h.log), `"method":"thread/rollback"`)
	if !strings.Contains(rb, `"numTurns":2`) || !strings.Contains(rb, `"threadId":"thr-1"`) {
		t.Errorf("rollback request = %s", rb)
	}
	// The two turns are gone from the adapter's list; the first is still there.
	if r := h.call("_flockdeck/forkpoint", map[string]any{"sessionId": sid, "beforeTurn": tok2}); r.Error == nil {
		t.Errorf("a rewound turn is still known: %s", r.Result)
	}
	if r := h.call("_flockdeck/rewind", map[string]any{"sessionId": sid, "beforeTurn": tok1}); r.Error == nil {
		t.Errorf("the first turn has an earlier point: %s", r.Result)
	}
}

func TestRewindIsRefusedWhileATurnRuns(t *testing.T) {
	h := newHarness(t, []step{
		expect(`"method":"turn/start"`), startedTurn(), endTurn("completed"),
		expect(`"method":"turn/start"`), startedTurn(), // never ends
	})
	sid := h.start()
	stopReason(t, h.await(h.promptTurn(sid, "go", tok1)))
	h.promptTurn(sid, "go", tok2)
	deadline := time.Now().Add(10 * time.Second)
	for strings.Count(strings.Join(readLog(h.log), " "), `"method":"turn/start"`) < 2 {
		if time.Now().After(deadline) {
			t.Fatal("the second turn did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	r := h.call("_flockdeck/rewind", map[string]any{"sessionId": sid, "beforeTurn": tok2})
	if r.Error == nil || !strings.Contains(r.Error.Message, "not idle") {
		t.Errorf("a rewind during a turn = %+v", r)
	}
	if logHas(readLog(h.log), `"method":"thread/rollback"`) {
		t.Error("codex was asked to roll back during a turn")
	}
}

func TestRewindRefusesAnUnknownTurnWithoutCallingCodex(t *testing.T) {
	h := newHarness(t, threeTurns())
	sid := runThree(t, h)
	for _, p := range []map[string]any{
		{"sessionId": sid, "beforeTurn": ""},
		{"sessionId": sid, "beforeTurn": "x"},
		{"sessionId": sid, "beforeTurn": tok1},
		{"sessionId": sid, "beforeTurn": strings.Replace(tok2, "2", "9", -1)},
		{"sessionId": "nope", "beforeTurn": tok2},
	} {
		if r := h.call("_flockdeck/rewind", p); r.Error == nil || r.Error.Code != codeInvalidParams {
			t.Errorf("rewind %v = %+v", p, r)
		}
	}
	if logHas(readLog(h.log), `"method":"thread/rollback"`) {
		t.Error("codex was asked to roll back for a turn that is not known")
	}
}

func TestACommandAfterThePointMakesItUnusable(t *testing.T) {
	h := newHarness(t, []step{
		expect(`"method":"turn/start"`), startedTurn(), endTurn("completed"),
		expect(`"method":"turn/start"`), startedTurn(), endTurn("completed"),
		expect(`"method":"thread/compact/start"`), startedTurn(), endTurn("completed"),
	})
	sid := h.start()
	stopReason(t, h.await(h.promptTurn(sid, "go", tok1)))
	stopReason(t, h.await(h.promptTurn(sid, "go", tok2)))
	stopReason(t, h.await(h.promptTurn(sid, "/compact", tok3)))
	// tok2 is before the compact, so going back to before it would drop it too.
	if r := h.call("_flockdeck/forkpoint", map[string]any{"sessionId": sid, "beforeTurn": tok2}); r.Error == nil {
		t.Errorf("a point before a /compact was given: %s", r.Result)
	}
	if r := h.call("_flockdeck/forkpoint", map[string]any{"sessionId": sid, "beforeTurn": tok3}); r.Error == nil {
		t.Errorf("a point at a /compact was given: %s", r.Result)
	}
}

func TestForkIsAThreadForkThenARollback(t *testing.T) {
	h := newHarness(t, nil)
	h.call("initialize", map[string]any{"protocolVersion": 1})
	r := h.call("session/fork", map[string]any{"sessionId": "thr-src", "cwd": h.cwd, "mcpServers": []any{},
		"_meta": map[string]any{"flockdeck.dev": map[string]any{"at": "rb2"}}})
	if r.Error != nil || !strings.Contains(string(r.Result), `"sessionId":"thr-fork"`) {
		t.Fatalf("fork = %s %+v", r.Result, r.Error)
	}
	log := readLog(h.log)
	f := logFind(log, `"method":"thread/fork"`)
	if !strings.Contains(f, `"threadId":"thr-src"`) || logHas(log, `"method":"thread/start"`) {
		t.Errorf("fork request = %s", f)
	}
	rb := logFind(log, `"method":"thread/rollback"`)
	if !strings.Contains(rb, `"numTurns":2`) || !strings.Contains(rb, `"threadId":"thr-fork"`) {
		t.Errorf("rollback request = %s", rb)
	}
}

func TestForkOfTheWholeThreadRollsNothingBack(t *testing.T) {
	// A point that is not "rbN" is dropped, not passed to codex.
	for _, at := range []any{nil, "rb0", "x", "rb-1", "rb99999", map[string]any{"a": 1}} {
		h := newHarness(t, nil)
		h.call("initialize", map[string]any{"protocolVersion": 1})
		meta := map[string]any{}
		if at != nil {
			meta["at"] = at
		}
		r := h.call("session/fork", map[string]any{"sessionId": "thr-src", "cwd": h.cwd, "mcpServers": []any{},
			"_meta": map[string]any{"flockdeck.dev": meta}})
		if r.Error != nil {
			t.Fatalf("fork at %v = %+v", at, r.Error)
		}
		if logHas(readLog(h.log), `"method":"thread/rollback"`) {
			t.Errorf("fork at %v: a rollback was asked for: %v", at, logFind(readLog(h.log), `"method":"thread/rollback"`))
		}
	}
}

func TestForkRefusesASourceThatIsNotAThreadId(t *testing.T) {
	h := newHarness(t, nil)
	h.call("initialize", map[string]any{"protocolVersion": 1})
	for _, id := range []string{"", "../x", "a b", "a\nb", strings.Repeat("a", 200)} {
		if r := h.call("session/fork", map[string]any{"sessionId": id, "cwd": h.cwd, "mcpServers": []any{}}); r.Error == nil || r.Error.Code != codeInvalidParams {
			t.Errorf("fork of %q = %+v", id, r)
		}
	}
	if logHas(readLog(h.log), `"method":"thread/fork"`) {
		t.Error("codex was asked to fork a bad id")
	}
}
