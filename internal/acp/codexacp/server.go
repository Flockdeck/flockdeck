// Package codexacp is an ACP agent for Codex. It runs as a subcommand of the
// flockdeck binary (`flockdeck acp-adapter codex`), speaks ACP on its own stdin
// and stdout, and drives one `codex app-server --listen stdio://` process per
// session. It needs nothing but the codex executable.
//
// What it does, in the order a session goes:
//
//   - initialize answers from a fixed description of what the adapter can do.
//   - session/new starts codex, does the app-server initialize and initialized
//     handshake, starts a thread, and returns the modes and models. The skills
//     codex lists, and /review and /compact, follow as an
//     available_commands_update. session/resume does the same with
//     thread/resume.
//   - session/prompt turns the ACP content blocks into one turn/start. Text,
//     reasoning and tool activity are sent as session/update while the turn
//     runs, and the prompt ends when codex reports the turn completed.
//   - codex's approval requests become session/request_permission. Anything
//     the adapter does not understand is refused.
//   - session/cancel sends turn/interrupt.
//
// The prompt policy for a prompt that arrives while a turn is running is to
// queue it: each prompt gets its own turn and its own stop reason, in order. A
// cancel ends the running turn and every prompt queued behind it.
//
// See docs/acp-codex-adapter.md for what is and is not done, and which parts of
// the wire were built from the route document instead of a real codex.
package codexacp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jmwri/flockdeck/internal/acp/agentlogin"
)

// Options configure Run.
type Options struct {
	// CodexPath is the codex executable: a path, or a name looked up on PATH.
	// Default "codex".
	CodexPath string
	// Env is the environment codex gets. Nil means this process's own. Either
	// way the variables Flockdeck sets for itself are removed.
	Env []string
	// Version is reported in initialize as the adapter's version.
	Version string
	// Stderr receives diagnostics: why codex could not start, what it wrote on
	// its stderr. Nil discards them.
	Stderr io.Writer
	// ControlTimeout bounds codex's answer to a request such as initialize or
	// thread/start. Default 30s.
	ControlTimeout time.Duration
	// CancelGrace is how long an interrupt may take to end the turn before the
	// codex process is killed. Default 15s.
	CancelGrace time.Duration
}

func (o *Options) defaults() {
	if o.CodexPath == "" {
		o.CodexPath = "codex"
	}
	if o.Env == nil {
		o.Env = os.Environ()
	}
	o.Env = cleanEnv(o.Env)
	if o.Stderr == nil {
		o.Stderr = io.Discard
	}
	if o.ControlTimeout <= 0 {
		o.ControlTimeout = 30 * time.Second
	}
	if o.CancelGrace <= 0 {
		o.CancelGrace = 15 * time.Second
	}
	if o.Version == "" {
		o.Version = "dev"
	}
}

