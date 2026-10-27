package codexacp

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jmwri/flockdeck/internal/proctree"
	"github.com/jmwri/flockdeck/internal/sysproc"
)

// A mode is an approval policy and a sandbox. full-access (never ask, no
// sandbox) is left out: it would make the permission cards pointless.
type modeDef struct {
	id, name, desc string
	approval       string
	sandbox        string // the thread/start name
	policy         map[string]any
}

var modes = []modeDef{
	{"auto", "Auto", "Edit files in the project and run commands there; ask before anything else",
		"on-request", "workspace-write", map[string]any{"type": "workspaceWrite", "writableRoots": []any{}, "networkAccess": false}},
	{"read-only", "Read only", "Read files; ask before anything that changes them",
		"on-request", "read-only", map[string]any{"type": "readOnly"}},
	{"ask", "Ask first", "Ask before every command that is not known to be safe",
		"untrusted", "workspace-write", map[string]any{"type": "workspaceWrite", "writableRoots": []any{}, "networkAccess": false}},
}

func findMode(id string) (modeDef, bool) {
	for _, m := range modes {
		if m.id == id {
			return m, true
		}
	}
	return modeDef{}, false
}

func modeList() []any {
	var out []any
	for _, m := range modes {
		out = append(out, map[string]any{"id": m.id, "name": m.name, "description": m.desc})
	}
	return out
}

type promptReq struct {
	id    *json.RawMessage
	kind  string           // turn, compact or review
	input []map[string]any // turn: the UserInput items
	text  string           // review: custom instructions, if any
	gen   int
}

type turnResult struct {
	stop string
	err  *rpcErr
}

type turn struct {
	id        string // codex's id for the turn, once known
	done      chan turnResult
	finished  chan struct{} // closed by runPrompt when it is done with the turn
	gen       int           // ss.gen when the turn began
	cancelled bool
	interrupt bool // an interrupt was sent
}

type session struct {
	srv *server
	id  string // the codex thread id, which is also the ACP session id
	cwd string

	cmd     *exec.Cmd
	tree    *proctree.Tree
	stdin   io.WriteCloser
	wmu     sync.Mutex
	reaped  chan struct{}
	dead    chan struct{}
	quit    chan struct{}
	deadOne sync.Once
	exitErr atomic.Value // string
	nextID  atomic.Int64
	closing atomic.Bool
	reqSem  chan struct{} // bounds the requests from codex being answered at once

	imgMu  sync.Mutex
	imgDir string

	queue chan *promptReq

	mu       sync.Mutex
	pending  map[int64]chan rpcMsg // our requests to codex, by id
	cur      *turn
	gen      int
	done     map[string]turnResult // turns that ended before we knew their id was ours
	mode     modeDef
	model    string // the model shown as current
	modelSet bool   // the person chose a model: send it with every turn
	models   map[string]bool
	items    map[string]*itemState
	perms    map[string]*openPerm // codex's requests awaiting an answer, by request id
	allowed  map[string]bool      // allow-always rules
	skills   map[string]string    // skill name to path
	lastUsed int64
	window   int64
	secrets  []string     // values from the MCP servers, kept out of the log
	imgBytes int64        // bytes of images staged so far
	idv      atomic.Value // the thread id as a string, readable while the session is still starting
}

// tid is the thread id, "" until thread/start has answered. Anything coming
// from codex is checked against it, not against id, which is set before the
// session is shared but is not safe to read from the goroutine reading codex.
func (ss *session) tid() string {
	s, _ := ss.idv.Load().(string)
	return s
}

// maxPerms is how many approval requests may wait for the client at once.
const maxPerms = 32

