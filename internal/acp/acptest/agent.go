// Package acptest is a fake ACP agent for testing clients without a real agent
// or a login. It is the contract suite's other half: internal/acp's tests start
// it as a child process and check the client against the behaviours below.
//
// The agent is this program run again. A test binary's TestMain calls
// RunIfAgent first; Command then gives the path, arguments and environment to
// start it with. Which behaviour a prompt gets is chosen by its first word:
//
//	plain       two message chunks, end_turn
//	tool        a tool call with updates, array content, usage, end_turn
//	perm        a permission request with a numeric id; the outcome is reported
//	perm-str    the same with the string id "perm-abc"; reports whether the id came back as a string
//	slow        works until session/cancel, then ends the turn cancelled
//	slow-perm   asks for permission, then waits for session/cancel
//	report      reports how many responses each permission request got
//	plan        a plan update
//	unknown     update kinds, content block types and fields the client does not know
//	big         a tool result of 5 MiB
//	order       200 numbered chunks, then end_turn
//	stderr      1 MiB on stderr, then plain
//	hold-perm   asks for permission and writes the outcome to the marker file
//	exit        exits with status 3 and a message on stderr, mid-turn
//	garbage     a line that is not JSON, a response to nothing, a malformed update, a second response
//	unknown-method  a request for fs/read_text_file, reports the error code it got back
//	perm-chunks chunks, a permission request, more chunks, then the tool update
//	storm       a thousand 64 KiB rewrites of one tool call, then 8 MiB of thought chunks
//	silent      never answers
//	flood       a stdout line with no end
//	blank       empty and whitespace-only lines, then a chunk
//	line N      one notification of exactly N bytes, then a chunk
//	stderr-flood  4 MiB on stderr in one line, then plain
//	spam-perm   fifty permission requests at once, reports how many were answered
//	ratelimit   answers the prompt with the JSON-RPC error -32603 "rate limit exceeded"
//	env         reports its whole environment, one NAME=VALUE per line, in one chunk
//	deaf-perm   asks for permission and stops reading stdin
//
// WithMute makes an agent that reads and never answers.
//
// Authentication is switched on with WithAuth.
package acptest

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	envAgent  = "FLOCKDECK_ACPTEST_AGENT"
	envAuth   = "FLOCKDECK_ACPTEST_AUTH"
	envMarker = "FLOCKDECK_ACPTEST_MARKER"
	envMute   = "FLOCKDECK_ACPTEST_MUTE"
	envDeaf   = "FLOCKDECK_ACPTEST_DEAF"
)

// ArgAgent, as an argument, also makes a process the fake agent. A test of how
// an environment is built starts the agent this way, since the environment it
// asked for must not hold the variable that otherwise says so.
const ArgAgent = "--acptest-agent"

// ArgMarker, as an argument followed by a path, does what WithMarker does for an
// agent started with ArgAgent, whose environment is not the test's.
const ArgMarker = "--acptest-marker"

// RunIfAgent runs the fake agent and returns true when this process was
// started as one. Call it first in TestMain and return when it reports true.
func RunIfAgent() bool {
	asked := os.Getenv(envAgent) == "1"
	if len(os.Args) > 1 && os.Args[1] == ArgTee {
		os.Exit(runTee(os.Args[2:]))
	}
	for _, a := range os.Args[1:] {
		if a == ArgAgent {
			asked = true
		}
	}
	if !asked {
		return false
	}
	newAgent().run()
	return true
}

type agent struct {
	out *bufio.Writer
	omu sync.Mutex

	mu         sync.Mutex
	waiters    map[string]chan msg // request id (raw) -> its response
	responses  map[string]int      // request id (raw) -> responses seen
	cancels    chan struct{}
	turns      sync.WaitGroup
	deaf       atomic.Bool
	mute       bool
	authed     bool
	reqSeq     int
	authMode   string
	markerPath string
}

func newAgent() *agent {
	marker := os.Getenv(envMarker)
	for i, a := range os.Args[1:] {
		if a == ArgMarker && i+2 < len(os.Args) {
			marker = os.Args[i+2]
		}
	}
	return &agent{
		out:        bufio.NewWriterSize(os.Stdout, 1<<16),
		waiters:    map[string]chan msg{},
		responses:  map[string]int{},
		cancels:    make(chan struct{}, 8),
		authMode:   os.Getenv(envAuth),
		mute:       os.Getenv(envMute) == "1",
		markerPath: marker,
	}
}

type msg struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  json.RawMessage `json:"error"`
}

