package acp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"
)

// serve answers one request from the agent, on its own goroutine.
func (c *Client) serve(m rpcMsg) {
	switch m.Method {
	case "session/request_permission":
		c.servePermission(m)
	default:
		// fs/* and terminal/* are not advertised, so a conforming agent does
		// not send them; anything else is not something this client does.
		c.reply(m.ID, nil, &RPCError{Code: -32601, Message: "method not supported: " + m.Method})
	}
}

func (c *Client) servePermission(m rpcMsg) {
	var req PermissionRequest
	if err := json.Unmarshal(m.Params, &req); err != nil {
		c.reply(m.ID, nil, &RPCError{Code: -32602, Message: err.Error()})
		return
	}
	req.RequestID = string(*m.ID)
	req.Raw = m.Params
	if req.Options == nil {
		req.Options = []PermissionOption{}
	}

	ctx, cancel := context.WithCancel(context.Background())
	p := &permission{id: m.ID, sessionID: req.SessionID, options: req.Options, ctx: ctx, cancel: cancel}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		cancel()
		c.answer(p, "")
		return
	}
	c.perms[p] = struct{}{}
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.perms, p)
		c.mu.Unlock()
		cancel()
	}()

	optionID := c.cfg.Handler.Permission(ctx, req)
	// The chosen option has to be one the agent offered. If the request was
	// cancelled meanwhile (Cancel, Close), it has already been answered and
	// this answer is dropped by answer's once.
	if optionID != "" {
		known := false
		for _, o := range req.Options {
			known = known || o.OptionID == optionID
		}
		if !known {
			optionID = ""
		}
	}
	c.answer(p, optionID)
}

// answer sends the reply to a permission request, once: the first answer wins
// and a later one (a late response after cancel) is dropped. An empty optionID
// answers cancelled.
func (c *Client) answer(p *permission, optionID string) {
	p.once.Do(func() {
		outcome := map[string]any{"outcome": "cancelled"}
		if optionID != "" {
			outcome = map[string]any{"outcome": "selected", "optionId": optionID}
		}
		c.reply(p.id, map[string]any{"outcome": outcome}, nil)
	})
}

// cancelPermissions answers the open requests of one session, or of every
// session when sessionID is empty, as cancelled and cancels their contexts.
func (c *Client) cancelPermissions(sessionID string) {
	c.mu.Lock()
	var open []*permission
	for p := range c.perms {
		if sessionID == "" || p.sessionID == sessionID {
			open = append(open, p)
		}
	}
	c.mu.Unlock()
	for _, p := range open {
		c.answer(p, "")
		p.cancel()
	}
}

func (c *Client) write(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_, err = c.in.Write(append(b, '\n'))
	return err
}

// reply answers a request from the agent. id is echoed as the raw JSON the
// agent sent.
func (c *Client) reply(id *json.RawMessage, result any, e *RPCError) {
	m := map[string]any{"jsonrpc": "2.0", "id": id}
	if e != nil {
		m["error"] = e
	} else {
		m["result"] = result
	}
	_ = c.write(m)
}

// call sends a request and waits for its response. ctx ends the wait, not the
// request: the agent may still answer it.
func (c *Client) call(ctx context.Context, method string, params, result any) error {
	id := c.nextID.Add(1)
	ch := make(chan reply, 1)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrClosed
	}
	c.pending[id] = ch
	c.mu.Unlock()

	forget := func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}
	if err := c.write(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		forget()
		select {
		case <-c.loopDone:
			return &ExitError{Stderr: c.StderrTail()}
		default:
		}
		return err
	}
	select {
	case r := <-ch:
		if r.err != nil {
			return r.err
		}
		if r.msg.Error != nil {
			return c.rpcError(method, r.msg.Error)
		}
		if result != nil && len(r.msg.Result) > 0 {
			return json.Unmarshal(r.msg.Result, result)
		}
		return nil
	case <-ctx.Done():
		forget()
		return ctx.Err()
	}
}

// rpcError turns the agent's error into an AuthRequiredError when it is the
// authentication one.
func (c *Client) rpcError(method string, e *RPCError) error {
	if e.Code != codeAuthRequired {
		return e
	}
	c.mu.Lock()
	methods := append([]AuthMethod(nil), c.authMethods...)
	c.mu.Unlock()
	return &AuthRequiredError{Method: method, Message: e.Message, Methods: methods, Err: e}
}

