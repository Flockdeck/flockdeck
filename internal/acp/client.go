// Package acp is a client for the Agent Client Protocol (version 1): JSON-RPC
// 2.0 over newline-delimited stdio to an agent process Flockdeck starts. It
// covers initialize, authenticate, session/new, session/prompt, session/cancel,
// session/request_permission and the session/update stream. session/load and
// session/resume are not part of it.
//
// The client advertises no fs/* or terminal/* methods, so a conforming agent
// does not call them, and any request it does send that the client does not
// know is answered with a method-not-found error.
//
// Rules the code keeps, each from the ACP spike (see the tests):
//
//   - Responses and notifications are handled on the read loop in wire order.
//     A goroutine per line reordered updates, so Handler.Update must not block.
//   - Only agent-to-client requests run on their own goroutine, because
//     session/request_permission blocks on a person.
//   - Request ids are echoed back as the raw JSON the agent sent (a number or a
//     string).
//   - Unknown update kinds, content block types and fields are kept as raw JSON
//     and delivered, never dropped and never an error.
//   - Lines are read with bufio.Reader.ReadBytes, so a large tool result does
//     not hit a line limit; stderr is always drained.
//   - The executable path must be absolute: with Dir set, a relative one is
//     resolved against Dir and fails.
package acp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jmwri/flockdeck/internal/proctree"
)

// Handler receives what the agent initiates.
type Handler interface {
	// Update receives each session/update on the client's read loop, in the
	// order the agent sent them. It must not block: nothing else is read
	// while it runs.
	Update(Update)

	// Permission is called on its own goroutine and may block until a person
	// answers. It returns the id of the chosen option, which must be one of
	// req.Options, or "" to answer cancelled. ctx is cancelled when the request
	// no longer needs an answer: the session was cancelled, or the client is
	// closing or has lost the agent. An answer returned after that is dropped,
	// because the request was already answered cancelled.
	Permission(ctx context.Context, req PermissionRequest) (optionID string)
}

// Config describes the agent to start.
type Config struct {
	// Path is the agent's executable, and must be absolute. Args follow it.
	// For an agent that is a Node script, Path is node and Args begins with
	// the script's path; a .cmd shim was not tried.
	Path string
	Args []string
	// Dir is the working directory of the process. Env is its full
	// environment: nil means the parent's, so pass one built on purpose.
	Dir string
	Env []string

	Handler Handler
	// Stderr, if set, receives each line the agent writes to stderr. Stderr is
	// drained whether or not it is set.
	Stderr func(line string)

	// ClientName and ClientVersion go in initialize. Defaults: "flockdeck", "".
	ClientName    string
	ClientVersion string

	// ExitGrace is how long Close waits for the agent to exit by itself after
	// its stdin closes, before the tree is ended. Default 750ms.
	ExitGrace time.Duration
}

const (
	defaultExitGrace = 750 * time.Millisecond
	// treeGrace is how long Close waits for the agent to be gone once its
	// tree has been ended.
	treeGrace = 3 * time.Second
	// handlerGrace is how long Close waits for Permission calls to return after
	// their context is cancelled.
	handlerGrace = time.Second
	stderrTail   = 4096
)

type rpcMsg struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Method  string           `json:"method,omitempty"`
	Params  json.RawMessage  `json:"params,omitempty"`
	Result  json.RawMessage  `json:"result,omitempty"`
	Error   *RPCError        `json:"error,omitempty"`
}

// Client is a connection to one agent process.
type Client struct {
	cfg  Config
	cmd  *exec.Cmd
	tree *proctree.Tree
	in   io.WriteCloser
	out  io.ReadCloser

	wmu    sync.Mutex
	nextID atomic.Int64

	mu          sync.Mutex
	pending     map[int64]chan reply
	perms       map[*permission]struct{}
	handlers    sync.WaitGroup
	closed      bool
	authMethods []AuthMethod
	errTail     []byte

	loopDone chan struct{} // the read loop has ended
	reaped   chan struct{} // the agent process has been waited for

	closeOnce sync.Once
	closeErr  error
}

// reply is what a waiting call is woken with: the agent's response, or the
// reason there will not be one.
type reply struct {
	msg rpcMsg
	err error
}

// permission is an agent request waiting for a person.
type permission struct {
	id        *json.RawMessage
	sessionID string
	options   []PermissionOption
	ctx       context.Context
	cancel    context.CancelFunc
	once      sync.Once
}

