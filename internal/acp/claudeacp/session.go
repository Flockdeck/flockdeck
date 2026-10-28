package claudeacp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/jmwri/flockdeck/internal/proctree"
	"github.com/jmwri/flockdeck/internal/sysproc"
)

// modes are the permission modes offered. bypassPermissions is left out: it
// needs a flag at launch and would make the permission cards pointless.
var modes = []map[string]any{
	{"id": "default", "name": "Default", "description": "Ask before edits and commands"},
	{"id": "acceptEdits", "name": "Accept edits", "description": "Edit files without asking; still ask for commands"},
	{"id": "plan", "name": "Plan", "description": "Plan only; no edits or commands"},
}

func knownMode(id string) bool {
	for _, m := range modes {
		if m["id"] == id {
			return true
		}
	}
	return false
}

type promptReq struct {
	id      *json.RawMessage
	content []map[string]any
	gen     int
	turn    string // the client's token for the turn, or ""
}

type turnResult struct {
	stop string
	err  *rpcErr
}

type turn struct {
	done      chan turnResult
	finished  chan struct{} // closed by runPrompt when it is done with the turn
	cancelled bool
}

type toolInfo struct {
	name   string
	kind   string
	edit   bool
	hidden bool // shown some other way (TodoWrite is a plan)
}

type openPerm struct {
	abandoned chan struct{}
	once      sync.Once
}

func (p *openPerm) abandon() { p.once.Do(func() { close(p.abandoned) }) }

type session struct {
	srv *server
	id  string
	cwd string

	// claudeID is the id claude reported when it is not ss.id, else "".
	claudeID string
	// resume says claude is started with --resume id, not --session-id id.
	resume bool
	// resumeAt, with resume, cuts the conversation after that message uuid.
	resumeAt string
	// forkOf, when set, is the session this one is a fork of: claude is started
	// with --resume forkOf --fork-session --session-id id, and resumeAt cuts it.
	forkOf string
	// forkID, with forkOf, is the id claude is given with --session-id when it
	// differs from id: a rewind keeps the id the client prompts with, and the
	// conversation moves to a new claude session (rewind.go).
	forkID string
	// pendingSrc and pendingAt say that claude writes this session's file only
	// when it gets its first message, so until then the session is the source
	// session cut at pendingAt, and anything that forks it or goes back in it
	// starts from the source (the uuids of the kept messages are the same in
	// both). Both are empty once a message was sent, and for a session that was
	// not made by a fork.
	pendingSrc, pendingAt string
	// cleared says claude moved the conversation to an id Flockdeck did not
	// choose (/clear), so the uuids in turns name messages of another file.
	cleared bool
	// mcp is what the client gave for mcpServers, to write the file again when
	// the session is started over.
	mcp []json.RawMessage

	cmd     *exec.Cmd
	tree    *proctree.Tree
	stdin   io.WriteCloser
	wmu     sync.Mutex
	reaped  chan struct{}
	dead    chan struct{}
	quit    chan struct{}
	deadOne sync.Once
	exitErr atomic.Value // string
	mcpFile string
	ctlSeq  atomic.Int64
	closing atomic.Bool

	queue chan *promptReq

	mu        sync.Mutex
	idle      *sync.Cond                      // on mu: claude finished a turn it started itself, a cancel, or the session ended
	busy      bool                            // claude is in a turn that no prompt of ours started
	waiters   map[string]chan json.RawMessage // our control requests, by request_id
	cur       *turn
	gen       int
	mode      string
	model     string
	announced map[string]toolInfo
	streamed  map[string]bool // assistant message ids whose text was already streamed
	allowed   map[string]bool // allow-always rules
	perms     map[string]*openPerm
	lastUsed  int64
	window    int64
	sawInit   bool
	lastLine  time.Time // when claude last wrote a line
	commands  []map[string]any
	facts     turnFacts // what claude said about the running turn, for its result

	// turns are the turns of the session the client named, oldest first, and
	// lastMsg the uuid of claude's last assistant message in the running turn
	// (rewind.go). rewinding is set while the session is being started over, and
	// no prompt is taken then.
	turns     []turnRec
	lastMsg   string
	rewinding bool
}