func (a *agent) send(v any) {
	b, _ := json.Marshal(v)
	a.omu.Lock()
	a.out.Write(append(b, '\n'))
	a.out.Flush()
	a.omu.Unlock()
}

func (a *agent) result(id json.RawMessage, v any) {
	a.send(map[string]any{"jsonrpc": "2.0", "id": id, "result": v})
}

func (a *agent) rpcError(id json.RawMessage, code int, message string) {
	a.send(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": message}})
}

func (a *agent) update(sid string, u map[string]any) {
	a.send(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": sid, "update": u}})
}

func textBlock(s string) map[string]any { return map[string]any{"type": "text", "text": s} }

func (a *agent) chunk(sid, s string) {
	a.update(sid, map[string]any{"sessionUpdate": "agent_message_chunk", "content": textBlock(s)})
}

func (a *agent) run() {
	if os.Getenv(envDeaf) == "1" {
		time.Sleep(time.Hour) // never reads its input at all
	}
	br := bufio.NewReaderSize(os.Stdin, 1<<20)
	for {
		if a.deaf.Load() {
			time.Sleep(time.Hour) // stops reading stdin, as a wedged agent does (a bare select would trip the deadlock detector)
		}
		line, err := br.ReadBytes('\n')
		if a.mute {
			if err != nil {
				return
			}
			continue
		}
		if len(bytes.TrimSpace(line)) > 0 {
			var m msg
			if json.Unmarshal(line, &m) == nil {
				a.handle(m)
			}
		}
		if err != nil {
			// stdin closed: the client is done with us. A turn that was
			// answered a moment ago gets a moment to finish what it does with
			// the answer.
			done := make(chan struct{})
			go func() { a.turns.Wait(); close(done) }()
			select {
			case <-done:
			case <-time.After(500 * time.Millisecond):
			}
			return
		}
	}
}

func (a *agent) handle(m msg) {
	switch {
	case m.Method == "" && len(m.ID) > 0: // a response to a permission request
		key := string(m.ID)
		a.mu.Lock()
		a.responses[key]++
		ch := a.waiters[key]
		a.mu.Unlock()
		if ch != nil {
			select {
			case ch <- m:
			default:
			}
		}
	case m.Method == "initialize":
		res := map[string]any{
			"protocolVersion":   1,
			"agentCapabilities": map[string]any{"loadSession": false, "promptCapabilities": map[string]any{}, "futureCapability": map[string]any{"x": 1}},
			"agentInfo":         map[string]any{"name": "acptest", "version": "0.0.1"},
			"authMethods":       []any{},
		}
		if a.authMode != "" {
			res["authMethods"] = []any{
				map[string]any{"id": "api-key", "name": "API key", "description": "Use a key"},
				map[string]any{"id": "login", "name": "Log in"},
			}
		}
		a.result(m.ID, res)
	case m.Method == "authenticate":
		a.mu.Lock()
		a.authed = true
		a.mu.Unlock()
		a.result(m.ID, map[string]any{})
	case m.Method == "session/new":
		var p struct {
			Cwd        string            `json:"cwd"`
			McpServers []json.RawMessage `json:"mcpServers"`
		}
		_ = json.Unmarshal(m.Params, &p)
		fmt.Fprintln(os.Stderr, "acptest: session/new cwd =", p.Cwd)
		if p.McpServers == nil {
			a.rpcError(m.ID, -32602, "mcpServers must be an array")
			return
		}
		if a.needsAuth("new") {
			a.rpcError(m.ID, -32000, "Authentication required")
			return
		}
		a.result(m.ID, map[string]any{"sessionId": "sess-1"})
	case m.Method == "session/cancel":
		select {
		case a.cancels <- struct{}{}:
		default:
		}
	case m.Method == "session/prompt":
		if a.needsAuth("prompt") {
			a.rpcError(m.ID, -32000, "Authentication required")
			return
		}
		a.mu.Lock()
		a.reqSeq++
		n := a.reqSeq
		a.mu.Unlock()
		if strings.HasPrefix(promptText(m.Params), "deaf-perm") {
			a.deaf.Store(true)
		}
		a.turns.Add(1)
		go func() {
			defer a.turns.Done()
			a.prompt(m.ID, m.Params, n)
		}()
	}
}

func (a *agent) needsAuth(at string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.authMode == at && !a.authed
}

// askPermission sends a session/request_permission and returns the request's
// key and the channel its response arrives on.
func (a *agent) askPermission(sid string, id json.RawMessage) chan msg {
	ch := make(chan msg, 1)
	a.mu.Lock()
	a.waiters[string(id)] = ch
	a.mu.Unlock()
	a.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": "session/request_permission", "params": map[string]any{
		"sessionId": sid,
		"toolCall":  map[string]any{"toolCallId": "call-1", "title": "Write notes.txt", "kind": "edit", "status": "pending", "rawInput": map[string]any{"path": "notes.txt"}},
		"options": []any{
			map[string]any{"optionId": "allow-1", "name": "Allow once", "kind": "allow_once"},
			map[string]any{"optionId": "allow-all", "name": "Always allow", "kind": "allow_always"},
			map[string]any{"optionId": "reject-1", "name": "Reject", "kind": "reject_once"},
		},
	}})
	return ch
}

