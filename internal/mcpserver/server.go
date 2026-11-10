// Package mcpserver is the Flockdeck MCP server: a stdio server an agent starts
// from the mcpServers entry Flockdeck gives its session, with three tools,
// send_message, list_peers and spawn.
//
// The server knows who it speaks for only through the token in its own
// environment, which Flockdeck issued for one pane. It never takes a pane id
// from a tool call: an argument that is not one of the tool's own is refused.
package mcpserver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/jmwri/flockdeck/internal/hooks"
	"github.com/jmwri/flockdeck/internal/team"
)

const (
	// MaxLine is the longest message the server reads from its client. A longer
	// one is answered with a parse error and skipped.
	MaxLine = 1 << 20

	// latestProtocol is the MCP version the server speaks when the client asks
	// for one it does not know.
	latestProtocol = "2025-06-18"
)

var knownProtocols = map[string]bool{"2024-11-05": true, "2025-03-26": true, "2025-06-18": true}

// Backend is what the tools do. The real one calls Flockdeck; tests use a fake.
type Backend interface {
	Send(ctx context.Context, to, text string) (hooks.MsgResult, error)
	Peers(ctx context.Context) (hooks.MCPPeers, error)
	Spawn(ctx context.Context, body hooks.MCPSpawnBody) (hooks.SpawnResult, error)
}

// API is the Backend that calls Flockdeck's loopback API with a pane's token.
type API struct{ URL, Token string }

func (a API) Send(ctx context.Context, to, text string) (hooks.MsgResult, error) {
	return hooks.MCPSend(ctx, a.URL, a.Token, to, text)
}

func (a API) Peers(ctx context.Context) (hooks.MCPPeers, error) {
	return hooks.MCPListPeers(ctx, a.URL, a.Token)
}

func (a API) Spawn(ctx context.Context, body hooks.MCPSpawnBody) (hooks.SpawnResult, error) {
	return hooks.MCPSpawn(ctx, a.URL, a.Token, body)
}

// Options are what Run needs besides the streams.
type Options struct {
	Backend Backend
	Version string
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// Run serves the client on in and out until in ends or ctx is done. Requests are
// answered one at a time, in the order they arrive, each with its own id.
func Run(ctx context.Context, in io.Reader, out io.Writer, opt Options) error {
	if opt.Backend == nil {
		return errors.New("no backend")
	}
	s := &session{opt: opt, out: out}
	lines := make(chan lineResult)
	done := make(chan struct{})
	defer close(done)
	go func() {
		defer close(lines)
		r := bufio.NewReaderSize(in, 64<<10)
		for {
			b, tooLong, err := readLine(r)
			select {
			case lines <- lineResult{b, tooLong, err}:
			case <-done:
				return
			}
			if err != nil {
				return
			}
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return nil
		case l, ok := <-lines:
			if !ok || l.err != nil {
				if l.err != nil && !errors.Is(l.err, io.EOF) {
					return l.err
				}
				return nil
			}
			if l.tooLong {
				if err := s.reply(nil, nil, &rpcError{-32700, fmt.Sprintf("a message is longer than %d bytes", MaxLine)}); err != nil {
					return err
				}
				continue
			}
			if err := s.handle(ctx, l.b); err != nil {
				return err
			}
		}
	}
}

type lineResult struct {
	b       []byte
	tooLong bool
	err     error
}

// readLine reads up to MaxLine bytes of one line. A longer line is read to its
// end and dropped, so memory stays bounded and the next message is not lost.
func readLine(r *bufio.Reader) (line []byte, tooLong bool, err error) {
	for {
		part, isPrefix, err := r.ReadLine()
		if err != nil {
			if len(line) > 0 && errors.Is(err, io.EOF) {
				return line, tooLong, nil
			}
			return nil, false, err
		}
		if !tooLong {
			if len(line)+len(part) > MaxLine {
				tooLong, line = true, nil
			} else {
				line = append(line, part...)
			}
		}
		if !isPrefix {
			return line, tooLong, nil
		}
	}
}

type session struct {
	opt         Options
	out         io.Writer
	initialized bool
}

func (s *session) reply(id json.RawMessage, result any, e *rpcError) error {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	b, err := json.Marshal(response{JSONRPC: "2.0", ID: id, Result: result, Error: e})
	if err != nil {
		return err
	}
	_, err = s.out.Write(append(b, '\n'))
	return err
}

// validID says id is a JSON string or integer, the ids a request may have.
func validID(id json.RawMessage) bool {
	t := bytes.TrimSpace(id)
	if len(t) == 0 {
		return false
	}
	switch t[0] {
	case '"':
		var s string
		return json.Unmarshal(t, &s) == nil
	case 'n', 't', 'f', '{', '[':
		return false
	}
	var n json.Number
	return json.Unmarshal(t, &n) == nil && !strings.ContainsAny(string(t), ".eE")
}

func (s *session) handle(ctx context.Context, line []byte) error {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return nil
	}
	if line[0] == '[' {
		return s.reply(nil, nil, &rpcError{-32600, "batches are not supported"})
	}
	var req request
	if err := json.Unmarshal(line, &req); err != nil || req.JSONRPC != "2.0" || req.Method == "" {
		return s.reply(nil, nil, &rpcError{-32600, "not a JSON-RPC 2.0 request"})
	}
	notification := len(req.ID) == 0
	if !notification && !validID(req.ID) {
		return s.reply(nil, nil, &rpcError{-32600, "the id must be a string or an integer"})
	}
	if notification {
		// Nothing that arrives without an id is answered.
		return nil
	}
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		v := latestProtocol
		if knownProtocols[p.ProtocolVersion] {
			v = p.ProtocolVersion
		}
		s.initialized = true
		return s.reply(req.ID, map[string]any{
			"protocolVersion": v,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "flockdeck", "version": s.opt.Version},
			"instructions": "send_message sends text to another agent pane of this project; list_peers lists them; spawn starts a helper agent. " +
				"The sender, and the pane a helper is started for, is always the pane this server was started for.",
		}, nil)
	case "ping":
		return s.reply(req.ID, map[string]any{}, nil)
	case "tools/list":
		if !s.initialized {
			return s.reply(req.ID, nil, &rpcError{-32002, "initialize first"})
		}
		return s.reply(req.ID, map[string]any{"tools": toolList}, nil)
	case "tools/call":
		if !s.initialized {
			return s.reply(req.ID, nil, &rpcError{-32002, "initialize first"})
		}
		res, e := s.call(ctx, req.Params)
		return s.reply(req.ID, res, e)
	}
	return s.reply(req.ID, nil, &rpcError{-32601, "method not found"})
}