// openSession handles session/new and, with resume, session/resume and, with
// load as well, session/load (which replays the stored conversation first): claude is
// started with --resume and the id it was given, which is the id the adapter
// gave the session when it was new. An id that is not a UUID is refused before
// anything is started, and so is one that is open already. An id claude does
// not know makes claude exit, which is the same answer as any start that fails.
func (s *server) openSession(m rpcMsg, kind string) {
	load, fork := kind == "session/load", kind == "session/fork"
	resume := kind == "session/resume" || load
	var p struct {
		SessionID  string            `json:"sessionId"`
		Cwd        string            `json:"cwd"`
		McpServers []json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(m.Params, &p); err != nil {
		s.replyErr(m.ID, &rpcErr{Code: codeInvalidParams, Message: err.Error()})
		return
	}
	if !filepath.IsAbs(p.Cwd) {
		s.replyErr(m.ID, &rpcErr{Code: codeInvalidParams, Message: "cwd must be an absolute path"})
		return
	}
	id := uuid.NewString()
	var forkOf, at string
	if fork {
		u, err := uuid.Parse(p.SessionID)
		if err != nil {
			s.replyErr(m.ID, &rpcErr{Code: codeInvalidParams, Message: "sessionId is not a session id"})
			return
		}
		forkOf = u.String()
		var bad bool
		_, at, bad = extMeta(m.Params)
		if bad {
			s.replyErr(m.ID, &rpcErr{Code: codeInvalidParams, Message: "at is not a fork point"})
			return
		}
		// A session that has had no message has no file to fork: fork what it
		// was made from.
		if src := s.session(forkOf); src != nil {
			src.mu.Lock()
			if src.pendingSrc != "" {
				forkOf = src.pendingSrc
				if at == "" {
					at = src.pendingAt
				}
			}
			src.mu.Unlock()
		}
	}
	if resume {
		u, err := uuid.Parse(p.SessionID)
		if err != nil {
			s.replyErr(m.ID, &rpcErr{Code: codeInvalidParams, Message: "sessionId is not a session id"})
			return
		}
		// The canonical spelling, so that the one id cannot be open twice under
		// two spellings and nothing odd reaches claude's command line.
		id = u.String()
		if s.session(id) != nil {
			s.replyErr(m.ID, &rpcErr{Code: codeInvalidParams, Message: "the session is already open"})
			return
		}
	}
	ss := s.newSession(id, p.Cwd, p.McpServers)
	ss.resume, ss.forkOf, ss.resumeAt = resume, forkOf, at
	if fork {
		ss.pendingSrc, ss.pendingAt = forkOf, at
	}
	var err error
	if ss.mcpFile, err = writeMCPConfig(p.McpServers); err != nil {
		s.replyErr(m.ID, &rpcErr{Code: codeInvalidParams, Message: err.Error()})
		return
	}
	if load {
		// The replay comes before the answer, as the protocol has it, and before
		// claude is started, so that nothing claude does on its own can be taken
		// for part of it. The session is not registered yet either, so no prompt
		// can reach it.
		s.replayHistory(p.SessionID, id, p.Cwd)
	}
	if err := ss.start(); err != nil {
		s.logf("start claude: %v", err)
		ss.cleanup()
		// The reason stays on stderr: the person is told only that the agent is
		// not available.
		s.replyErr(m.ID, &rpcErr{Code: codeInternal, Message: unavailable})
		return
	}
	models, herr := ss.initialize()
	select {
	case <-ss.dead:
		s.logf("claude exited before the session started: %v", ss.exitErr.Load())
		ss.cleanup()
		s.replyErr(m.ID, &rpcErr{Code: codeInternal, Message: unavailable})
		return
	default:
	}
	if herr != nil {
		// The control protocol did not answer as the adapter expects: claude
		// changed it, or is not claude. Nothing is guessed.
		s.logf("the control protocol handshake failed: %v", herr)
		ss.close()
		s.replyErr(m.ID, &rpcErr{Code: codeInternal, Message: unavailable})
		return
	}
	s.mu.Lock()
	if s.closed {
		// The adapter was told to stop while this session was starting.
		s.mu.Unlock()
		ss.close()
		s.replyErr(m.ID, &rpcErr{Code: codeInternal, Message: unavailable})
		return
	}
	if s.sessions[ss.id] != nil {
		// Two requests opened one session.
		s.mu.Unlock()
		ss.close()
		s.replyErr(m.ID, &rpcErr{Code: codeInvalidParams, Message: "the session is already open"})
		return
	}
	s.sessions[ss.id] = ss
	s.mu.Unlock()
	go ss.worker()
	tested, _ := s.versionTested()

	res := map[string]any{
		"sessionId": ss.id,
		"modes":     map[string]any{"currentModeId": ss.mode, "availableModes": modes},
	}
	if len(models) > 0 {
		res["models"] = map[string]any{"currentModelId": ss.model, "availableModels": models}
	}
	s.reply(m.ID, res)
	if !tested {
		s.update(ss.id, map[string]any{"sessionUpdate": "flockdeck_notice", "code": "untested_version"})
	}
	if len(ss.commands) > 0 {
		s.update(ss.id, map[string]any{"sessionUpdate": "available_commands_update", "availableCommands": ss.commands})
	}
}

// newSession is a session that is not started yet.
func (s *server) newSession(id, cwd string, mcp []json.RawMessage) *session {
	ss := &session{
		srv: s, id: id, cwd: cwd, mcp: mcp,
		reaped: make(chan struct{}), dead: make(chan struct{}), quit: make(chan struct{}),
		queue:   make(chan *promptReq, 64),
		waiters: map[string]chan json.RawMessage{}, announced: map[string]toolInfo{},
		streamed: map[string]bool{}, allowed: map[string]bool{}, perms: map[string]*openPerm{},
		mode: "default", model: "default",
	}
	ss.idle = sync.NewCond(&ss.mu)
	return ss
}

// start launches claude and the goroutines that read and reap it.
func (ss *session) start() error {
	o := ss.srv.opts
	args := []string{"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose",
		"--include-partial-messages", "--permission-prompt-tool", "stdio"}
	switch {
	case ss.forkOf != "":
		args = append(args, "--resume", ss.forkOf)
		if ss.resumeAt != "" {
			args = append(args, "--resume-session-at", ss.resumeAt)
		}
		newID := ss.id
		if ss.forkID != "" {
			newID = ss.forkID
		}
		args = append(args, "--fork-session", "--session-id", newID)
	case ss.resume:
		args = append(args, "--resume", ss.id)
		if ss.resumeAt != "" {
			args = append(args, "--resume-session-at", ss.resumeAt)
		}
	default:
		args = append(args, "--session-id", ss.id)
	}
	if ss.mcpFile != "" {
		args = append(args, "--mcp-config", ss.mcpFile)
	}
	cmd := exec.Command(o.ClaudePath, args...)
	cmd.Dir = ss.cwd
	cmd.Env = o.Env
	sysproc.NoWindow(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	tree, err := proctree.Start(cmd)
	if err != nil {
		return err
	}
	ss.cmd, ss.tree, ss.stdin = cmd, tree, stdin
	ss.lastLine = time.Now()
	go ss.busyWatchdog()
	go ss.drainStderr(stderr)
	go func() {
		ss.readLoop(stdout)
		err := cmd.Wait()
		close(ss.reaped)
		ss.tree.End(cmd.Process, ss.reaped, 0)
		if err != nil {
			ss.exitErr.Store(err.Error())
		}
		ss.markDead()
	}()
	return nil
}

// busyWatchdog stops prompts waiting for a turn of claude's own that has gone
// silent: a result that never comes would otherwise hold them for ever.
func (ss *session) busyWatchdog() {
	idle := ss.srv.opts.BusyIdle
	tick := idle / 10
	if tick < 10*time.Millisecond {
		tick = 10 * time.Millisecond
	}
	if tick > 5*time.Second {
		tick = 5 * time.Second
	}
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			ss.mu.Lock()
			if ss.busy && time.Since(ss.lastLine) > idle {
				ss.busy = false
				ss.idle.Broadcast()
				ss.srv.logf("claude's own turn sent nothing for %v; prompts no longer wait for it", idle)
			}
			ss.mu.Unlock()
		case <-ss.dead:
			return
		case <-ss.quit:
			return
		}
	}
}

