package claudeacp

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestSessionNew(t *testing.T) {
	h := newHarness(t, []step{expect("never")})
	r := h.call("initialize", map[string]any{"protocolVersion": 1})
	var init struct {
		ProtocolVersion   int `json:"protocolVersion"`
		AgentCapabilities struct {
			LoadSession        bool            `json:"loadSession"`
			PromptCapabilities map[string]bool `json:"promptCapabilities"`
		} `json:"agentCapabilities"`
		AuthMethods []any `json:"authMethods"`
	}
	_ = json.Unmarshal(r.Result, &init)
	if init.ProtocolVersion != 1 || init.AgentCapabilities.LoadSession || !init.AgentCapabilities.PromptCapabilities["image"] || init.AuthMethods == nil {
		t.Errorf("initialize = %s", r.Result)
	}

	r = h.call("session/new", map[string]any{"cwd": h.cwd, "mcpServers": []any{}})
	var res struct {
		SessionID string `json:"sessionId"`
		Modes     struct {
			Current   string           `json:"currentModeId"`
			Available []map[string]any `json:"availableModes"`
		} `json:"modes"`
		Models struct {
			Current   string           `json:"currentModelId"`
			Available []map[string]any `json:"availableModels"`
		} `json:"models"`
	}
	_ = json.Unmarshal(r.Result, &res)
	if len(res.SessionID) != 36 {
		t.Errorf("session id %q is not a uuid", res.SessionID)
	}
	if res.Modes.Current != "default" || len(res.Modes.Available) != 3 {
		t.Errorf("modes = %+v", res.Modes)
	}
	if res.Models.Current != "default" || len(res.Models.Available) != 2 || res.Models.Available[1]["modelId"] != "haiku" {
		t.Errorf("models = %+v", res.Models)
	}

	// the commands follow the answer, without the slash
	m := h.waitFor(func(m rpcMsg) bool {
		return m.Method == "session/update" && strings.Contains(string(m.Params), "available_commands_update")
	}, 5*time.Second)
	var up struct {
		Update struct {
			Commands []struct {
				Name        string
				Description string
				Input       map[string]string
			} `json:"availableCommands"`
		} `json:"update"`
	}
	_ = json.Unmarshal(m.Params, &up)
	got := up.Update.Commands
	if len(got) != 2 || got[0].Name != "compact" || got[1].Name != "review" || got[0].Input["hint"] == "" || got[1].Input != nil {
		t.Errorf("commands = %+v", got)
	}

	log := readLog(h.log)
	args := log[0]
	for _, want := range []string{"-p", "--input-format stream-json", "--output-format stream-json", "--verbose", "--include-partial-messages", "--permission-prompt-tool stdio", "--session-id " + res.SessionID} {
		if !strings.Contains(args, want) {
			t.Errorf("claude was started with %q, missing %q", args, want)
		}
	}
	if strings.Contains(args, "--mcp-config") {
		t.Errorf("no MCP servers were given, but %q has --mcp-config", args)
	}
	if !logHas(log, `"subtype":"initialize"`) {
		t.Errorf("claude was not sent an initialize control request: %v", log)
	}
}

func TestSessionNewRefusals(t *testing.T) {
	t.Run("relative cwd", func(t *testing.T) {
		h := newHarness(t, nil)
		r := h.call("session/new", map[string]any{"cwd": "work", "mcpServers": []any{}})
		if r.Error == nil || r.Error.Code != codeInvalidParams {
			t.Errorf("got %+v", r)
		}
	})
	t.Run("claude is missing", func(t *testing.T) {
		h := newHarnessWith(t, `C:\no\such\place\claude.exe`, nil)
		r := h.call("session/new", map[string]any{"cwd": h.cwd, "mcpServers": []any{}})
		if r.Error == nil || r.Error.Message != "Claude Code is unavailable" {
			t.Fatalf("got %+v", r)
		}
		if strings.Contains(r.Error.Message, "no such") {
			t.Error("the error names the missing file")
		}
	})
	t.Run("claude exits at once", func(t *testing.T) {
		h := newHarness(t, []step{exitWith(1)})
		r := h.call("session/new", map[string]any{"cwd": h.cwd, "mcpServers": []any{}})
		if r.Error == nil || r.Error.Message != "Claude Code is unavailable" {
			t.Errorf("got %+v", r)
		}
	})
	t.Run("unknown method", func(t *testing.T) {
		h := newHarness(t, nil)
		r := h.call("session/load", map[string]any{})
		if r.Error == nil || r.Error.Code != codeMethodNotFound {
			t.Errorf("got %+v", r)
		}
	})
}