func (s *server) openSession(m rpcMsg, resume bool) {
	var p struct {
		SessionID  string            `json:"sessionId"`
		Cwd        string            `json:"cwd"`
		McpServers []json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(m.Params, &p); err != nil {
		s.replyErr(m.ID, &rpcErr{Code: codeInvalidParams, Message: "bad parameters"})
		return
	}
	if !filepath.IsAbs(p.Cwd) {
		s.replyErr(m.ID, &rpcErr{Code: codeInvalidParams, Message: "cwd must be an absolute path"})
		return
	}
	if resume && !threadIDRe.MatchString(p.SessionID) {
		s.replyErr(m.ID, &rpcErr{Code: codeInvalidParams, Message: "sessionId is not a session id"})
		return
	}
	if resume && s.session(p.SessionID) != nil {
		s.replyErr(m.ID, &rpcErr{Code: codeInvalidParams, Message: "the session is already open"})
		return
	}
	mcp, err := mcpConfig(p.McpServers)
	if err != nil {
		s.replyErr(m.ID, &rpcErr{Code: codeInvalidParams, Message: err.Error()})
		return
	}
	secrets := mcpSecrets(p.McpServers)
	ss := &session{
		srv: s, cwd: p.Cwd,
		reaped: make(chan struct{}), dead: make(chan struct{}), quit: make(chan struct{}),
		reqSem:  make(chan struct{}, maxPerms),
		queue:   make(chan *promptReq, 64),
		pending: map[int64]chan rpcMsg{}, done: map[string]turnResult{},
		items: map[string]*itemState{}, perms: map[string]*openPerm{}, allowed: map[string]bool{},
		skills: map[string]string{}, models: map[string]bool{},
		mode: modes[0], secrets: secrets,
	}
	// From here on every failure ends with ss.close: it kills codex and removes
	// the session's files.
	if err := ss.start(); err != nil {
		ss.logf("start codex: %v", err)
		ss.close()
		s.replyErr(m.ID, &rpcErr{Code: codeInternal, Message: unavailable})
		return
	}
	// A session that is still starting is closed with the others, so that a
	// stop does not wait for codex to answer.
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		ss.close()
		s.replyErr(m.ID, &rpcErr{Code: codeInternal, Message: unavailable})
		return
	}
	s.starting[ss] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.starting, ss)
		s.mu.Unlock()
	}()
	if err := ss.handshake(); err != nil {
		ss.logf("codex handshake: %v", err)
		ss.close()
		s.replyErr(m.ID, &rpcErr{Code: codeInternal, Message: unavailable})
		return
	}
	params := map[string]any{
		"cwd":            p.Cwd,
		"approvalPolicy": ss.mode.approval,
		"sandbox":        ss.mode.sandbox,
	}
	if mcp != nil {
		params["config"] = map[string]any{"mcp_servers": mcp}
	}
	method := "thread/start"
	if resume {
		method = "thread/resume"
		params["threadId"] = p.SessionID
	}
	raw, err := ss.call(method, params)
	var started struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
		Model string `json:"model"`
	}
	if err == nil {
		err = json.Unmarshal(raw, &started)
	}
	if err == nil && !threadIDRe.MatchString(started.Thread.ID) {
		err = errors.New("no usable thread id in the answer")
	}
	if err == nil && resume && started.Thread.ID != p.SessionID {
		err = errors.New("codex resumed a different thread")
	}
	if err != nil {
		ss.logf("%s: %v", method, err)
		ss.close()
		s.replyErr(m.ID, &rpcErr{Code: codeInternal, Message: unavailable})
		return
	}
	// TODO(real codex): the thread/start and thread/resume results should echo the
	// policy that took effect, as `approvalPolicy` and `sandbox` (a string or an
	// object, not known). Compare them with ss.mode.approval and ss.mode.sandbox
	// and fail the session when they differ, so that a codex which ignored the mode
	// cannot run with more rights than the person chose. Not done: the field names
	// are a guess until a real codex has been seen.
	ss.id = started.Thread.ID
	ss.idv.Store(ss.id)
	ss.model = started.Model
	models := ss.listModels()
	commands := ss.listCommands()
	select {
	case <-ss.dead:
		ss.logf("codex exited before the session started: %v", ss.exitErr.Load())
		ss.close()
		s.replyErr(m.ID, &rpcErr{Code: codeInternal, Message: unavailable})
		return
	default:
	}
	s.mu.Lock()
	if s.closed || s.sessions[ss.id] != nil {
		// The adapter was told to stop while this session was starting, or two
		// requests opened one thread.
		s.mu.Unlock()
		ss.close()
		s.replyErr(m.ID, &rpcErr{Code: codeInternal, Message: unavailable})
		return
	}
	s.sessions[ss.id] = ss
	s.mu.Unlock()
	go ss.worker()

	res := map[string]any{
		"sessionId": ss.id,
		"modes":     map[string]any{"currentModeId": ss.mode.id, "availableModes": modeList()},
	}
	if len(models) > 0 {
		res["models"] = map[string]any{"currentModelId": ss.model, "availableModels": models}
	}
	s.reply(m.ID, res)
	s.update(ss.id, map[string]any{"sessionUpdate": "available_commands_update", "availableCommands": commands})
}