// adopt notes the session id claude reports in its init line. claude 2.1.311
// keeps the id it is given for --session-id and --resume, so this is normally
// the id the client has. If it ever differs, the session is also reachable
// under claude's id (a resume of it is refused as open, a prompt for it is
// taken), and the difference is logged. The id arrives with the first turn,
// after session/new and session/resume have answered, and ACP has no update
// that changes a session's id, so the client keeps the one it was given.
func (ss *session) adopt(reported string) {
	u, err := uuid.Parse(reported)
	if err != nil || u.String() == ss.id {
		return
	}
	id := u.String()
	ss.mu.Lock()
	old := ss.claudeID
	ss.claudeID = id
	if id != ss.forkID {
		ss.cleared = true
	}
	ss.mu.Unlock()
	if old == id {
		return
	}
	ss.srv.logf("claude reports session %s for the session opened as %s", id, ss.id)
	ss.srv.mu.Lock()
	// A newer id replaces the earlier alias, so /clear after /clear does not
	// leave every id claude ever reported pointing at this session.
	if old != "" && old != id && ss.srv.sessions[old] == ss {
		delete(ss.srv.sessions, old)
	}
	if ss.srv.sessions[id] == nil {
		ss.srv.sessions[id] = ss
	}
	ss.srv.mu.Unlock()
	// The client keeps the id it prompts with, and is told the one to resume by:
	// after /clear that is the new conversation's.
	ss.srv.update(ss.id, map[string]any{"sessionUpdate": "flockdeck_session", "newSessionId": id})
}