func TestMCPServersReachClaude(t *testing.T) {
	h := newHarness(t, nil)
	h.call("initialize", map[string]any{"protocolVersion": 1})
	r := h.call("session/new", map[string]any{"cwd": h.cwd, "mcpServers": []any{
		map[string]any{"name": "pingsrv", "command": "ping-mcp", "args": []string{"--x"}, "env": []any{map[string]any{"name": "K", "value": "V"}}},
		map[string]any{"type": "http", "name": "web", "url": "https://example.test/mcp", "headers": []any{map[string]any{"name": "Authorization", "value": "Bearer t"}}},
	}})
	if r.Error != nil {
		t.Fatal(r.Error)
	}
	args := readLog(h.log)[0]
	i := strings.Index(args, "--mcp-config ")
	if i < 0 {
		t.Fatalf("no --mcp-config in %q", args)
	}
	path := strings.Fields(args[i:])[1]
	if cfg := string(readFile(t, path)); !strings.Contains(cfg, `"pingsrv"`) || !strings.Contains(cfg, "example.test/mcp") {
		t.Errorf("config = %s", cfg)
	}
	// the file is private to the session: it is gone when the session ends
	h.in.Close()
	<-h.stopped
	if _, err := os.Stat(path); err == nil {
		t.Errorf("%s was left behind", path)
	}
}