// Start launches the agent and begins reading its output. The caller then
// calls Initialize.
func Start(cfg Config) (*Client, error) {
	if cfg.Handler == nil {
		return nil, errors.New("acp: Config.Handler is required")
	}
	if !filepath.IsAbs(cfg.Path) {
		return nil, fmt.Errorf("acp: the agent path %q is not absolute", cfg.Path)
	}
	if cfg.ExitGrace <= 0 {
		cfg.ExitGrace = defaultExitGrace
	}
	if cfg.ClientName == "" {
		cfg.ClientName = "flockdeck"
	}

	cmd := exec.Command(cfg.Path, cfg.Args...)
	cmd.Dir = cfg.Dir
	cmd.Env = cfg.Env
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	errp, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	tree, err := proctree.Start(cmd)
	if err != nil {
		return nil, fmt.Errorf("acp: start %s: %w", cfg.Path, err)
	}

	c := &Client{
		cfg:      cfg,
		cmd:      cmd,
		tree:     tree,
		in:       in,
		out:      out,
		pending:  map[int64]chan reply{},
		perms:    map[*permission]struct{}{},
		loopDone: make(chan struct{}),
		reaped:   make(chan struct{}),
	}
	// Process.Wait, not Cmd.Wait: Cmd.Wait would wait for the pipes to close
	// too, and a program the agent left running can hold them open.
	go func() {
		_, _ = cmd.Process.Wait()
		close(c.reaped)
	}()
	go c.drainStderr(errp)
	go c.readLoop()
	return c, nil
}

// Done is closed when the agent's output has ended.
func (c *Client) Done() <-chan struct{} { return c.loopDone }

// StderrTail returns the last few KB the agent wrote to stderr.
func (c *Client) StderrTail() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return string(c.errTail)
}

func (c *Client) drainStderr(r io.Reader) {
	br := bufio.NewReader(r)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			c.mu.Lock()
			c.errTail = append(c.errTail, line...)
			if over := len(c.errTail) - stderrTail; over > 0 {
				c.errTail = append([]byte(nil), c.errTail[over:]...)
			}
			c.mu.Unlock()
			if c.cfg.Stderr != nil {
				c.cfg.Stderr(string(bytes.TrimRight(line, "\r\n")))
			}
		}
		if err != nil {
			return
		}
	}
}

func (c *Client) readLoop() {
	defer close(c.loopDone)
	br := bufio.NewReaderSize(c.out, 1<<20)
	for {
		line, err := br.ReadBytes('\n') // no line-length cap, unlike bufio.Scanner
		if len(bytes.TrimSpace(line)) > 0 {
			var m rpcMsg
			if json.Unmarshal(line, &m) == nil {
				c.dispatch(m)
			}
		}
		if err != nil {
			c.lost()
			return
		}
	}
}

// lost is the agent's output ending: calls waiting are failed and requests
// waiting on a person no longer need an answer.
func (c *Client) lost() {
	c.failPending(&ExitError{Stderr: c.StderrTail()})
	c.mu.Lock()
	perms := make([]*permission, 0, len(c.perms))
	for p := range c.perms {
		perms = append(perms, p)
	}
	c.mu.Unlock()
	for _, p := range perms {
		p.cancel()
	}
}

func (c *Client) failPending(err error) {
	c.mu.Lock()
	pending := c.pending
	c.pending = map[int64]chan reply{}
	c.mu.Unlock()
	for _, ch := range pending {
		ch <- reply{err: err}
	}
}

func (c *Client) dispatch(m rpcMsg) {
	switch {
	case m.Method == "" && m.ID != nil: // a response to one of our requests
		var id int64
		if json.Unmarshal(*m.ID, &id) != nil {
			return
		}
		c.mu.Lock()
		ch := c.pending[id]
		delete(c.pending, id)
		c.mu.Unlock()
		if ch != nil {
			ch <- reply{msg: m} // buffered: never blocks the read loop
		}
	case m.Method != "" && m.ID == nil: // a notification
		if m.Method != "session/update" {
			return
		}
		var p struct {
			SessionID string          `json:"sessionId"`
			Update    json.RawMessage `json:"update"`
		}
		if json.Unmarshal(m.Params, &p) != nil || len(p.Update) == 0 {
			return
		}
		c.cfg.Handler.Update(decodeUpdate(p.SessionID, p.Update))
	case m.Method != "" && m.ID != nil: // a request from the agent
		// Its own goroutine: a permission request waits for a person, and
		// even a refusal is a write that can block while the agent is not
		// reading.
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			return
		}
		c.handlers.Add(1)
		c.mu.Unlock()
		go func() {
			defer c.handlers.Done()
			c.serve(m)
		}()
	}
}
