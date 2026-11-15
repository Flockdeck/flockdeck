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
	// claude's own turn starts when the session does, and the idle time runs from
	// there. Starting the session can itself take longer than the idle time on a
	// slow machine (under the race detector it did), by which point the turn has
	// already been given up on and the prompt waits for nothing. So the time is
	// taken from before the session starts: the prompt cannot be answered sooner
	// than the idle time after that.
	start := time.Now()
	sid := h.start()
	if got := stopReason(t, h.await(h.promptText(sid, "mine"))); got != "end_turn" {
		t.Errorf("stop reason = %q", got)
	}
	if d := time.Since(start); d < 300*time.Millisecond || d > 10*time.Second {
		t.Errorf("the session started and the prompt was answered in %v, want about the idle time", d)
	}
	// the client was told the turn began and that the wait ended
	var busy []bool
	for _, u := range h.updates() {
		if u["sessionUpdate"] == "flockdeck_busy" {
			b, _ := u["busy"].(bool)
			busy = append(busy, b)
		}
	}
	if len(busy) != 2 || !busy[0] || busy[1] {
		t.Errorf("flockdeck_busy updates = %v, want [true false]", busy)
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

// busySeq is the flockdeck_busy values sent so far, in order.
func busySeq(h *harness) []bool {
	var out []bool
	for _, u := range h.updates() {
		if u["sessionUpdate"] == "flockdeck_busy" {
			b, _ := u["busy"].(bool)
			out = append(out, b)
		}
	}
	return out
}

func waitBusySeq(t *testing.T, h *harness, want []bool, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for {
		got := busySeq(h)
		if len(got) == len(want) {
			ok := true
			for i := range got {
				ok = ok && got[i] == want[i]
			}
			if ok {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("flockdeck_busy updates = %v, want %v", got, want)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// A claude that exits during its own turn is no longer in one: the client is
// told, or it would hold prompts for a result that cannot come.
func TestClaudeExitingDuringItsOwnTurnClearsBusy(t *testing.T) {
	h := newHarness(t, append(append([]step{}, ownTurn...), sleep(300), exitWith(1)))
	h.start()
	waitBusySeq(t, h, []bool{true, false}, 10*time.Second)
}

// So is one that was ended because it ignored the interrupt.
func TestClaudeEndedAfterAnIgnoredInterruptClearsBusy(t *testing.T) {
	h := newHarness(t, append(append([]step{}, ownTurn...), sleep(60000)))
	sid := h.start()
	waitBusySeq(t, h, []bool{true}, 10*time.Second)
	h.notify("session/cancel", map[string]any{"sessionId": sid})
	waitBusySeq(t, h, []bool{true, false}, 12*time.Second)
}

// A watchdog false is followed by a later init true, in that order.
func TestBusyFalseFromTheWatchdogComesBeforeALaterTrue(t *testing.T) {
	old := testBusyIdle
	testBusyIdle = 300 * time.Millisecond
	defer func() { testBusyIdle = old }()
	h := newHarness(t, append(append([]step{}, ownTurn...), sleep(1500), emit(lInit), emit(lMsgStart), sleep(5000)))
	h.start()
	waitBusySeq(t, h, []bool{true, false, true}, 10*time.Second)
}
