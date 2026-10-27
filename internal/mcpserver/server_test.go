package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/hooks"
	"github.com/jmwri/flockdeck/internal/team"
)

type fake struct {
	mu    sync.Mutex
	sends [][2]string
	err   error
}

func (f *fake) Send(_ context.Context, to, text string) (hooks.MsgResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return hooks.MsgResult{}, f.err
	}
	f.sends = append(f.sends, [2]string{to, text})
	return hooks.MsgResult{Sent: []hooks.Sent{{ID: 4, Pane: "p2", To: "rev"}}}, nil
}

func (f *fake) Peers(context.Context) (hooks.MCPPeers, error) {
	if f.err != nil {
		return hooks.MCPPeers{}, f.err
	}
	return hooks.MCPPeers{Peers: []hooks.MCPPeer{{Pane: "p1", Label: "me", Status: "idle", Self: true}}}, nil
}

// run feeds lines to a server and returns what it wrote, one decoded message per
// line.
func run(t *testing.T, b Backend, lines ...string) []map[string]any {
	t.Helper()
	var out bytes.Buffer
	in := strings.NewReader(strings.Join(lines, "\n") + "\n")
	if err := Run(context.Background(), in, &out, Options{Backend: b, Version: "t"}); err != nil {
		t.Fatal(err)
	}
	var msgs []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if l == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("not JSON: %q", l)
		}
		msgs = append(msgs, m)
	}
	return msgs
}

const initLine = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"c","version":"1"}}}`

func call(id int, name, args string) string {
	return `{"jsonrpc":"2.0","id":` + strconv.Itoa(id) + `,"method":"tools/call","params":{"name":"` + name + `","arguments":` + args + `}}`
}

func errCode(m map[string]any) float64 {
	e, _ := m["error"].(map[string]any)
	c, _ := e["code"].(float64)
	return c
}

func toolText(t *testing.T, m map[string]any) (string, bool) {
	t.Helper()
	r, ok := m["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result: %+v", m)
	}
	c := r["content"].([]any)[0].(map[string]any)["text"].(string)
	isErr, _ := r["isError"].(bool)
	return c, isErr
}

func TestInitializeListAndCall(t *testing.T) {
	f := &fake{}
	got := run(t, f, initLine, `{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		call(3, "send_message", `{"to":"rev","text":"look at b"}`),
		call(4, "list_peers", `{}`),
		`{"jsonrpc":"2.0","id":5,"method":"ping"}`)
	if len(got) != 5 {
		t.Fatalf("got %d replies, want 5 (the notification gets none): %+v", len(got), got)
	}
	res := got[0]["result"].(map[string]any)
	if res["protocolVersion"] != "2025-06-18" || res["serverInfo"].(map[string]any)["name"] != "flockdeck" {
		t.Errorf("initialize = %+v", res)
	}
	tools := got[1]["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 2 || tools[0].(map[string]any)["name"] != "send_message" || tools[1].(map[string]any)["name"] != "list_peers" {
		t.Errorf("tools = %+v", tools)
	}
	for _, tl := range tools {
		if tl.(map[string]any)["inputSchema"].(map[string]any)["additionalProperties"] != false {
			t.Errorf("a tool schema allows extra arguments: %+v", tl)
		}
	}
	if text, isErr := toolText(t, got[2]); isErr || !strings.Contains(text, `"id":4`) || !strings.Contains(text, `"pane":"p2"`) {
		t.Errorf("send_message = %q, %v", text, isErr)
	}
	if len(f.sends) != 1 || f.sends[0] != [2]string{"rev", "look at b"} {
		t.Errorf("backend saw %v", f.sends)
	}
	if text, _ := toolText(t, got[3]); !strings.Contains(text, `"self":true`) {
		t.Errorf("list_peers = %q", text)
	}
	for i, m := range got {
		if m["id"] == nil {
			t.Errorf("reply %d has no id", i)
		}
	}
}

func TestRepliesCarryTheIDOfTheirRequest(t *testing.T) {
	got := run(t, &fake{}, initLine, `{"jsonrpc":"2.0","id":"abc","method":"ping"}`, `{"jsonrpc":"2.0","id":77,"method":"ping"}`)
	if got[1]["id"] != "abc" || got[2]["id"] != float64(77) {
		t.Errorf("ids = %v, %v", got[1]["id"], got[2]["id"])
	}
}

