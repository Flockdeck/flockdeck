// Package claudeacp is an ACP agent for Claude Code. It runs as a subcommand of
// the flockdeck binary (`flockdeck acp-adapter claude`), speaks ACP on its own
// stdin and stdout, and drives one `claude -p --input-format stream-json
// --output-format stream-json` process per session. It needs nothing but the
// claude executable.
//
// What it does, in the order a session goes:
//
//   - initialize answers from a fixed description of what the adapter can do.
//   - session/new starts claude with a session id of the adapter's choosing,
//     sends claude's own initialize control request, and returns the modes and
//     models claude lists. The slash commands claude lists follow as an
//     available_commands_update.
//   - session/prompt turns the ACP content blocks into one stream-json user
//     message and ends when claude's result line arrives. Text, thinking and
//     tool activity are sent as session/update while the turn runs.
//   - claude's can_use_tool control requests become session/request_permission.
//     "Allow always" is kept by the adapter for the life of the session.
//   - session/cancel sends claude an interrupt control request.
//
// The prompt policy for a prompt that arrives while a turn is running is to
// queue it: each prompt gets its own turn and its own stop reason, in order. A
// cancel ends the running turn and every prompt queued behind it.
//
// session/resume starts claude with --resume and the session's id. Claude keeps
// the conversation in its own files and does not send it again, so there is no
// session/load: the adapter says loadSession false, and the client shows an
// empty transcript for a resumed session. Not done yet: turns claude starts on its own (a
// finished background task), AskUserQuestion, terminal and file system
// delegation to the client. See docs/acp-claude-adapter.md.
package claudeacp

import (
	"bufio"
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
)

// Options configure Run.
type Options struct {
	// ClaudePath is the claude executable: a path, or a name looked up on PATH.
	// Default "claude".
	ClaudePath string
	// Env is the environment claude gets. Nil means this process's own, without
	// the variables Flockdeck sets for itself.
	Env []string
	// Version is reported in initialize as the adapter's version.
	Version string
	// Stderr receives diagnostics: why claude could not start, what it wrote
	// on its stderr. Nil discards them.
	Stderr io.Writer
	// ControlTimeout bounds claude's answer to a control request such as
	// initialize or set_model. Default 30s.
	ControlTimeout time.Duration
	// CancelGrace is how long an interrupt may take to end the turn before the
	// claude process is killed. Default 15s.
	CancelGrace time.Duration
	// BusyIdle is how long a turn claude started on its own may go without a
	// line from claude before prompts stop waiting for it. Default 120s.
	BusyIdle time.Duration
}

func (o *Options) defaults() {
	if o.ClaudePath == "" {
		o.ClaudePath = "claude"
	}
	if o.Env == nil {
		o.Env = cleanEnv(os.Environ())
	}
	if o.Stderr == nil {
		o.Stderr = io.Discard
	}
	if o.ControlTimeout <= 0 {
		o.ControlTimeout = 30 * time.Second
	}
	if o.CancelGrace <= 0 {
		o.CancelGrace = 15 * time.Second
	}
	if o.BusyIdle <= 0 {
		o.BusyIdle = 120 * time.Second
	}
	if o.Version == "" {
		o.Version = "dev"
	}
}

// cleanEnv drops Flockdeck's own variables: claude has no use for them and a
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

// unavailable is the only thing a person is told when claude cannot be
// started. What went wrong goes to the adapter's stderr.
const unavailable = "Claude Code is unavailable"

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

type server struct {
	opts Options
	out  *bufio.Writer
	omu  sync.Mutex

	mu       sync.Mutex
	closed   bool // closeAll has run: a session that finishes starting now is closed
	sessions map[string]*session
	clientRQ map[int64]chan rpcMsg // requests we made to the client, by id
	nextReq  atomic.Int64
	wg       sync.WaitGroup
}

// Run serves ACP on in and out until in ends or ctx is done, then closes every
// session. It returns nil when in ended cleanly or ctx was cancelled. In the
// second case the goroutine reading in is left blocked, which is fine for a
// process that is about to exit.
func Run(ctx context.Context, in io.Reader, out io.Writer, o Options) error {
	o.defaults()
	sweepMCPDirs()
	s := &server{
		opts:     o,
		out:      bufio.NewWriterSize(out, 1<<16),
		sessions: map[string]*session{},
		clientRQ: map[int64]chan rpcMsg{},
	}
	type readEnd struct{ err error }
	ended := make(chan readEnd, 1)
	go func() {
		br := bufio.NewReaderSize(in, 1<<20)
		for {
			line, err := br.ReadBytes('\n')
			if len(strings.TrimSpace(string(line))) > 0 {
				var m rpcMsg
				if json.Unmarshal(line, &m) == nil {
					s.handle(m)
				} else {
					s.logf("ignored a line that is not JSON")
				}
			}
			if err != nil {
				if errors.Is(err, io.EOF) {
					err = nil
				}
				ended <- readEnd{err}
				return
			}
		}
	}()
	var readErr error
	select {
	case e := <-ended:
		readErr = e.err
	case <-ctx.Done():
	}
	s.closeAll()
	s.wg.Wait()
	s.closeAll() // a session/new that was starting when the first call ran
	return readErr
}

func (s *server) logf(format string, a ...any) {
	if s.opts.Stderr == io.Discard {
		return
	}
	line := "claude-acp: " + fmt.Sprintf(format, a...) + "\n"
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

// request sends a request to the client and returns the channel its response
// arrives on, and the id to forget it by.
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
				"mcpCapabilities":     map[string]any{"http": true, "sse": true},
				"sessionCapabilities": map[string]any{"resume": map[string]any{}},
			},
			"agentInfo":   map[string]any{"name": "flockdeck-claude", "title": "Claude Code", "version": s.opts.Version},
			"authMethods": []any{},
		})
	case "session/new", "session/resume":
		s.wg.Add(1)
		go func() { defer s.wg.Done(); s.openSession(m, m.Method == "session/resume") }()
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
		s.replyErr(m.ID, &rpcErr{Code: codeMethodNotFound, Message: "method not supported: " + m.Method})
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
	s.mu.Lock()
	s.closed = true
	var all []*session
	seen := map[*session]bool{}
	for _, ss := range s.sessions {
		// A session adopted under claude's id is listed twice.
		if !seen[ss] {
			seen[ss] = true
			all = append(all, ss)
		}
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
		s.replyErr(m.ID, &rpcErr{Code: codeInvalidParams, Message: err.Error()})
		return
	}
	ss := s.session(p.SessionID)
	if ss == nil {
		s.replyErr(m.ID, &rpcErr{Code: codeInvalidParams, Message: "unknown session " + p.SessionID})
		return
	}
	content, err := promptContent(p.Prompt)
	if err != nil {
		s.replyErr(m.ID, &rpcErr{Code: codeInvalidParams, Message: err.Error()})
		return
	}
	ss.enqueue(&promptReq{id: m.ID, content: content})
}

func (s *server) setting(m rpcMsg) {
	var p struct {
		SessionID string `json:"sessionId"`
		ModeID    string `json:"modeId"`
		ModelID   string `json:"modelId"`
	}
	if err := json.Unmarshal(m.Params, &p); err != nil {
		s.replyErr(m.ID, &rpcErr{Code: codeInvalidParams, Message: err.Error()})
		return
	}
	ss := s.session(p.SessionID)
	if ss == nil {
		s.replyErr(m.ID, &rpcErr{Code: codeInvalidParams, Message: "unknown session " + p.SessionID})
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