func TestWriteMCPConfig(t *testing.T) {
	path, err := writeMCPConfig([]json.RawMessage{
		json.RawMessage(`{"name":"pingsrv","command":"ping-mcp","args":["--x"],"env":[{"name":"K","value":"V"}]}`),
		json.RawMessage(`{"type":"sse","name":"web","url":"https://example.test/sse","headers":[{"name":"A","value":"B"}]}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer removeFile(path)
	var got map[string]map[string]map[string]any
	b := readFile(t, path)
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]map[string]any{
		"pingsrv": {"type": "stdio", "command": "ping-mcp", "args": []any{"--x"}, "env": map[string]any{"K": "V"}},
		"web":     {"type": "sse", "url": "https://example.test/sse", "headers": map[string]any{"A": "B"}},
	}
	if !reflect.DeepEqual(got["mcpServers"], want) {
		t.Errorf("config = %s", b)
	}
	for _, bad := range []string{`{"command":"x"}`, `{"name":"x"}`, `[]`} {
		if p, err := writeMCPConfig([]json.RawMessage{json.RawMessage(bad)}); err == nil {
			removeFile(p)
			t.Errorf("%s was accepted", bad)
		}
	}
	if p, err := writeMCPConfig(nil); p != "" || err != nil {
		t.Errorf("no servers: %q, %v", p, err)
	}
}

// A table of turns: what claude says, what the ACP client sees.
func TestTurns(t *testing.T) {
	type want struct {
		stop    string
		errCode int
		kinds   []string // the update kinds, after available_commands_update
		message string   // the agent_message_chunk text, joined
		thought string
		stdin   []string // substrings that must be in claude's stdin
		noStdin []string // ... and must not
	}
	tests := []struct {
		name   string
		steps  []step
		onReq  func(m rpcMsg) any
		prompt []any
		want   want
	}{
		{
			name: "streamed text",
			steps: []step{expect(`"role":"user"`), emit(lInit), emit(lMsgStart),
				emit(textDelta("Hel")), emit(textDelta("lo")),
				emit(`{"type":"assistant","message":{"id":"msg_1","content":[{"type":"text","text":"Hello"}],"usage":{"input_tokens":2,"output_tokens":3,"cache_read_input_tokens":16829,"cache_creation_input_tokens":6973}},"parent_tool_use_id":null}`),
				emit(lResultOK)},
			want: want{stop: "end_turn", kinds: []string{"agent_message_chunk", "agent_message_chunk", "usage_update"}, message: "Hello",
				stdin: []string{`"content":[{"text":"hi","type":"text"}]`}},
		},
		{
			name: "no partial messages: the whole message is sent once",
			steps: []step{expect(`"role":"user"`), emit(lInit),
				emit(`{"type":"assistant","message":{"id":"msg_9","content":[{"type":"thinking","thinking":"hm"},{"type":"text","text":"Whole"}]},"parent_tool_use_id":null}`),
				emit(lResultOK)},
			want: want{stop: "end_turn", kinds: []string{"agent_thought_chunk", "agent_message_chunk", "usage_update"}, message: "Whole", thought: "hm"},
		},
		{
			name: "thinking deltas",
			steps: []step{expect(`"role":"user"`), emit(lMsgStart),
				emit(`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"ponder"}},"parent_tool_use_id":null}`),
				emit(`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"x"}},"parent_tool_use_id":null}`),
				emit(lResultOK)},
			want: want{stop: "end_turn", kinds: []string{"agent_thought_chunk", "usage_update"}, thought: "ponder"},
		},
		{
			name: "subagent text is left out",
			steps: []step{expect(`"role":"user"`),
				emit(`{"type":"assistant","message":{"id":"msg_s","content":[{"type":"text","text":"inner"}]},"parent_tool_use_id":"toolu_T"}`),
				emit(lResultOK)},
			want: want{stop: "end_turn", kinds: []string{"usage_update"}},
		},
		{
			name: "bash call and result",
			steps: []step{expect(`"role":"user"`),
				emit(`{"type":"assistant","message":{"id":"m","content":[{"type":"tool_use","id":"toolu_B","name":"Bash","input":{"command":"echo hi\nls","description":"say hi"}}]},"parent_tool_use_id":null}`),
				emit(`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_B","content":"hi\nfile","is_error":false}]},"parent_tool_use_id":null}`),
				emit(lResultOK)},
			want: want{stop: "end_turn", kinds: []string{"tool_call", "tool_call_update", "usage_update"}},
		},
		{
			name: "tool result as blocks, failed",
			steps: []step{expect(`"role":"user"`),
				emit(`{"type":"assistant","message":{"id":"m","content":[{"type":"tool_use","id":"toolu_R","name":"Read","input":{"file_path":"x.txt","offset":4}}]},"parent_tool_use_id":null}`),
				emit(`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_R","content":[{"type":"text","text":"no such file"}],"is_error":true}]},"parent_tool_use_id":null}`),
				emit(lResultOK)},
			want: want{stop: "end_turn", kinds: []string{"tool_call", "tool_call_update", "usage_update"}},
		},
		{
			name: "todo list becomes a plan",
			steps: []step{expect(`"role":"user"`),
				emit(`{"type":"assistant","message":{"id":"m","content":[{"type":"tool_use","id":"toolu_P","name":"TodoWrite","input":{"todos":[{"content":"a","status":"in_progress","activeForm":"doing a"},{"content":"b","status":"odd"}]}}]},"parent_tool_use_id":null}`),
				emit(`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_P","content":"ok"}]},"parent_tool_use_id":null}`),
				emit(lResultOK)},
			want: want{stop: "end_turn", kinds: []string{"plan", "usage_update"}},
		},
		{
			name: "permission allowed once",
			steps: []step{expect(`"role":"user"`), emit(lToolUseWrite), emit(lPermWrite),
				expect(`"request_id":"perm-1"`),
				emit(`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_W1","content":"File created"}]},"parent_tool_use_id":null}`),
				emit(lResultOK)},
			onReq: func(rpcMsg) any { return outcome("allow") },
			want: want{stop: "end_turn", kinds: []string{"tool_call", "tool_call_update", "tool_call_update", "usage_update"},
				stdin: []string{`"behavior":"allow"`, `"updatedInput":{"file_path":"C:\\scratch\\a.txt","content":"hi"}`}},
		},
		{
			name: "permission rejected",
			steps: []step{expect(`"role":"user"`), emit(lToolUseWrite), emit(lPermWrite),
				expect(`"request_id":"perm-1"`),
				emit(`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_W1","content":"The user rejected this tool call.","is_error":true}]},"parent_tool_use_id":null}`),
				emit(lResultOK)},
			onReq: func(rpcMsg) any { return outcome("reject") },
			want: want{stop: "end_turn", kinds: []string{"tool_call", "tool_call_update", "usage_update"},
				stdin: []string{`"behavior":"deny"`, `The user rejected this tool call.`}, noStdin: []string{`"behavior":"allow"`}},
		},
		{
			name:  "permission answered cancelled",
			steps: []step{expect(`"role":"user"`), emit(lPermWrite), expect(`"request_id":"perm-1"`), emit(lResultOK)},
			onReq: func(rpcMsg) any { return cancelledOutcome() },
			want: want{stop: "end_turn", kinds: []string{"tool_call", "usage_update"},
				stdin: []string{`"behavior":"deny"`}, noStdin: []string{`"behavior":"allow"`}},
		},
		{
			name:  "request before the tool_use line announces the tool",
			steps: []step{expect(`"role":"user"`), emit(lPermWrite), expect(`"request_id":"perm-1"`), emit(lResultOK)},
			onReq: func(rpcMsg) any { return outcome("allow") },
			want:  want{stop: "end_turn", kinds: []string{"tool_call", "tool_call_update", "usage_update"}},
		},
		{
			name:  "image block",
			steps: []step{expect(`"role":"user"`), emit(lResultOK)},
			prompt: []any{
				map[string]any{"type": "text", "text": "what colour?"},
				map[string]any{"type": "image", "data": "UE5H", "mimeType": "image/png"},
			},
			want: want{stop: "end_turn", kinds: []string{"usage_update"},
				stdin: []string{`"text":"what colour?"`, `"source":{"data":"UE5H","media_type":"image/png","type":"base64"}`, `"type":"image"`}},
		},
		{
			name:  "text resource and link",
			steps: []step{expect(`"role":"user"`), emit(lResultOK)},
			prompt: []any{
				map[string]any{"type": "text", "text": "see"},
				map[string]any{"type": "resource", "resource": map[string]any{"uri": "file:///a.go", "text": "package a"}},
				map[string]any{"type": "resource_link", "name": "spec", "uri": "https://x.test/spec"},
			},
			want: want{stop: "end_turn", kinds: []string{"usage_update"},
				stdin: []string{`package a`, `file:///a.go`, `[spec](https://x.test/spec)`}},
		},
		{
			name:   "slash command goes as text",
			steps:  []step{expect(`/review 12`), emit(resultWith("done"))},
			prompt: []any{map[string]any{"type": "text", "text": "/review 12"}},
			want:   want{stop: "end_turn", kinds: []string{"usage_update"}, stdin: []string{`"text":"/review 12"`}},
		},
		{
			name:  "max turns",
			steps: []step{expect(`"role":"user"`), emit(strings.Replace(lResultOK, `"terminal_reason":"completed"`, `"terminal_reason":"max_turns"`, 1))},
			want:  want{stop: "max_turn_requests", kinds: []string{"usage_update"}},
		},
		{
			name:  "max tokens",
			steps: []step{expect(`"role":"user"`), emit(strings.Replace(lResultOK, `"stop_reason":"end_turn"`, `"stop_reason":"max_tokens"`, 1))},
			want:  want{stop: "max_tokens", kinds: []string{"usage_update"}},
		},
		{
			name:  "api error",
			steps: []step{expect(`"role":"user"`), emit(strings.Replace(strings.Replace(lResultOK, `"is_error":false`, `"is_error":true`, 1), `"result":"one"`, `"result":"API Error: 529 overloaded"`, 1))},
			want:  want{errCode: codeInternal, kinds: []string{"usage_update"}},
		},
		{
			name:  "not logged in",
			steps: []step{expect(`"role":"user"`), emit(strings.Replace(strings.Replace(lResultOK, `"is_error":false`, `"is_error":true`, 1), `"result":"one"`, `"result":"Invalid API key · Please run /login"`, 1))},
			want:  want{errCode: codeAuthRequired, kinds: []string{"usage_update"}},
		},
		{
			name:  "claude exits mid-turn",
			steps: []step{expect(`"role":"user"`), emit(lMsgStart), emit(textDelta("par")), exitWith(3)},
			want:  want{errCode: codeInternal, kinds: []string{"agent_message_chunk"}, message: "par"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, tc.steps)
			h.onReq = tc.onReq
			sid := h.start()
			prompt := tc.prompt
			if prompt == nil {
				prompt = []any{map[string]any{"type": "text", "text": "hi"}}
			}
			r := h.call("session/prompt", map[string]any{"sessionId": sid, "prompt": prompt})
			if tc.want.errCode != 0 {
				if r.Error == nil || r.Error.Code != tc.want.errCode {
					t.Errorf("response = %+v %s, want error %d", r.Error, r.Result, tc.want.errCode)
				}
			} else if got := stopReason(t, r); got != tc.want.stop {
				t.Errorf("stop reason = %q, want %q", got, tc.want.stop)
			}
			us := h.updates()
			var got []string
			for _, k := range kinds(us) {
				if k != "available_commands_update" {
					got = append(got, k)
				}
			}
			if !reflect.DeepEqual(got, tc.want.kinds) {
				t.Errorf("updates = %v, want %v", got, tc.want.kinds)
			}
			if m := joinText(us, "agent_message_chunk"); m != tc.want.message {
				t.Errorf("message = %q, want %q", m, tc.want.message)
			}
			if m := joinText(us, "agent_thought_chunk"); m != tc.want.thought {
				t.Errorf("thought = %q, want %q", m, tc.want.thought)
			}
			// the fake logs a line when the last steps ran; wait for stdin to settle
			time.Sleep(50 * time.Millisecond)
			log := readLog(h.log)
			for _, s := range tc.want.stdin {
				if !logHas(log, s) {
					t.Errorf("claude's stdin lacks %s\n%s", s, strings.Join(log, "\n"))
				}
			}
			for _, s := range tc.want.noStdin {
				if logHas(log, s) {
					t.Errorf("claude's stdin has %s", s)
				}
			}
		})
	}
}

