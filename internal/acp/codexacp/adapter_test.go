package codexacp

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// The lines below are the shapes in docs/codex-adapter-route.md, written by
// hand: no real codex has been run against this adapter.

func startedTurn() step {
	return note("turn/started", `{"threadId":"thr-1","turn":{"id":"turn-1","status":"inProgress"}}`)
}

func endTurn(status string) step {
	return note("turn/completed", `{"threadId":"thr-1","turn":{"id":"turn-1","status":"`+status+`"}}`)
}

func delta(item, text string) step {
	b, _ := json.Marshal(text)
	return note("item/agentMessage/delta", `{"threadId":"thr-1","turnId":"turn-1","itemId":"`+item+`","delta":`+string(b)+`}`)
}

func cmdApproval(id, itemID, command string, extra string) step {
	b, _ := json.Marshal(command)
	return emit(`{"id":"` + id + `","method":"item/commandExecution/requestApproval","params":{"threadId":"thr-1","turnId":"turn-1","itemId":"` +
		itemID + `","command":` + string(b) + `,"cwd":"/work"` + extra + `}}`)
}

func cmdItem(method, id, command, status string) step {
	b, _ := json.Marshal(command)
	return note(method, `{"threadId":"thr-1","turnId":"turn-1","item":{"type":"commandExecution","id":"`+id+`","command":`+string(b)+`,"cwd":"/work","status":"`+status+`"}}`)
}

func newText(h *harness, sid string) rpcMsg {
	return h.await(h.promptText(sid, "go"))
}

func TestSessionNew(t *testing.T) {
	h := newHarness(t, nil)
	if r := h.call("initialize", map[string]any{"protocolVersion": 1}); r.Error != nil {
		t.Fatal(r.Error)
	} else if !strings.Contains(string(r.Result), `"resume"`) || !strings.Contains(string(r.Result), `"sse":false`) {
		t.Errorf("initialize = %s", r.Result)
	}
	r := h.call("session/new", map[string]any{"cwd": h.cwd})
	if r.Error != nil {
		t.Fatal(r.Error)
	}
	var res struct {
		SessionID string `json:"sessionId"`
		Modes     struct {
			Current string           `json:"currentModeId"`
			Avail   []map[string]any `json:"availableModes"`
		} `json:"modes"`
		Models struct {
			Current string           `json:"currentModelId"`
			Avail   []map[string]any `json:"availableModels"`
		} `json:"models"`
	}
	_ = json.Unmarshal(r.Result, &res)
	if res.SessionID != "thr-1" || res.Modes.Current != "auto" || len(res.Modes.Avail) != 3 || res.Models.Current != "gpt-fake" || len(res.Models.Avail) != 2 {
		t.Errorf("session/new = %s", r.Result)
	}
	cmd := h.waitFor(func(m rpcMsg) bool {
		return m.Method == "session/update" && strings.Contains(string(m.Params), "available_commands_update")
	}, 10*time.Second)
	for _, want := range []string{`"review"`, `"compact"`, `"deploy"`} {
		if !strings.Contains(string(cmd.Params), want) {
			t.Errorf("commands lack %s: %s", want, cmd.Params)
		}
	}
	if strings.Contains(string(cmd.Params), `"off"`) {
		t.Error("a disabled skill is offered")
	}
	log := readLog(h.log)
	if log[0] != "ARGS: app-server --listen stdio://" {
		t.Errorf("args = %v", log[0])
	}
	// The handshake: initialize with the client's name, then initialized, then
	// the thread. The wire has no "jsonrpc" member.
	init := logFind(log, `"method":"initialize"`)
	if !strings.Contains(init, `"clientInfo"`) || strings.Contains(init, "jsonrpc") {
		t.Errorf("initialize = %s", init)
	}
	var order []string
	for _, l := range log {
		for _, m := range []string{`"method":"initialize"`, `"method":"initialized"`, `"method":"thread/start"`} {
			if strings.HasPrefix(l, "IN: ") && strings.Contains(l, m) {
				order = append(order, m)
			}
		}
	}
	if strings.Join(order, " ") != `"method":"initialize" "method":"initialized" "method":"thread/start"` {
		t.Errorf("handshake order = %v", order)
	}
	ts := logFind(log, `"method":"thread/start"`)
	for _, want := range []string{`"approvalPolicy":"on-request"`, `"sandbox":"workspace-write"`, `"cwd":`} {
		if !strings.Contains(ts, want) {
			t.Errorf("thread/start lacks %s: %s", want, ts)
		}
	}
}