func (ss *session) markDead() {
	ss.deadOne.Do(func() { close(ss.dead) })
	ss.wake()
}

// wake lets a prompt that waits for claude's own turn look again.
func (ss *session) wake() {
	ss.mu.Lock()
	ss.idle.Broadcast()
	ss.mu.Unlock()
}

// drainStderr logs claude's stderr. It never stops reading: a claude that
// cannot write to a full pipe would stop too.
func (ss *session) drainStderr(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		ss.srv.logf("claude: %s", sc.Text())
	}
	_, _ = io.Copy(io.Discard, r)
}

// initialize sends claude's initialize control request and reads the commands,
// models and mode out of the answer. An error, no answer in time, or an answer
// that is not a JSON object is a failed handshake.
func (ss *session) initialize() ([]map[string]any, error) {
	resp, err := ss.control("initialize", nil)
	if err != nil {
		return nil, err
	}
	var r struct {
		Commands []struct {
			Name         string `json:"name"`
			Description  string `json:"description"`
			ArgumentHint string `json:"argumentHint"`
		} `json:"commands"`
		Models []struct {
			Value       string `json:"value"`
			DisplayName string `json:"displayName"`
			Description string `json:"description"`
		} `json:"models"`
		Mode        string `json:"permissionMode"`
		CurrentMode string `json:"currentMode"`
	}
	if err := json.Unmarshal(resp, &r); err != nil {
		return nil, fmt.Errorf("initialize: the answer is not an object: %w", err)
	}
	for _, c := range r.Commands {
		name := strings.TrimPrefix(c.Name, "/")
		if name == "" {
			continue
		}
		cmd := map[string]any{"name": name, "description": c.Description}
		if c.ArgumentHint != "" {
			cmd["input"] = map[string]any{"hint": c.ArgumentHint}
		}
		ss.commands = append(ss.commands, cmd)
	}
	for _, m := range []string{r.Mode, r.CurrentMode} {
		if knownMode(m) {
			ss.mode = m
			break
		}
	}
	var models []map[string]any
	for _, m := range r.Models {
		if m.Value == "" {
			continue
		}
		name := m.DisplayName
		if name == "" {
			name = m.Value
		}
		models = append(models, map[string]any{"modelId": m.Value, "name": name, "description": m.Description})
	}
	if len(models) > 0 {
		ss.model = fmt.Sprint(models[0]["modelId"])
		for _, m := range models {
			if m["modelId"] == "default" {
				ss.model = "default"
			}
		}
	}
	return models, nil
}

// write sends one JSON line to claude's stdin.
func (ss *session) write(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	ss.wmu.Lock()
	defer ss.wmu.Unlock()
	_, err = ss.stdin.Write(append(b, '\n'))
	return err
}

