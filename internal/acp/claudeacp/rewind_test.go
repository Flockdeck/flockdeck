package claudeacp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/google/uuid"
)

const (
	tok1 = "11111111-1111-4111-8111-111111111111"
	tok2 = "22222222-2222-4222-8222-222222222222"
	tok3 = "33333333-3333-4333-8333-333333333333"
	msg1 = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	msg2 = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
)

func rewindAssistantLine(uuid string) string {
	return `{"type":"assistant","uuid":"` + uuid + `","message":{"id":"msg_` + uuid[:4] + `","role":"assistant","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":1,"output_tokens":1}},"parent_tool_use_id":null}`
}

// aborted is a turn that ends without end_turn.
func abortedWith(uuid string) []step {
	return []step{emit(rewindAssistantLine(uuid)), emit(lResultAborted)}
}

func (h *harness) promptTurn(sid, text, turn string) int {
	params := map[string]any{"sessionId": sid, "prompt": []any{map[string]any{"type": "text", "text": text}}}
	if turn != "" {
		params["_meta"] = map[string]any{"flockdeck.dev": map[string]any{"turn": turn}}
	}
	return h.send("session/prompt", params)
}

// twoTurns is a claude that finishes two turns, the second in answer to a prompt
// that carries the token tok2. A claude started again by a rewind plays it from
// the start, so it answers the same two prompts.
func twoTurns() []step { return twoTurnsInit(lInit) }