func TestAPaneIDArgumentIsRefused(t *testing.T) {
	f := &fake{}
	got := run(t, f, initLine,
		call(2, "send_message", `{"to":"rev","text":"hi","pane":"p9"}`),
		call(3, "send_message", `{"to":"rev","text":"hi","from":"p9"}`),
		call(4, "send_message", `{"to":"rev","text":"hi","sender":"p9"}`),
		call(5, "list_peers", `{"pane":"p9"}`))
	for _, m := range got[1:] {
		if errCode(m) != -32602 {
			t.Errorf("reply = %+v, want invalid params", m)
		}
	}
	if len(f.sends) != 0 {
		t.Errorf("a spoofed call reached the backend: %v", f.sends)
	}
}

func TestArgumentsOfTheWrongTypeAreRefused(t *testing.T) {
	f := &fake{}
	bad := []string{
		`{"to":1,"text":"hi"}`, `{"to":"a","text":2}`, `{"to":["a"],"text":"hi"}`, `{"to":"a","text":null}`,
		`{"to":"a","text":{"x":1}}`, `{"to":true,"text":"hi"}`, `{"to":"a"}`, `{"text":"hi"}`, `{}`, `null`, `[]`, `"x"`,
		`{"to":"","text":"hi"}`, `{"to":"  ","text":"hi"}`, `{"to":"a","text":""}`,
	}
	lines := []string{initLine}
	for _, a := range bad {
		lines = append(lines, call(2, "send_message", a))
	}
	got := run(t, f, lines...)
	for i, m := range got[1:] {
		if errCode(m) != -32602 {
			t.Errorf("arguments %s = %+v, want invalid params", bad[i], m)
		}
	}
	if len(f.sends) != 0 {
		t.Errorf("bad arguments reached the backend: %v", f.sends)
	}
	// The call itself can be malformed too.
	got = run(t, f, initLine, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":"x"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":5}}`, call(4, "nope", `{}`))
	for _, m := range got[1:] {
		if errCode(m) != -32602 {
			t.Errorf("reply = %+v, want invalid params", m)
		}
	}
}

func TestAnOversizeMessageIsRefusedBeforeTheBackend(t *testing.T) {
	f := &fake{}
	got := run(t, f, initLine, call(2, "send_message", `{"to":"rev","text":"`+strings.Repeat("x", team.MaxText+1)+`"}`),
		call(3, "send_message", `{"to":"rev","text":"`+strings.Repeat("x", team.MaxText)+`"}`))
	if text, isErr := toolText(t, got[1]); !isErr || !strings.Contains(text, "limit is 8192") {
		t.Errorf("oversize = %q, %v", text, isErr)
	}
	if _, isErr := toolText(t, got[2]); isErr || len(f.sends) != 1 {
		t.Errorf("a message at the limit was refused: %v", f.sends)
	}
}

func TestARefusalFromFlockdeckIsAToolError(t *testing.T) {
	got := run(t, &fake{err: errors.New("no pane named x in this project")}, initLine, call(2, "send_message", `{"to":"x","text":"hi"}`), call(3, "list_peers", `{}`))
	for _, m := range got[1:] {
		if text, isErr := toolText(t, m); !isErr || !strings.Contains(text, "no pane named") {
			t.Errorf("reply = %q, %v", text, isErr)
		}
	}
}

func TestToolsNeedInitializeFirst(t *testing.T) {
	f := &fake{}
	got := run(t, f, call(1, "send_message", `{"to":"a","text":"b"}`), `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	for _, m := range got {
		if errCode(m) != -32002 {
			t.Errorf("reply = %+v, want not initialized", m)
		}
	}
	if len(f.sends) != 0 {
		t.Error("a call before initialize reached the backend")
	}
}