// control sends a control request of ours and waits for claude's answer. The
// result is the "response" member of a success.
func (ss *session) control(subtype string, fields map[string]any) (json.RawMessage, error) {
	id := fmt.Sprintf("fd-%d", ss.ctlSeq.Add(1))
	req := map[string]any{"subtype": subtype}
	for k, v := range fields {
		req[k] = v
	}
	ch := make(chan json.RawMessage, 1)
	ss.mu.Lock()
	ss.waiters[id] = ch
	ss.mu.Unlock()
	defer func() {
		ss.mu.Lock()
		delete(ss.waiters, id)
		ss.mu.Unlock()
	}()
	if err := ss.write(map[string]any{"type": "control_request", "request_id": id, "request": req}); err != nil {
		return nil, err
	}
	select {
	case raw := <-ch:
		var r struct {
			Subtype  string          `json:"subtype"`
			Response json.RawMessage `json:"response"`
			Error    string          `json:"error"`
		}
		_ = json.Unmarshal(raw, &r)
		if r.Subtype == "error" {
			return nil, fmt.Errorf("%s: %s", subtype, r.Error)
		}
		return r.Response, nil
	case <-ss.dead:
		return nil, fmt.Errorf("%s: claude exited", subtype)
	case <-time.After(ss.srv.opts.ControlTimeout):
		return nil, fmt.Errorf("%s: no answer", subtype)
	}
}

func (ss *session) setMode(id string) *rpcErr {
	if !knownMode(id) {
		return &rpcErr{Code: codeInvalidParams, Message: "unknown mode " + id}
	}
	if _, err := ss.control("set_permission_mode", map[string]any{"mode": id}); err != nil {
		ss.srv.logf("set_permission_mode: %v", err)
		return &rpcErr{Code: codeInternal, Message: "the agent could not change the mode"}
	}
	ss.mu.Lock()
	ss.mode = id
	ss.mu.Unlock()
	ss.srv.update(ss.id, map[string]any{"sessionUpdate": "current_mode_update", "currentModeId": id})
	return nil
}

func (ss *session) setModel(id string) *rpcErr {
	if id == "" {
		return &rpcErr{Code: codeInvalidParams, Message: "modelId is required"}
	}
	if _, err := ss.control("set_model", map[string]any{"model": id}); err != nil {
		ss.srv.logf("set_model: %v", err)
		return &rpcErr{Code: codeInternal, Message: "the agent could not change the model"}
	}
	ss.mu.Lock()
	ss.model = id
	ss.mu.Unlock()
	return nil
}

func closedCh(c <-chan struct{}) bool {
	select {
	case <-c:
		return true
	default:
		return false
	}
}

func (ss *session) enqueue(r *promptReq) {
	ss.mu.Lock()
	r.gen = ss.gen
	if ss.rewinding {
		ss.mu.Unlock()
		ss.srv.replyErr(r.id, &rpcErr{Code: codeInternal, Message: "the session is being rewound"})
		return
	}
	// Pushed with the lock held, so that a rewind that found the queue empty
	// is not followed by a prompt it never sees.
	select {
	case ss.queue <- r:
		ss.mu.Unlock()
	default:
		ss.mu.Unlock()
		ss.srv.replyErr(r.id, &rpcErr{Code: codeInternal, Message: "too many prompts are waiting"})
	}
}

// worker runs the queued prompts one at a time. After claude has exited it
// goes on answering them, with an error, until the session is closed.
func (ss *session) worker() {
	for {
		select {
		case r := <-ss.queue:
			select {
			case <-ss.dead:
				ss.srv.replyErr(r.id, &rpcErr{Code: codeInternal, Message: "the agent has stopped"})
			default:
				ss.runPrompt(r)
			}
		case <-ss.quit:
			return
		}
	}
}

