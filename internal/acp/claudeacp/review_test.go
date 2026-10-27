package claudeacp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func bashRequest(rid, toolID, input string) string {
	return `{"type":"control_request","request_id":"` + rid + `","request":{"subtype":"can_use_tool","tool_name":"Bash","input":` + input + `,"tool_use_id":"` + toolID + `"}}`
}

// Allow always is offered for a shell command only, and covers that call with
// all of its input.
func TestAllowAlwaysIsForBashAndTheWholeInput(t *testing.T) {
	steps := []step{expect(`"role":"user"`)}
	calls := []struct{ rid, tool, input string }{
		{"p1", "toolu_1", `{"command":"npm test","description":"run the tests"}`},    // asked, always
		{"p2", "toolu_2", `{"command":"npm test","description":"another label"}`},    // same call: allowed without a card
		{"p3", "toolu_3", `{"command":"npm test","dangerouslyDisableSandbox":true}`}, // not the same call: asked
		{"p4", "toolu_4", `{"command":"npm test","run_in_background":true}`},         // asked
		{"p5", "toolu_5", `{"command":"npm test ","description":"x"}`},               // not the same command: asked
	}
	for _, c := range calls {
		steps = append(steps, emit(bashRequest(c.rid, c.tool, c.input)), expect(`"request_id":"`+c.rid+`"`))
	}
	steps = append(steps, emit(lResultOK))
	h := newHarness(t, steps)
	var mu sync.Mutex
	var asked []string
	var optionSets [][]string
	h.onReq = func(m rpcMsg) any {
		var p struct {
			ToolCall struct {
				ID string `json:"toolCallId"`
			} `json:"toolCall"`
			Options []struct{ OptionID string } `json:"options"`
		}
		_ = json.Unmarshal(m.Params, &p)
		var ids []string
		for _, o := range p.Options {
			ids = append(ids, o.OptionID)
		}
		mu.Lock()
		asked = append(asked, p.ToolCall.ID)
		optionSets = append(optionSets, ids)
		mu.Unlock()
		if p.ToolCall.ID == "toolu_1" {
			return outcome("allow_always")
		}
		return outcome("reject")
	}
	sid := h.start()
	stopReason(t, h.await(h.promptText(sid, "go")))
	mu.Lock()
	defer mu.Unlock()
	if want := []string{"toolu_1", "toolu_3", "toolu_4", "toolu_5"}; !reflect.DeepEqual(asked, want) {
		t.Errorf("the client was asked about %v, want %v", asked, want)
	}
	for _, ids := range optionSets {
		if !reflect.DeepEqual(ids, []string{"allow", "allow_always", "reject"}) {
			t.Errorf("a shell card offers %v", ids)
		}
	}
	log := readLog(h.log)
	answered := func(rid, behavior string) bool {
		for _, l := range log {
			if strings.Contains(l, `"request_id":"`+rid+`"`) && strings.Contains(l, `"behavior":"`+behavior+`"`) {
				return true
			}
		}
		return false
	}
	for rid, b := range map[string]string{"p1": "allow", "p2": "allow", "p3": "deny", "p4": "deny", "p5": "deny"} {
		if !answered(rid, b) {
			t.Errorf("%s was not answered %s", rid, b)
		}
	}
}

// No other tool gets an allow-always card, and an allow_always answer to one that
// was not offered is only an allow.
func TestOtherToolsHaveNoAllowAlways(t *testing.T) {
	tools := []string{"Write", "Edit", "MultiEdit", "NotebookEdit", "WebFetch", "Agent", "Task", "mcp__pingsrv__ping", "Read", "Glob"}
	steps := []step{expect(`"role":"user"`)}
	for i, tool := range tools {
		rid := "r" + string(rune('a'+i))
		steps = append(steps,
			emit(`{"type":"control_request","request_id":"`+rid+`","request":{"subtype":"can_use_tool","tool_name":"`+tool+`","input":{"file_path":"x"},"tool_use_id":"toolu_`+rid+`"}}`),
			expect(`"request_id":"`+rid+`"`))
	}
	// the same Write twice: the first answer was allow_always, which must not stick
	steps = append(steps,
		emit(`{"type":"control_request","request_id":"again","request":{"subtype":"can_use_tool","tool_name":"Write","input":{"file_path":"x"},"tool_use_id":"toolu_again"}}`),
		expect(`"request_id":"again"`), emit(lResultOK))
	h := newHarness(t, steps)
	var mu sync.Mutex
	asked := 0
	h.onReq = func(m rpcMsg) any {
		var p struct {
			Options []struct{ OptionID string } `json:"options"`
		}
		_ = json.Unmarshal(m.Params, &p)
		for _, o := range p.Options {
			if o.OptionID == "allow_always" {
				t.Errorf("an allow_always option was offered: %s", m.Params)
			}
		}
		mu.Lock()
		asked++
		mu.Unlock()
		return outcome("allow_always")
	}
	sid := h.start()
	stopReason(t, h.await(h.promptText(sid, "go")))
	mu.Lock()
	defer mu.Unlock()
	if asked != len(tools)+1 {
		t.Errorf("%d cards for %d calls: a rule was kept", asked, len(tools)+1)
	}
}