func TestPromptStreamsAndEnds(t *testing.T) {
	h := newHarness(t, []step{
		expect(`"method":"turn/start"`),
		startedTurn(),
		note("item/reasoning/summaryTextDelta", `{"threadId":"thr-1","turnId":"turn-1","itemId":"r1","delta":"thinking","summaryIndex":0}`),
		note("item/reasoning/textDelta", `{"threadId":"thr-1","turnId":"turn-1","itemId":"r1","delta":"RAW"}`),
		note("item/started", `{"threadId":"thr-1","turnId":"turn-1","item":{"type":"agentMessage","id":"m1","text":""}}`),
		delta("m1", "Hello "), delta("m1", "world"),
		note("item/completed", `{"threadId":"thr-1","turnId":"turn-1","item":{"type":"agentMessage","id":"m1","text":"Hello world"}}`),
		note("item/completed", `{"threadId":"thr-1","turnId":"turn-1","item":{"type":"agentMessage","id":"m2","text":"whole"}}`),
		note("thread/tokenUsage/updated", `{"threadId":"thr-1","turnId":"turn-1","tokenUsage":{"total":{"totalTokens":900},"last":{"totalTokens":120},"modelContextWindow":200000}}`),
		note("turn/plan/updated", `{"threadId":"thr-1","turnId":"turn-1","plan":[{"step":"one","status":"completed"},{"step":"two","status":"inProgress"},{"step":"three","status":"pending"}]}`),
		// another thread's events are not ours
		note("item/agentMessage/delta", `{"threadId":"other","turnId":"turn-1","itemId":"x","delta":"LEAK"}`),
		endTurn("completed"),
	})
	sid := h.start()
	r := newText(h, sid)
	if got := stopReason(t, r); got != "end_turn" {
		t.Fatalf("stop = %s", got)
	}
	us := h.updates()
	if got := joinText(us, "agent_message_chunk"); got != "Hello worldwhole" {
		t.Errorf("message = %q", got)
	}
	if got := joinText(us, "agent_thought_chunk"); got != "thinking" {
		t.Errorf("thought = %q (raw reasoning is shown only without a summary)", got)
	}
	var usage, plan map[string]any
	for _, u := range us {
		switch u["sessionUpdate"] {
		case "usage_update":
			usage = u
		case "plan":
			plan = u
		}
	}
	if usage == nil || usage["used"] != float64(120) || usage["size"] != float64(200000) || usage["cost"] != nil {
		t.Errorf("usage = %v", usage)
	}
	if b, _ := json.Marshal(plan["entries"]); !strings.Contains(string(b), `"status":"in_progress"`) || strings.Count(string(b), "priority") != 3 {
		t.Errorf("plan = %s", b)
	}
	turn := logFind(readLog(h.log), `"method":"turn/start"`)
	for _, want := range []string{`"threadId":"thr-1"`, `"type":"text"`, `"text":"go"`, `"approvalPolicy":"on-request"`, `"type":"workspaceWrite"`} {
		if !strings.Contains(turn, want) {
			t.Errorf("turn/start lacks %s: %s", want, turn)
		}
	}
	if strings.Contains(turn, `"model"`) {
		t.Errorf("a model was sent that nobody chose: %s", turn)
	}
}

