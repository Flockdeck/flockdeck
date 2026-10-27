package claudeacp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// Recorded lines. They are the shapes in docs/claude-headless-spike.md (claude
// 2.1.311, Haiku 5.5), trimmed to what the adapter reads.
const (
	lInit = `{"type":"system","subtype":"init","cwd":"C:\\scratch","session_id":"SID","tools":["Bash","Write"],"model":"claude-haiku-5-5","permissionMode":"default","slash_commands":["compact","review"],"apiKeySource":"none","claude_code_version":"2.1.311"}`

	lMsgStart = `{"type":"stream_event","event":{"type":"message_start","message":{"id":"msg_1","role":"assistant","content":[]}},"session_id":"SID","parent_tool_use_id":null}`

	lResultOK = `{"type":"result","subtype":"success","is_error":false,"result":"one","stop_reason":"end_turn","terminal_reason":"completed","session_id":"SID","total_cost_usd":0.00156,"num_turns":1,"queued_turn_count":0,"permission_denials":[],"usage":{"input_tokens":2,"output_tokens":3,"cache_read_input_tokens":16829,"cache_creation_input_tokens":6973},"modelUsage":{"claude-haiku-5-5":{"costUSD":0.00156,"contextWindow":1000000}}}`

	lResultAborted = `{"type":"result","subtype":"error_during_execution","is_error":true,"result":"","stop_reason":null,"terminal_reason":"aborted_tools","session_id":"SID","total_cost_usd":0.003,"usage":{"input_tokens":2,"output_tokens":3,"cache_read_input_tokens":100,"cache_creation_input_tokens":0},"modelUsage":{"claude-haiku-5-5":{"contextWindow":1000000}}}`

	lPermWrite = `{"type":"control_request","request_id":"perm-1","request":{"subtype":"can_use_tool","tool_name":"Write","display_name":"Write","input":{"file_path":"C:\\scratch\\a.txt","content":"hi"},"description":"a.txt","permission_suggestions":[{"type":"setMode","mode":"acceptEdits","destination":"session"}],"tool_use_id":"toolu_W1"}}`

	lToolUseWrite = `{"type":"assistant","message":{"id":"msg_2","role":"assistant","content":[{"type":"tool_use","id":"toolu_W1","name":"Write","input":{"file_path":"C:\\scratch\\a.txt","content":"hi"}}],"usage":{"input_tokens":5,"output_tokens":20,"cache_read_input_tokens":1000,"cache_creation_input_tokens":0}},"parent_tool_use_id":null}`
)

func textDelta(s string) string {
	b, _ := json.Marshal(s)
	return `{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":` + string(b) + `}},"parent_tool_use_id":null}`
}

func resultWith(txt string) string {
	return strings.Replace(lResultOK, `"result":"one"`, `"result":"`+txt+`"`, 1)
}

// testBusyIdle is how long a turn of claude's own may be silent before prompts
// stop waiting for it. Long, so that only a test about it sees it.
var testBusyIdle = 30 * time.Second

// harness is the ACP client side of a test: it runs the adapter in this
// process, with the fake claude as a child, and records everything the adapter
// sends.
type harness struct {
	t       *testing.T
	in      io.WriteCloser
	log     string
	cwd     string
	mu      sync.Mutex
	cond    *sync.Cond
	msgs    []rpcMsg
	nextID  int
	onReq   func(m rpcMsg) any // answer to a request from the adapter; nil selects the first allow option
	stopped chan struct{}
	runErr  error
}

func newHarness(t *testing.T, steps []step) *harness {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return newHarnessWith(t, exe, steps)
}

func newHarnessWith(t *testing.T, claudePath string, steps []step, extraEnv ...string) *harness {
	t.Helper()
	env, logPath := fakeEnv(t, steps)
	env = append(env, extraEnv...)
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	h := &harness{t: t, in: inW, log: logPath, cwd: t.TempDir(), stopped: make(chan struct{})}
	h.cond = sync.NewCond(&h.mu)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		h.runErr = Run(ctx, inR, outW, Options{
			ClaudePath: claudePath, Env: env, Version: "test",
			Stderr: testWriter{t}, ControlTimeout: 10 * time.Second, CancelGrace: 3 * time.Second, BusyIdle: testBusyIdle,
		})
		outW.Close()
		close(h.stopped)
	}()
	go func() {
		br := bufio.NewReaderSize(outR, 1<<20)
		for {
			l, err := br.ReadBytes('\n')
			if len(strings.TrimSpace(string(l))) > 0 {
				var m rpcMsg
				if json.Unmarshal(l, &m) != nil {
					t.Errorf("the adapter wrote a line that is not JSON: %s", l)
				} else {
					h.deliver(m)
				}
			}
			if err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		inW.Close()
		select {
		case <-h.stopped:
		case <-time.After(10 * time.Second):
			t.Error("the adapter did not stop when its input closed")
		}
		cancel()
	})
	return h
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	defer func() { _ = recover() }() // a write after the test ended
	w.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