// cleanEnv drops Flockdeck's own variables: codex has no use for them and a
// pane's session or token names should not reach a model's tools.
func cleanEnv(env []string) []string {
	// Not nil, even when every variable is dropped: a nil Env on an exec.Cmd
	// means the parent's environment.
	out := []string{}
	for _, kv := range env {
		// Windows names are not case sensitive, so neither is this.
		up := strings.ToUpper(kv)
		if strings.HasPrefix(up, "FLOCKDECK_") || strings.HasPrefix(up, "PERCH_") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// JSON-RPC error codes used here.
const (
	codeInvalidParams  = -32602
	codeMethodNotFound = -32601
	codeInternal       = -32603
	codeAuthRequired   = -32000
)

// unavailable is the only thing a person is told when codex cannot be started.
// What went wrong goes to the adapter's stderr.
const unavailable = "Codex is unavailable"

type rpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcErr) Error() string { return e.Message }

type rpcMsg struct {
	ID     *json.RawMessage `json:"id,omitempty"`
	Method string           `json:"method,omitempty"`
	Params json.RawMessage  `json:"params,omitempty"`
	Result json.RawMessage  `json:"result,omitempty"`
	Error  *rpcErr          `json:"error,omitempty"`
}

// The longest line read from the client and from codex. A line past the limit
// ends the connection it came on, and is never held in memory whole. Variables
// so that a test can use a small one.
var (
	maxClientLine = 32 << 20
	maxCodexLine  = 16 << 20
)

type server struct {
	opts Options
	out  *bufio.Writer
	omu  sync.Mutex

	mu       sync.Mutex
	closed   bool // closeAll has run: a session that finishes starting now is closed
	sessions map[string]*session
	starting map[*session]struct{} // started, not yet answered session/new
	clientRQ map[int64]chan rpcMsg // requests we made to the client, by id
	login    agentlogin.Runner
	nextReq  atomic.Int64
	wg       sync.WaitGroup
}

// Run serves ACP on in and out until in ends or ctx is done, then closes every
// session. It returns nil when in ended cleanly or ctx was cancelled. In the
// second case the goroutine reading in is left blocked, which is fine for a
// process that is about to exit. A line from the client over the limit ends
// the run with an error.
func Run(ctx context.Context, in io.Reader, out io.Writer, o Options) error {
	o.defaults()
	sweepImageDirs()
	s := &server{
		opts:     o,
		out:      bufio.NewWriterSize(out, 1<<16),
		sessions: map[string]*session{},
		starting: map[*session]struct{}{},
		clientRQ: map[int64]chan rpcMsg{},
	}
	ended := make(chan error, 1)
	go func() {
		ended <- readLines(in, maxClientLine, func(b []byte) {
			var m rpcMsg
			if json.Unmarshal(b, &m) == nil {
				s.handle(m)
			} else {
				s.logf("ignored a line that is not JSON")
			}
		})
	}()
	var readErr error
	select {
	case readErr = <-ended:
	case <-ctx.Done():
	}
	if errors.Is(readErr, errLineTooLong) {
		s.logf("a line from the client was over %d bytes; stopping", maxClientLine)
	}
	s.closeAll()
	s.wg.Wait()
	s.closeAll() // a session that was starting when the first call ran
	return readErr
}

func (s *server) logf(format string, a ...any) {
	if s.opts.Stderr == io.Discard {
		return
	}
	line := "codex-acp: " + fmt.Sprintf(format, a...) + "\n"
	_, _ = io.WriteString(s.opts.Stderr, line)
}

func (s *server) send(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	s.omu.Lock()
	_, _ = s.out.Write(append(b, '\n'))
	_ = s.out.Flush()
	s.omu.Unlock()
}

func (s *server) reply(id *json.RawMessage, result any) {
	if id == nil {
		return
	}
	s.send(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func (s *server) replyErr(id *json.RawMessage, e *rpcErr) {
	if id == nil {
		return
	}
	s.send(map[string]any{"jsonrpc": "2.0", "id": id, "error": e})
}

func (s *server) update(sessionID string, u map[string]any) {
	s.send(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": sessionID, "update": u}})
}

// request sends a request to the client and returns the id to forget it by and
// the channel its response arrives on.
func (s *server) request(method string, params any) (int64, chan rpcMsg) {
	id := s.nextReq.Add(1)
	ch := make(chan rpcMsg, 1)
	s.mu.Lock()
	s.clientRQ[id] = ch
	s.mu.Unlock()
	s.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	return id, ch
}

func (s *server) forget(id int64) {
	s.mu.Lock()
	delete(s.clientRQ, id)
	s.mu.Unlock()
}

func (s *server) handle(m rpcMsg) {
	if m.Method == "" {
		s.handleResponse(m)
		return
	}
	switch m.Method {
	case "initialize":
		s.reply(m.ID, map[string]any{
			"protocolVersion": 1,
			"agentCapabilities": map[string]any{
				"loadSession":         false,
				"promptCapabilities":  map[string]any{"image": true, "audio": false, "embeddedContext": true},
				"mcpCapabilities":     map[string]any{"http": true, "sse": false},
				"sessionCapabilities": map[string]any{"resume": map[string]any{}, "fork": map[string]any{}},
				"_meta":               map[string]any{extKey: map[string]any{"rewind": 1}},
			},
			"agentInfo":   map[string]any{"name": "flockdeck-codex", "title": "Codex", "version": s.opts.Version},
			"authMethods": authMethods(),
		})
	case "session/new", "session/resume", "session/fork":
		s.wg.Add(1)
		go func() { defer s.wg.Done(); s.openSession(m, m.Method) }()
	case "authenticate":
		s.wg.Add(1)
		go func() { defer s.wg.Done(); s.authenticate(m) }()
	case "_flockdeck/forkpoint":
		s.forkPoint(m)
	case "_flockdeck/rewind":
		s.wg.Add(1)
		go func() { defer s.wg.Done(); s.rewind(m) }()
	case "session/prompt":
		s.prompt(m)
	case "session/cancel":
		var p struct {
			SessionID string `json:"sessionId"`
		}
		_ = json.Unmarshal(m.Params, &p)
		if ss := s.session(p.SessionID); ss != nil {
			ss.cancel()
		}
	case "session/set_mode", "session/set_model":
		s.wg.Add(1)
		go func() { defer s.wg.Done(); s.setting(m) }()
	default:
		s.replyErr(m.ID, &rpcErr{Code: codeMethodNotFound, Message: "method not supported: " + clip(m.Method, 80)})
	}
}

func (s *server) handleResponse(m rpcMsg) {
	if m.ID == nil {
		return
	}
	var id int64
	if json.Unmarshal(*m.ID, &id) != nil {
		return
	}
	s.mu.Lock()
	ch := s.clientRQ[id]
	delete(s.clientRQ, id)
	s.mu.Unlock()
	if ch != nil {
		ch <- m
	}
}

func (s *server) session(id string) *session {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[id]
}

func (s *server) closeAll() {
	s.login.Close()
	s.mu.Lock()
	s.closed = true
	var all []*session
	for _, ss := range s.sessions {
		all = append(all, ss)
	}
	for ss := range s.starting {
		all = append(all, ss)
	}
	s.mu.Unlock()
	var wg sync.WaitGroup
	for _, ss := range all {
		wg.Add(1)
		go func() { defer wg.Done(); ss.close() }()
	}
	wg.Wait()
}

func (s *server) prompt(m rpcMsg) {
	var p struct {
		SessionID string          `json:"sessionId"`
		Prompt    json.RawMessage `json:"prompt"`
	}
	if err := json.Unmarshal(m.Params, &p); err != nil {
		s.replyErr(m.ID, &rpcErr{Code: codeInvalidParams, Message: "bad prompt parameters"})
		return
	}
	ss := s.session(p.SessionID)
	if ss == nil {
		s.replyErr(m.ID, &rpcErr{Code: codeInvalidParams, Message: "unknown session"})
		return
	}
	req, err := ss.promptRequest(p.Prompt)
	if err != nil {
		s.replyErr(m.ID, &rpcErr{Code: codeInvalidParams, Message: err.Error()})
		return
	}
	req.id = m.ID
	req.turn, _ = extMeta(m.Params)
	ss.enqueue(req)
}

func (s *server) setting(m rpcMsg) {
	var p struct {
		SessionID string `json:"sessionId"`
		ModeID    string `json:"modeId"`
		ModelID   string `json:"modelId"`
	}
	if err := json.Unmarshal(m.Params, &p); err != nil {
		s.replyErr(m.ID, &rpcErr{Code: codeInvalidParams, Message: "bad parameters"})
		return
	}
	ss := s.session(p.SessionID)
	if ss == nil {
		s.replyErr(m.ID, &rpcErr{Code: codeInvalidParams, Message: "unknown session"})
		return
	}
	var err *rpcErr
	if m.Method == "session/set_mode" {
		err = ss.setMode(p.ModeID)
	} else {
		err = ss.setModel(p.ModelID)
	}
	if err != nil {
		s.replyErr(m.ID, err)
		return
	}
	s.reply(m.ID, map[string]any{})
}

var errLineTooLong = errors.New("line too long")

// readLines calls fn with each non-blank line of r, never holding more than
// max bytes of one. fn must not keep the slice. It returns nil at the end of
// r, errLineTooLong when a line is over max, or the read error.
func readLines(r io.Reader, max int, fn func([]byte)) error {
	br := bufio.NewReaderSize(r, 64<<10)
	var buf []byte
	for {
		chunk, err := br.ReadSlice('\n')
		if len(buf)+len(chunk) > max {
			return errLineTooLong
		}
		buf = append(buf, chunk...)
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if len(bytes.TrimSpace(buf)) > 0 {
			fn(buf)
		}
		if cap(buf) > 1<<20 {
			buf = nil
		} else {
			buf = buf[:0]
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

// clip shortens s to n bytes for a log line.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
