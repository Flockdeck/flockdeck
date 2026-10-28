package acp

import (
	"bytes"
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
		c.answer(p, "", nil)
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

	optionID, answers := c.callPermission(ctx, req)
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
	if optionID == "" {
		answers = nil
	}
	c.answer(p, optionID, answers)
}

// answer sends the reply to a permission request, once: the first answer wins
// and a later one (a late response after cancel) is dropped. An empty optionID
// answers cancelled.
func (c *Client) answer(p *permission, optionID string, answers json.RawMessage) {
	p.once.Do(func() {
		outcome := map[string]any{"outcome": "cancelled"}
		if optionID != "" {
			outcome = map[string]any{"outcome": "selected", "optionId": optionID}
		}
		res := map[string]any{"outcome": outcome}
		if len(answers) > 0 && json.Valid(answers) {
			res["_meta"] = map[string]any{"flockdeck": map[string]any{"answers": answers}}
		}
		c.reply(p.id, res, nil)
	})
}

// cancelPermissions answers the open requests of one session, or of every
// session when sessionID is empty, as cancelled and cancels their contexts.
// The answers are queued, not written, so this does not wait for the agent.
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
		c.answer(p, "", nil)
		p.cancel()
	}
}

// abandonedKept is how many abandoned ids are remembered; the oldest is
// forgotten first.
const abandonedKept = 256

// abandon remembers the id of a call that gave up waiting. Call with c.mu held.
func (c *Client) abandon(id int64) {
	if len(c.abandonedRing) < abandonedKept {
		c.abandonedRing = append(c.abandonedRing, id)
	} else {
		delete(c.abandoned, c.abandonedRing[c.abandonedNext])
		c.abandonedRing[c.abandonedNext] = id
		c.abandonedNext = (c.abandonedNext + 1) % abandonedKept
	}
	c.abandoned[id] = struct{}{}
}

var errOutboxFull = errors.New("acp: the agent is not reading its input")

// enqueue queues one message for the writer. It never waits: a full queue
// means the agent has stopped reading, and the caller is told so.
func (c *Client) enqueue(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	select {
	case <-c.stop:
		return ErrClosed
	default:
	}
	c.queued.Add(1)
	select {
	case c.outbox <- append(b, '\n'):
		return nil
	default:
		c.queued.Add(-1)
		return errOutboxFull
	}
}

// writer is the only goroutine that writes to the agent's stdin, and so the
// only one that can block on an agent that does not read. It ends when Close
// stops it; a Write it is stuck in ends when the agent does.
func (c *Client) writer() {
	defer close(c.writerDone)
	for {
		select {
		case b := <-c.outbox:
			c.tap(TapOut, bytes.TrimRight(b, "\n"))
			_, err := c.in.Write(b)
			c.queued.Add(-1)
			if err != nil {
				c.failPending(fmt.Errorf("acp: write to the agent: %w", err))
			}
		case <-c.stop:
			return
		}
	}
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
	if err := c.enqueue(m); errors.Is(err, errOutboxFull) {
		// The answer is lost, and the agent may wait for it for ever. This is
		// only reachable when the agent has stopped reading; it is counted in
		// ProtocolErrors so that it is not silent.
		c.protoErr("a reply to the agent could not be queued")
	}
}

// call sends a request and waits for its response. ctx ends the wait, not the
// request: the agent may still answer it. The wait also ends when the agent's
// output does, and when the client is closed.
func (c *Client) call(ctx context.Context, method string, params, result any) error {
	return c.callAs(ctx, method, params, result, "")
}

// callAs is call that, when loading is not empty, marks the updates for that
// session id as replay from before the request is sent until its answer is read
// (or the call ends).
func (c *Client) callAs(ctx context.Context, method string, params, result any, loading string) error {
	id := c.nextID.Add(1)
	ch := make(chan reply, 1)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrClosed
	}
	c.pending[id] = ch
	if loading != "" {
		c.loads[id] = loading
	}
	c.mu.Unlock()
	if loading != "" {
		defer func() {
			c.mu.Lock()
			delete(c.loads, id)
			c.mu.Unlock()
		}()
	}

	forget := func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}
	if err := c.enqueue(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		forget()
		return err
	}
	var r reply
	select {
	case r = <-ch:
	case <-c.loopDone:
		// The agent's output ended. A response that arrived just before is
		// still taken; otherwise there will not be one.
		select {
		case r = <-ch:
		default:
			forget()
			c.mu.Lock()
			closed := c.closed
			c.mu.Unlock()
			if closed {
				return ErrClosed
			}
			return &ExitError{Stderr: c.StderrTail()}
		}
	case <-ctx.Done():
		// The request was sent and the agent may still answer it: remember the
		// id so that answer is not counted as a stray one.
		c.mu.Lock()
		if _, waiting := c.pending[id]; waiting {
			delete(c.pending, id)
			c.abandon(id)
		}
		c.mu.Unlock()
		return ctx.Err()
	}
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
	return c.enqueue(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

// setup bounds a setup call (initialize, session/new) by SetupTimeout.
func (c *Client) setup(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, c.cfg.SetupTimeout)
}