func (h *harness) deliver(m rpcMsg) {
	if m.Method != "" && m.ID != nil {
		go func() {
			var res any
			if h.onReq != nil {
				res = h.onReq(m)
			} else {
				res = outcome("allow")
			}
			if res == nil {
				return
			}
			h.write(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": res})
		}()
	}
	h.mu.Lock()
	h.msgs = append(h.msgs, m)
	h.cond.Broadcast()
	h.mu.Unlock()
}

func outcome(optionID string) any {
	return map[string]any{"outcome": map[string]any{"outcome": "selected", "optionId": optionID}}
}

func cancelledOutcome() any { return map[string]any{"outcome": map[string]any{"outcome": "cancelled"}} }

func (h *harness) write(v any) {
	b, _ := json.Marshal(v)
	_, _ = h.in.Write(append(b, '\n'))
}

// send writes a request and returns its id.
func (h *harness) send(method string, params any) int {
	h.mu.Lock()
	h.nextID++
	id := h.nextID
	h.mu.Unlock()
	h.write(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	return id
}

func (h *harness) notify(method string, params any) {
	h.write(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

// await waits for the response to id.
func (h *harness) await(id int) rpcMsg {
	h.t.Helper()
	raw := json.RawMessage(itoa(id))
	return h.waitFor(func(m rpcMsg) bool { return m.Method == "" && m.ID != nil && string(*m.ID) == string(raw) }, 20*time.Second)
}

func itoa(i int) string { b, _ := json.Marshal(i); return string(b) }

func (h *harness) waitFor(pred func(rpcMsg) bool, d time.Duration) rpcMsg {
	h.t.Helper()
	deadline := time.Now().Add(d)
	timer := time.AfterFunc(d, func() { h.mu.Lock(); h.cond.Broadcast(); h.mu.Unlock() })
	defer timer.Stop()
	h.mu.Lock()
	defer h.mu.Unlock()
	seen := 0
	for {
		for ; seen < len(h.msgs); seen++ {
			if pred(h.msgs[seen]) {
				return h.msgs[seen]
			}
		}
		if time.Now().After(deadline) {
			h.t.Fatalf("timed out; the adapter sent %d messages", len(h.msgs))
		}
		h.cond.Wait()
	}
}

func (h *harness) call(method string, params any) rpcMsg {
	h.t.Helper()
	return h.await(h.send(method, params))
}

// start runs initialize and session/new and returns the session id.
func (h *harness) start() string {
	h.t.Helper()
	if r := h.call("initialize", map[string]any{"protocolVersion": 1}); r.Error != nil {
		h.t.Fatalf("initialize: %v", r.Error)
	}
	r := h.call("session/new", map[string]any{"cwd": h.cwd, "mcpServers": []any{}})
	if r.Error != nil {
		h.t.Fatalf("session/new: %v", r.Error)
	}
	var res struct {
		SessionID string `json:"sessionId"`
	}
	_ = json.Unmarshal(r.Result, &res)
	return res.SessionID
}

func (h *harness) promptText(sid, text string) int {
	return h.send("session/prompt", map[string]any{"sessionId": sid, "prompt": []any{map[string]any{"type": "text", "text": text}}})
}

// updates are the session/update objects received so far, in order.
func (h *harness) updates() []map[string]any {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []map[string]any
	for _, m := range h.msgs {
		if m.Method != "session/update" {
			continue
		}
		var p struct {
			Update map[string]any `json:"update"`
		}
		_ = json.Unmarshal(m.Params, &p)
		out = append(out, p.Update)
	}
	return out
}

// requests are the requests the adapter made of the client, in order.
func (h *harness) requests(method string) []rpcMsg {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []rpcMsg
	for _, m := range h.msgs {
		if m.Method == method && m.ID != nil {
			out = append(out, m)
		}
	}
	return out
}

func kinds(us []map[string]any) []string {
	var out []string
	for _, u := range us {
		out = append(out, u["sessionUpdate"].(string))
	}
	return out
}

func stopReason(t *testing.T, r rpcMsg) string {
	t.Helper()
	if r.Error != nil {
		t.Fatalf("prompt failed: %v", r.Error)
	}
	var res struct {
		StopReason string `json:"stopReason"`
	}
	_ = json.Unmarshal(r.Result, &res)
	return res.StopReason
}

func joinText(us []map[string]any, kind string) string {
	var b strings.Builder
	for _, u := range us {
		if u["sessionUpdate"] != kind {
			continue
		}
		if c, ok := u["content"].(map[string]any); ok {
			b.WriteString(c["text"].(string))
		}
	}
	return b.String()
}

func logHas(log []string, sub string) bool {
	for _, l := range log {
		if strings.Contains(l, sub) {
			return true
		}
	}
	return false
}

func removeFile(p string) { _ = os.Remove(p) }

func readFile(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