func TestToolCallsAreMatchedByItemID(t *testing.T) {
	h := newHarness(t, []step{
		expect(`"method":"turn/start"`),
		startedTurn(),
		cmdItem("item/started", "c1", "ls", "inProgress"),
		cmdItem("item/started", "c2", "cat a.txt", "inProgress"),
		note("item/commandExecution/outputDelta", `{"threadId":"thr-1","turnId":"turn-1","itemId":"c1","delta":"partial"}`),
		// c2 finishes first, with a failure; c1 afterwards
		note("item/completed", `{"threadId":"thr-1","turnId":"turn-1","item":{"type":"commandExecution","id":"c2","command":"cat a.txt","cwd":"/work","status":"failed","aggregatedOutput":"no such file","exitCode":1}}`),
		note("item/completed", `{"threadId":"thr-1","turnId":"turn-1","item":{"type":"commandExecution","id":"c1","command":"ls","cwd":"/work","status":"completed","aggregatedOutput":"a b","exitCode":0}}`),
		note("item/started", `{"threadId":"thr-1","turnId":"turn-1","item":{"type":"fileChange","id":"f1","status":"inProgress","changes":[{"path":"new.txt","kind":{"type":"add"},"diff":"hello\n"},{"path":"old.txt","kind":{"type":"update"},"diff":"@@ -1 +1 @@\n-a\n+b\n"}]}}`),
		note("item/completed", `{"threadId":"thr-1","turnId":"turn-1","item":{"type":"mcpToolCall","id":"t1","server":"docs","tool":"search","arguments":{"q":"x"},"status":"completed","result":{"content":[{"type":"text","text":"found it"}]}}}`),
		note("item/completed", `{"threadId":"thr-1","turnId":"turn-1","item":{"type":"webSearch","id":"w1","query":"go acp","status":"completed"}}`),
		endTurn("completed"),
	})
	sid := h.start()
	if got := stopReason(t, newText(h, sid)); got != "end_turn" {
		t.Fatalf("stop = %s", got)
	}
	byID := map[string][]map[string]any{}
	for _, u := range h.updates() {
		if id, ok := u["toolCallId"].(string); ok {
			byID[id] = append(byID[id], u)
		}
	}
	last := func(id string) map[string]any { l := byID[id]; return l[len(l)-1] }
	if byID["c1"][0]["sessionUpdate"] != "tool_call" || byID["c1"][0]["kind"] != "execute" || byID["c1"][0]["title"] != "ls" {
		t.Errorf("c1 = %v", byID["c1"][0])
	}
	if last("c1")["status"] != "completed" || !strings.Contains(toJSON(last("c1")), "a b") {
		t.Errorf("c1 end = %v", last("c1"))
	}
	if last("c2")["status"] != "failed" || !strings.Contains(toJSON(last("c2")), "no such file") || strings.Contains(toJSON(last("c2")), "a b") {
		t.Errorf("c2 end = %v", last("c2"))
	}
	f := toJSON(byID["f1"][0])
	if byID["f1"][0]["kind"] != "edit" || !strings.Contains(f, `"type":"diff"`) || !strings.Contains(f, `"newText":"hello\n"`) || !strings.Contains(f, `-a`) {
		t.Errorf("f1 = %s", f)
	}
	if m := toJSON(last("t1")); byID["t1"][0]["title"] != "docs: search" || !strings.Contains(m, "found it") {
		t.Errorf("t1 = %s", m)
	}
	if byID["w1"][0]["kind"] != "fetch" {
		t.Errorf("w1 = %v", byID["w1"][0])
	}
}

func toJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestStaleTurnEndIsNotOurs(t *testing.T) {
	h := newHarness(t, []step{
		expect(`"method":"turn/start"`),
		startedTurn(),
		note("turn/completed", `{"threadId":"thr-1","turn":{"id":"turn-0","status":"completed"}}`),
		delta("m1", "still going"),
		sleep(300),
		endTurn("completed"),
	})
	sid := h.start()
	id := h.promptText(sid, "go")
	time.Sleep(200 * time.Millisecond)
	r := h.await(id)
	if stopReason(t, r) != "end_turn" {
		t.Fatal("bad stop")
	}
	// the prompt answered after the text arrived, so it was not ended by turn-0
	if !strings.Contains(joinText(h.updates(), "agent_message_chunk"), "still going") {
		t.Error("the stale end finished the prompt before the turn did")
	}
}