func TestBadFramesGetErrorsAndTheServerGoesOn(t *testing.T) {
	got := run(t, &fake{}, `not json`, `[{"jsonrpc":"2.0","id":1,"method":"ping"}]`, `{"jsonrpc":"1.0","id":1,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":{"a":1},"method":"ping"}`, `{"jsonrpc":"2.0","id":1.5,"method":"ping"}`, `{"jsonrpc":"2.0","id":null,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":9,"method":"nope"}`, `{"jsonrpc":"2.0","method":"nope"}`, `{"jsonrpc":"2.0","id":10,"method":"ping"}`)
	if len(got) != 8 {
		t.Fatalf("got %d replies: %+v", len(got), got)
	}
	if errCode(got[6]) != -32601 || got[7]["id"] != float64(10) || got[7]["result"] == nil {
		t.Errorf("last replies = %+v, %+v", got[6], got[7])
	}
	for _, m := range got[:6] {
		if errCode(m) != -32600 {
			t.Errorf("reply = %+v, want invalid request", m)
		}
	}
}

func TestALineOverTheLimitIsSkippedWithoutBeingKept(t *testing.T) {
	long := `{"jsonrpc":"2.0","id":1,"method":"ping","params":{"x":"` + strings.Repeat("a", MaxLine) + `"}}`
	got := run(t, &fake{}, long, `{"jsonrpc":"2.0","id":2,"method":"ping"}`)
	if len(got) != 2 || errCode(got[0]) != -32700 || got[1]["id"] != float64(2) {
		t.Errorf("replies = %+v", got)
	}
}

func TestAnUnknownProtocolVersionGetsOurs(t *testing.T) {
	got := run(t, &fake{}, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"1999-01-01"}}`)
	if got[0]["result"].(map[string]any)["protocolVersion"] != latestProtocol {
		t.Errorf("reply = %+v", got[0])
	}
}

func TestRunEndsWhenTheContextIsDone(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, pr, io.Discard, Options{Backend: &fake{}}) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run kept going after its context was done")
	}
}

func TestNoBackendIsRefused(t *testing.T) {
	if err := Run(context.Background(), strings.NewReader(""), io.Discard, Options{}); err == nil {
		t.Error("Run started with no backend")
	}
}

// The real backend carries the pane's token to a real hook server and the
// sender is the pane the token was issued for.
func TestAPIBackendCallsFlockdeckAsThePaneOfTheToken(t *testing.T) {
	srv, err := hooks.Serve(func(hooks.Event) {})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	var from string
	srv.SetMsgHandler(func(r hooks.MsgRequest) (hooks.MsgResult, error) {
		from = r.Pane
		return hooks.MsgResult{Sent: []hooks.Sent{{ID: 9, Pane: "p2", To: "rev"}}}, nil
	})
	srv.SetTeamHandler(func(r hooks.TeamRequest) (hooks.TeamResult, error) {
		return hooks.TeamResult{Members: []team.Member{{Pane: r.Pane, Name: "n", Status: "idle", Self: true}}}, nil
	})
	tok := srv.IssuePaneToken("pA")
	got := run(t, API{URL: srv.BaseURL(), Token: tok}, initLine, call(2, "send_message", `{"to":"rev","text":"hi"}`), call(3, "list_peers", `{}`),
		call(4, "send_message", `{"to":"rev","text":"hi","pane":"pB"}`))
	if text, isErr := toolText(t, got[1]); isErr || !strings.Contains(text, `"id":9`) || from != "pA" {
		t.Errorf("send = %q %v, sender %q", text, isErr, from)
	}
	if text, isErr := toolText(t, got[2]); isErr || !strings.Contains(text, `"pane":"pA"`) {
		t.Errorf("peers = %q %v", text, isErr)
	}
	if errCode(got[3]) != -32602 {
		t.Errorf("a spoofed call = %+v", got[3])
	}
	// A wrong token is a tool error that does not repeat the token.
	got = run(t, API{URL: srv.BaseURL(), Token: "wrongtoken-abc"}, initLine, call(2, "send_message", `{"to":"rev","text":"hi"}`))
	if text, isErr := toolText(t, got[1]); !isErr || strings.Contains(text, "wrongtoken-abc") {
		t.Errorf("wrong token = %q %v", text, isErr)
	}
	// And so is a Flockdeck that is gone.
	srv.Close()
	got = run(t, API{URL: srv.BaseURL(), Token: tok}, initLine, call(2, "list_peers", `{}`))
	if text, isErr := toolText(t, got[1]); !isErr || strings.Contains(text, tok) {
		t.Errorf("closed server = %q %v", text, isErr)
	}
}