var toolList = []map[string]any{
	{
		"name": "send_message",
		"description": "Send a message to another agent pane of this project. `to` is a pane id, a pane name or a role " +
			"(a role reaches every pane running it); list_peers shows them. The text is at most 8192 bytes. " +
			"The sender is this pane and cannot be set. Returns where the message was recorded.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"to":   map[string]any{"type": "string", "description": "A pane id, a pane name or a role."},
				"text": map[string]any{"type": "string", "description": "The message, at most 8192 bytes."},
			},
			"required":             []string{"to", "text"},
			"additionalProperties": false,
		},
	},
	{
		"name":        "list_peers",
		"description": "List the agent panes of this project: pane id, label, role, agent and status. Your own entry is marked self.",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false},
	},
	{
		"name": "spawn",
		"description": "Start a helper agent for this pane, as `flockdeck spawn` does. The helper works on `task` and is a pane of this project. " +
			"Unless `role` names one that shares the checkout, or no_worktree is set, a helper that works on its own branch " +
			"gets a git worktree and a branch named from its task; `worktree` names the branch. " +
			"The role you are in decides which roles you may start. Returns the new pane id and where it works.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task":        map[string]any{"type": "string", "description": "What the helper is to do. Required."},
				"role":        map[string]any{"type": "string", "description": "Id of the role to start the helper as, such as worker, reviewer or tester."},
				"name":        map[string]any{"type": "string", "description": "What the helper works on, in 2-5 words, up to 40 characters. Made from the task when not given."},
				"tab":         map[string]any{"type": "string", "description": "Title of the tab to put the helper in: the tab of that name in this project, made if there is none. Use one title for the panes of one piece of work. Up to 60 characters."},
				"worktree":    map[string]any{"type": "string", "description": "Branch for the helper's own git worktree, made for it."},
				"no_worktree": map[string]any{"type": "boolean", "description": "Start the helper in your own checkout instead of a worktree. Cannot be combined with worktree."},
				"split":       map[string]any{"type": "boolean", "description": "Place the helper beside this pane instead of in a new tab. tab takes precedence."},
				"agent":       map[string]any{"type": "string", "description": "Id of the agent to start. The role's agent applies where this is not given."},
				"model":       map[string]any{"type": "string", "description": "Which of that agent's models to ask for."},
				"terminal":    map[string]any{"type": "boolean", "description": "Start the agent in a terminal pane instead of a structured one."},
			},
			"required":             []string{"task"},
			"additionalProperties": false,
		},
	},
}

type toolResult struct {
	Content []map[string]string `json:"content"`
	IsError bool                `json:"isError,omitempty"`
}