// Initialize negotiates the protocol version and returns what the agent says
// about itself, including the authentication methods it lists. It advertises
// no fs or terminal capabilities. It gives up after Config.SetupTimeout.
func (c *Client) Initialize(ctx context.Context) (InitResult, error) {
	ctx, cancel := c.setup(ctx)
	defer cancel()
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
	r.Capabilities = parseCapabilities(r.AgentCapabilities)
	r.Capabilities.Rewind = r.Capabilities.Rewind && c.cfg.RewindAllowed
	c.mu.Lock()
	c.authMethods = r.AuthMethods
	c.caps = r.Capabilities
	c.mu.Unlock()
	return r, nil
}

// Capabilities is what the agent said it can do at initialize. It is the zero
// value until Initialize has succeeded.
func (c *Client) Capabilities() AgentCapabilities {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.caps
}

// Authenticate asks the agent to authenticate with one of the methods it
// listed at initialize. It gives up after Config.SetupTimeout, like the other
// setup calls: it is for a method that needs no person, such as a key the agent
// already has.
func (c *Client) Authenticate(ctx context.Context, methodID string) error {
	ctx, cancel := c.setup(ctx)
	defer cancel()
	return c.call(ctx, "authenticate", map[string]any{"methodId": methodID}, nil)
}

// NewSession starts a session and returns its id. See NewSessionInfo for the
// rest of the reply.
func (c *Client) NewSession(ctx context.Context, cwd string, mcpServers []MCPServer) (string, error) {
	r, err := c.NewSessionInfo(ctx, cwd, mcpServers)
	return r.SessionID, err
}

// NewSessionInfo starts a session. cwd must be absolute. mcpServers may be nil.
// Besides the id it returns the modes, models and config options the agent
// lists for the session; an agent that lists none leaves them empty. If the
// agent has no login this returns an *AuthRequiredError (some agents refuse
// here, others at the first prompt). It gives up after Config.SetupTimeout.
func (c *Client) NewSessionInfo(ctx context.Context, cwd string, mcpServers []MCPServer) (SessionNew, error) {
	if !filepath.IsAbs(cwd) {
		return SessionNew{}, fmt.Errorf("acp: the session cwd %q is not absolute", cwd)
	}
	if mcpServers == nil {
		mcpServers = []MCPServer{}
	}
	ctx, cancel := c.setup(ctx)
	defer cancel()
	var raw json.RawMessage
	if err := c.call(ctx, "session/new", map[string]any{"cwd": cwd, "mcpServers": mcpServers}, &raw); err != nil {
		return SessionNew{}, err
	}
	r := decodeSessionNew(raw)
	if r.SessionID == "" {
		return SessionNew{}, errors.New("acp: session/new returned no sessionId")
	}
	return r, nil
}

// ErrUnsupported is returned by LoadSession and ResumeSession when the agent did
// not say at initialize that it has the method.
var ErrUnsupported = errors.New("acp: the agent does not have that method")

// LoadSession reopens the session sessionID, which the agent made earlier, in
// cwd. The agent replays the conversation as session/update calls before it
// answers; the Handler gets them with Update.Replay set, and they are not
// a turn. It returns the modes, models and config options the agent lists for
// the session, as NewSessionInfo does; the id in the result is sessionID.
// It returns ErrUnsupported when the agent has no loadSession capability, and
// the agent's own error when it does not know the session. It gives up after
// Config.LoadTimeout.
func (c *Client) LoadSession(ctx context.Context, sessionID, cwd string, mcpServers []MCPServer) (SessionNew, error) {
	if !c.Capabilities().LoadSession {
		return SessionNew{}, ErrUnsupported
	}
	return c.reopen(ctx, "session/load", sessionID, cwd, mcpServers, c.cfg.LoadTimeout, sessionID)
}

// ResumeSession reopens the session sessionID like LoadSession, without the
// agent replaying the conversation. It returns ErrUnsupported when the agent
// has no session resume capability.
func (c *Client) ResumeSession(ctx context.Context, sessionID, cwd string, mcpServers []MCPServer) (SessionNew, error) {
	if !c.Capabilities().Resume {
		return SessionNew{}, ErrUnsupported
	}
	return c.reopen(ctx, "session/resume", sessionID, cwd, mcpServers, c.cfg.SetupTimeout, "")
}

