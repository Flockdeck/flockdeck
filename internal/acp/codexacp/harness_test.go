package codexacp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// harness is the ACP client side of a test: it runs the adapter in this
// process, with the fake codex as a child, and records everything the adapter
// sends.
type harness struct {
	t        *testing.T
	in       io.WriteCloser
	log      string
	cwd      string
	mu       sync.Mutex
	cond     *sync.Cond
	msgs     []rpcMsg
	raw      []string // every line the adapter wrote, as written
	nextID   int
	onReq    func(m rpcMsg) any // answer to a request from the adapter; nil selects "allow"
	stopped  chan struct{}
	runErr   error
	stopOnce sync.Once
	cancel   context.CancelFunc
	errlog   logBuf
}

// logBuf keeps what the adapter wrote to its stderr.
type logBuf struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *logBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *logBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

type hopts struct {
	grace     time.Duration
	extraEnv  []string
	codexPath string
	maxCodex  int
	maxClient int
	control   time.Duration
}

func newHarness(t *testing.T, steps []step, opts ...hopts) *harness {
	t.Helper()
	var o hopts
	if len(opts) > 0 {
		o = opts[0]
	}
	if o.codexPath == "" {
		exe, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		o.codexPath = exe
	}
	if o.grace == 0 {
		o.grace = 3 * time.Second
	}
	env, logPath := fakeEnv(t, steps)
	env = append(env, o.extraEnv...)
	if o.maxCodex > 0 {
		old := maxCodexLine
		maxCodexLine = o.maxCodex
		t.Cleanup(func() { maxCodexLine = old })
	}
	if o.maxClient > 0 {
		old := maxClientLine
		maxClientLine = o.maxClient
		t.Cleanup(func() { maxClientLine = old })
	}
	if o.control == 0 {
		o.control = 10 * time.Second
	}
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	h := &harness{t: t, in: inW, log: logPath, cwd: t.TempDir(), stopped: make(chan struct{})}
	h.cond = sync.NewCond(&h.mu)
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	go func() {
		h.runErr = Run(ctx, inR, outW, Options{
			CodexPath: o.codexPath, Env: env, Version: "test",
			Stderr: io.MultiWriter(testWriter{t}, &h.errlog), ControlTimeout: o.control, CancelGrace: o.grace,
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
					h.deliver(m, string(l))
				}
			}
			if err != nil {
				return
			}
		}
	}()
	t.Cleanup(h.shutdown)
	return h
}

// shutdown closes the adapter's input and waits for it to stop.
func (h *harness) shutdown() {
	h.stopOnce.Do(func() {
		h.in.Close()
		select {
		case <-h.stopped:
		case <-time.After(15 * time.Second):
			h.t.Error("the adapter did not stop when its input closed")
		}
		h.cancel()
	})
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	defer func() { _ = recover() }() // a write after the test ended
	w.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

func (h *harness) deliver(m rpcMsg, line string) {
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
			if e, ok := res.(*rpcErr); ok {
				h.write(map[string]any{"jsonrpc": "2.0", "id": m.ID, "error": e})
				return
			}
			h.write(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": res})
		}()
	}
	h.mu.Lock()
	h.msgs = append(h.msgs, m)
	h.raw = append(h.raw, line)
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

// await waits for the response to id.
func (h *harness) await(id int) rpcMsg {
	h.t.Helper()
	want := strconv.Itoa(id)
	return h.waitFor(func(m rpcMsg) bool { return m.Method == "" && m.ID != nil && string(*m.ID) == want }, 20*time.Second)
}

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

func (h *harness) everything() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return strings.Join(h.raw, "\n")
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

// waitLog waits until the fake's log has a stdin line containing sub.
func waitLog(t *testing.T, path, sub string) string {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if l := logFind(readLog(path), sub); l != "" {
			return l
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the fake codex never received %q; log: %v", sub, readLog(path))
	return ""
}

func fakePID(t *testing.T, path string) int {
	t.Helper()
	for _, l := range readLog(path) {
		if strings.HasPrefix(l, "PID: ") {
			n, _ := strconv.Atoi(strings.TrimPrefix(l, "PID: "))
			return n
		}
	}
	t.Fatal("the fake codex logged no pid")
	return 0
}
