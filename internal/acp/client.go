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
	"github.com/jmwri/flockdeck/internal/sysproc"
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
	// block, and must be safe for concurrent use: it is also called from the
	// goroutines that serve the agent's requests. ProtocolErrors counts them
	// either way.
	OnProtocolError func(description string)
	// Tap, if set, is given each line of JSON-RPC traffic: dir is TapIn for a
	// line read from the agent and TapOut for a line written to it, raw is one
	// message without its newline. A line from the agent is tapped before it is
	// parsed, so a line that is not JSON is seen too; a line to the agent is
	// tapped as the writer takes it, once encoded and before it is written, so
	// a message the agent never reads is still recorded. Blank lines are not
	// tapped.
	//
	// Tap is called on a goroutine of its own, one line at a time, in the order
	// the lines were read or taken for writing. The lines wait in a queue of
	// 1024 lines and 32 MiB; a line that does not fit is dropped and counted
	// (TapDropped), so a Tap that is slow, or stuck, never holds up the read
	// loop or the writer, and never costs a reply to the agent. A recorder that
	// must not lose lines has to keep up, and whoever owns it has to read
	// TapDropped: nothing here writes a marker where lines were dropped, so a
	// recording with gaps looks complete unless the count is polled. Lines of the two directions are queued
	// from different goroutines, so their order relative to each other is the
	// order they reached the queue.
	//
	// raw is the client's copy, so Tap may change it, but it is reused: copy
	// what you keep. A panic in Tap is recovered and counted in TapPanics, apart
	// from ProtocolErrors, which counts what the agent did. A nil Tap costs one
	// comparison per line.
	//
	// Tap must not call Close: Close waits half a second for the queue to
	// empty, which a Tap that is waiting for Close would make a long wait. A
	// Tap that blocks for ever leaves its goroutine running after Close; Close
	// does not wait for it.
	//
	// The client taps every line up to MaxLineBytes; a longer line ends the
	// session and is not tapped. What a recorder does with a long line is the
	// recorder's to decide: internal/record notes a message over 1 MiB and
	// keeps none of it, and its reader takes files up to 32 MiB.
	// internal/acp does not import internal/record; TapIn and TapOut have the
	// values of its RPCIn and RPCOut.
	Tap func(dir string, raw []byte)

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

	// Config.Tap is given lines through tapQ by tapLoop. tapBytes is what the
	// queue holds; tapStop ends the loop once the queue is empty.
	tapQ       chan tapItem
	tapBytes   atomic.Int64
	tapDropped atomic.Int64
	tapPanics  atomic.Int64
	tapStop    chan struct{}
	tapDone    chan struct{}

	mu      sync.Mutex
	pending map[int64]chan reply
	// abandoned holds the ids of calls whose context ended before the answer
	// came, so a late answer is not taken for a stray one. Bounded by a ring (abandonedKept).
	abandoned     map[int64]struct{}
	abandonedRing []int64
	abandonedNext int
	perms         map[*permission]struct{}
	inflight      int
	handlers      sync.WaitGroup
	closed        bool
	authMethods   []AuthMethod
	errTail       []byte

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
	// An adapter is a console program run in the background; on Windows it
	// would otherwise open a terminal window.
	sysproc.NoWindow(cmd)
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
	if cfg.Tap != nil {
		c.initTap()
		go c.tapLoop()
	}
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

// The directions Config.Tap is told. They are the values of record.RPCIn and
// record.RPCOut, which this package does not import.
const (
	TapIn  = "in"
	TapOut = "out"
)

const (
	tapQueueLines = 1024
	tapQueueBytes = 32 << 20
	// tapGrace is how long Close waits for the queue of tapped lines to empty.
	tapGrace = 500 * time.Millisecond
)

type tapItem struct {
	dir string
	raw []byte
}

// initTap makes the queue and the channels the tap loop uses.
func (c *Client) initTap() {
	c.tapQ, c.tapStop, c.tapDone = make(chan tapItem, tapQueueLines), make(chan struct{}), make(chan struct{})
}

// tap queues a line for Config.Tap, if there is one. It never waits: a line that
// does not fit in the queue is dropped and counted.
func (c *Client) tap(dir string, raw []byte) {
	if c.cfg.Tap == nil {
		return
	}
	if c.tapBytes.Add(int64(len(raw))) > tapQueueBytes {
		c.tapBytes.Add(-int64(len(raw)))
		c.tapDropped.Add(1)
		return
	}
	select {
	case c.tapQ <- tapItem{dir, append([]byte(nil), raw...)}:
	default:
		c.tapBytes.Add(-int64(len(raw)))
		c.tapDropped.Add(1)
	}
}

// tapLoop gives the queued lines to Config.Tap, one at a time. It ends once the
// client is stopped and the queue is empty.
func (c *Client) tapLoop() {
	defer close(c.tapDone)
	call := func(it tapItem) {
		defer func() {
			if recover() != nil {
				c.tapPanics.Add(1)
			}
		}()
		c.cfg.Tap(it.dir, it.raw)
	}
	for {
		select {
		case it := <-c.tapQ:
			c.tapBytes.Add(-int64(len(it.raw)))
			call(it)
		case <-c.tapStop:
			for {
				select {
				case it := <-c.tapQ:
					c.tapBytes.Add(-int64(len(it.raw)))
					call(it)
				default:
					return
				}
			}
		}
	}
}

// TapDropped counts the lines Config.Tap was not given because it was behind.
func (c *Client) TapDropped() int64 { return c.tapDropped.Load() }

// TapPanics counts the panics recovered in Config.Tap.
func (c *Client) TapPanics() int64 { return c.tapPanics.Load() }

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
			c.tap(TapIn, bytes.TrimRight(line, "\r\n"))
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
