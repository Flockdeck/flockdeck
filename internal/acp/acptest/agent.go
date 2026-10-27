// Package acptest is a fake ACP agent for testing clients without a real agent
// or a login. It is the contract suite's other half: internal/acp's tests start
// it as a child process and check the client against the behaviours below.
//
// The agent is this program run again. A test binary's TestMain calls
// RunIfAgent first; Command then gives the path, arguments and environment to
// start it with. Which behaviour a prompt gets is chosen by its first word:
//
//	plain       two message chunks, end_turn
//	state       the session-state updates (commands, mode, config options, title, markdown plan, replayed user message, compaction, usage)
//	tool        a tool call with updates, array content, usage, end_turn
//	perm        a permission request with a numeric id; the outcome is reported
//	perm-str    the same with the string id "perm-abc"; reports whether the id came back as a string
//	slow        works until session/cancel, then ends the turn cancelled
//	slow-perm   asks for permission, then waits for session/cancel
//	report      reports how many responses each permission request got
//	plan        a plan update
//	unknown     update kinds, content block types and fields the client does not know
//	big         a tool result of 5 MiB
//	order       200 numbered chunks, then end_turn
//	stderr      1 MiB on stderr, then plain
//	hold-perm   asks for permission and writes the outcome to the marker file
//	exit        exits with status 3 and a message on stderr, mid-turn
//	garbage     a line that is not JSON, a response to nothing, a malformed update, a second response
//	unknown-method  a request for fs/read_text_file, reports the error code it got back
//	perm-chunks chunks, a permission request, more chunks, then the tool update
//	storm       a thousand 64 KiB rewrites of one tool call, then 8 MiB of thought chunks
//	silent      never answers
//	flood       a stdout line with no end
//	blank       empty and whitespace-only lines, then a chunk
//	line N      one notification of exactly N bytes, then a chunk
//	stderr-flood  4 MiB on stderr in one line, then plain
//	spam-perm   fifty permission requests at once, reports how many were answered
//	ratelimit   answers the prompt with the JSON-RPC error -32603 "rate limit exceeded"
//	env         reports its whole environment, one NAME=VALUE per line, in one chunk
//	deaf-perm   asks for permission and stops reading stdin
//	whoami      reports the session id the prompt came on, as "session=ID", then end_turn
//	grandchild  starts a process of its own that sleeps for a minute, reports its id as "pid=N", then end_turn
//
// An unknown request gets error -32601, as a real agent that lacks the method does.
// WithConfigOptions and WithModes give session/new the two shapes of session
// state agents use.
//
// WithSessionLoad, WithSessionResume and WithKnownSessions give the agent the
// session/load and session/resume methods and say which session ids it has. A
// session/load of a known id replays a short conversation (a user message, an
// agent reply, a tool call, a second user message and reply) as updates before
// it answers, and session/resume of one answers with no replay. An id it does not
// know is refused with error -32002 and a message that no one should show.
//
// WithMute makes an agent that reads and never answers.
//
// Authentication is switched on with WithAuth.
package acptest

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jmwri/flockdeck/internal/sysproc"
	"github.com/jmwri/flockdeck/internal/testiso/depth"
)

// McpRecordFile is a file name in a session's working directory. If the file
// exists when session/new arrives, the agent writes the mcpServers it was sent
// to it as JSON.
const McpRecordFile = ".acptest-mcpservers"

const (
	envAgent  = "FLOCKDECK_ACPTEST_AGENT"
	envAuth   = "FLOCKDECK_ACPTEST_AUTH"
	envMarker = "FLOCKDECK_ACPTEST_MARKER"
	envMute   = "FLOCKDECK_ACPTEST_MUTE"
	envDeaf   = "FLOCKDECK_ACPTEST_DEAF"
	envSlow   = "FLOCKDECK_ACPTEST_SLOWSTART"
	envShape  = "FLOCKDECK_ACPTEST_SHAPE"
	// envSetLog is a file the agent appends the params of each setting request
	// to. envSetOpts+"_bare" answers session/set_config_option with no
	// options, and envSetOpts+"_bool" adds a boolean option to the config shape.
	envSetLog  = "FLOCKDECK_ACPTEST_SETLOG"
	envSetOpts = "FLOCKDECK_ACPTEST_SETOPTS"
	// envSessions is a comma list of session/load, session/resume and the ids
	// the agent knows: "load", "resume", "id=<id>".
	envSessions = "FLOCKDECK_ACPTEST_SESSIONS"
)

// ArgAgent, as an argument, also makes a process the fake agent. A test of how
// an environment is built starts the agent this way, since the environment it
// asked for must not hold the variable that otherwise says so.
const ArgAgent = "--acptest-agent"

// ArgMarker, as an argument followed by a path, does what WithMarker does for an
// agent started with ArgAgent, whose environment is not the test's.
const ArgMarker = "--acptest-marker"

// ArgSessions is the session options (see WithSessionLoad) as one argument, as
// ArgSessions+"load,resume,id=old-1", for an agent started with ArgAgent.
const ArgSessions = "--acptest-sessions="