func TestRuleKey(t *testing.T) {
	k := func(tool, in string) string { return ruleKey(tool, json.RawMessage(in)) }
	if k("Bash", `{"command":"ls","description":"a"}`) != k("Bash", `{"description":"b","command":"ls"}`) {
		t.Error("the description or the key order makes a new rule")
	}
	for name, other := range map[string]string{
		"another command":   `{"command":"ls -la"}`,
		"sandbox off":       `{"command":"ls","dangerouslyDisableSandbox":true}`,
		"in the background": `{"command":"ls","run_in_background":true}`,
		"a timeout":         `{"command":"ls","timeout":5000}`,
	} {
		if k("Bash", `{"command":"ls"}`) == k("Bash", other) {
			t.Errorf("%s shares a rule", name)
		}
	}
	if k("Bash", `{"command":"ls"}`) == k("PowerShell", `{"command":"ls"}`) {
		t.Error("two tools share a rule")
	}
	if k("Bash", `not json`) == k("Bash", `also not json`) {
		t.Error("two unparseable inputs share a rule")
	}
}

func TestCanAlways(t *testing.T) {
	for tool, want := range map[string]bool{"Bash": true, "PowerShell": true, "Write": false, "Edit": false, "MultiEdit": false,
		"WebFetch": false, "Agent": false, "Task": false, "mcp__x__y": false, "Read": false} {
		if canAlways(tool) != want {
			t.Errorf("canAlways(%s) = %v", tool, !want)
		}
	}
}

