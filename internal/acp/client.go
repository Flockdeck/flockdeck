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
// The agent is not trusted. It may stop reading its input, flood its output,
// send garbage or never answer, and none of that may hang or exhaust the
// client: writes go through a bounded queue and a writer goroutine, so Close,
// Cancel and every call return even when the agent has stopped reading; lines
// are capped; requests from the agent in flight at once are capped; every wait
// ends when the agent's output ends.
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
//   - Lines are read with bufio.Reader, not Scanner, so a large tool result does
//     not hit Scanner's limit (the cap is Config.MaxLineBytes); stderr is always
//     drained.
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
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jmwri/flockdeck/internal/proctree"
)

// Handler receives what the agent initiates. A panic in either method is
// recovered: a failed Update is dropped and counted, a failed Permission
// answers cancelled. Panics in the Config.Stderr and Config.OnProtocolError
// callbacks are recovered and ignored.
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
	// because the request was already answered cancelled. Permission must
	// return soon after ctx is done: Close waits for it one second and then
	// returns anyway, leaving the goroutine running.
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
	// environment. Nil means MinimalEnv, not the parent's: the agent is
	// untrusted, and a caller that wants it to see more says so by passing a
	// list. MinimalEnv has no HOME, USERPROFILE or APPDATA, so an agent that
	// reads its login from there (opencode, for one) needs an Env from the
	// caller that includes what it should see.
	Dir string
	Env []string

	Handler Handler
	// Stderr, if set, receives each line the agent writes to stderr, cut at
	// 64 KiB. It is called from its own goroutine; if it blocks, lines are
	// dropped rather than the agent's stderr left unread.
	Stderr func(line string)
	// OnProtocolError, if set, is told of each thing the agent sent that the
	// client could not use: a line that is not JSON, an update it could not
	// read, a response to nothing. It is called on the read loop and must not
	// block. ProtocolErrors counts them either way.
	OnProtocolError func(description string)

	// ClientName and ClientVersion go in initialize. Defaults: "flockdeck", "".
	ClientName    string
	ClientVersion string

	// ExitGrace is how long Close waits for the agent to exit by itself after
	// its stdin closes, before the tree is ended. Default 750ms.
	ExitGrace time.Duration
	// SetupTimeout bounds initialize and session/new, whatever the caller's
	// context allows. Default 60s.
	SetupTimeout time.Duration
	// MaxLineBytes is the longest line read from the agent's stdout. A longer
	// one is a lost peer. Default 64 MiB.
	MaxLineBytes int
	// MaxAgentRequests is how many requests from the agent may be in flight
	// at once; more are answered with an error. Default 32.
	MaxAgentRequests int
}

const (
	defaultExitGrace    = 750 * time.Millisecond
	defaultSetupTimeout = 60 * time.Second
	defaultMaxLine      = 64 << 20
	defaultMaxRequests  = 32

	// treeGrace is how long Close waits for the agent to be gone once its
	// tree has been ended.
	treeGrace = 3 * time.Second
	// handlerGrace is how long Close waits for Permission calls to return after
	// their context is cancelled.
	handlerGrace = time.Second
	// flushGrace is how long Close gives the writer to send the cancelled
	// answers to open permission requests before it stops waiting for an agent
	// that is not reading.
	flushGrace = 500 * time.Millisecond

	stderrTail    = 4096
	stderrMaxLine = 64 << 10
	outboxSize    = 256
)

// MinimalEnv is the environment an agent gets when Config.Env is nil: enough
// to find programs and a temp directory, and none of the caller's secrets or
// home directory.
func MinimalEnv() []string {
	var env []string
	for _, k := range []string{"PATH", "SYSTEMROOT", "SystemRoot", "SYSTEMDRIVE", "COMSPEC", "PATHEXT", "TEMP", "TMP", "TMPDIR", "LANG", "LC_ALL"} {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	return env
}

type rpcMsg struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Method  string           `json:"method,omitempty"`
	Params  json.RawMessage  `json:"params,omitempty"`
	Result  json.RawMessage  `json:"result,omitempty"`
	Error   *RPCError        `json:"error,omitempty"`
}