func twoTurnsInit(init string) []step {
	return []step{
		expect(`"uuid":"` + tok1 + `"`), emit(init), emit(rewindAssistantLine(msg1)), emit(lResultOK),
		expect(`"uuid":"` + tok2 + `"`), emit(rewindAssistantLine(msg2)), emit(lResultOK),
	}
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

func TestTurnTokenIsClaudesMessageUUIDAndOnlyThen(t *testing.T) {
	h := newHarness(t, twoTurns())
	sid := h.start()
	if got := stopReason(t, h.await(h.promptTurn(sid, "one", tok1))); got != "end_turn" {
		t.Fatalf("stop = %q", got)
	}
	var user string
	for _, l := range readLog(h.log) {
		if strings.HasPrefix(l, "IN: ") && strings.Contains(l, `"type":"user"`) {
			user = l
		}
	}
	if !strings.Contains(user, `"uuid":"`+tok1+`"`) {
		t.Errorf("the user message carried no uuid: %s", user)
	}
}

func TestPromptWithoutAValidTokenIsNotNamed(t *testing.T) {
	for _, bad := range []string{"", "not-a-uuid", "AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA", tok1 + "0", "1;rm -rf"} {
		h := newHarness(t, []step{expect(`"type":"user"`), emit(lInit), emit(lResultOK)})
		sid := h.start()
		if got := stopReason(t, h.await(h.promptTurn(sid, "one", bad))); got != "end_turn" {
			t.Fatalf("stop = %q", got)
		}
		for _, l := range readLog(h.log) {
			if strings.Contains(l, `"type":"user"`) && strings.Contains(l, `"uuid"`) {
				t.Errorf("token %q reached claude: %s", bad, l)
			}
		}
		if r := h.call("_flockdeck/forkpoint", map[string]any{"sessionId": sid, "beforeTurn": bad}); r.Error == nil {
			t.Errorf("a fork point for %q was given", bad)
		}
	}
}

func TestForkPointIsTheEndOfTheTurnBefore(t *testing.T) {
	h := newHarness(t, twoTurns())
	sid := h.start()
	stopReason(t, h.await(h.promptTurn(sid, "one", tok1)))
	stopReason(t, h.await(h.promptTurn(sid, "two", tok2)))

	r := h.call("_flockdeck/forkpoint", map[string]any{"sessionId": sid, "beforeTurn": tok2})
	if r.Error != nil || !strings.Contains(string(r.Result), msg1) {
		t.Fatalf("fork point of the second turn = %s %+v", r.Result, r.Error)
	}
	if strings.Contains(string(r.Result), msg2) {
		t.Errorf("the fork point includes the turn it should drop: %s", r.Result)
	}
	// Nothing comes before the first turn, and an unknown turn is refused.
	for _, turn := range []string{tok1, tok3} {
		if r := h.call("_flockdeck/forkpoint", map[string]any{"sessionId": sid, "beforeTurn": turn}); r.Error == nil || r.Error.Code != codeInvalidParams {
			t.Errorf("fork point of %s = %+v", turn, r)
		}
	}
	if r := h.call("_flockdeck/forkpoint", map[string]any{"sessionId": "nope", "beforeTurn": tok2}); r.Error == nil {
		t.Error("a fork point of an unknown session was given")
	}
}

func TestTurnThatDidNotEndIsNotAPoint(t *testing.T) {
	h := newHarness(t, []step{
		expect(`"uuid":"` + tok1 + `"`), emit(lInit), emit(rewindAssistantLine(msg1)), emit(lResultAborted),
		expect(`"uuid":"` + tok2 + `"`), emit(rewindAssistantLine(msg2)), emit(lResultOK),
	})
	sid := h.start()
	if r := h.await(h.promptTurn(sid, "one", tok1)); r.Error == nil {
		t.Fatalf("the aborted turn ended well: %s", r.Result)
	}
	stopReason(t, h.await(h.promptTurn(sid, "two", tok2)))
	if r := h.call("_flockdeck/forkpoint", map[string]any{"sessionId": sid, "beforeTurn": tok2}); r.Error == nil {
		t.Errorf("a point after a cancelled turn was given: %s", r.Result)
	}
	if r := h.call("_flockdeck/rewind", map[string]any{"sessionId": sid, "beforeTurn": tok2}); r.Error == nil {
		t.Errorf("a rewind to after a cancelled turn was done: %s", r.Result)
	}
}

func TestForkStartsClaudeWithTheForkFlags(t *testing.T) {
	h := newHarness(t, twoTurns())
	sid := h.start()
	stopReason(t, h.await(h.promptTurn(sid, "one", tok1)))
	stopReason(t, h.await(h.promptTurn(sid, "two", tok2)))

	// In another adapter the source is not open: a fork needs only its id and the
	// point. Here the same adapter is used, which allows it too.
	r := h.call("session/fork", map[string]any{"sessionId": sid, "cwd": h.cwd, "mcpServers": []any{},
		"_meta": map[string]any{"flockdeck.dev": map[string]any{"at": msg1}}})
	if r.Error != nil {
		t.Fatalf("fork = %+v", r.Error)
	}
	var res struct {
		SessionID string `json:"sessionId"`
	}
	_ = json.Unmarshal(r.Result, &res)
	if res.SessionID == "" || res.SessionID == sid {
		t.Fatalf("fork answered %s", r.Result)
	}
	var args string
	for _, l := range readLog(h.log) {
		if strings.Contains(l, "--fork-session") {
			args = l
		}
	}
	want := "--resume " + sid + " --resume-session-at " + msg1 + " --fork-session --session-id " + res.SessionID
	if !strings.Contains(args, want) {
		t.Errorf("claude was started with %q, want %q in it", args, want)
	}

	// A fork point that is present but is not a uuid is an error: the whole
	// conversation is not forked in its place, and nothing is started.
	started := len(argsLines(h))
	for _, at := range []any{"x --dangerously-skip-permissions", "", 7, map[string]any{"a": 1}, strings.ToUpper(msg1) + "0"} {
		r = h.call("session/fork", map[string]any{"sessionId": sid, "cwd": h.cwd, "mcpServers": []any{},
			"_meta": map[string]any{"flockdeck.dev": map[string]any{"at": at}}})
		if r.Error == nil || r.Error.Code != codeInvalidParams {
			t.Errorf("fork at %v = %s %+v", at, r.Result, r.Error)
		}
	}
	if n := len(argsLines(h)); n != started {
		t.Errorf("claude was started %d more times for a bad fork point", n-started)
	}
	for _, l := range readLog(h.log) {
		if strings.Contains(l, "--dangerously") || strings.Contains(l, "skip-permissions") {
			t.Errorf("text from the point reached claude's command line: %s", l)
		}
	}
	// No point at all is a fork of the whole conversation, without a cut.
	r = h.call("session/fork", map[string]any{"sessionId": sid, "cwd": h.cwd, "mcpServers": []any{}})
	if r.Error != nil {
		t.Fatalf("whole fork = %+v", r.Error)
	}
	last := readLog(h.log)
	if l := last[len(last)-1]; strings.Contains(l, "--resume-session-at") {
		t.Errorf("a whole fork was cut: %s", l)
	}
	// A source that is not a session id is refused before anything is started.
	if r := h.call("session/fork", map[string]any{"sessionId": "../x", "cwd": h.cwd, "mcpServers": []any{}}); r.Error == nil || r.Error.Code != codeInvalidParams {
		t.Errorf("fork of a bad id = %+v", r)
	}
}

func TestRewindStartsClaudeAgainOnTheSameSession(t *testing.T) {
	// claude reports the mode it is in at the start of a turn.
	h := newHarness(t, twoTurnsInit(strings.Replace(lInit, `"permissionMode":"default"`, `"permissionMode":"acceptEdits"`, 1)))
	sid := h.start()
	if r := h.call("session/set_mode", map[string]any{"sessionId": sid, "modeId": "acceptEdits"}); r.Error != nil {
		t.Fatal(r.Error)
	}
	if r := h.call("session/set_model", map[string]any{"sessionId": sid, "modelId": "haiku"}); r.Error != nil {
		t.Fatal(r.Error)
	}
	stopReason(t, h.await(h.promptTurn(sid, "one", tok1)))
	stopReason(t, h.await(h.promptTurn(sid, "two", tok2)))

	if r := h.call("_flockdeck/rewind", map[string]any{"sessionId": sid, "beforeTurn": tok2}); r.Error != nil {
		t.Fatalf("rewind = %+v", r.Error)
	}
	var args []string
	for _, l := range readLog(h.log) {
		if strings.HasPrefix(l, "ARGS: ") {
			args = append(args, l)
		}
	}
	if len(args) != 2 {
		t.Fatalf("claude was started %d times: %q", len(args), args)
	}
	// The rewind is a fork of the session at that message, under a new id, so that
	// the cut survives a later resume even if no message is sent first.
	want := "--resume " + sid + " --resume-session-at " + msg1 + " --fork-session --session-id "
	if !strings.Contains(args[1], want) {
		t.Errorf("claude was started again with %q, want %q in it", args[1], want)
	}
	newID := args[1][strings.Index(args[1], "--session-id ")+len("--session-id "):]
	if _, err := uuid.Parse(strings.TrimSpace(newID)); err != nil || strings.TrimSpace(newID) == sid {
		t.Errorf("the new session id is %q", newID)
	}
	// The client is told the conversation now lives under the new id, and keeps
	// prompting with the one it has.
	h.waitFor(func(m rpcMsg) bool {
		return m.Method == "session/update" && strings.Contains(string(m.Params), `"flockdeck_session"`) && strings.Contains(string(m.Params), strings.TrimSpace(newID))
	}, 10e9)
	// The mode the person chose is put back in the new claude.
	if n := strings.Count(strings.Join(readLog(h.log), "\n"), `"mode":"acceptEdits"`); n != 2 {
		t.Errorf("the mode was set %d times, want 2 (once for each claude): %q", n, readLog(h.log))
	}
	// And so is the model.
	if n := strings.Count(strings.Join(readLog(h.log), "\n"), `"model":"haiku"`); n != 2 {
		t.Errorf("the model was set %d times, want 2 (once for each claude)", n)
	}
	// The session answers on the same id, and the turn is gone but the one before is not.
	if r := h.call("_flockdeck/forkpoint", map[string]any{"sessionId": sid, "beforeTurn": tok2}); r.Error == nil {
		t.Errorf("the dropped turn is still known: %s", r.Result)
	}
	if got := stopReason(t, h.await(h.promptTurn(sid, "one", tok1))); got != "end_turn" {
		// The new claude plays the script from its start.
		t.Errorf("stop after the rewind = %q", got)
	}
}

func TestRewindIsRefusedWhileBusy(t *testing.T) {
	h := newHarness(t, []step{
		expect(`"uuid":"` + tok1 + `"`), emit(lInit), emit(rewindAssistantLine(msg1)), emit(lResultOK),
		expect(`"uuid":"` + tok2 + `"`), emit(lInit), emit(lMsgStart), emit(textDelta("working")), // never ends
	})
	sid := h.start()
	stopReason(t, h.await(h.promptTurn(sid, "one", tok1)))
	id := h.promptTurn(sid, "two", tok2)
	h.waitFor(func(m rpcMsg) bool {
		return m.Method == "session/update" && strings.Contains(string(m.Params), "working")
	}, 20e9)
	if r := h.call("_flockdeck/rewind", map[string]any{"sessionId": sid, "beforeTurn": tok2}); r.Error == nil {
		t.Errorf("a rewind was done during a turn: %s", r.Result)
	}
	// A turn that is there is not harmed by the refusal.
	h.notify("session/cancel", map[string]any{"sessionId": sid})
	_ = id
	if n := len(argsLines(h)); n != 1 {
		t.Errorf("claude was started %d times", n)
	}
}

func argsLines(h *harness) []string {
	var out []string
	for _, l := range readLog(h.log) {
		if strings.HasPrefix(l, "ARGS: ") {
			out = append(out, l)
		}
	}
	return out
}

func TestRewindRefusesBadArguments(t *testing.T) {
	h := newHarness(t, twoTurns())
	sid := h.start()
	stopReason(t, h.await(h.promptTurn(sid, "one", tok1)))
	for _, p := range []map[string]any{
		{"sessionId": sid, "beforeTurn": ""},
		{"sessionId": sid, "beforeTurn": "x"},
		{"sessionId": sid, "beforeTurn": tok1}, // nothing before it
		{"sessionId": sid, "beforeTurn": tok3}, // not known
		{"sessionId": "nope", "beforeTurn": tok1},
		{"sessionId": sid},
	} {
		if r := h.call("_flockdeck/rewind", p); r.Error == nil || r.Error.Code != codeInvalidParams {
			t.Errorf("rewind %v = %+v", p, r)
		}
	}
	if n := len(argsLines(h)); n != 1 {
		t.Errorf("claude was started %d times", n)
	}
}

// A prompt that arrives while the session is being started over is refused: it
// would go to the claude that is about to end.
func TestAPromptDuringARewindIsRefused(t *testing.T) {
	var out bytes.Buffer
	s := &server{out: bufio.NewWriter(&out)}
	ss := &session{srv: s, queue: make(chan *promptReq, 4), rewinding: true}
	id := json.RawMessage(`7`)
	ss.enqueue(&promptReq{id: &id})
	if len(ss.queue) != 0 {
		t.Fatal("the prompt was queued during a rewind")
	}
	if !strings.Contains(out.String(), `"error"`) {
		t.Errorf("no error was sent: %q", out.String())
	}
	ss.rewinding = false
	ss.enqueue(&promptReq{id: &id})
	if len(ss.queue) != 1 {
		t.Error("the prompt was not queued after the rewind")
	}
}

// A rewind whose new claude cannot start leaves the session as it was: the
// rewinding flag is cleared and the first claude goes on taking prompts.
func TestARewindThatCannotStartClaudeLeavesTheSessionUsable(t *testing.T) {
	steps := append(twoTurns(),
		expect(`"uuid":"`+tok3+`"`), emit(rewindAssistantLine(msg1)), emit(lResultOK))
	h := newHarnessWith(t, mustExe(t), steps, envNoCut+"=1")
	sid := h.start()
	stopReason(t, h.await(h.promptTurn(sid, "one", tok1)))
	stopReason(t, h.await(h.promptTurn(sid, "two", tok2)))
	r := h.call("_flockdeck/rewind", map[string]any{"sessionId": sid, "beforeTurn": tok2})
	if r.Error == nil || r.Error.Code != codeInternal {
		t.Fatalf("rewind = %s %+v", r.Result, r.Error)
	}
	if strings.Contains(r.Error.Message, "exit") || strings.Contains(r.Error.Message, "claude") {
		t.Errorf("the error says what claude did: %q", r.Error.Message)
	}
	// Prompts are taken again, by the claude that was there before.
	if got := stopReason(t, h.await(h.promptTurn(sid, "three", tok3))); got != "end_turn" {
		t.Errorf("stop after the failed rewind = %q", got)
	}
}

// After /clear claude holds the conversation under a new id, and the uuids the
// adapter holds name messages of the old file: no point is given.
func TestNoPointAfterTheSessionWasCleared(t *testing.T) {
	ss := &session{id: "old", turns: []turnRec{{token: tok1, last: msg1}, {token: tok2, last: msg2}}}
	if _, e := ss.pointLocked(tok2); e != nil {
		t.Fatalf("a point before /clear: %+v", e)
	}
	// An id claude was told to use (a rewind's) is not a /clear.
	ss.forkID = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	ss.srv = &server{sessions: map[string]*session{}, out: bufio.NewWriter(io.Discard), opts: Options{Stderr: io.Discard}}
	ss.adopt(ss.forkID)
	if _, e := ss.pointLocked(tok2); e != nil {
		t.Errorf("no point after the id of a rewind: %+v", e)
	}
	// An id claude chose is.
	ss.adopt("cccccccc-cccc-4ccc-8ccc-cccccccccccc")
	if _, e := ss.pointLocked(tok2); e == nil {
		t.Error("a point was given after /clear")
	}
}

// A second rewind goes on from the conversation the first one made: claude is
// resumed by the id the first moved it to, and the kept turns still name
// messages of it.
func TestASecondRewindResumesTheFirstRewindsConversation(t *testing.T) {
	const msg3 = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	h := newHarness(t, append(twoTurns(),
		expect(`"uuid":"`+tok3+`"`), emit(rewindAssistantLine(msg3)), emit(lResultOK)))
	sid := h.start()
	for _, p := range []struct{ text, tok string }{{"one", tok1}, {"two", tok2}, {"three", tok3}} {
		stopReason(t, h.await(h.promptTurn(sid, p.text, p.tok)))
	}
	if r := h.call("_flockdeck/rewind", map[string]any{"sessionId": sid, "beforeTurn": tok3}); r.Error != nil {
		t.Fatalf("first rewind = %+v", r.Error)
	}
	if r := h.call("_flockdeck/rewind", map[string]any{"sessionId": sid, "beforeTurn": tok2}); r.Error != nil {
		t.Fatalf("second rewind = %+v", r.Error)
	}
	args := argsLines(h)
	if len(args) != 3 {
		t.Fatalf("claude was started %d times: %q", len(args), args)
	}
	first := strings.TrimSpace(args[1][strings.Index(args[1], "--session-id ")+len("--session-id "):])
	if want := "--resume " + first + " --resume-session-at " + msg1 + " --fork-session --session-id "; !strings.Contains(args[2], want) {
		t.Errorf("the second claude was started with %q, want %q in it", args[2], want)
	}
}