func text(s string, isErr bool) toolResult {
	return toolResult{Content: []map[string]string{{"type": "text", "text": s}}, IsError: isErr}
}

func jsonText(v any) toolResult {
	b, err := json.Marshal(v)
	if err != nil {
		return text("Flockdeck's answer could not be shown", true)
	}
	return text(string(b), false)
}

func (s *session) call(ctx context.Context, params json.RawMessage) (any, *rpcError) {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &rpcError{-32602, "invalid params"}
	}
	switch p.Name {
	case "send_message":
		args, bad := strictArgs(p.Arguments, "to", "text")
		if bad != "" {
			return nil, &rpcError{-32602, bad}
		}
		to, text_ := args["to"], args["text"]
		if strings.TrimSpace(to) == "" || text_ == "" {
			return nil, &rpcError{-32602, "to and text must not be empty"}
		}
		if len(text_) > team.MaxText {
			return text(fmt.Sprintf("the message is %d bytes and the limit is %d; write it to a file and send the path instead", len(text_), team.MaxText), true), nil
		}
		res, err := s.opt.Backend.Send(ctx, to, text_)
		if err != nil {
			return text(err.Error(), true), nil
		}
		return jsonText(res), nil
	case "spawn":
		body, bad := spawnArgs(p.Arguments)
		if bad != "" {
			return nil, &rpcError{-32602, bad}
		}
		if strings.TrimSpace(body.Task) == "" {
			return nil, &rpcError{-32602, "task must not be empty"}
		}
		res, err := s.opt.Backend.Spawn(ctx, body)
		if err != nil {
			return text(err.Error(), true), nil
		}
		return jsonText(res), nil
	case "list_peers":
		if _, bad := strictArgs(p.Arguments); bad != "" {
			return nil, &rpcError{-32602, bad}
		}
		res, err := s.opt.Backend.Peers(ctx)
		if err != nil {
			return text(err.Error(), true), nil
		}
		return jsonText(res), nil
	}
	return nil, &rpcError{-32602, "unknown tool"}
}

// strictArgs reads the arguments of a call as an object whose members are exactly
// the names given, each a JSON string. Missing arguments are not allowed for the
// names given; any other member is refused, which is how a pane id passed by the
// model is turned down. bad is the reason, or "".
func strictArgs(raw json.RawMessage, names ...string) (map[string]string, string) {
	m := map[string]json.RawMessage{}
	if t := bytes.TrimSpace(raw); len(t) > 0 && string(t) != "null" {
		if err := json.Unmarshal(t, &m); err != nil {
			return nil, "arguments must be an object"
		}
	}
	out := map[string]string{}
	for k, v := range m {
		if !contains(names, k) {
			return nil, fmt.Sprintf("unknown argument %q; the sender is fixed by Flockdeck and takes no argument", truncate(k, 40))
		}
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			return nil, fmt.Sprintf("argument %s must be a string", k)
		}
		out[k] = s
	}
	for _, n := range names {
		if _, ok := out[n]; !ok {
			return nil, fmt.Sprintf("argument %s is required", n)
		}
	}
	return out, ""
}

// spawnArgs reads the arguments of a spawn call. Like strictArgs it refuses any
// member the tool does not list, which is how a parent, a token or a pane id
// passed by the model is turned down, and it checks each member's type.
// bad is the reason, or "".
func spawnArgs(raw json.RawMessage) (hooks.MCPSpawnBody, string) {
	var body hooks.MCPSpawnBody
	m := map[string]json.RawMessage{}
	if t := bytes.TrimSpace(raw); len(t) > 0 && string(t) != "null" {
		if err := json.Unmarshal(t, &m); err != nil {
			return body, "arguments must be an object"
		}
	}
	strs := map[string]*string{
		"task": &body.Task, "role": &body.Role, "name": &body.Name, "tab": &body.Tab,
		"worktree": &body.Worktree, "agent": &body.Agent, "model": &body.Model,
	}
	bools := map[string]*bool{"no_worktree": &body.NoWorktree, "split": &body.Split, "terminal": &body.Terminal}
	for k, v := range m {
		switch {
		case strs[k] != nil:
			if err := json.Unmarshal(v, strs[k]); err != nil {
				return body, fmt.Sprintf("argument %s must be a string", k)
			}
		case bools[k] != nil:
			if err := json.Unmarshal(v, bools[k]); err != nil {
				return body, fmt.Sprintf("argument %s must be true or false", k)
			}
		default:
			return body, fmt.Sprintf("unknown argument %q; the parent of a helper is fixed by Flockdeck and takes no argument", truncate(k, 40))
		}
	}
	if _, ok := m["task"]; !ok {
		return body, "argument task is required"
	}
	return body, ""
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
