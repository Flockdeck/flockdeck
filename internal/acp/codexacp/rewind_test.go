package codexacp

import (
	"encoding/json"
	"strconv"
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
	for turn, want := range map[string]string{tok3: "k2", tok2: "k1"} {
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

// A prompt sent without a token is a turn codex has: the counts of the turns
// that follow it include it, and it cannot be named itself.
func TestATurnSentWithoutATokenIsCounted(t *testing.T) {
	h := newHarness(t, threeTurns())
	sid := h.start()
	for _, tok := range []string{tok1, "", tok3} {
		if got := stopReason(t, h.await(h.promptTurn(sid, "go", tok))); got != "end_turn" {
			t.Fatalf("stop = %q", got)
		}
	}
	// Before the third prompt codex keeps two turns, not one.
	r := h.call("_flockdeck/forkpoint", map[string]any{"sessionId": sid, "beforeTurn": tok3})
	if r.Error != nil || !strings.Contains(string(r.Result), `"at":"k2"`) {
		t.Errorf("fork point = %s %+v, want k2", r.Result, r.Error)
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

func TestForkIsAThreadForkThenARollbackOfWhatIsBeyondThePoint(t *testing.T) {
	// The new thread is counted when it is made: it has 4 turns, 2 are kept.
	h := newHarness(t, nil, hopts{extraEnv: []string{envTurns + "=4"}})
	h.call("initialize", map[string]any{"protocolVersion": 1})
	r := h.call("session/fork", map[string]any{"sessionId": "thr-src", "cwd": h.cwd, "mcpServers": []any{},
		"_meta": map[string]any{"flockdeck.dev": map[string]any{"at": "k2"}}})
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

// A turn that ran on the source after the point was chosen is one more turn on
// the new thread, and goes too: the cut is at the same place.
func TestATurnRunAfterTheForkPointWasChosenDoesNotMoveTheCut(t *testing.T) {
	for turns, want := range map[int]string{3: `"numTurns":1`, 4: `"numTurns":2`, 5: `"numTurns":3`} {
		h := newHarness(t, nil, hopts{extraEnv: []string{envTurns + "=" + strconv.Itoa(turns)}})
		h.call("initialize", map[string]any{"protocolVersion": 1})
		r := h.call("session/fork", map[string]any{"sessionId": "thr-src", "cwd": h.cwd, "mcpServers": []any{},
			"_meta": map[string]any{"flockdeck.dev": map[string]any{"at": "k2"}}})
		if r.Error != nil {
			t.Fatalf("fork with %d turns = %+v", turns, r.Error)
		}
		if rb := logFind(readLog(h.log), `"method":"thread/rollback"`); !strings.Contains(rb, want) {
			t.Errorf("%d turns: rollback request = %s, want %s", turns, rb, want)
		}
	}
}

// If the turns of the new thread cannot be counted, or there are fewer than the
// point keeps, the fork is refused rather than guessed.
func TestAForkWhoseTurnsCannotBeCountedIsRefused(t *testing.T) {
	for name, env := range map[string][]string{"no count": nil, "too few": {envTurns + "=1"}} {
		h := newHarness(t, nil, hopts{extraEnv: env})
		h.call("initialize", map[string]any{"protocolVersion": 1})
		r := h.call("session/fork", map[string]any{"sessionId": "thr-src", "cwd": h.cwd, "mcpServers": []any{},
			"_meta": map[string]any{"flockdeck.dev": map[string]any{"at": "k2"}}})
		if r.Error == nil {
			t.Errorf("%s: the fork was made: %s", name, r.Result)
		}
		if logHas(readLog(h.log), `"method":"thread/rollback"`) {
			t.Errorf("%s: a rollback was asked for", name)
		}
	}
}

// A fork point that is present but is not one of ours is an error: the whole
// thread is not forked in its place.
func TestAForkPointThatIsNotOursIsAnErrorNotAWholeFork(t *testing.T) {
	for _, at := range []any{"rb2", "x", "k-1", "k99999", 7, map[string]any{"a": 1}, ""} {
		h := newHarness(t, nil)
		h.call("initialize", map[string]any{"protocolVersion": 1})
		r := h.call("session/fork", map[string]any{"sessionId": "thr-src", "cwd": h.cwd, "mcpServers": []any{},
			"_meta": map[string]any{"flockdeck.dev": map[string]any{"at": at}}})
		if r.Error == nil || r.Error.Code != codeInvalidParams {
			t.Errorf("fork at %v = %s %+v", at, r.Result, r.Error)
		}
		if logHas(readLog(h.log), `"method":"thread/fork"`) {
			t.Errorf("fork at %v: codex was asked to fork", at)
		}
	}
}

func TestForkOfTheWholeThreadRollsNothingBack(t *testing.T) {
	for _, meta := range []map[string]any{{}, {"at": nil}, {"turn": "x"}} {
		h := newHarness(t, nil)
		h.call("initialize", map[string]any{"protocolVersion": 1})
		r := h.call("session/fork", map[string]any{"sessionId": "thr-src", "cwd": h.cwd, "mcpServers": []any{},
			"_meta": map[string]any{"flockdeck.dev": meta}})
		if r.Error != nil {
			t.Fatalf("fork with %v = %+v", meta, r.Error)
		}
		if logHas(readLog(h.log), `"method":"thread/rollback"`) {
			t.Errorf("fork with %v: a rollback was asked for", meta)
		}
	}
}

// A session that was resumed or forked has turns the adapter did not run, so a
// count from the start of the thread cannot be given for it.
func TestAResumedSessionGivesNoForkPoint(t *testing.T) {
	h := newHarness(t, threeTurns())
	h.call("initialize", map[string]any{"protocolVersion": 1})
	r := h.call("session/resume", map[string]any{"sessionId": "thr-1", "cwd": h.cwd, "mcpServers": []any{}})
	if r.Error != nil {
		t.Fatalf("resume = %+v", r.Error)
	}
	sid := "thr-1"
	stopReason(t, h.await(h.promptTurn(sid, "go", tok1)))
	stopReason(t, h.await(h.promptTurn(sid, "go", tok2)))
	if r := h.call("_flockdeck/forkpoint", map[string]any{"sessionId": sid, "beforeTurn": tok2}); r.Error == nil {
		t.Errorf("a fork point was given for a resumed session: %s", r.Result)
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