// Nothing of claude's own error text reaches the client in an error.
func TestErrorsCarryNoClaudeText(t *testing.T) {
	secret := "SECRET-claude-detail C:\\Users\\someone\\.claude"
	t.Run("a failed turn", func(t *testing.T) {
		h := newHarness(t, []step{expect(`"role":"user"`),
			emit(strings.Replace(strings.Replace(lResultOK, `"is_error":false`, `"is_error":true`, 1), `"result":"one"`, `"result":"API Error: `+strings.ReplaceAll(secret, `\`, `\\`)+`"`, 1))})
		sid := h.start()
		r := h.await(h.promptText(sid, "go"))
		if r.Error == nil || strings.Contains(r.Error.Message, "SECRET") || r.Error.Message != "the turn failed" {
			t.Errorf("error = %+v", r.Error)
		}
	})
}

// claude's own error text for a setting it refuses stays out of the answer.
func TestSettingErrorsAreFixedText(t *testing.T) {
	h := newHarnessWith(t, mustExe(t), []step{expect("never")}, envCtlErr+"=set_model", envCtlErr2+"=set_permission_mode")
	sid := h.start()
	for method, params := range map[string]map[string]any{
		"session/set_model": {"sessionId": sid, "modelId": "haiku"},
		"session/set_mode":  {"sessionId": sid, "modeId": "plan"},
	} {
		r := h.call(method, params)
		if r.Error == nil || strings.Contains(r.Error.Message, "SECRET") || !strings.HasPrefix(r.Error.Message, "the agent could not change") {
			t.Errorf("%s: %+v", method, r.Error)
		}
	}
}

// A turn claude starts on its own must not take the user's prompt with it: the
// prompt waits for that turn's result and gets its own.
func TestPromptWaitsForATurnClaudeStarted(t *testing.T) {
	h := newHarness(t, []step{
		emit(lInit), emit(lMsgStart), emit(textDelta("background")),
		absent(`"role":"user"`, 700),
		emit(resultWith("background")),
		expect(`"text":"mine"`),
		emit(lInit), emit(lMsgStart), emit(textDelta("answer")),
		emit(resultWith("mine")),
	})
	sid := h.start()
	r := h.await(h.promptText(sid, "mine"))
	if got := stopReason(t, r); got != "end_turn" {
		t.Fatalf("stop reason = %q", got)
	}
	if logHas(readLog(h.log), "VIOLATION") {
		t.Error("the prompt reached claude during the turn claude started")
	}
	us := h.updates()
	if got := joinText(us, "agent_message_chunk"); got != "backgroundanswer" {
		t.Errorf("text = %q", got)
	}
	// the prompt was answered after its own result: the last update is the second usage_update
	if k := kinds(us); k[len(k)-1] != "usage_update" {
		t.Errorf("updates = %v", k)
	}
}

// A cancel while the prompt waits for claude's turn answers it cancelled.
func TestCancelWhileWaitingForClaudesTurn(t *testing.T) {
	h := newHarness(t, []step{
		emit(lInit), emit(lMsgStart), emit(textDelta("background")),
		absent(`"role":"user"`, 1500),
	})
	sid := h.start()
	id := h.promptText(sid, "mine")
	time.Sleep(200 * time.Millisecond)
	h.notify("session/cancel", map[string]any{"sessionId": sid})
	if got := stopReason(t, h.await(id)); got != "cancelled" {
		t.Errorf("stop reason = %q", got)
	}
}

// A line over the limit ends the session instead of being held in memory, and
// the prompt is answered.
func TestOversizedLineEndsTheSession(t *testing.T) {
	old := maxLine
	maxLine = 64 << 10
	defer func() { maxLine = old }()
	h := newHarness(t, []step{expect(`"role":"user"`), emit(textDelta(strings.Repeat("x", 200<<10))), sleep(60000)})
	sid := h.start()
	start := time.Now()
	r := h.await(h.promptText(sid, "go"))
	if r.Error == nil {
		t.Fatalf("the prompt succeeded: %s", r.Result)
	}
	if time.Since(start) > 15*time.Second {
		t.Errorf("took %v", time.Since(start))
	}
	// claude was ended: its stdin was closed, which the fake logs
	deadline := time.Now().Add(5 * time.Second)
	for !logHas(readLog(h.log), "STDIN_CLOSED") && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
}

// Shutdown while session/new is waiting for claude's initialize leaves no claude
// running.
func TestShutdownDuringSessionNewLeavesNothingRunning(t *testing.T) {
	h := newHarnessWith(t, mustExe(t), []step{expect("never")}, envDelay+"=1500")
	h.call("initialize", map[string]any{"protocolVersion": 1})
	id := h.send("session/new", map[string]any{"cwd": h.cwd, "mcpServers": []any{}})
	time.Sleep(300 * time.Millisecond)
	h.in.Close() // the client is gone
	select {
	case <-h.stopped:
	case <-time.After(20 * time.Second):
		t.Fatal("the adapter did not stop")
	}
	// The answer may be an error or not arrive; what matters is claude's end.
	_ = id
	deadline := time.Now().Add(5 * time.Second)
	for !logHas(readLog(h.log), "STDIN_CLOSED") && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !logHas(readLog(h.log), "STDIN_CLOSED") {
		t.Error("claude was left running with its stdin open")
	}
}

func TestCleanEnvDropsFlockdeckNamesInAnyCase(t *testing.T) {
	got := cleanEnv([]string{"PATH=/bin", "FLOCKDECK_TOKEN=a", "Flockdeck_Api=b", "perch_x=c", "PERCH_Y=d", "FLOCKDECKX=keep", "HOME=/h"})
	if want := []string{"PATH=/bin", "FLOCKDECKX=keep", "HOME=/h"}; !reflect.DeepEqual(got, want) {
		t.Errorf("env = %v", got)
	}
}

func TestMCPConfigLivesInADirectoryOfItsOwn(t *testing.T) {
	path, err := writeMCPConfig([]json.RawMessage{json.RawMessage(`{"name":"a","command":"x"}`)})
	if err != nil {
		t.Fatal(err)
	}
	dir := dirOf(path)
	if !strings.Contains(dir, mcpDirPrefix) {
		t.Errorf("the file is in %s", dir)
	}
	// the session removes the directory, not only the file
	ss := &session{mcpFile: path}
	ss.cleanup()
	if exists(dir) {
		t.Errorf("%s was left", dir)
	}
}

func mustExe(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return exe
}

func dirOf(p string) string { return filepath.Dir(p) }

func exists(p string) bool { _, err := os.Stat(p); return err == nil }