func TestTurnIDFromNotificationOnly(t *testing.T) {
	h := newHarness(t, []step{
		expect(`"method":"turn/start"`),
		startedTurn(),
		delta("m1", "hi"),
		endTurn("completed"),
	}, hopts{extraEnv: []string{envNoTurn + "=500"}})
	sid := h.start()
	if got := stopReason(t, newText(h, sid)); got != "end_turn" {
		t.Fatalf("stop = %s", got)
	}
}

func TestImageIsAFileThatIsRemoved(t *testing.T) {
	h := newHarness(t, []step{
		expect(`"method":"turn/start"`), startedTurn(), endTurn("completed"),
	})
	sid := h.start()
	png := base64.StdEncoding.EncodeToString([]byte("\x89PNG\r\n\x1a\nfake"))
	r := h.call("session/prompt", map[string]any{"sessionId": sid, "prompt": []any{
		map[string]any{"type": "text", "text": "what is this"},
		map[string]any{"type": "image", "data": png, "mimeType": "image/png"},
	}})
	if stopReason(t, r) != "end_turn" {
		t.Fatal("bad stop")
	}
	turn := logFind(readLog(h.log), `"method":"turn/start"`)
	var msg struct {
		Params struct {
			Input []map[string]any `json:"input"`
		} `json:"params"`
	}
	_ = json.Unmarshal([]byte(turn), &msg)
	if len(msg.Params.Input) != 2 || msg.Params.Input[1]["type"] != "localImage" {
		t.Fatalf("input = %v", msg.Params.Input)
	}
	path := msg.Params.Input[1]["path"].(string)
	b, err := os.ReadFile(path)
	if err != nil || string(b) != "\x89PNG\r\n\x1a\nfake" {
		t.Fatalf("image file: %q, %v", b, err)
	}
	if !strings.HasSuffix(path, ".png") {
		t.Errorf("path = %s", path)
	}
	if fi, _ := os.Stat(path); fi != nil && fi.Mode().Perm()&0o077 != 0 && os.PathSeparator == '/' {
		t.Errorf("the image file is readable by others: %v", fi.Mode())
	}
	h.shutdown()
	if _, err := os.Stat(path); err == nil {
		t.Error("the image file was not removed with the session")
	}
	if _, err := os.Stat(strings.TrimSuffix(path, path[strings.LastIndexAny(path, `/\`):])); err == nil {
		t.Error("the image directory was not removed with the session")
	}
}

func TestBadImagesAreRefused(t *testing.T) {
	h := newHarness(t, nil)
	sid := h.start()
	for name, block := range map[string]map[string]any{
		"not base64": {"type": "image", "data": "!!!", "mimeType": "image/png"},
		"svg":        {"type": "image", "data": "AAAA", "mimeType": "image/svg+xml"},
		"no data":    {"type": "image", "mimeType": "image/png"},
		"audio":      {"type": "audio", "data": "AAAA", "mimeType": "audio/wav"},
	} {
		r := h.call("session/prompt", map[string]any{"sessionId": sid, "prompt": []any{block}})
		if r.Error == nil || r.Error.Code != codeInvalidParams {
			t.Errorf("%s: %+v", name, r)
		}
	}
	if logHas(readLog(h.log), `"method":"turn/start"`) {
		t.Error("a refused prompt reached codex")
	}
}

func TestSlashCommands(t *testing.T) {
	h := newHarness(t, []step{
		expect(`"method":"thread/compact/start"`), startedTurn(), endTurn("completed"),
		expect(`"method":"review/start"`), startedTurn(), endTurn("completed"),
		expect(`"method":"review/start"`), startedTurn(), endTurn("completed"),
		expect(`"method":"turn/start"`), startedTurn(), endTurn("completed"),
		expect(`"method":"turn/start"`), startedTurn(), endTurn("completed"),
	})
	sid := h.start()
	for _, text := range []string{"/compact", "/review", "/review check the tests", "/deploy to staging", "/nosuch thing"} {
		if got := stopReason(t, h.await(h.promptText(sid, text))); got != "end_turn" {
			t.Fatalf("%s: %s", text, got)
		}
	}
	log := readLog(h.log)
	var reviews, turns []string
	for _, l := range log {
		if strings.HasPrefix(l, "IN: ") && strings.Contains(l, `"method":"review/start"`) {
			reviews = append(reviews, l)
		}
		if strings.HasPrefix(l, "IN: ") && strings.Contains(l, `"method":"turn/start"`) {
			turns = append(turns, l)
		}
	}
	if len(reviews) != 2 || !strings.Contains(reviews[0], `"uncommittedChanges"`) || !strings.Contains(reviews[1], `"custom"`) || !strings.Contains(reviews[1], "check the tests") {
		t.Errorf("reviews = %v", reviews)
	}
	if len(turns) != 2 || !strings.Contains(turns[0], `"type":"skill"`) || !strings.Contains(turns[0], `/s/deploy/SKILL.md`) || !strings.Contains(turns[0], "to staging") {
		t.Errorf("skill turn = %v", turns)
	}
	if !strings.Contains(turns[1], `"text":"/nosuch thing"`) {
		t.Errorf("unknown slash turn = %v", turns[1])
	}
}

func TestModesAndModels(t *testing.T) {
	h := newHarness(t, []step{
		expect(`"method":"turn/start"`), startedTurn(), endTurn("completed"),
	})
	sid := h.start()
	if r := h.call("session/set_mode", map[string]any{"sessionId": sid, "modeId": "nope"}); r.Error == nil {
		t.Error("an unknown mode was accepted")
	}
	if r := h.call("session/set_model", map[string]any{"sessionId": sid, "modelId": "nope"}); r.Error == nil {
		t.Error("an unknown model was accepted")
	}
	if r := h.call("session/set_mode", map[string]any{"sessionId": sid, "modeId": "read-only"}); r.Error != nil {
		t.Fatal(r.Error)
	}
	if r := h.call("session/set_model", map[string]any{"sessionId": sid, "modelId": "gpt-small"}); r.Error != nil {
		t.Fatal(r.Error)
	}
	if stopReason(t, newText(h, sid)) != "end_turn" {
		t.Fatal("bad stop")
	}
	turn := logFind(readLog(h.log), `"method":"turn/start"`)
	for _, want := range []string{`"type":"readOnly"`, `"approvalPolicy":"on-request"`, `"model":"gpt-small"`} {
		if !strings.Contains(turn, want) {
			t.Errorf("turn/start lacks %s: %s", want, turn)
		}
	}
	found := false
	for _, u := range h.updates() {
		found = found || (u["sessionUpdate"] == "current_mode_update" && u["currentModeId"] == "read-only")
	}
	if !found {
		t.Error("no current_mode_update")
	}
}

func TestQueueRunsOneTurnAtATime(t *testing.T) {
	h := newHarness(t, []step{
		expect(`"method":"turn/start"`), startedTurn(),
		absent(`"method":"turn/start"`, 300),
		delta("a", "one"), endTurn("completed"),
		expect(`"method":"turn/start"`),
		note("turn/started", `{"threadId":"thr-1","turn":{"id":"turn-1","status":"inProgress"}}`),
		delta("b", "two"), endTurn("completed"),
	})
	sid := h.start()
	a := h.promptText(sid, "first")
	b := h.promptText(sid, "second")
	if stopReason(t, h.await(a)) != "end_turn" || stopReason(t, h.await(b)) != "end_turn" {
		t.Fatal("bad stops")
	}
	if logHas(readLog(h.log), "VIOLATION") || len(readLog(h.log)) == 0 {
		t.Errorf("the second turn started early: %v", readLog(h.log))
	}
	for _, l := range readLog(h.log) {
		if strings.HasPrefix(l, "VIOLATION") {
			t.Error(l)
		}
	}
}

func TestResume(t *testing.T) {
	h := newHarness(t, []step{expect(`"method":"turn/start"`), startedTurn(), endTurn("completed")})
	h.call("initialize", map[string]any{"protocolVersion": 1})
	r := h.call("session/resume", map[string]any{"sessionId": "thr-1", "cwd": h.cwd})
	if r.Error != nil || !strings.Contains(string(r.Result), `"sessionId":"thr-1"`) {
		t.Fatalf("resume = %+v", r)
	}
	tr := logFind(readLog(h.log), `"method":"thread/resume"`)
	if !strings.Contains(tr, `"threadId":"thr-1"`) || logHas(readLog(h.log), `"method":"thread/start"`) {
		t.Errorf("resume request = %s", tr)
	}
	if stopReason(t, newText(h, "thr-1")) != "end_turn" {
		t.Fatal("bad stop")
	}
	// a second resume of an open session, and an id with odd characters
	if r := h.call("session/resume", map[string]any{"sessionId": "thr-1", "cwd": h.cwd}); r.Error == nil {
		t.Error("an open session was resumed again")
	}
	if r := h.call("session/resume", map[string]any{"sessionId": "../x\"", "cwd": h.cwd}); r.Error == nil {
		t.Error("a bad session id was accepted")
	}
}

func TestMCPServersGoInTheThreadConfig(t *testing.T) {
	h := newHarness(t, nil)
	h.call("initialize", map[string]any{"protocolVersion": 1})
	before := tempDirsLike("flockdeck-")
	r := h.call("session/new", map[string]any{"cwd": h.cwd, "mcpServers": []any{
		map[string]any{"name": "tools", "command": "mcp-tools", "args": []string{"--x"}, "env": []any{map[string]any{"name": "TOKEN", "value": "sekrit"}}},
		map[string]any{"type": "http", "name": "web", "url": "https://example.test/mcp", "headers": []any{map[string]any{"name": "Authorization", "value": "Bearer sekrit"}}},
	}})
	if r.Error != nil {
		t.Fatal(r.Error)
	}
	ts := logFind(readLog(h.log), `"method":"thread/start"`)
	for _, want := range []string{`"mcp_servers"`, `"tools":{"args":["--x"],"command":"mcp-tools","env":{"TOKEN":"sekrit"}}`, `"url":"https://example.test/mcp"`, `"http_headers"`} {
		if !strings.Contains(ts, want) {
			t.Errorf("thread/start lacks %s: %s", want, ts)
		}
	}
	for _, l := range readLog(h.log) {
		if strings.HasPrefix(l, "ARGS:") && strings.Contains(l, "sekrit") {
			t.Error("a secret is on the command line")
		}
	}
	if after := tempDirsLike("flockdeck-"); len(after) != len(before) {
		t.Errorf("session/new left files in the temp directory: %v", after)
	}
	for name, bad := range map[string]map[string]any{
		"sse":      {"type": "sse", "name": "s", "url": "https://x.test"},
		"no name":  {"command": "x"},
		"odd name": {"name": "a.b", "command": "x"},
		"no cmd":   {"name": "ok"},
	} {
		r := h.call("session/new", map[string]any{"cwd": h.cwd, "mcpServers": []any{bad}})
		if r.Error == nil || r.Error.Code != codeInvalidParams {
			t.Errorf("%s: %+v", name, r)
		}
	}
}

func tempDirsLike(prefix string) []string {
	es, _ := os.ReadDir(os.TempDir())
	var out []string
	for _, e := range es {
		if strings.HasPrefix(e.Name(), prefix) && !strings.HasPrefix(e.Name(), "flockdeck-test") {
			out = append(out, e.Name())
		}
	}
	return out
}