// The shapes of the updates, one by one.
func TestUpdateShapes(t *testing.T) {
	h := newHarness(t, []step{expect(`"role":"user"`),
		emit(`{"type":"assistant","message":{"id":"m","content":[{"type":"tool_use","id":"toolu_E","name":"Edit","input":{"file_path":"C:\\scratch\\a.go","old_string":"x","new_string":"y"}}]},"parent_tool_use_id":null}`),
		emit(`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_E","content":"updated"}]},"parent_tool_use_id":null}`),
		emit(`{"type":"assistant","message":{"id":"m2","content":[{"type":"tool_use","id":"toolu_B","name":"Bash","input":{"command":"go test ./..."}}],"usage":{"input_tokens":5,"output_tokens":20,"cache_read_input_tokens":1000,"cache_creation_input_tokens":0}},"parent_tool_use_id":null}`),
		emit(`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_B","content":"ok"}]},"parent_tool_use_id":null}`),
		emit(lResultOK)})
	sid := h.start()
	stopReason(t, h.await(h.promptText(sid, "go")))
	us := h.updates()
	var calls []map[string]any
	for _, u := range us {
		if k := u["sessionUpdate"]; k == "tool_call" || k == "tool_call_update" || k == "usage_update" {
			calls = append(calls, u)
		}
	}
	if len(calls) != 5 {
		t.Fatalf("got %d updates: %v", len(calls), calls)
	}
	edit := calls[0]
	if edit["toolCallId"] != "toolu_E" || edit["kind"] != "edit" || edit["status"] != "pending" || edit["title"] != `Edit C:\scratch\a.go` {
		t.Errorf("edit call = %v", edit)
	}
	diff := edit["content"].([]any)[0].(map[string]any)
	if diff["type"] != "diff" || diff["oldText"] != "x" || diff["newText"] != "y" || diff["path"] != `C:\scratch\a.go` {
		t.Errorf("edit diff = %v", diff)
	}
	if loc := edit["locations"].([]any)[0].(map[string]any); loc["path"] != `C:\scratch\a.go` {
		t.Errorf("locations = %v", edit["locations"])
	}
	if done := calls[1]; done["status"] != "completed" || done["content"] != nil {
		t.Errorf("an edit's result keeps its diff: %v", done)
	}
	if b := calls[2]; b["kind"] != "execute" || b["title"] != "go test ./..." {
		t.Errorf("bash call = %v", b)
	}
	bd := calls[3]["content"].([]any)[0].(map[string]any)["content"].(map[string]any)
	if bd["text"] != "ok" {
		t.Errorf("bash result = %v", calls[3])
	}
	u := calls[4]
	// context is the last assistant message's usage: 5+20+1000
	if u["used"] != float64(1025) || u["size"] != float64(1000000) {
		t.Errorf("usage = %v", u)
	}
	if c := u["cost"].(map[string]any); c["amount"] != 0.00156 || c["currency"] != "USD" {
		t.Errorf("cost = %v", u["cost"])
	}
}