// threadIDRe is what a thread id looks like: codex uses UUIDs, and the id is
// used as the ACP session id and in nothing else, but it is checked all the same.
var threadIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

// start launches codex and the goroutines that read and reap it.
func (ss *session) start() error {
	o := ss.srv.opts
	cmd := exec.Command(o.CodexPath, "app-server", "--listen", "stdio://")
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
	go ss.drainStderr(stderr)
	go func() {
		if err := readLines(stdout, maxCodexLine, ss.dispatch); err != nil {
			ss.logf("reading from codex: %v; ending the session", err)
			ss.kill()
		}
		waited := make(chan error, 1)
		go func() { waited <- cmd.Wait() }()
		var werr error
		select {
		case werr = <-waited:
		case <-time.After(2 * time.Second):
			// stdout is closed but the process is not gone.
			_ = cmd.Process.Kill()
			werr = <-waited
		}
		close(ss.reaped)
		ss.tree.End(cmd.Process, ss.reaped, 0)
		if werr != nil {
			ss.exitErr.Store(werr.Error())
		}
		ss.markDead()
	}()
	return nil
}

func (ss *session) markDead() {
	ss.deadOne.Do(func() { close(ss.dead) })
}

// drainStderr logs codex's stderr. It never stops reading: a codex that cannot
// write to a full pipe would stop too. It goes to the adapter's stderr only.
func (ss *session) drainStderr(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		ss.logf("codex: %s", sc.Text())
	}
	_, _ = io.Copy(io.Discard, r)
}

func (ss *session) handshake() error {
	_, err := ss.call("initialize", map[string]any{
		"clientInfo":   map[string]any{"name": "flockdeck", "title": "Flockdeck", "version": ss.srv.opts.Version},
		"capabilities": map[string]any{"experimentalApi": false},
	})
	if err != nil {
		return err
	}
	return ss.write(map[string]any{"method": "initialized"})
}

// write sends one JSON line to codex. The "jsonrpc" member is left out, as
// codex leaves it out.
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

// call sends a request to codex and waits for the response with its id. The
// error text is for the adapter's log; it is never sent to the client.
func (ss *session) call(method string, params any) (json.RawMessage, error) {
	id := ss.nextID.Add(1)
	ch := make(chan rpcMsg, 1)
	ss.mu.Lock()
	ss.pending[id] = ch
	ss.mu.Unlock()
	defer func() {
		ss.mu.Lock()
		delete(ss.pending, id)
		ss.mu.Unlock()
	}()
	if err := ss.write(map[string]any{"id": id, "method": method, "params": params}); err != nil {
		return nil, fmt.Errorf("%s: %w", method, err)
	}
	timer := time.NewTimer(ss.srv.opts.ControlTimeout)
	defer timer.Stop()
	select {
	case m := <-ch:
		if m.Error != nil {
			return nil, fmt.Errorf("%s: codex said %d: %s", method, m.Error.Code, clip(m.Error.Message, 300))
		}
		return m.Result, nil
	case <-ss.dead:
		return nil, fmt.Errorf("%s: codex exited", method)
	case <-ss.quit:
		return nil, fmt.Errorf("%s: session closed", method)
	case <-timer.C:
		return nil, fmt.Errorf("%s: no answer", method)
	}
}

