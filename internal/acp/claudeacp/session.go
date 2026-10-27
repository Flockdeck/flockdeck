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
}

type turnResult struct {
	stop string
	err  *rpcErr
}

type turn struct {
	done      chan turnResult
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
	commands  []map[string]any
}

func (s *server) newSession(m rpcMsg) {
	var p struct {
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
	ss := &session{
		srv: s, id: uuid.NewString(), cwd: p.Cwd,
		reaped: make(chan struct{}), dead: make(chan struct{}), quit: make(chan struct{}),
		queue:   make(chan *promptReq, 64),
		waiters: map[string]chan json.RawMessage{}, announced: map[string]toolInfo{},
		streamed: map[string]bool{}, allowed: map[string]bool{}, perms: map[string]*openPerm{},
		mode: "default", model: "default",
	}
	var err error
	if ss.mcpFile, err = writeMCPConfig(p.McpServers); err != nil {
		s.replyErr(m.ID, &rpcErr{Code: codeInvalidParams, Message: err.Error()})
		return
	}
	if err := ss.start(); err != nil {
		s.logf("start claude: %v", err)
		ss.cleanup()
		// The reason stays on stderr: the person is told only that the agent is
		// not available.
		s.replyErr(m.ID, &rpcErr{Code: codeInternal, Message: "Claude Code is unavailable"})
		return
	}
	models := ss.initialize()
	select {
	case <-ss.dead:
		s.logf("claude exited before the session started: %v", ss.exitErr.Load())
		ss.cleanup()
		s.replyErr(m.ID, &rpcErr{Code: codeInternal, Message: "Claude Code is unavailable"})
		return
	default:
	}
	s.mu.Lock()
	s.sessions[ss.id] = ss
	s.mu.Unlock()
	go ss.worker()

	res := map[string]any{
		"sessionId": ss.id,
		"modes":     map[string]any{"currentModeId": ss.mode, "availableModes": modes},
	}
	if len(models) > 0 {
		res["models"] = map[string]any{"currentModelId": ss.model, "availableModels": models}
	}
	s.reply(m.ID, res)
	if len(ss.commands) > 0 {
		s.update(ss.id, map[string]any{"sessionUpdate": "available_commands_update", "availableCommands": ss.commands})
	}
}

// start launches claude and the goroutines that read and reap it.
func (ss *session) start() error {
	o := ss.srv.opts
	args := []string{"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose",
		"--include-partial-messages", "--permission-prompt-tool", "stdio", "--session-id", ss.id}
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

func (ss *session) markDead() { ss.deadOne.Do(func() { close(ss.dead) }) }

func (ss *session) drainStderr(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		ss.srv.logf("claude: %s", sc.Text())
	}
}

// initialize sends claude's initialize control request and reads the commands,
// models and mode out of the answer. A failure leaves the defaults.
func (ss *session) initialize() []map[string]any {
	resp, err := ss.control("initialize", nil)
	if err != nil {
		ss.srv.logf("initialize: %v", err)
		return nil
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
	_ = json.Unmarshal(resp, &r)
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
	return models
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
		return &rpcErr{Code: codeInternal, Message: err.Error()}
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
		return &rpcErr{Code: codeInternal, Message: err.Error()}
	}
	ss.mu.Lock()
	ss.model = id
	ss.mu.Unlock()
	return nil
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
	if r.gen != ss.gen {
		ss.mu.Unlock()
		ss.srv.reply(r.id, map[string]any{"stopReason": "cancelled"})
		return
	}
	t := &turn{done: make(chan turnResult, 1)}
	ss.cur = t
	ss.mu.Unlock()

	err := ss.write(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": r.content}})
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
	ss.mu.Unlock()
	if t == nil {
		return
	}
	go func() {
		if _, err := ss.control("interrupt", nil); err != nil {
			ss.srv.logf("interrupt: %v", err)
		}
	}()
	go func() {
		select {
		case <-t.done:
		case <-ss.dead:
		case <-time.After(ss.srv.opts.CancelGrace):
			ss.srv.logf("the interrupt did not end the turn; ending claude")
			ss.kill()
		}
	}()
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
		_ = os.Remove(ss.mcpFile)
	}
}