type outcome struct {
	Outcome struct {
		Outcome  string `json:"outcome"`
		OptionID string `json:"optionId"`
	} `json:"outcome"`
}

// promptText is the text of a session/prompt's first block.
func promptText(params json.RawMessage) string {
	var p struct {
		Prompt []struct {
			Text string `json:"text"`
		} `json:"prompt"`
	}
	_ = json.Unmarshal(params, &p)
	if len(p.Prompt) == 0 {
		return ""
	}
	return p.Prompt[0].Text
}

func (a *agent) prompt(id, params json.RawMessage, n int) {
	var p struct {
		SessionID string `json:"sessionId"`
		Prompt    []struct {
			Text string `json:"text"`
		} `json:"prompt"`
	}
	_ = json.Unmarshal(params, &p)
	sid, ask := p.SessionID, ""
	if len(p.Prompt) > 0 {
		ask = p.Prompt[0].Text
	}
	word, _, _ := strings.Cut(ask, " ")
	done := func(reason string) { a.result(id, map[string]any{"stopReason": reason}) }

	// A cancel from an earlier turn is not this turn's.
	for len(a.cancels) > 0 {
		<-a.cancels
	}

	switch word {
	case "plain":
		a.chunk(sid, "Hello")
		a.chunk(sid, " world")
		done("end_turn")

	case "tool":
		a.update(sid, map[string]any{"sessionUpdate": "tool_call", "toolCallId": "call-1", "title": "Run echo", "kind": "execute",
			"status": "pending", "locations": []any{map[string]any{"path": "/work/a.txt", "line": 3}}, "rawInput": map[string]any{"command": "echo hi"}})
		a.update(sid, map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "call-1", "status": "in_progress",
			"content": []any{map[string]any{"type": "content", "content": textBlock("hi\n")}}})
		a.update(sid, map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "call-1", "status": "completed",
			"rawOutput": map[string]any{"stdout": "hi\n"},
			"content": []any{
				map[string]any{"type": "content", "content": textBlock("hi\n")},
				map[string]any{"type": "diff", "path": "/work/a.txt", "oldText": nil, "newText": "hi\n"},
			}})
		a.chunk(sid, "Done.")
		a.update(sid, map[string]any{"sessionUpdate": "usage_update", "used": 1234, "size": 200000, "cost": map[string]any{"amount": 0.0042, "currency": "USD"}})
		done("end_turn")

	case "perm", "perm-str":
		var rid json.RawMessage
		if word == "perm-str" {
			rid = json.RawMessage(`"perm-abc"`)
		} else {
			rid = json.RawMessage(strconv.Itoa(1000 + n))
		}
		a.update(sid, map[string]any{"sessionUpdate": "tool_call", "toolCallId": "call-1", "title": "Write notes.txt", "kind": "edit", "status": "pending"})
		raw := (<-a.askPermission(sid, rid)).Result
		var o outcome
		_ = json.Unmarshal(raw, &o)
		switch o.Outcome.OptionID {
		case "allow-1", "allow-all":
			a.update(sid, map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "call-1", "status": "completed"})
		default:
			a.update(sid, map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "call-1", "status": "failed"})
		}
		a.chunk(sid, fmt.Sprintf("outcome=%s option=%s", o.Outcome.Outcome, o.Outcome.OptionID))
		if word == "perm-str" {
			a.chunk(sid, "id-echo="+string(rid))
		}
		done("end_turn")

	case "slow":
		a.chunk(sid, "working...")
		select {
		case <-a.cancels:
			done("cancelled")
		case <-time.After(30 * time.Second):
			done("end_turn")
		}

	case "slow-perm":
		a.askPermission(sid, json.RawMessage(strconv.Itoa(2000+n)))
		select {
		case <-a.cancels:
			// Give the client's own cancelled answer time to arrive, so that a
			// second, late answer from the person has a chance to follow it.
			time.Sleep(300 * time.Millisecond)
			done("cancelled")
		case <-time.After(30 * time.Second):
			done("end_turn")
		}

	case "report":
		a.mu.Lock()
		var parts []string
		for k, v := range a.responses {
			parts = append(parts, fmt.Sprintf("%s=%d", k, v))
		}
		a.mu.Unlock()
		a.chunk(sid, "responses: "+strings.Join(sortedStrings(parts), ","))
		done("end_turn")

	case "plan":
		a.update(sid, map[string]any{"sessionUpdate": "plan", "entries": []any{
			map[string]any{"content": "Edit the file", "priority": "high", "status": "in_progress"},
			map[string]any{"content": "Run tests", "priority": "medium", "status": "pending"},
			map[string]any{"content": "Something odd", "priority": "urgent", "status": "paused"},
		}})
		done("end_turn")

	case "unknown":
		a.update(sid, map[string]any{"sessionUpdate": "future_kind", "foo": 1, "nested": map[string]any{"a": []any{1, 2}}})
		a.update(sid, map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "hologram", "data": "x", "n": 7}})
		a.update(sid, map[string]any{"sessionUpdate": "tool_call", "toolCallId": "call-9", "title": "Teleport", "kind": "teleport", "status": "pending", "zzz": true})
		a.update(sid, map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "call-9",
			"content": []any{map[string]any{"type": "hologram", "x": 1}, map[string]any{"type": "content", "content": textBlock("ok")}}})
		a.update(sid, map[string]any{"sessionUpdate": "agent_message_chunk", "content": textBlock("after unknown"), "extra": 1})
		a.update(sid, map[string]any{"sessionUpdate": "agent_message_chunk", "content": "not an object"})
		done("end_turn")

	case "long":
		a.chunk(sid, strings.Repeat("r", 12000))
		done("end_turn")

	case "big":
		a.update(sid, map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "call-1", "status": "completed",
			"content": []any{map[string]any{"type": "content", "content": textBlock(strings.Repeat("x", 5<<20))}}})
		a.chunk(sid, "after big")
		done("end_turn")

	case "perm-chunks":
		a.update(sid, map[string]any{"sessionUpdate": "tool_call", "toolCallId": "call-1", "title": "Write notes.txt", "kind": "edit", "status": "pending"})
		for i := 0; i < 20; i++ {
			a.chunk(sid, "a")
		}
		raw := (<-a.askPermission(sid, json.RawMessage(strconv.Itoa(1000+n)))).Result
		var o outcome
		_ = json.Unmarshal(raw, &o)
		for i := 0; i < 20; i++ {
			a.chunk(sid, "b")
		}
		status := "failed"
		if o.Outcome.OptionID == "allow-1" {
			status = "completed"
		}
		a.update(sid, map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "call-1", "status": status})
		done("end_turn")

	case "storm":
		// Far more than a person could read: a thousand rewrites of one tool
		// call, each with 64 KiB of output, then 8 MiB of thoughts in 1 KiB
		// chunks.
		blob := strings.Repeat("storm output line\n", 3600)
		a.update(sid, map[string]any{"sessionUpdate": "tool_call", "toolCallId": "call-1", "title": "Run storm", "kind": "execute", "status": "pending"})
		for i := 0; i < 1000; i++ {
			a.update(sid, map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "call-1", "status": "in_progress",
				"content": []any{map[string]any{"type": "content", "content": textBlock(blob)}}})
		}
		kb := strings.Repeat("t", 1024)
		for i := 0; i < 8192; i++ {
			a.update(sid, map[string]any{"sessionUpdate": "agent_thought_chunk", "content": textBlock(kb)})
		}
		a.update(sid, map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "call-1", "status": "completed"})
		done("end_turn")

	case "order":
		for i := 0; i < 200; i++ {
			a.chunk(sid, strconv.Itoa(i))
		}
		done("end_turn")

	case "ratelimit":
		a.rpcError(id, -32603, "rate limit exceeded")

	case "env":
		env := os.Environ()
		sort.Strings(env)
		a.chunk(sid, strings.Join(env, "\n"))
		done("end_turn")

	case "stderr":
		chunk := strings.Repeat("e", 1023) + "\n"
		for i := 0; i < 1024; i++ {
			fmt.Fprint(os.Stderr, chunk)
		}
		fmt.Fprintln(os.Stderr, "acptest: last stderr line")
		a.chunk(sid, "after stderr")
		done("end_turn")

	case "hold-perm":
		ch := a.askPermission(sid, json.RawMessage(strconv.Itoa(3000+n)))
		raw := (<-ch).Result
		var o outcome
		_ = json.Unmarshal(raw, &o)
		if a.markerPath != "" {
			_ = os.WriteFile(a.markerPath, []byte(o.Outcome.Outcome+" "+o.Outcome.OptionID), 0o600)
		}
		done("cancelled")

	case "garbage":
		// Lines a client must survive: not JSON, a response to a request it
		// never made, a malformed update, and (after the real one) a second
		// response to the prompt.
		a.omu.Lock()
		a.out.WriteString("this is not json\n")
		a.out.Flush()
		a.omu.Unlock()
		a.send(map[string]any{"jsonrpc": "2.0", "id": 99999, "result": map[string]any{}})
		a.send(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": "not an object"})
		a.chunk(sid, "after garbage")
		done("end_turn")
		done("end_turn")

	case "unknown-method":
		// An agent-to-client request the client does not implement.
		ch := make(chan msg, 1)
		a.mu.Lock()
		a.waiters[`"m1"`] = ch
		a.mu.Unlock()
		a.send(map[string]any{"jsonrpc": "2.0", "id": "m1", "method": "fs/read_text_file", "params": map[string]any{"sessionId": sid, "path": "/x"}})
		r := <-ch
		var e struct {
			Code int `json:"code"`
		}
		_ = json.Unmarshal(r.Error, &e)
		a.chunk(sid, "unknown-method error="+strconv.Itoa(e.Code))
		done("end_turn")

	case "delayed":
		time.Sleep(400 * time.Millisecond)
		a.chunk(sid, "late")
		done("end_turn")

	case "silent":
		select {} // never answers

	case "flood":
		// A line with no end.
		a.omu.Lock()
		chunk := strings.Repeat("x", 1<<20)
		a.out.WriteString(`{"jsonrpc":"2.0","method":"x","params":"`)
		for i := 0; i < 4; i++ {
			a.out.WriteString(chunk)
		}
		a.out.Flush()
		a.omu.Unlock()
		select {}

	case "blank":
		// Lines with nothing in them, between real ones.
		a.omu.Lock()
		a.out.WriteString("\n   \r\n\n")
		a.out.Flush()
		a.omu.Unlock()
		a.chunk(sid, "after blank")
		done("end_turn")

	case "line":
		// "line N": one notification of exactly N bytes without its newline, then
		// a chunk and the end of the turn.
		_, arg, _ := strings.Cut(ask, " ")
		n, _ := strconv.Atoi(arg)
		const open, shut = `{"jsonrpc":"2.0","method":"x","params":"`, `"}`
		if n < len(open)+len(shut) {
			n = len(open) + len(shut)
		}
		a.omu.Lock()
		a.out.WriteString(open)
		a.out.WriteString(strings.Repeat("p", n-len(open)-len(shut)))
		a.out.WriteString(shut + "\n")
		a.out.Flush()
		a.omu.Unlock()
		a.chunk(sid, "after line")
		done("end_turn")

	case "stderr-flood":
		// Four MiB on stderr in one line, then a normal turn.
		fmt.Fprint(os.Stderr, strings.Repeat("e", 4<<20)+"\n")
		a.chunk(sid, "after stderr flood")
		done("end_turn")

	case "spam-perm":
		// Fifty permission requests at once.
		for i := 0; i < 50; i++ {
			a.askPermission(sid, json.RawMessage(strconv.Itoa(5000+i)))
		}
		time.Sleep(700 * time.Millisecond)
		a.mu.Lock()
		answered := len(a.responses)
		a.mu.Unlock()
		a.chunk(sid, "answered="+strconv.Itoa(answered))
		select {
		case <-a.cancels:
			done("cancelled")
		case <-time.After(30 * time.Second):
			done("end_turn")
		}

	case "deaf-perm":
		// Asks for permission; handle() has already stopped reading stdin.
		a.askPermission(sid, json.RawMessage(strconv.Itoa(4000+n)))
		select {}

	case "exit":
		fmt.Fprintln(os.Stderr, "acptest: exiting on request")
		os.Exit(3)

	default:
		a.chunk(sid, "unknown scenario: "+ask)
		done("end_turn")
	}
}

func sortedStrings(s []string) []string {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
	return s
}