func TestPermissionRequestShape(t *testing.T) {
	h := newHarness(t, []step{expect(`"role":"user"`), emit(lToolUseWrite), emit(lPermWrite), expect(`"request_id":"perm-1"`), emit(lResultOK)})
	sid := h.start()
	stopReason(t, h.await(h.promptText(sid, "write")))
	reqs := h.requests("session/request_permission")
	if len(reqs) != 1 {
		t.Fatalf("%d permission requests", len(reqs))
	}
	var p struct {
		SessionID string `json:"sessionId"`
		ToolCall  struct {
			ID       string          `json:"toolCallId"`
			Title    string          `json:"title"`
			Kind     string          `json:"kind"`
			Status   string          `json:"status"`
			RawInput json.RawMessage `json:"rawInput"`
		} `json:"toolCall"`
		Options []struct{ OptionID, Name, Kind string } `json:"options"`
	}
	_ = json.Unmarshal(reqs[0].Params, &p)
	if p.SessionID != sid || p.ToolCall.ID != "toolu_W1" || p.ToolCall.Kind != "edit" || p.ToolCall.Status != "pending" || !strings.Contains(string(p.ToolCall.RawInput), `"content":"hi"`) {
		t.Errorf("request = %s", reqs[0].Params)
	}
	var opts []string
	for _, o := range p.Options {
		opts = append(opts, o.OptionID+"/"+o.Kind)
	}
	if !reflect.DeepEqual(opts, []string{"allow/allow_once", "reject/reject_once"}) {
		t.Errorf("options = %v", opts)
	}
}

func TestOtherControlRequestsAreRefused(t *testing.T) {
	h := newHarness(t, []step{expect(`"role":"user"`),
		emit(`{"type":"control_request","request_id":"hook-1","request":{"subtype":"hook_callback","callback_id":"x"}}`),
		expect(`"request_id":"hook-1"`),
		emit(`{"type":"control_request","request_id":"ask-1","request":{"subtype":"can_use_tool","tool_name":"AskUserQuestion","input":{"questions":[]},"tool_use_id":"toolu_Q"}}`),
		expect(`"request_id":"ask-1"`),
		emit(lResultOK)})
	sid := h.start()
	stopReason(t, h.await(h.promptText(sid, "go")))
	log := readLog(h.log)
	if !logHas(log, `"error":"not supported by this client: hook_callback"`) {
		t.Errorf("hook_callback was not refused: %v", log)
	}
	if !logHas(log, `"behavior":"deny"`) || len(h.requests("session/request_permission")) != 0 {
		t.Errorf("AskUserQuestion was not denied without a card: %v", log)
	}
}