func (ss *session) setMode(id string) *rpcErr {
	m, ok := findMode(id)
	if !ok {
		return &rpcErr{Code: codeInvalidParams, Message: "unknown mode"}
	}
	// Codex takes the policy with each turn, so this applies from the next.
	ss.mu.Lock()
	ss.mode = m
	ss.mu.Unlock()
	ss.srv.update(ss.id, map[string]any{"sessionUpdate": "current_mode_update", "currentModeId": id})
	return nil
}

func (ss *session) setModel(id string) *rpcErr {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	if id == "" || (len(ss.models) > 0 && !ss.models[id]) {
		return &rpcErr{Code: codeInvalidParams, Message: "unknown model"}
	}
	ss.model, ss.modelSet = id, true
	return nil
}

// listModels asks codex for its models. A failure leaves the thread's own.
func (ss *session) listModels() []map[string]any {
	raw, err := ss.call("model/list", map[string]any{})
	if err != nil {
		ss.logf("%v", err)
		return nil
	}
	var r struct {
		Data []struct {
			ID          string `json:"id"`
			Model       string `json:"model"`
			DisplayName string `json:"displayName"`
			Description string `json:"description"`
		} `json:"data"`
	}
	_ = json.Unmarshal(raw, &r)
	var out []map[string]any
	seen := false
	for _, m := range r.Data {
		id := m.Model
		if id == "" {
			id = m.ID
		}
		if id == "" || len(out) >= 200 {
			continue
		}
		name := m.DisplayName
		if name == "" {
			name = id
		}
		out = append(out, map[string]any{"modelId": id, "name": name, "description": m.Description})
		ss.models[id] = true
		seen = seen || id == ss.model
	}
	if !seen && ss.model != "" && len(out) > 0 {
		out = append([]map[string]any{{"modelId": ss.model, "name": ss.model}}, out...)
		ss.models[ss.model] = true
	}
	return out
}

// fixedCommands are the actions the adapter takes over from codex's slash menu.
var fixedCommands = []map[string]any{
	{"name": "review", "description": "Review the uncommitted changes, or follow your instructions", "input": map[string]any{"hint": "optional review instructions"}},
	{"name": "compact", "description": "Summarise the conversation to free context"},
}

func (ss *session) listCommands() []map[string]any {
	out := append([]map[string]any{}, fixedCommands...)
	raw, err := ss.call("skills/list", map[string]any{"cwds": []string{ss.cwd}})
	if err != nil {
		ss.logf("%v", err)
		return out
	}
	var r struct {
		Data []struct {
			Skills []struct {
				Name             string `json:"name"`
				Description      string `json:"description"`
				ShortDescription string `json:"shortDescription"`
				Path             string `json:"path"`
				Enabled          *bool  `json:"enabled"`
			} `json:"skills"`
		} `json:"data"`
	}
	_ = json.Unmarshal(raw, &r)
	for _, d := range r.Data {
		for _, sk := range d.Skills {
			if sk.Name == "" || sk.Path == "" || (sk.Enabled != nil && !*sk.Enabled) || len(ss.skills) >= 200 {
				continue
			}
			if strings.EqualFold(sk.Name, "review") || strings.EqualFold(sk.Name, "compact") {
				continue
			}
			if _, dup := ss.skills[sk.Name]; dup {
				continue
			}
			ss.skills[sk.Name] = sk.Path
			desc := sk.ShortDescription
			if desc == "" {
				desc = sk.Description
			}
			out = append(out, map[string]any{"name": sk.Name, "description": clip(desc, 300), "input": map[string]any{"hint": "optional arguments"}})
		}
	}
	return out
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
	ss.mu.Unlock()
	select {
	case ss.queue <- r:
	default:
		ss.srv.replyErr(r.id, &rpcErr{Code: codeInternal, Message: "too many prompts are waiting"})
	}
}