// argSleeper makes a process do nothing for a minute. The grandchild
// scenario starts one, as an agent starts a tool, so that a test can see
// whether closing the client ended what the agent had started.
const argSleeper = "--acptest-sleeper"

// RunIfAgent runs the fake agent and returns true when this process was
// started as one. Call it first in TestMain and return when it reports true.
func RunIfAgent() bool {
	depth.Guard()
	for _, a := range os.Args[1:] {
		if a == argSleeper {
			time.Sleep(time.Minute)
			return true
		}
	}
	asked := os.Getenv(envAgent) == "1"
	if len(os.Args) > 1 && os.Args[1] == ArgTee {
		os.Exit(runTee(os.Args[2:]))
	}
	for _, a := range os.Args[1:] {
		if a == ArgAgent {
			asked = true
		}
	}
	if !asked {
		return false
	}
	newAgent().run()
	return true
}

type agent struct {
	out *bufio.Writer
	omu sync.Mutex

	mu         sync.Mutex
	waiters    map[string]chan msg // request id (raw) -> its response
	responses  map[string]int      // request id (raw) -> responses seen
	cancels    chan struct{}
	turns      sync.WaitGroup
	deaf       atomic.Bool
	mute       bool
	authed     bool
	reqSeq     int
	authMode   string
	markerPath string
	// sessions is the comma list of session options (see WithSessionLoad).
	sessions string
	// slowStart is how long a turn waits to begin after its prompt was read, as
	// it does on a machine that is busy.
	slowStart time.Duration
	// chosen holds the settings the client changed, by "mode", "model", "fast"
	// and so on; the session shape reports them as current.
	chosen map[string]string
}

func newAgent() *agent {
	marker := os.Getenv(envMarker)
	for i, a := range os.Args[1:] {
		if a == ArgMarker && i+2 < len(os.Args) {
			marker = os.Args[i+2]
		}
	}
	sessions := os.Getenv(envSessions)
	for _, a := range os.Args[1:] {
		if v, ok := strings.CutPrefix(a, ArgSessions); ok {
			sessions += "," + v
		}
	}
	return &agent{
		sessions:   strings.Trim(sessions, ","),
		out:        bufio.NewWriterSize(os.Stdout, 1<<16),
		waiters:    map[string]chan msg{},
		responses:  map[string]int{},
		cancels:    make(chan struct{}, 8),
		authMode:   os.Getenv(envAuth),
		mute:       os.Getenv(envMute) == "1",
		markerPath: marker,
		slowStart:  slowStartFromEnv(),
		chosen:     map[string]string{},
	}
}

type msg struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  json.RawMessage `json:"error"`
}

func (a *agent) send(v any) {
	b, _ := json.Marshal(v)
	a.omu.Lock()
	a.out.Write(append(b, '\n'))
	a.out.Flush()
	a.omu.Unlock()
}

func (a *agent) result(id json.RawMessage, v any) {
	a.send(map[string]any{"jsonrpc": "2.0", "id": id, "result": v})
}