func TestCancel(t *testing.T) {
	t.Run("running turn", func(t *testing.T) {
		h := newHarness(t, []step{expect(`"role":"user"`), emit(lMsgStart), emit(textDelta("working")),
			expect(`"subtype":"interrupt"`), emit(`{"type":"control_cancel_request","request_id":"none"}`), emit(lResultAborted)})
		sid := h.start()
		id := h.promptText(sid, "slow")
		h.waitFor(func(m rpcMsg) bool { return strings.Contains(string(m.Params), "working") }, 5*time.Second)
		h.notify("session/cancel", map[string]any{"sessionId": sid})
		if got := stopReason(t, h.await(id)); got != "cancelled" {
			t.Errorf("stop reason = %q", got)
		}
		// the process stays: the next prompt runs
	})
	t.Run("open permission", func(t *testing.T) {
		h := newHarness(t, []step{expect(`"role":"user"`), emit(lToolUseWrite), emit(lPermWrite),
			expect(`"subtype":"interrupt"`), emit(`{"type":"control_cancel_request","request_id":"perm-1"}`), emit(lResultAborted)})
		release := make(chan struct{})
		h.onReq = func(rpcMsg) any { <-release; return cancelledOutcome() }
		defer close(release)
		sid := h.start()
		id := h.promptText(sid, "write")
		h.waitFor(func(m rpcMsg) bool { return m.Method == "session/request_permission" }, 5*time.Second)
		h.notify("session/cancel", map[string]any{"sessionId": sid})
		if got := stopReason(t, h.await(id)); got != "cancelled" {
			t.Errorf("stop reason = %q", got)
		}
		if logHas(readLog(h.log), `"request_id":"perm-1","response"`) || logHas(readLog(h.log), `"behavior"`) {
			t.Errorf("a retracted request was answered: %v", readLog(h.log))
		}
	})
	t.Run("idle", func(t *testing.T) {
		h := newHarness(t, []step{expect(`"role":"user"`), emit(lResultOK)})
		sid := h.start()
		h.notify("session/cancel", map[string]any{"sessionId": sid})
		// a cancel with no turn must not cancel the next prompt
		if got := stopReason(t, h.await(h.promptText(sid, "hi"))); got != "end_turn" {
			t.Errorf("stop reason = %q", got)
		}
		if logHas(readLog(h.log), `"subtype":"interrupt"`) {
			t.Error("an interrupt was sent with no turn running")
		}
	})
	t.Run("interrupt that does nothing ends claude", func(t *testing.T) {
		h := newHarness(t, []step{expect(`"role":"user"`), sleep(60000)})
		sid := h.start()
		id := h.promptText(sid, "hang")
		time.Sleep(200 * time.Millisecond)
		h.notify("session/cancel", map[string]any{"sessionId": sid})
		r := h.await(id)
		if r.Error == nil && stopReason(t, r) != "cancelled" {
			t.Errorf("response = %s", r.Result)
		}
	})
}

// A prompt that arrives during a turn waits for it: its own turn, its own stop
// reason, nothing sent to claude until the first result.
func TestMidTurnPromptIsQueued(t *testing.T) {
	h := newHarness(t, []step{
		expect("FIRST"), absent("SECOND", 400), emit(resultWith("one")),
		expect("SECOND"), emit(resultWith("two")),
	})
	sid := h.start()
	a := h.promptText(sid, "FIRST")
	b := h.promptText(sid, "SECOND")
	ra, rb := h.await(a), h.await(b)
	if stopReason(t, ra) != "end_turn" || stopReason(t, rb) != "end_turn" {
		t.Errorf("responses: %+v %+v", ra, rb)
	}
	log := readLog(h.log)
	if logHas(log, "VIOLATION") {
		t.Errorf("the second prompt reached claude during the first turn: %v", log)
	}
	// the answers come in order
	order := []string{}
	h.mu.Lock()
	for _, m := range h.msgs {
		if m.Method == "" && m.ID != nil && (string(*m.ID) == itoa(a) || string(*m.ID) == itoa(b)) {
			order = append(order, string(*m.ID))
		}
	}
	h.mu.Unlock()
	if !reflect.DeepEqual(order, []string{itoa(a), itoa(b)}) {
		t.Errorf("answers came in order %v", order)
	}
}

func TestCancelDropsQueuedPrompts(t *testing.T) {
	h := newHarness(t, []step{expect("FIRST"), expect(`"subtype":"interrupt"`), emit(lResultAborted), absent("SECOND", 300)})
	sid := h.start()
	a := h.promptText(sid, "FIRST")
	b := h.promptText(sid, "SECOND")
	time.Sleep(200 * time.Millisecond)
	h.notify("session/cancel", map[string]any{"sessionId": sid})
	if got := stopReason(t, h.await(a)); got != "cancelled" {
		t.Errorf("first: %q", got)
	}
	if got := stopReason(t, h.await(b)); got != "cancelled" {
		t.Errorf("second: %q", got)
	}
	time.Sleep(400 * time.Millisecond)
	if logHas(readLog(h.log), "VIOLATION") || logHas(readLog(h.log), `"text":"SECOND"`) {
		t.Error("a queued prompt reached claude after the cancel")
	}
}

func TestSetModeAndModel(t *testing.T) {
	h := newHarness(t, []step{expect("never")})
	sid := h.start()

	if r := h.call("session/set_mode", map[string]any{"sessionId": sid, "modeId": "plan"}); r.Error != nil {
		t.Fatalf("set_mode: %v", r.Error)
	}
	m := h.waitFor(func(m rpcMsg) bool { return strings.Contains(string(m.Params), "current_mode_update") }, 5*time.Second)
	if !strings.Contains(string(m.Params), `"currentModeId":"plan"`) {
		t.Errorf("update = %s", m.Params)
	}
	if r := h.call("session/set_mode", map[string]any{"sessionId": sid, "modeId": "bypassPermissions"}); r.Error == nil || r.Error.Code != codeInvalidParams {
		t.Errorf("bypassPermissions was accepted: %+v", r)
	}
	if r := h.call("session/set_model", map[string]any{"sessionId": sid, "modelId": "haiku"}); r.Error != nil {
		t.Fatalf("set_model: %v", r.Error)
	}
	if r := h.call("session/set_model", map[string]any{"sessionId": "nope", "modelId": "haiku"}); r.Error == nil {
		t.Error("an unknown session was accepted")
	}
	log := readLog(h.log)
	if !logHas(log, `"subtype":"set_permission_mode"`) || !logHas(log, `"mode":"plan"`) || !logHas(log, `"subtype":"set_model"`) || !logHas(log, `"model":"haiku"`) {
		t.Errorf("claude was sent: %v", log)
	}
	if logHas(log, "bypassPermissions") {
		t.Error("the refused mode reached claude")
	}
}