// worker runs the queued prompts one at a time. After codex has exited it goes
// on answering them, with an error, until the session is closed.
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

func (ss *session) turnParams(r *promptReq) (string, map[string]any) {
	switch r.kind {
	case "compact":
		return "thread/compact/start", map[string]any{"threadId": ss.id}
	case "review":
		target := map[string]any{"type": "uncommittedChanges"}
		if r.text != "" {
			target = map[string]any{"type": "custom", "instructions": r.text}
		}
		return "review/start", map[string]any{"threadId": ss.id, "target": target, "delivery": "inline"}
	}
	ss.mu.Lock()
	mode, model, set := ss.mode, ss.model, ss.modelSet
	ss.mu.Unlock()
	p := map[string]any{
		"threadId":       ss.id,
		"input":          r.input,
		"approvalPolicy": mode.approval,
		"sandboxPolicy":  mode.policy,
	}
	if set {
		p["model"] = model
	}
	return "turn/start", p
}

func (ss *session) runPrompt(r *promptReq) {
	ss.mu.Lock()
	if r.gen != ss.gen {
		ss.mu.Unlock()
		ss.srv.reply(r.id, map[string]any{"stopReason": "cancelled"})
		return
	}
	t := &turn{done: make(chan turnResult, 1), finished: make(chan struct{}), gen: ss.gen}
	ss.cur = t
	ss.mu.Unlock()
	defer close(t.finished)

	method, params := ss.turnParams(r)
	raw, err := ss.call(method, params)
	var res turnResult
	if err != nil {
		ss.logf("%v", err)
		ss.mu.Lock()
		cancelled := t.cancelled
		ss.mu.Unlock()
		if cancelled {
			res = turnResult{stop: "cancelled"}
		} else {
			res = turnResult{err: &rpcErr{Code: codeInternal, Message: "the agent could not start the turn"}}
		}
	} else {
		var started struct {
			Turn struct {
				ID string `json:"id"`
			} `json:"turn"`
		}
		_ = json.Unmarshal(raw, &started)
		if started.Turn.ID != "" {
			ss.setTurnID(t, started.Turn.ID)
		}
		res = ss.awaitTurn(t, r.kind != "turn")
	}
	ss.mu.Lock()
	ss.cur = nil
	ss.items = map[string]*itemState{}
	ss.done = map[string]turnResult{}
	ss.mu.Unlock()
	if res.err != nil {
		ss.srv.replyErr(r.id, res.err)
		return
	}
	ss.srv.reply(r.id, map[string]any{"stopReason": res.stop})
}

// awaitTurn waits for the end of the turn. /compact and /review answer before
// any turn is announced; if codex never names a turn within ControlTimeout of
// the answer, nothing was started and the prompt ends instead of waiting for ever.
func (ss *session) awaitTurn(t *turn, bounded bool) turnResult {
	stopped := turnResult{err: &rpcErr{Code: codeInternal, Message: "the agent stopped during the turn"}}
	var idle <-chan time.Time
	noID := func() bool { ss.mu.Lock(); defer ss.mu.Unlock(); return t.id == "" }
	if bounded && noID() {
		tm := time.NewTimer(ss.srv.opts.ControlTimeout)
		defer tm.Stop()
		idle = tm.C
	}
	for {
		select {
		case res := <-t.done:
			return res
		case <-ss.quit:
			return stopped
		case <-ss.dead:
			select {
			case res := <-t.done: // the result was read just before the exit
				return res
			default:
				return stopped
			}
		case <-idle:
			idle = nil
			if noID() {
				ss.logf("codex announced no turn after the request; ending the prompt")
				return turnResult{stop: "end_turn"}
			}
		}
	}
}

// logf logs for this session, with the secrets the client handed over (MCP
// server env and header values) taken out.
func (ss *session) logf(format string, a ...any) {
	if ss.srv.opts.Stderr == io.Discard {
		return
	}
	line := fmt.Sprintf(format, a...)
	for _, sec := range ss.secrets {
		line = strings.ReplaceAll(line, sec, "[redacted]")
	}
	ss.srv.logf("%s", line)
}