func (a *agent) rpcError(id json.RawMessage, code int, message string) {
	a.send(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": message}})
}

func (a *agent) update(sid string, u map[string]any) {
	a.send(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": sid, "update": u}})
}

func textBlock(s string) map[string]any { return map[string]any{"type": "text", "text": s} }

func (a *agent) chunk(sid, s string) {
	a.update(sid, map[string]any{"sessionUpdate": "agent_message_chunk", "content": textBlock(s)})
}

func (a *agent) run() {
	if os.Getenv(envDeaf) == "1" {
		time.Sleep(time.Hour) // never reads its input at all
	}
	br := bufio.NewReaderSize(os.Stdin, 1<<20)
	for {
		if a.deaf.Load() {
			time.Sleep(time.Hour) // stops reading stdin, as a wedged agent does (a bare select would trip the deadlock detector)
		}
		line, err := br.ReadBytes('\n')
		if a.mute {
			if err != nil {
				return
			}
			continue
		}
		if len(bytes.TrimSpace(line)) > 0 {
			var m msg
			if json.Unmarshal(line, &m) == nil {
				a.handle(m)
			}
		}
		if err != nil {
			// stdin closed: the client is done with us. A turn that was
			// answered a moment ago gets a moment to finish what it does with
			// the answer.
			done := make(chan struct{})
			go func() { a.turns.Wait(); close(done) }()
			select {
			case <-done:
			case <-time.After(500 * time.Millisecond):
			}
			return
		}
	}
}

func (a *agent) handle(m msg) {
	switch {
	case m.Method == "" && len(m.ID) > 0: // a response to a permission request
		key := string(m.ID)
		a.mu.Lock()
		a.responses[key]++
		ch := a.waiters[key]
		a.mu.Unlock()
		if ch != nil {
			select {
			case ch <- m:
			default:
			}
		}
	case m.Method == "initialize":
		caps := map[string]any{"loadSession": false, "promptCapabilities": map[string]any{}, "futureCapability": map[string]any{"x": 1}}
		if os.Getenv(envShape) != "" {
			caps = map[string]any{"loadSession": true,
				"promptCapabilities":  map[string]any{"image": true, "embeddedContext": true},
				"mcpCapabilities":     map[string]any{"http": true},
				"sessionCapabilities": map[string]any{"list": map[string]any{}, "fork": map[string]any{}},
			}
		}
		if a.sessions != "" {
			// The session methods are what the test asked for, whatever the shape.
			caps["loadSession"] = a.hasSessionOpt("load")
			sc, _ := caps["sessionCapabilities"].(map[string]any)
			sc = cloneMap(sc)
			delete(sc, "list")
			delete(sc, "fork")
			if a.hasSessionOpt("resume") {
				sc["resume"] = map[string]any{}
			}
			caps["sessionCapabilities"] = sc
		}
		res := map[string]any{
			"protocolVersion":   1,
			"agentCapabilities": caps,
			"agentInfo":         map[string]any{"name": "acptest", "version": "0.0.1"},
			"authMethods":       []any{},
		}
		if a.authMode != "" {
			res["authMethods"] = []any{
				map[string]any{"id": "api-key", "name": "API key", "description": "Use a key"},
				map[string]any{"id": "login", "name": "Log in"},
			}
		}
		a.result(m.ID, res)
	case m.Method == "authenticate":
		a.mu.Lock()
		a.authed = true
		a.mu.Unlock()
		a.result(m.ID, map[string]any{})
	case m.Method == "session/new":
		var p struct {
			Cwd        string            `json:"cwd"`
			McpServers []json.RawMessage `json:"mcpServers"`
		}
		_ = json.Unmarshal(m.Params, &p)
		fmt.Fprintln(os.Stderr, "acptest: session/new cwd =", p.Cwd)
		if p.McpServers == nil {
			a.rpcError(m.ID, -32602, "mcpServers must be an array")
			return
		}
		// A test that wants to see the servers it was sent makes this file first;
		// the agent fills it with the mcpServers array as it arrived.
		if f := filepath.Join(p.Cwd, McpRecordFile); p.Cwd != "" {
			if _, err := os.Stat(f); err == nil {
				b, _ := json.Marshal(p.McpServers)
				_ = os.WriteFile(f, b, 0o600)
			}
		}
		if a.needsAuth("new") {
			a.rpcError(m.ID, -32000, "Authentication required")
			return
		}
		if a.hasSessionOpt("late-replay") {
			// The new session is slow to come, so the replay that is still being
			// sent arrives while the client does not know its id.
			time.Sleep(600 * time.Millisecond)
		}
		res := map[string]any{"sessionId": "sess-1"}
		a.sessionShape(res)
		a.result(m.ID, res)
	case m.Method == "session/load" || m.Method == "session/resume":
		a.reopen(m)
	case m.Method == "session/cancel":
		select {
		case a.cancels <- struct{}{}:
		default:
		}
	case m.Method == "session/cancel":
		select {
		case a.cancels <- struct{}{}:
		default:
		}
	case m.Method != "" && len(m.ID) == 0:
		// A notification the agent does not know is dropped, as the protocol says.
	case (m.Method == "session/set_mode" || m.Method == "session/set_model" || m.Method == "session/set_config_option") && os.Getenv(envShape) != "":
		a.setting(m)
	case m.Method != "" && m.Method != "session/prompt":
		// A request it does not know. Every method that is not one of the cases
		// above and below is unknown to it, as it is to a real agent that has not
		// implemented it (set_mode, set_config_option, session/list, ...).
		a.rpcError(m.ID, -32601, "method not found: "+m.Method)
	case m.Method == "session/prompt":
		if a.needsAuth("prompt") {
			a.rpcError(m.ID, -32000, "Authentication required")
			return
		}
		a.mu.Lock()
		a.reqSeq++
		n := a.reqSeq
		a.mu.Unlock()
		if strings.HasPrefix(promptText(m.Params), "deaf-perm") {
			a.deaf.Store(true)
		}
		// A cancel that is already here is from an earlier turn. One that arrives
		// after this point is this turn's, however late the turn gets going: this
		// is the reader, and the cancel is read after the prompt.
		// Not a receive that waits: a turn still running can take the last one.
	drain:
		for {
			select {
			case <-a.cancels:
			default:
				break drain
			}
		}
		a.turns.Add(1)
		go func() {
			defer a.turns.Done()
			if a.slowStart > 0 {
				time.Sleep(a.slowStart)
			}
			a.prompt(m.ID, m.Params, n)
		}()
	}
}

// current is the value of a setting now: what the client last chose, or def.
func (a *agent) current(key, def string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if v, ok := a.chosen[key]; ok {
		return v
	}
	return def
}

func (a *agent) setOpt(name string) bool {
	return os.Getenv(envSetOpts+"_"+name) == "1"
}

// configList is the config shape's options with the current values.
func (a *agent) configList() []any {
	list := []any{
		map[string]any{"id": "model", "name": "Model", "category": "model", "type": "select", "currentValue": a.current("model", "big"), "options": []any{
			map[string]any{"value": "big", "name": "Big", "description": "The large one"},
			map[string]any{"value": "small", "name": "Small"},
		}},
		map[string]any{"id": "mode", "name": "Mode", "category": "mode", "type": "select", "currentValue": a.current("mode", "build"), "options": []any{
			map[string]any{"group": "agents", "name": "Agents", "options": []any{
				map[string]any{"value": "build", "name": "Build"},
				map[string]any{"value": "plan", "name": "Plan"},
			}},
		}},
	}
	if a.setOpt("bool") {
		list = append(list, map[string]any{"id": "fast", "name": "Fast", "type": "boolean", "currentValue": a.current("fast", "false") == "true"})
	}
	return list
}

// sessionShape adds to res the session state the chosen shape lists.
func (a *agent) sessionShape(res map[string]any) {
	switch os.Getenv(envShape) {
	case "config":
		// The shape opencode and Codex use: settings are config options.
		res["configOptions"] = a.configList()
	case "modes":
		// The shape Gemini uses: a modes block and a models block.
		res["modes"] = map[string]any{"currentModeId": a.current("mode", "default"), "availableModes": []any{
			map[string]any{"id": "default", "name": "Default", "description": "Asks before it edits"},
			map[string]any{"id": "yolo", "name": "YOLO"},
		}}
		res["models"] = map[string]any{"currentModelId": a.current("model", "gem-pro"), "availableModels": []any{
			map[string]any{"modelId": "gem-pro", "name": "Gem Pro"},
			map[string]any{"modelId": "gem-flash", "name": "Gem Flash", "description": "Quick"},
		}}
	}
}

// setting answers session/set_mode, session/set_model and
// session/set_config_option for the shape the agent was given. A value the
// shape does not list is refused with an error whose text must never reach a
// person.
func (a *agent) setting(m msg) {
	if f := os.Getenv(envSetLog); f != "" {
		if w, err := os.OpenFile(f, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
			fmt.Fprintln(w, m.Method, string(m.Params))
			w.Close()
		}
	}
	var p struct {
		SessionID string          `json:"sessionId"`
		ModeID    string          `json:"modeId"`
		ModelID   string          `json:"modelId"`
		ConfigID  string          `json:"configId"`
		Value     json.RawMessage `json:"value"`
	}
	_ = json.Unmarshal(m.Params, &p)
	refuse := func(what string) { a.rpcError(m.ID, -32602, "SECRET-AGENT-TEXT: bad "+what) }
	has := func(set ...string) func(string) bool {
		return func(v string) bool {
			for _, s := range set {
				if s == v {
					return true
				}
			}
			return false
		}
	}
	shape := os.Getenv(envShape)
	if a.setOpt("refuse") {
		refuse("setting")
		return
	}
	switch m.Method {
	case "session/set_mode":
		if shape != "modes" || !has("default", "yolo")(p.ModeID) {
			refuse("mode")
			return
		}
		a.mu.Lock()
		a.chosen["mode"] = p.ModeID
		a.mu.Unlock()
		a.update(p.SessionID, map[string]any{"sessionUpdate": "current_mode_update", "currentModeId": p.ModeID})
		a.result(m.ID, map[string]any{})
	case "session/set_model":
		if shape != "modes" || !has("gem-pro", "gem-flash")(p.ModelID) {
			refuse("model")
			return
		}
		a.mu.Lock()
		a.chosen["model"] = p.ModelID
		a.mu.Unlock()
		a.result(m.ID, map[string]any{})
	default:
		var v string
		var b bool
		if json.Unmarshal(p.Value, &v) != nil {
			if json.Unmarshal(p.Value, &b) != nil {
				refuse("value")
				return
			}
			v = fmt.Sprint(b)
		}
		ok := shape == "config" && (p.ConfigID == "model" && has("big", "small")(v) || p.ConfigID == "mode" && has("build", "plan")(v) ||
			p.ConfigID == "fast" && a.setOpt("bool") && has("true", "false")(v) && json.Unmarshal(p.Value, &b) == nil)
		if !ok {
			refuse("option")
			return
		}
		a.mu.Lock()
		a.chosen[p.ConfigID] = v
		a.mu.Unlock()
		if a.setOpt("bare") {
			a.result(m.ID, map[string]any{})
			return
		}
		a.result(m.ID, map[string]any{"configOptions": a.configList()})
	}
}

func cloneMap(m map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range m {
		out[k] = v
	}
	return out
}

// hasSessionOpt says the test gave the agent the named session option.
func (a *agent) hasSessionOpt(name string) bool {
	for _, o := range strings.Split(a.sessions, ",") {
		if o == name {
			return true
		}
	}
	return false
}

// knowsSession says the test told the agent it has the session id.
func (a *agent) knowsSession(id string) bool {
	return id != "" && a.hasSessionOpt("id="+id)
}

// reopen answers session/load and session/resume.
func (a *agent) reopen(m msg) {
	var p struct {
		SessionID  string            `json:"sessionId"`
		Cwd        string            `json:"cwd"`
		McpServers []json.RawMessage `json:"mcpServers"`
	}
	_ = json.Unmarshal(m.Params, &p)
	fmt.Fprintf(os.Stderr, "acptest: %s id = %s cwd = %s\n", m.Method, p.SessionID, p.Cwd)
	if p.McpServers == nil {
		a.rpcError(m.ID, -32602, "mcpServers must be an array")
		return
	}
	method := strings.TrimPrefix(m.Method, "session/")
	if !a.hasSessionOpt(method) {
		a.rpcError(m.ID, -32601, "method not found: "+m.Method)
		return
	}
	if a.needsAuth("new") {
		a.rpcError(m.ID, -32000, "Authentication required")
		return
	}
	if !a.knowsSession(p.SessionID) {
		a.rpcError(m.ID, -32002, "session not found: SECRET-AGENT-TEXT "+p.SessionID)
		return
	}
	sid := p.SessionID
	if method == "load" && a.hasSessionOpt("load-refused") {
		a.rpcError(m.ID, -32603, "load failed: SECRET-AGENT-TEXT")
		return
	}
	if method == "load" && a.hasSessionOpt("late-replay") {
		a.update(sid, map[string]any{"sessionUpdate": "agent_message_chunk", "content": textBlock("first ")})
		go func() {
			for i := 0; i < 10; i++ {
				time.Sleep(100 * time.Millisecond)
				a.update(sid, map[string]any{"sessionUpdate": "agent_message_chunk", "content": textBlock("LATE")})
			}
		}()
		return
	}
	if method == "load" {
		// A real agent replays it all before it answers.
		a.update(sid, map[string]any{"sessionUpdate": "user_message_chunk", "content": textBlock("first question")})
		a.update(sid, map[string]any{"sessionUpdate": "agent_message_chunk", "content": textBlock("first ")})
		a.update(sid, map[string]any{"sessionUpdate": "agent_message_chunk", "content": textBlock("answer")})
		a.update(sid, map[string]any{"sessionUpdate": "tool_call", "toolCallId": "old-1", "title": "Read a.txt", "kind": "read", "status": "in_progress"})
		a.update(sid, map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "old-1", "status": "completed"})
		a.update(sid, map[string]any{"sessionUpdate": "user_message_chunk", "content": textBlock("second question")})
		a.update(sid, map[string]any{"sessionUpdate": "agent_message_chunk", "content": textBlock("second answer")})
		if a.hasSessionOpt("replay-hang") {
			a.update(sid, map[string]any{"sessionUpdate": "agent_message_chunk", "content": textBlock("cut off")})
			os.Exit(4)
		}
	}
	res := map[string]any{}
	a.sessionShape(res)
	a.result(m.ID, res)
	if method == "load" {
		// An update after the answer is not part of the replay.
		a.update(sid, map[string]any{"sessionUpdate": "available_commands_update", "availableCommands": []any{map[string]any{"name": "after-load"}}})
	}
}

func (a *agent) needsAuth(at string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.authMode == at && !a.authed
}

// askPermission sends a session/request_permission and returns the request's
// key and the channel its response arrives on.
func (a *agent) askPermission(sid string, id json.RawMessage) chan msg {
	ch := make(chan msg, 1)
	a.mu.Lock()
	a.waiters[string(id)] = ch
	a.mu.Unlock()
	a.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": "session/request_permission", "params": map[string]any{
		"sessionId": sid,
		"toolCall":  map[string]any{"toolCallId": "call-1", "title": "Write notes.txt", "kind": "edit", "status": "pending", "rawInput": map[string]any{"path": "notes.txt"}},
		"options": []any{
			map[string]any{"optionId": "allow-1", "name": "Allow once", "kind": "allow_once"},
			map[string]any{"optionId": "allow-all", "name": "Always allow", "kind": "allow_always"},
			map[string]any{"optionId": "reject-1", "name": "Reject", "kind": "reject_once"},
		},
	}})
	return ch
}

