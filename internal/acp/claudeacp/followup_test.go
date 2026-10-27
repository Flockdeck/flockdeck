package claudeacp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"
)

// Run returns when its context is done, even though its input is still open and
// nothing is being read from it, and the session it had is closed first.
func TestRunReturnsWhenTheContextIsDone(t *testing.T) {
	env, logPath := fakeEnv(t, []step{expect("never")})
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	defer inW.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, inR, outW, Options{ClaudePath: mustExe(t), Env: env, ControlTimeout: 10 * time.Second})
		outW.Close()
	}()
	lines := make(chan string, 16)
	go func() {
		sc := bufio.NewScanner(outR)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	write := func(v any) { b, _ := json.Marshal(v); _, _ = inW.Write(append(b, '\n')) }
	write(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "session/new", "params": map[string]any{"cwd": t.TempDir(), "mcpServers": []any{}}})
	select {
	case l := <-lines:
		if !strings.Contains(l, "sessionId") {
			t.Fatalf("session/new: %s", l)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("no session")
	}
	cancel() // a signal, as the subcommand sees it
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after its context was done")
	}
	if !logHas(readLog(logPath), "STDIN_CLOSED") {
		t.Error("claude was not closed")
	}
}

// The setup of a claude that starts a turn of its own: init, some text, no end.
var ownTurn = []step{emit(lInit), emit(lMsgStart), emit(textDelta("background"))}

// A cancel while claude is in a turn of its own interrupts it, and the prompt
// that was waiting is answered cancelled.
func TestCancelInterruptsClaudesOwnTurn(t *testing.T) {
	h := newHarness(t, append(append([]step{}, ownTurn...),
		expect(`"subtype":"interrupt"`), emit(lResultAborted),
		expect(`"text":"next"`), emit(resultWith("next"))))
	sid := h.start()
	id := h.promptText(sid, "mine")
	time.Sleep(200 * time.Millisecond)
	h.notify("session/cancel", map[string]any{"sessionId": sid})
	if got := stopReason(t, h.await(id)); got != "cancelled" {
		t.Errorf("stop reason = %q", got)
	}
	// the interrupt ended claude's turn, so the next prompt runs
	if got := stopReason(t, h.await(h.promptText(sid, "next"))); got != "end_turn" {
		t.Errorf("next prompt: %q", got)
	}
	if !logHas(readLog(h.log), `"subtype":"interrupt"`) {
		t.Error("claude was not interrupted")
	}
}

// If the interrupt does not end claude's own turn, claude is ended after the
// grace.
func TestCancelEndsClaudeThatIgnoresTheInterrupt(t *testing.T) {
	h := newHarness(t, append(append([]step{}, ownTurn...), sleep(60000)))
	sid := h.start()
	h.notify("session/cancel", map[string]any{"sessionId": sid})
	time.Sleep(300 * time.Millisecond)
	start := time.Now()
	r := h.await(h.promptText(sid, "next"))
	if r.Error == nil {
		t.Fatalf("a prompt to the ended session succeeded: %s", r.Result)
	}
	if d := time.Since(start); d > 12*time.Second {
		t.Errorf("took %v", d)
	}
}

// A turn of claude's own that goes silent stops holding prompts back.
func TestSilentTurnOfClaudesOwnStopsHoldingPrompts(t *testing.T) {
	old := testBusyIdle
	testBusyIdle = 500 * time.Millisecond
	defer func() { testBusyIdle = old }()
	h := newHarness(t, append(append([]step{}, ownTurn...),
		expect(`"text":"mine"`), emit(resultWith("mine"))))
	sid := h.start()
	start := time.Now()
	if got := stopReason(t, h.await(h.promptText(sid, "mine"))); got != "end_turn" {
		t.Errorf("stop reason = %q", got)
	}
	if d := time.Since(start); d < 300*time.Millisecond || d > 10*time.Second {
		t.Errorf("the prompt waited %v, want about the idle time", d)
	}
}

// Integers too large for a float64 stay different.
func TestRuleKeyKeepsLargeNumbers(t *testing.T) {
	a := ruleKey("Bash", json.RawMessage(`{"command":"x","timeout":9007199254740993}`))
	b := ruleKey("Bash", json.RawMessage(`{"command":"x","timeout":9007199254740992}`))
	if a == b {
		t.Error("two large numbers share a rule")
	}
	if ruleKey("Bash", json.RawMessage(`{"command":"x"} {"y":1}`)) == ruleKey("Bash", json.RawMessage(`{"command":"x"}`)) {
		t.Error("trailing data is ignored")
	}
}