// reply is what a waiting call is woken with: the agent's response, or the
// reason there will not be one.
type reply struct {
	msg rpcMsg
	err error
}

// Client is a connection to one agent process.
type Client struct {
	cfg  Config
	cmd  *exec.Cmd
	tree *proctree.Tree
	in   io.WriteCloser
	out  io.ReadCloser
	errR io.ReadCloser

	// Writes go to outbox and are made by writer, the only goroutine that can
	// block on the agent's stdin. queued counts what is in the queue or being
	// written.
	outbox     chan []byte
	queued     atomic.Int64
	stop       chan struct{}
	writerDone chan struct{}

	nextID    atomic.Int64
	protoErrs atomic.Int64

	mu      sync.Mutex
	pending map[int64]chan reply
	// abandoned holds the ids of calls whose context ended before the answer
	// came, so a late answer is not taken for a stray one. Bounded.
	abandoned   map[int64]struct{}
	perms       map[*permission]struct{}
	inflight    int
	handlers    sync.WaitGroup
	closed      bool
	authMethods []AuthMethod
	errTail     []byte

	loopDone chan struct{} // the read loop has ended
	reaped   chan struct{} // the agent process has been waited for

	closeOnce sync.Once
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
	if cfg.SetupTimeout <= 0 {
		cfg.SetupTimeout = defaultSetupTimeout
	}
	if cfg.MaxLineBytes <= 0 {
		cfg.MaxLineBytes = defaultMaxLine
	}
	if cfg.MaxAgentRequests <= 0 {
		cfg.MaxAgentRequests = defaultMaxRequests
	}
	if cfg.ClientName == "" {
		cfg.ClientName = "flockdeck"
	}
	if cfg.Env == nil {
		cfg.Env = MinimalEnv()
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
		cfg:        cfg,
		cmd:        cmd,
		tree:       tree,
		in:         in,
		out:        out,
		errR:       errp,
		outbox:     make(chan []byte, outboxSize),
		stop:       make(chan struct{}),
		writerDone: make(chan struct{}),
		pending:    map[int64]chan reply{},
		abandoned:  map[int64]struct{}{},
		perms:      map[*permission]struct{}{},
		loopDone:   make(chan struct{}),
		reaped:     make(chan struct{}),
	}
	// Process.Wait, not Cmd.Wait: Cmd.Wait would wait for the pipes to close
	// too, and a program the agent left running can hold them open.
	go func() {
		_, _ = cmd.Process.Wait()
		close(c.reaped)
	}()
	go c.writer()
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

// ProtocolErrors counts what the agent sent that the client could not use.
func (c *Client) ProtocolErrors() int64 { return c.protoErrs.Load() }

func (c *Client) protoErr(description string) {
	c.protoErrs.Add(1)
	if c.cfg.OnProtocolError != nil {
		defer func() { _ = recover() }() // a callback that panics must not end the read loop
		c.cfg.OnProtocolError(description)
	}
}

// errLineTooLong is a line over the limit.
var errLineTooLong = errors.New("line too long")

// ErrLineTooLong is returned to calls waiting when the agent sent a line over
// Config.MaxLineBytes.
var ErrLineTooLong = errors.New("acp: the agent sent a line longer than the limit")

// readLine reads one line without the line-length cap of bufio.Scanner and
// with a cap of its own. Over max it returns errLineTooLong, or, with truncate,
// the first max bytes with the rest of the line skipped.
func readLine(br *bufio.Reader, max int, truncate bool) ([]byte, error) {
	var buf []byte
	over := false
	for {
		frag, err := br.ReadSlice('\n')
		if !over {
			if len(buf)+len(frag) > max {
				if !truncate {
					return nil, errLineTooLong
				}
				frag = frag[:max-len(buf)]
				over = true
			}
			buf = append(buf, frag...)
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return buf, err
	}
}

func (c *Client) drainStderr(r io.Reader) {
	var lines chan string
	if c.cfg.Stderr != nil {
		lines = make(chan string, 256)
		go func() {
			for l := range lines {
				func() {
					defer func() { _ = recover() }()
					c.cfg.Stderr(l)
				}()
			}
		}()
		defer close(lines)
	}
	br := bufio.NewReaderSize(r, 32<<10)
	for {
		line, err := readLine(br, stderrMaxLine, true)
		if len(line) > 0 {
			c.mu.Lock()
			c.errTail = append(c.errTail, line...)
			if over := len(c.errTail) - stderrTail; over > 0 {
				c.errTail = append([]byte(nil), c.errTail[over:]...)
			}
			c.mu.Unlock()
			if lines != nil {
				select {
				case lines <- string(bytes.TrimRight(line, "\r\n")):
				default: // the callback is behind; dropping beats stalling the agent
				}
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
		line, err := readLine(br, c.cfg.MaxLineBytes, false)
		if errors.Is(err, errLineTooLong) {
			// A peer that never ends a line is a lost peer. Ending it from
			// here would wait on this goroutine, so Close runs on its own.
			c.protoErr("a line over the limit")
			c.failPending(ErrLineTooLong)
			c.cancelPermissions("")
			go func() { _ = c.Close() }()
			return
		}
		if len(bytes.TrimSpace(line)) > 0 {
			var m rpcMsg
			if json.Unmarshal(line, &m) != nil {
				c.protoErr("a line that is not JSON")
			} else {
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
			c.protoErr("a response with an id that is not ours")
			return
		}
		c.mu.Lock()
		ch := c.pending[id]
		delete(c.pending, id)
		_, late := c.abandoned[id]
		delete(c.abandoned, id)
		c.mu.Unlock()
		if ch == nil && late {
			return // the caller gave up on this one; its answer is not an error
		}
		if ch == nil {
			c.protoErr("a response to a request that is not waiting")
			return
		}
		ch <- reply{msg: m} // buffered: never blocks the read loop
	case m.Method != "" && m.ID == nil: // a notification
		if m.Method != "session/update" {
			return
		}
		var p struct {
			SessionID string          `json:"sessionId"`
			Update    json.RawMessage `json:"update"`
		}
		if json.Unmarshal(m.Params, &p) != nil || len(p.Update) == 0 {
			c.protoErr("a session/update that could not be read")
			return
		}
		c.callUpdate(decodeUpdate(p.SessionID, p.Update))
	case m.Method != "" && m.ID != nil: // a request from the agent
		// Its own goroutine: a permission request waits for a person. The
		// number in flight is capped; the agent is told when it asks for more.
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			return
		}
		if c.inflight >= c.cfg.MaxAgentRequests {
			c.mu.Unlock()
			c.reply(m.ID, nil, &RPCError{Code: -32001, Message: "too many requests in flight"})
			return
		}
		c.inflight++
		c.handlers.Add(1)
		c.mu.Unlock()
		go func() {
			defer func() {
				c.mu.Lock()
				c.inflight--
				c.mu.Unlock()
				c.handlers.Done()
			}()
			c.serve(m)
		}()
	}
}

// callUpdate gives an update to the handler, which may panic.
func (c *Client) callUpdate(u Update) {
	defer func() {
		if recover() != nil {
			c.protoErr("the handler panicked in Update")
		}
	}()
	c.cfg.Handler.Update(u)
}

// callPermission asks the handler, which may panic. A panic answers cancelled.
func (c *Client) callPermission(ctx context.Context, req PermissionRequest) (optionID string) {
	defer func() {
		if recover() != nil {
			optionID = ""
		}
	}()
	return c.cfg.Handler.Permission(ctx, req)
}