// setTurnID records codex's id for the running turn, which arrives with the
// turn/start response and with turn/started, whichever is first. A turn that
// was cancelled before it had an id is interrupted now, and one that already
// ended is finished.
func (ss *session) setTurnID(t *turn, id string) {
	ss.mu.Lock()
	if t.id == "" {
		t.id = id
	}
	if t.id != id {
		ss.mu.Unlock()
		return
	}
	res, ended := ss.done[id]
	delete(ss.done, id)
	send := t.cancelled && !t.interrupt && !ended
	if send {
		t.interrupt = true
	}
	ss.mu.Unlock()
	if ended {
		select {
		case t.done <- res:
		default:
		}
		return
	}
	if send {
		go ss.interrupt(id)
	}
}

func (ss *session) interrupt(turnID string) {
	if _, err := ss.call("turn/interrupt", map[string]any{"threadId": ss.id, "turnId": turnID}); err != nil {
		ss.logf("%v", err)
	}
}

// finishTurn is the end of a turn: turn/completed.
func (ss *session) finishTurn(turnID string, res turnResult) {
	if turnID == "" {
		return
	}
	ss.mu.Lock()
	t := ss.cur
	if t == nil || t.id != turnID {
		// No turn of ours has this id (yet). Remember the end, bounded, in case
		// the turn/start response is still on its way.
		if t != nil && t.id == "" && len(ss.done) < 16 {
			ss.done[turnID] = res
		}
		ss.mu.Unlock()
		return
	}
	if t.cancelled && res.err == nil {
		res = turnResult{stop: "cancelled"}
	}
	ss.mu.Unlock()
	select {
	case t.done <- res:
	default:
		ss.logf("a second end for one turn was dropped")
	}
}

// cancel ends the running turn and drops the prompts queued behind it.
func (ss *session) cancel() {
	ss.mu.Lock()
	ss.gen++
	t := ss.cur
	var id string
	send := false
	if t != nil {
		t.cancelled = true
		id = t.id
		if id != "" && !t.interrupt {
			t.interrupt = true
			send = true
		}
	}
	perms := make([]*openPerm, 0, len(ss.perms))
	for _, p := range ss.perms {
		perms = append(perms, p)
	}
	ss.mu.Unlock()
	// A waiting approval is refused, so that codex does not sit on it, and the
	// card is withdrawn.
	for _, p := range perms {
		p.answer(ss, p.cancelPayload)
		p.abandon()
	}
	if t == nil {
		return
	}
	if send {
		go ss.interrupt(id)
	}
	go func() {
		select {
		case <-t.finished:
		case <-ss.dead:
		case <-ss.quit:
		case <-time.After(ss.srv.opts.CancelGrace):
			ss.logf("the interrupt did not end the turn; ending codex")
			ss.kill()
		}
	}()
}

func (ss *session) kill() {
	if ss.cmd != nil && ss.cmd.Process != nil {
		ss.tree.End(ss.cmd.Process, ss.reaped, 0)
	}
}

// close ends the session: stdin is closed so codex finishes, then the tree is
// ended if it has not gone. It is safe to call on a session that never
// started, and twice.
func (ss *session) close() {
	if !ss.closing.CompareAndSwap(false, true) {
		return
	}
	close(ss.quit)
	ss.mu.Lock()
	for _, p := range ss.perms {
		p.abandon()
	}
	ss.mu.Unlock()
	if ss.stdin != nil {
		_ = ss.stdin.Close()
		select {
		case <-ss.reaped:
		case <-time.After(750 * time.Millisecond):
			ss.kill()
			<-ss.reaped
		}
	}
	ss.cleanup()
}

func (ss *session) cleanup() {
	ss.imgMu.Lock()
	defer ss.imgMu.Unlock()
	if ss.imgDir != "" {
		_ = os.RemoveAll(ss.imgDir)
		ss.imgDir = ""
	}
}