type outcome struct {
	Outcome struct {
		Outcome  string `json:"outcome"`
		OptionID string `json:"optionId"`
	} `json:"outcome"`
}

// promptText is the text of a session/prompt's first block.
func promptText(params json.RawMessage) string {
	var p struct {
		Prompt []struct {
			Text string `json:"text"`
		} `json:"prompt"`
	}
	_ = json.Unmarshal(params, &p)
	if len(p.Prompt) == 0 {
		return ""
	}
	return p.Prompt[0].Text
}

func (a *agent) prompt(id, params json.RawMessage, n int) {
	var p struct {
		SessionID string `json:"sessionId"`
		Prompt    []struct {
			Text string `json:"text"`
		} `json:"prompt"`
	}
	_ = json.Unmarshal(params, &p)
	sid, ask := p.SessionID, ""
	var blocks []string
	for _, b := range p.Prompt {
		blocks = append(blocks, b.Text)
	}
	if len(blocks) > 0 {
		ask = blocks[0]
	}
	// A prompt of several blocks that starts "Flockdeck message from" is mail: the
	// last block is the message body and says what to do, the first only names the
	// sender.
	if len(blocks) > 1 && strings.HasPrefix(ask, "Flockdeck message from") {
		ask = strings.TrimPrefix(blocks[len(blocks)-1], "Text of the message: ")
	}
	word, _, _ := strings.Cut(ask, " ")
	done := func(reason string) { a.result(id, map[string]any{"stopReason": reason}) }

	switch word {
	case "whoami":
		a.chunk(sid, "session="+sid)
		done("end_turn")

	case "plain":
		a.chunk(sid, "Hello")
		a.chunk(sid, " world")
		done("end_turn")

	case "tool":
		a.update(sid, map[string]any{"sessionUpdate": "tool_call", "toolCallId": "call-1", "title": "Run echo", "kind": "execute",
			"status": "pending", "locations": []any{map[string]any{"path": "/work/a.txt", "line": 3}}, "rawInput": map[string]any{"command": "echo hi"}})
		a.update(sid, map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "call-1", "status": "in_progress",
			"content": []any{map[string]any{"type": "content", "content": textBlock("hi\n")}}})
		a.update(sid, map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "call-1", "status": "completed",
			"rawOutput": map[string]any{"stdout": "hi\n"},
			"content": []any{
				map[string]any{"type": "content", "content": textBlock("hi\n")},
				map[string]any{"type": "diff", "path": "/work/a.txt", "oldText": nil, "newText": "hi\n"},
			}})
		a.chunk(sid, "Done.")
		a.update(sid, map[string]any{"sessionUpdate": "usage_update", "used": 1234, "size": 200000, "cost": map[string]any{"amount": 0.0042, "currency": "USD"}})
		done("end_turn")

	case "perm", "perm-str":
		var rid json.RawMessage
		if word == "perm-str" {
			rid = json.RawMessage(`"perm-abc"`)
		} else {
			rid = json.RawMessage(strconv.Itoa(1000 + n))
		}
		a.update(sid, map[string]any{"sessionUpdate": "tool_call", "toolCallId": "call-1", "title": "Write notes.txt", "kind": "edit", "status": "pending"})
		raw := (<-a.askPermission(sid, rid)).Result
		var o outcome
		_ = json.Unmarshal(raw, &o)
		switch o.Outcome.OptionID {
		case "allow-1", "allow-all":
			a.update(sid, map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "call-1", "status": "completed"})
		default:
			a.update(sid, map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "call-1", "status": "failed"})
		}
		a.chunk(sid, fmt.Sprintf("outcome=%s option=%s", o.Outcome.Outcome, o.Outcome.OptionID))
		if word == "perm-str" {
			a.chunk(sid, "id-echo="+string(rid))
		}
		done("end_turn")

	case "refuse":
		done("refusal")

	case "maxtokens":
		done("max_tokens")

	case "oddstop":
		done("made_up_reason")

	case "blocks":
		a.chunk(sid, fmt.Sprintf("blocks=%d", len(blocks)))
		done("end_turn")

	case "slow":
		a.chunk(sid, "working...")
		select {
		case <-a.cancels:
			done("cancelled")
		case <-time.After(30 * time.Second):
			done("end_turn")
		}

	case "slow-perm":
		a.askPermission(sid, json.RawMessage(strconv.Itoa(2000+n)))
		select {
		case <-a.cancels:
			// Give the client's own cancelled answer time to arrive, so that a
			// second, late answer from the person has a chance to follow it.
			time.Sleep(300 * time.Millisecond)
			done("cancelled")
		case <-time.After(30 * time.Second):
			done("end_turn")
		}

	case "report":
		a.mu.Lock()
		var parts []string
		for k, v := range a.responses {
			parts = append(parts, fmt.Sprintf("%s=%d", k, v))
		}
		a.mu.Unlock()
		a.chunk(sid, "responses: "+strings.Join(sortedStrings(parts), ","))
		done("end_turn")

	case "plan":
		a.update(sid, map[string]any{"sessionUpdate": "plan", "entries": []any{
			map[string]any{"content": "Edit the file", "priority": "high", "status": "in_progress"},
			map[string]any{"content": "Run tests", "priority": "medium", "status": "pending"},
			map[string]any{"content": "Something odd", "priority": "urgent", "status": "paused"},
		}})
		done("end_turn")

	case "state":
		// The updates that carry session state, in the shapes agents send them,
		// and a replayed user message.
		a.update(sid, map[string]any{"sessionUpdate": "available_commands_update", "availableCommands": []any{
			map[string]any{"name": "compact", "description": "Summarise the conversation"},
			map[string]any{"name": "review", "description": "Review a change", "input": map[string]any{"hint": "branch or commit"}},
		}})
		a.update(sid, map[string]any{"sessionUpdate": "current_mode_update", "currentModeId": "yolo"})
		a.update(sid, map[string]any{"sessionUpdate": "config_option_update", "configOptions": []any{
			map[string]any{"id": "model", "name": "Model", "category": "model", "type": "select", "currentValue": "small", "options": []any{
				map[string]any{"value": "big", "name": "Big"}, map[string]any{"value": "small", "name": "Small"}}},
		}})
		a.update(sid, map[string]any{"sessionUpdate": "session_info_update", "title": "Fix the parser", "updatedAt": "2026-10-27T09:00:00Z"})
		a.update(sid, map[string]any{"sessionUpdate": "plan_update", "content": textBlock("1. Read the code\n2. Fix it")})
		a.update(sid, map[string]any{"sessionUpdate": "user_message_chunk", "content": textBlock("echo of the prompt")})
		a.update(sid, map[string]any{"sessionUpdate": "compaction", "status": "completed", "message": "Compacted 80 messages."})
		a.update(sid, map[string]any{"sessionUpdate": "usage_update", "used": 90, "size": 1000})
		a.chunk(sid, "state sent")
		done("end_turn")

	case "unknown":
		a.update(sid, map[string]any{"sessionUpdate": "future_kind", "foo": 1, "nested": map[string]any{"a": []any{1, 2}}})
		a.update(sid, map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "hologram", "data": "x", "n": 7}})
		a.update(sid, map[string]any{"sessionUpdate": "tool_call", "toolCallId": "call-9", "title": "Teleport", "kind": "teleport", "status": "pending", "zzz": true})
		a.update(sid, map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "call-9",
			"content": []any{map[string]any{"type": "hologram", "x": 1}, map[string]any{"type": "content", "content": textBlock("ok")}}})
		a.update(sid, map[string]any{"sessionUpdate": "agent_message_chunk", "content": textBlock("after unknown"), "extra": 1})
		a.update(sid, map[string]any{"sessionUpdate": "agent_message_chunk", "content": "not an object"})
		done("end_turn")

	case "long":
		a.chunk(sid, strings.Repeat("r", 12000))
		done("end_turn")

	case "big":
		a.update(sid, map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "call-1", "status": "completed",
			"content": []any{map[string]any{"type": "content", "content": textBlock(strings.Repeat("x", 5<<20))}}})
		a.chunk(sid, "after big")
		done("end_turn")

	case "perm-chunks":
		a.update(sid, map[string]any{"sessionUpdate": "tool_call", "toolCallId": "call-1", "title": "Write notes.txt", "kind": "edit", "status": "pending"})
		for i := 0; i < 20; i++ {
			a.chunk(sid, "a")
		}
		raw := (<-a.askPermission(sid, json.RawMessage(strconv.Itoa(1000+n)))).Result
		var o outcome
		_ = json.Unmarshal(raw, &o)
		for i := 0; i < 20; i++ {
			a.chunk(sid, "b")
		}
		status := "failed"
		if o.Outcome.OptionID == "allow-1" {
			status = "completed"
		}
		a.update(sid, map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "call-1", "status": status})
		done("end_turn")

	case "storm":
		// Far more than a person could read: a thousand rewrites of one tool
		// call, each with 64 KiB of output, then 8 MiB of thoughts in 1 KiB
		// chunks.
		blob := strings.Repeat("storm output line\n", 3600)
		a.update(sid, map[string]any{"sessionUpdate": "tool_call", "toolCallId": "call-1", "title": "Run storm", "kind": "execute", "status": "pending"})
		for i := 0; i < 1000; i++ {
			a.update(sid, map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "call-1", "status": "in_progress",
				"content": []any{map[string]any{"type": "content", "content": textBlock(blob)}}})
		}
		kb := strings.Repeat("t", 1024)
		for i := 0; i < 8192; i++ {
			a.update(sid, map[string]any{"sessionUpdate": "agent_thought_chunk", "content": textBlock(kb)})
		}
		a.update(sid, map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "call-1", "status": "completed"})
		done("end_turn")

	case "order":
		for i := 0; i < 200; i++ {
			a.chunk(sid, strconv.Itoa(i))
		}
		done("end_turn")

	case "ratelimit":
		a.rpcError(id, -32603, "rate limit exceeded")

	case "env":
		env := os.Environ()
		sort.Strings(env)
		a.chunk(sid, strings.Join(env, "\n"))
		done("end_turn")

	case "stderr":
		chunk := strings.Repeat("e", 1023) + "\n"
		for i := 0; i < 1024; i++ {
			fmt.Fprint(os.Stderr, chunk)
		}
		fmt.Fprintln(os.Stderr, "acptest: last stderr line")
		a.chunk(sid, "after stderr")
		done("end_turn")

	case "hold-perm":
		ch := a.askPermission(sid, json.RawMessage(strconv.Itoa(3000+n)))
		raw := (<-ch).Result
		var o outcome
		_ = json.Unmarshal(raw, &o)
		if a.markerPath != "" {
			_ = os.WriteFile(a.markerPath, []byte(o.Outcome.Outcome+" "+o.Outcome.OptionID), 0o600)
		}
		done("cancelled")

	case "garbage":
		// Lines a client must survive: not JSON, a response to a request it
		// never made, a malformed update, and (after the real one) a second
		// response to the prompt.
		a.omu.Lock()
		a.out.WriteString("this is not json\n")
		a.out.Flush()
		a.omu.Unlock()
		a.send(map[string]any{"jsonrpc": "2.0", "id": 99999, "result": map[string]any{}})
		a.send(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": "not an object"})
		a.chunk(sid, "after garbage")
		done("end_turn")
		done("end_turn")

	case "unknown-method":
		// An agent-to-client request the client does not implement.
		ch := make(chan msg, 1)
		a.mu.Lock()
		a.waiters[`"m1"`] = ch
		a.mu.Unlock()
		a.send(map[string]any{"jsonrpc": "2.0", "id": "m1", "method": "fs/read_text_file", "params": map[string]any{"sessionId": sid, "path": "/x"}})
		r := <-ch
		var e struct {
			Code int `json:"code"`
		}
		_ = json.Unmarshal(r.Error, &e)
		a.chunk(sid, "unknown-method error="+strconv.Itoa(e.Code))
		done("end_turn")

	case "delayed":
		time.Sleep(400 * time.Millisecond)
		a.chunk(sid, "late")
		done("end_turn")

	case "silent":
		select {} // never answers

	case "flood":
		// A line with no end.
		a.omu.Lock()
		chunk := strings.Repeat("x", 1<<20)
		a.out.WriteString(`{"jsonrpc":"2.0","method":"x","params":"`)
		for i := 0; i < 4; i++ {
			a.out.WriteString(chunk)
		}
		a.out.Flush()
		a.omu.Unlock()
		select {}

	case "blank":
		// Lines with nothing in them, between real ones.
		a.omu.Lock()
		a.out.WriteString("\n   \r\n\n")
		a.out.Flush()
		a.omu.Unlock()
		a.chunk(sid, "after blank")
		done("end_turn")

	case "line":
		// "line N": one notification of exactly N bytes without its newline, then
		// a chunk and the end of the turn.
		_, arg, _ := strings.Cut(ask, " ")
		n, _ := strconv.Atoi(arg)
		const open, shut = `{"jsonrpc":"2.0","method":"x","params":"`, `"}`
		if n < len(open)+len(shut) {
			n = len(open) + len(shut)
		}
		a.omu.Lock()
		a.out.WriteString(open)
		a.out.WriteString(strings.Repeat("p", n-len(open)-len(shut)))
		a.out.WriteString(shut + "\n")
		a.out.Flush()
		a.omu.Unlock()
		a.chunk(sid, "after line")
		done("end_turn")

	case "stderr-flood":
		// Four MiB on stderr in one line, then a normal turn.
		fmt.Fprint(os.Stderr, strings.Repeat("e", 4<<20)+"\n")
		a.chunk(sid, "after stderr flood")
		done("end_turn")

	case "spam-perm":
		// Fifty permission requests at once.
		for i := 0; i < 50; i++ {
			a.askPermission(sid, json.RawMessage(strconv.Itoa(5000+i)))
		}
		time.Sleep(700 * time.Millisecond)
		a.mu.Lock()
		answered := len(a.responses)
		a.mu.Unlock()
		a.chunk(sid, "answered="+strconv.Itoa(answered))
		select {
		case <-a.cancels:
			done("cancelled")
		case <-time.After(30 * time.Second):
			done("end_turn")
		}

	case "deaf-perm":
		// Asks for permission; handle() has already stopped reading stdin.
		a.askPermission(sid, json.RawMessage(strconv.Itoa(4000+n)))
		select {}

	case "grandchild":
		exe, err := os.Executable()
		if err != nil {
			a.chunk(sid, "no executable: "+err.Error())
			done("end_turn")
			return
		}
		cmd := exec.Command(exe, argSleeper)
		cmd.Env = []string{} // not the agent's variables, so it is not taken for an agent
		sysproc.NoWindow(cmd)
		if err := cmd.Start(); err != nil {
			a.chunk(sid, "start: "+err.Error())
			done("end_turn")
			return
		}
		go func() { _ = cmd.Wait() }()
		a.chunk(sid, "pid="+strconv.Itoa(cmd.Process.Pid))
		done("end_turn")

	case "exit":
		fmt.Fprintln(os.Stderr, "acptest: exiting on request")
		os.Exit(3)

	case "exit-idle":
		// The turn ends normally, and the agent then exits on its own while
		// the client is waiting for the next prompt.
		a.chunk(sid, "bye")
		done("end_turn")
		go func() {
			time.Sleep(300 * time.Millisecond)
			fmt.Fprintln(os.Stderr, "acptest: exiting while idle")
			os.Exit(4)
		}()

	case "perm-then-exit":
		// The turn ends, then the agent asks for permission outside any turn
		// and exits with the request still open. With a marker path it exits
		// only once the test has created that file, to say it has seen the
		// request, so a slow machine cannot lose the request first; without
		// one it exits after a second.
		done("end_turn")
		go func() {
			a.askPermission(sid, json.RawMessage(strconv.Itoa(4000+n)))
			if a.markerPath == "" {
				time.Sleep(time.Second)
			}
			for deadline := time.Now().Add(30 * time.Second); a.markerPath != "" && time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
				if _, err := os.Stat(a.markerPath); err == nil {
					break
				}
			}
			fmt.Fprintln(os.Stderr, "acptest: exiting with a request open")
			os.Exit(5)
		}()

	default:
		a.chunk(sid, "unknown scenario: "+ask)
		done("end_turn")
	}
}

func sortedStrings(s []string) []string {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
	return s
}

func slowStartFromEnv() time.Duration {
	ms, _ := strconv.Atoi(os.Getenv(envSlow))
	return time.Duration(ms) * time.Millisecond
}