func TestModeChangedByClaudeIsReported(t *testing.T) {
	h := newHarness(t, []step{expect(`"role":"user"`),
		emit(strings.Replace(lInit, `"permissionMode":"default"`, `"permissionMode":"acceptEdits"`, 1)), emit(lResultOK)})
	sid := h.start()
	stopReason(t, h.await(h.promptText(sid, "hi")))
	got := false
	for _, u := range h.updates() {
		got = got || (u["sessionUpdate"] == "current_mode_update" && u["currentModeId"] == "acceptEdits")
	}
	if !got {
		t.Errorf("updates = %v", kinds(h.updates()))
	}
}

func TestPromptRefusals(t *testing.T) {
	h := newHarness(t, nil)
	sid := h.start()
	for name, prompt := range map[string]any{
		"audio":      []any{map[string]any{"type": "audio", "data": "x", "mimeType": "audio/wav"}},
		"empty":      []any{},
		"bad image":  []any{map[string]any{"type": "image"}},
		"not a list": "hello",
	} {
		r := h.call("session/prompt", map[string]any{"sessionId": sid, "prompt": prompt})
		if r.Error == nil || r.Error.Code != codeInvalidParams {
			t.Errorf("%s: %+v", name, r)
		}
	}
	if r := h.call("session/prompt", map[string]any{"sessionId": "nope", "prompt": []any{}}); r.Error == nil {
		t.Error("unknown session")
	}
}

func TestPromptAfterClaudeExits(t *testing.T) {
	h := newHarness(t, []step{expect(`"role":"user"`), exitWith(0)})
	sid := h.start()
	if r := h.await(h.promptText(sid, "one")); r.Error == nil {
		t.Fatalf("the first prompt did not fail: %s", r.Result)
	}
	r := h.await(h.promptText(sid, "two"))
	if r.Error == nil {
		t.Errorf("a prompt to a dead session succeeded: %s", r.Result)
	}
}

func TestClassify(t *testing.T) {
	tests := []struct {
		name      string
		l         line
		cancelled bool
		stop      string
		code      int
	}{
		{"end", line{Subtype: "success", TerminalReas: "completed", StopReason: "end_turn"}, false, "end_turn", 0},
		{"cancelled wins", line{Subtype: "success"}, true, "cancelled", 0},
		{"aborted without a cancel", line{Subtype: "error_during_execution", IsError: true, TerminalReas: "aborted_tools"}, false, "", codeInternal},
		{"error_max_turns", line{Subtype: "error_max_turns"}, false, "max_turn_requests", 0},
		{"refusal", line{Subtype: "success", StopReason: "refusal"}, false, "refusal", 0},
		{"execution error", line{Subtype: "error_during_execution"}, false, "", codeInternal},
		{"login", line{IsError: true, Result: "Not logged in · Please run /login"}, false, "", codeAuthRequired},
	}
	for _, tc := range tests {
		stop, err := classify(tc.l, tc.cancelled)
		code := 0
		if err != nil {
			code = err.Code
		}
		if stop != tc.stop || code != tc.code {
			t.Errorf("%s: got %q %d, want %q %d", tc.name, stop, code, tc.stop, tc.code)
		}
	}
}