func (c *Client) reopen(ctx context.Context, method, sessionID, cwd string, mcpServers []MCPServer, limit time.Duration, loading string) (SessionNew, error) {
	if sessionID == "" {
		return SessionNew{}, errors.New("acp: no session id to reopen")
	}
	if !filepath.IsAbs(cwd) {
		return SessionNew{}, fmt.Errorf("acp: the session cwd %q is not absolute", cwd)
	}
	if mcpServers == nil {
		mcpServers = []MCPServer{}
	}
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	var raw json.RawMessage
	if err := c.callAs(ctx, method, map[string]any{"sessionId": sessionID, "cwd": cwd, "mcpServers": mcpServers}, &raw, loading); err != nil {
		return SessionNew{}, err
	}
	r := decodeSessionNew(raw)
	// The agent may leave the id out of the answer, as the protocol allows, or
	// send another; the session is the one asked for.
	r.SessionID = sessionID
	return r, nil
}

// Prompt sends one text prompt and blocks until the turn ends, returning the
// stop reason. Updates arrive on the Handler meanwhile. Cancel ends the turn
// early with StopCancelled. A turn can be long, so only ctx bounds it.
func (c *Client) Prompt(ctx context.Context, sessionID, text string) (string, error) {
	return c.PromptBlocks(ctx, sessionID, text)
}

// PromptBlocks is Prompt with the prompt in several text content blocks, in the
// order given. Every agent takes text blocks, and a prompt with more than one
// keeps them apart, so a sender's words can be kept out of the block that says
// who the sender is.
func (c *Client) PromptBlocks(ctx context.Context, sessionID string, blocks ...string) (string, error) {
	var r struct {
		StopReason string `json:"stopReason"`
	}
	content := make([]any, 0, len(blocks))
	for _, b := range blocks {
		content = append(content, map[string]any{"type": "text", "text": b})
	}
	err := c.call(ctx, "session/prompt", map[string]any{
		"sessionId": sessionID,
		"prompt":    content,
	}, &r)
	return r.StopReason, err
}

// Cancel ends the session's current turn. Requests for permission the agent
// has open on the session are answered cancelled first, as the protocol
// requires, so an answer a person gives afterwards is dropped. It does not
// wait for the agent: it returns an error if the agent has stopped reading and
// the queue is full.
func (c *Client) Cancel(sessionID string) error {
	c.cancelPermissions(sessionID)
	return c.notify("session/cancel", map[string]any{"sessionId": sessionID})
}

// Close answers every open permission request cancelled, closes the agent's
// stdin so it can exit by itself, then ends the agent and everything it
// started. It is safe to call more than once and from several goroutines, and
// it returns within a bounded time whatever the agent does: an agent that has
// stopped reading is waited for flushGrace, not forever.
func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.closed = true
		c.mu.Unlock()

		// The answers are queued; give the writer a moment to send them.
		c.cancelPermissions("")
		for deadline := time.Now().Add(flushGrace); c.queued.Load() > 0 && time.Now().Before(deadline); {
			time.Sleep(5 * time.Millisecond)
		}
		close(c.stop)
		// Closing a pipe a Write is stuck in may itself wait on some systems,
		// so it is not waited for: the process is ended below either way.
		go func() { _ = c.in.Close() }()

		waitFor(c.reaped, c.cfg.ExitGrace)
		c.tree.End(c.cmd.Process, c.reaped, treeGrace)
		_ = c.out.Close()
		_ = c.errR.Close()
		waitFor(c.loopDone, treeGrace)
		waitFor(c.writerDone, treeGrace)
		c.failPending(ErrClosed)

		// Everything is read and written by now: let the tap have what is
		// queued, for a moment.
		if c.tapStop != nil {
			close(c.tapStop)
			waitFor(c.tapDone, tapGrace)
		}

		handlers := make(chan struct{})
		go func() { c.handlers.Wait(); close(handlers) }()
		waitFor(handlers, handlerGrace)
	})
	return nil
}

func waitFor(ch <-chan struct{}, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ch:
	case <-t.C:
	}
}

// AuthenticateWait is Authenticate for a method that needs a person, such as a
// sign-in in a browser: it is bounded by ctx alone, not by Config.SetupTimeout.
func (c *Client) AuthenticateWait(ctx context.Context, methodID string) error {
	return c.call(ctx, "authenticate", map[string]any{"methodId": methodID}, nil)
}