func (c *Client) notify(method string, params any) error {
	return c.write(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

// Initialize negotiates the protocol version and returns what the agent says
// about itself, including the authentication methods it lists. It advertises
// no fs or terminal capabilities.
func (c *Client) Initialize(ctx context.Context) (InitResult, error) {
	var r InitResult
	err := c.call(ctx, "initialize", map[string]any{
		"protocolVersion": ProtocolVersion,
		"clientCapabilities": map[string]any{
			"fs":       map[string]any{"readTextFile": false, "writeTextFile": false},
			"terminal": false,
		},
		"clientInfo": map[string]any{"name": c.cfg.ClientName, "title": "Flockdeck", "version": c.cfg.ClientVersion},
	}, &r)
	if err != nil {
		return r, err
	}
	if r.ProtocolVersion != ProtocolVersion {
		return r, &UnsupportedVersionError{Version: r.ProtocolVersion}
	}
	if r.AuthMethods == nil {
		r.AuthMethods = []AuthMethod{}
	}
	c.mu.Lock()
	c.authMethods = r.AuthMethods
	c.mu.Unlock()
	return r, nil
}

// Authenticate asks the agent to authenticate with one of the methods it
// listed at initialize.
func (c *Client) Authenticate(ctx context.Context, methodID string) error {
	return c.call(ctx, "authenticate", map[string]any{"methodId": methodID}, nil)
}

// NewSession starts a session. cwd must be absolute. mcpServers may be nil.
// If the agent has no login this returns an *AuthRequiredError (some agents
// refuse here, others at the first prompt).
func (c *Client) NewSession(ctx context.Context, cwd string, mcpServers []MCPServer) (string, error) {
	if !filepath.IsAbs(cwd) {
		return "", fmt.Errorf("acp: the session cwd %q is not absolute", cwd)
	}
	if mcpServers == nil {
		mcpServers = []MCPServer{}
	}
	var r struct {
		SessionID string `json:"sessionId"`
	}
	if err := c.call(ctx, "session/new", map[string]any{"cwd": cwd, "mcpServers": mcpServers}, &r); err != nil {
		return "", err
	}
	if r.SessionID == "" {
		return "", errors.New("acp: session/new returned no sessionId")
	}
	return r.SessionID, nil
}

// Prompt sends one text prompt and blocks until the turn ends, returning the
// stop reason. Updates arrive on the Handler meanwhile. Cancel ends the turn
// early with StopCancelled.
func (c *Client) Prompt(ctx context.Context, sessionID, text string) (string, error) {
	var r struct {
		StopReason string `json:"stopReason"`
	}
	err := c.call(ctx, "session/prompt", map[string]any{
		"sessionId": sessionID,
		"prompt":    []any{map[string]any{"type": "text", "text": text}},
	}, &r)
	return r.StopReason, err
}

// Cancel ends the session's current turn. Requests for permission the agent
// has open on the session are answered cancelled first, as the protocol
// requires, so an answer a person gives afterwards is dropped.
func (c *Client) Cancel(sessionID string) error {
	c.cancelPermissions(sessionID)
	return c.notify("session/cancel", map[string]any{"sessionId": sessionID})
}

// Close answers every open permission request cancelled, closes the agent's
// stdin so it can exit by itself, then ends the agent and everything it
// started. It is safe to call more than once and from several goroutines.
func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.closed = true
		c.mu.Unlock()

		c.cancelPermissions("")
		_ = c.in.Close()
		waitFor(c.reaped, c.cfg.ExitGrace)
		c.tree.End(c.cmd.Process, c.reaped, treeGrace)
		_ = c.out.Close()
		waitFor(c.loopDone, treeGrace)
		c.failPending(ErrClosed)

		handlers := make(chan struct{})
		go func() { c.handlers.Wait(); close(handlers) }()
		waitFor(handlers, handlerGrace)
	})
	return c.closeErr
}

func waitFor(ch <-chan struct{}, d time.Duration) {
	select {
	case <-ch:
	case <-time.After(d):
	}
}