func TestDescribeTool(t *testing.T) {
	tests := []struct {
		tool, input, title, kind string
	}{
		{"Read", `{"file_path":"/a/b.go","offset":10}`, "Read /a/b.go", "read"},
		{"Write", `{"file_path":"/a/new.go","content":"x"}`, "Write /a/new.go", "edit"},
		{"MultiEdit", `{"file_path":"/a/b.go","edits":[{"old_string":"1","new_string":"2"},{"old_string":"3","new_string":"4"}]}`, "Edit /a/b.go", "edit"},
		{"NotebookEdit", `{"notebook_path":"/a/n.ipynb"}`, "Edit /a/n.ipynb", "edit"},
		{"Bash", `{"command":"  echo a\necho b "}`, "echo a …", "execute"},
		{"PowerShell", `{"command":"Get-ChildItem"}`, "Get-ChildItem", "execute"},
		{"Glob", `{"pattern":"**/*.go"}`, "Find **/*.go", "search"},
		{"Grep", `{"pattern":"TODO","path":"/a"}`, "Search TODO", "search"},
		{"WebFetch", `{"url":"https://x.test"}`, "Fetch https://x.test", "fetch"},
		{"WebSearch", `{"query":"go acp"}`, "Search the web: go acp", "fetch"},
		{"Task", `{"description":"Explore the tree"}`, "Explore the tree", "think"},
		{"ExitPlanMode", `{"plan":"p"}`, "Exit plan mode", "switch_mode"},
		{"mcp__pingsrv__ping", `{}`, "pingsrv: ping", "other"},
		{"Mystery", `not json`, "Mystery", "other"},
	}
	for _, tc := range tests {
		d := describeTool(tc.tool, json.RawMessage(tc.input), "/work", "")
		if d.title != tc.title || d.kind != tc.kind {
			t.Errorf("%s: %q %q, want %q %q", tc.tool, d.title, d.kind, tc.title, tc.kind)
		}
	}
	d := describeTool("Read", json.RawMessage(`{"file_path":"rel.go","offset":10}`), "/work", "")
	if loc := d.locations[0]; loc["line"] != 10 || !strings.HasSuffix(loc["path"].(string), "rel.go") || loc["path"] == "rel.go" {
		t.Errorf("locations = %v", d.locations)
	}
	if d := describeTool("Mystery", nil, "/work", "My tool"); d.title != "My tool" {
		t.Errorf("display name not used: %q", d.title)
	}
	if d := describeTool("Write", json.RawMessage(`{"file_path":"/no/such/file","content":"x"}`), "/", ""); d.content[0].(map[string]any)["oldText"] != nil {
		t.Errorf("a new file has an old text: %v", d.content)
	}
}

func TestResultTextIsCut(t *testing.T) {
	big := `"` + strings.Repeat("x", maxResultText+100) + `"`
	got := resultText(json.RawMessage(big))
	if len(got) > maxResultText+64 || !strings.Contains(got, "cut at 64 KiB") {
		t.Errorf("a %d byte result came back as %d bytes", maxResultText+100, len(got))
	}
	if got := resultText(json.RawMessage(`[{"type":"text","text":"a"},{"type":"image","source":{}},{"type":"text","text":"b"}]`)); got != "a\n[image]\nb" {
		t.Errorf("blocks = %q", got)
	}
}

func TestSessionResume(t *testing.T) {
	const id = "0b7e1f0e-5c2a-4a3e-9d7a-6f1f3f0b9c11"
	h := newHarness(t, nil)
	r := h.call("initialize", map[string]any{"protocolVersion": 1})
	var init struct {
		AgentCapabilities struct {
			LoadSession         bool                       `json:"loadSession"`
			SessionCapabilities map[string]json.RawMessage `json:"sessionCapabilities"`
		} `json:"agentCapabilities"`
	}
	_ = json.Unmarshal(r.Result, &init)
	if _, ok := init.AgentCapabilities.SessionCapabilities["resume"]; !ok || init.AgentCapabilities.LoadSession {
		t.Errorf("initialize = %s", r.Result)
	}

	r = h.call("session/resume", map[string]any{"sessionId": id, "cwd": h.cwd, "mcpServers": []any{}})
	var res struct {
		SessionID string `json:"sessionId"`
		Modes     struct {
			Current string `json:"currentModeId"`
		} `json:"modes"`
	}
	if r.Error != nil {
		t.Fatalf("resume = %+v", r.Error)
	}
	_ = json.Unmarshal(r.Result, &res)
	if res.SessionID != id || res.Modes.Current != "default" {
		t.Errorf("resume answer = %s", r.Result)
	}
	args := readLog(h.log)[0]
	if !strings.Contains(args, "--resume "+id) || strings.Contains(args, "--session-id") {
		t.Errorf("claude was started with %q", args)
	}

	// The session is open: a second resume of it is refused, as is any other
	// id that is not a UUID, and nothing more is started for either.
	if r := h.call("session/resume", map[string]any{"sessionId": id, "cwd": h.cwd, "mcpServers": []any{}}); r.Error == nil || r.Error.Code != codeInvalidParams {
		t.Errorf("a second resume = %+v", r)
	}
	for _, bad := range []string{"", "not-a-uuid", id + " --dangerously-skip-permissions", "-p"} {
		if r := h.call("session/resume", map[string]any{"sessionId": bad, "cwd": h.cwd, "mcpServers": []any{}}); r.Error == nil || r.Error.Code != codeInvalidParams {
			t.Errorf("resume of %q = %+v", bad, r)
		}
	}
	if n := len(readLog(h.log)); n < 1 {
		t.Fatal("no log")
	}
	for _, line := range readLog(h.log) {
		if strings.Contains(line, "dangerously") {
			t.Errorf("claude was started with a bad id: %q", line)
		}
	}
}

func TestSessionResumeOfAnIdClaudeDoesNotKnow(t *testing.T) {
	// claude exits at once for an id it has no conversation for.
	h := newHarness(t, []step{exitWith(1)})
	r := h.call("session/resume", map[string]any{"sessionId": "0b7e1f0e-5c2a-4a3e-9d7a-6f1f3f0b9c11", "cwd": h.cwd, "mcpServers": []any{}})
	if r.Error == nil || r.Error.Message != "Claude Code is unavailable" {
		t.Errorf("got %+v", r)
	}
}