func (ss *session) runPrompt(r *promptReq) {
	ss.mu.Lock()
	// A turn claude started on its own (a finished background task) ends with
	// a result of its own. A message written meanwhile would be folded into
	// that turn and its result taken for ours, so wait for it.
	for ss.busy && r.gen == ss.gen && !closedCh(ss.dead) && !closedCh(ss.quit) {
		ss.idle.Wait()
	}
	if r.gen != ss.gen {
		ss.mu.Unlock()
		ss.srv.reply(r.id, map[string]any{"stopReason": "cancelled"})
		return
	}
	if closedCh(ss.dead) {
		ss.mu.Unlock()
		ss.srv.replyErr(r.id, &rpcErr{Code: codeInternal, Message: "the agent has stopped"})
		return
	}
	t := &turn{done: make(chan turnResult, 1), finished: make(chan struct{})}
	ss.cur = t
	ss.facts = turnFacts{}
	ss.mu.Unlock()
	defer close(t.finished)

	msg := map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": r.content}}
	if r.turn != "" {
		msg["uuid"] = r.turn
	}
	ss.mu.Lock()
	ss.lastMsg = ""
	ss.pendingSrc, ss.pendingAt = "", ""
	ss.mu.Unlock()
	err := ss.write(msg)
	var res turnResult
	if err != nil {
		res = turnResult{err: &rpcErr{Code: codeInternal, Message: "the agent has stopped"}}
	} else {
		select {
		case res = <-t.done:
		case <-ss.dead:
			select {
			case res = <-t.done: // the result was read just before the exit
			default:
				res = turnResult{err: &rpcErr{Code: codeInternal, Message: "the agent stopped during the turn"}}
			}
		}
	}
	ss.mu.Lock()
	ss.cur = nil
	if r.turn != "" {
		rec := turnRec{token: r.turn}
		if res.err == nil && res.stop == "end_turn" {
			rec.last = ss.lastMsg
		}
		if len(ss.turns) >= maxTurns {
			ss.turns = append(ss.turns[:0], ss.turns[len(ss.turns)-maxTurns+1:]...)
		}
		ss.turns = append(ss.turns, rec)
	}
	ss.mu.Unlock()
	if res.err != nil {
		ss.srv.replyErr(r.id, res.err)
		return
	}
	ss.srv.reply(r.id, map[string]any{"stopReason": res.stop})
}

// cancel ends the running turn and drops the prompts queued behind it.
func (ss *session) cancel() {
	ss.mu.Lock()
	ss.gen++
	t := ss.cur
	if t != nil {
		t.cancelled = true
	}
	for _, p := range ss.perms {
		p.abandon()
	}
	ss.idle.Broadcast()
	busy := ss.busy
	ss.mu.Unlock()
	if t == nil {
		if busy {
			// claude is in a turn of its own: interrupt it, and end claude if
			// it does not stop, as for a prompt's turn.
			go func() {
				if _, err := ss.control("interrupt", nil); err != nil {
					ss.srv.logf("interrupt: %v", err)
				}
			}()
			go ss.killIfBusy()
		}
		return
	}
	go func() {
		if _, err := ss.control("interrupt", nil); err != nil {
			ss.srv.logf("interrupt: %v", err)
		}
	}()
	go func() {
		select {
		case <-t.finished:
		case <-ss.dead:
		case <-time.After(ss.srv.opts.CancelGrace):
			ss.mu.Lock()
			same := ss.cur == t
			ss.mu.Unlock()
			if same {
				ss.srv.logf("the interrupt did not end the turn; ending claude")
				ss.kill()
			}
		}
	}()
}

// killIfBusy ends claude if it is still in a turn of its own CancelGrace after an
// interrupt.
func (ss *session) killIfBusy() {
	deadline := time.After(ss.srv.opts.CancelGrace)
	poll := time.NewTicker(50 * time.Millisecond)
	defer poll.Stop()
	for {
		select {
		case <-deadline:
			ss.mu.Lock()
			still := ss.busy
			ss.mu.Unlock()
			if still {
				ss.srv.logf("the interrupt did not end claude's own turn; ending claude")
				ss.kill()
			}
			return
		case <-poll.C:
			ss.mu.Lock()
			still := ss.busy
			ss.mu.Unlock()
			if !still {
				return
			}
		case <-ss.dead:
			return
		case <-ss.quit:
			return
		}
	}
}

func (ss *session) kill() {
	if ss.cmd != nil && ss.cmd.Process != nil {
		ss.tree.End(ss.cmd.Process, ss.reaped, 0)
	}
}

// close ends the session: stdin is closed so claude finishes, then the tree is
// ended if it has not gone.
func (ss *session) close() {
	if !ss.closing.CompareAndSwap(false, true) {
		return
	}
	close(ss.quit)
	ss.wake()
	ss.mu.Lock()
	for _, p := range ss.perms {
		p.abandon()
	}
	ss.mu.Unlock()
	if ss.stdin != nil {
		_ = ss.stdin.Close()
	}
	select {
	case <-ss.reaped:
	case <-time.After(750 * time.Millisecond):
		ss.kill()
		<-ss.reaped
	}
	ss.cleanup()
}

func (ss *session) cleanup() {
	if ss.mcpFile != "" {
		_ = os.RemoveAll(filepath.Dir(ss.mcpFile))
	}
}
