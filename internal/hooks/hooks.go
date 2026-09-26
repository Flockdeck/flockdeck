// Package hooks carries agents' lifecycle events from panes back to Flockdeck.
//
// Claude Code is the case this was written for: its panes are launched with a
// generated --settings file registering command hooks that re-invoke this
// binary in `hook` mode, and those invocations POST to a loopback server Flockdeck
// runs, which is how a pane's status ("working", "waiting on you", "idle") is
// known accurately rather than being guessed from screen scraping. It is not
// the only case. The protocol is the event names below and nothing else, so
// any agent that can report its own lifecycle — including Flockdeck's own chat
// client, which speaks straight to a model API — reports it here, and the rest
// of the application never learns there was more than one kind of pane.
//
// An agent that reports nothing is not left in the dark: its status is
// inferred from what the pane prints, and the briefing this server hands back
// on SessionStart is put in front of its opening prompt instead.
package hooks

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jmwri/flockdeck/internal/spend"
)

// Event is a lifecycle notification from one pane.
type Event struct {
	SessionID string `json:"session"`
	Event     string `json:"event"`
	Tool      string `json:"tool,omitempty"`
	Cwd       string `json:"cwd,omitempty"`
	// Prompt is what the user asked, present on UserPromptSubmit. It is what
	// lets a tab name itself after the work rather than the directory.
	Prompt string `json:"prompt,omitempty"`
	// Source is how a SessionStart came about: "startup", "resume", "clear",
	// "compact" or "fork".
	Source string `json:"source,omitempty"`
	// Conversation is the agent's own id for the conversation, where it says.
	// It is SessionID until the user runs /clear: Claude Code then carries on
	// under a new id, in a new transcript, and reports that SessionStart
	// (source "clear") under it -- while the pane is still known by the id it
	// was started with, which from then on names the conversation before.
	Conversation string `json:"conversation,omitempty"`
	// Launch names the start of the pane's process the event comes from: the
	// pane's LaunchEnv, which is new each time the pane is started. A restarted
	// pane keeps its id, and a hook the process before it ran a moment too
	// late -- which on Windows can outlive the process that ran it -- is
	// otherwise an event about the new one. Empty from a hook that predates it.
	Launch string `json:"launch,omitempty"`
	// ToolInput is what a PreToolUse call's own tool_input says, kept to the
	// parts worth carrying past this process and each capped -- a Write's
	// whole file, say, never travels beyond what buildToolInput keeps of it --
	// then re-marshalled as compact JSON. Empty for every event but
	// PreToolUse, and for a PreToolUse whose input said nothing this cares
	// about. It is opaque to everything between here and the phone-facing
	// view that finally reads it (internal/server's waitingViews): Session
	// only stores and carries it forward the way it already does Tool.
	ToolInput string `json:"toolInput,omitempty"`
	// NotificationType is what a Notification is about -- Claude Code's
	// notification_type, carried through so the receiving end can tell an idle
	// nudge ("idle_prompt") apart from a real ask ("permission_prompt" and the
	// rest); see session.StatusForEvent. Empty for every other event, and for a
	// Notification from a Claude Code too old to say.
	NotificationType string `json:"notificationType,omitempty"`
	// Background says the event starts ("start") or ends ("end") a piece of
	// background work the agent keeps going after its turn is over -- a
	// run_in_background Bash command or a subagent -- and BackgroundID names it
	// so the end finds its start. Empty for every other event. See
	// backgroundOf for which events say so.
	Background   string `json:"background,omitempty"`
	BackgroundID string `json:"backgroundId,omitempty"`
}

// Background work a lifecycle event can report; see Event.Background.
const (
	BackgroundStart = "start"
	BackgroundEnd   = "end"
)

// LaunchEnv is the variable a pane's process is started with naming that
// start, which the hook reads back and sends as Event.Launch. Claude Code
// passes its environment on to the hooks it runs, whichever form they are
// written in, as it does FLOCKDECK_TOKEN.
const LaunchEnv = "FLOCKDECK_LAUNCH"

// payload is what the hook subprocess posts to the server.
type payload struct {
	Event
	Token string `json:"token"`
}

// claudePayload is the subset of the JSON Claude Code writes to a hook's stdin
// that we care about.
type claudePayload struct {
	SessionID string `json:"session_id"`
	ToolName  string `json:"tool_name"`
	Cwd       string `json:"cwd"`
	Prompt    string `json:"prompt"`
	// Claude Code has spelled the SessionStart source both ways; read either.
	Source string `json:"source"`
	How    string `json:"how"`
	// NotificationType says what a Notification is about.
	NotificationType string `json:"notification_type"`
	// IsInterrupt says a PostToolUseFailure is the user stopping the tool.
	IsInterrupt bool `json:"is_interrupt"`
	// ToolInput is a PreToolUse call's whole tool input -- the command for
	// Bash, the file and text for Edit/Write, the questions for
	// AskUserQuestion. Read as json.RawMessage rather than a concrete struct
	// because which fields it has depends on the tool, and this only ever
	// wants a few of them (see buildToolInput).
	ToolInput json.RawMessage `json:"tool_input"`
	// ToolResponse is what the tool answered on a PostToolUse; a background
	// Bash command names the shell it started there.
	ToolResponse json.RawMessage `json:"tool_response"`
	ToolUseID    string          `json:"tool_use_id"`
	// AgentID names the subagent a SubagentStart or SubagentStop is about.
	AgentID string `json:"agent_id"`
}

// backgroundWire is the few fields of a tool_input or tool_response that say
// something about background work, whichever tool's it is.
type backgroundWire struct {
	RunInBackground bool   `json:"run_in_background"`
	ShellID         string `json:"shell_id"`
	BashID          string `json:"bash_id"`
	TaskID          string `json:"task_id"`
	BackgroundTask  string `json:"backgroundTaskId"`
}

func (b backgroundWire) id() string {
	for _, s := range []string{b.BackgroundTask, b.ShellID, b.BashID, b.TaskID} {
		if s != "" {
			return s
		}
	}
	return ""
}

// backgroundOf reads whether a hook payload starts or ends background work.
//
// A background Bash command is known from its PostToolUse (after the tool has
// really run, so a refused call is never counted) and is ended by the model
// killing it. Claude Code fires no hook when one simply finishes, so it stays
// counted until the conversation is cleared -- a pane is then kept rather than
// closed by mistake, which is the safe way to be wrong. A subagent, background
// or not, is bracketed by SubagentStart and SubagentStop, which is what covers
// background Task/Agent calls without reading their input a second time.
func backgroundOf(event string, cp claudePayload) (op, id string) {
	switch event {
	case "SubagentStart":
		return BackgroundStart, "agent:" + cp.AgentID
	case "SubagentStop":
		return BackgroundEnd, "agent:" + cp.AgentID
	case "PostToolUse":
	default:
		return "", ""
	}
	var in, out backgroundWire
	_ = json.Unmarshal(cp.ToolInput, &in)
	_ = json.Unmarshal(cp.ToolResponse, &out)
	switch cp.ToolName {
	case "Bash":
		if !in.RunInBackground {
			return "", ""
		}
		id = out.id()
		if id == "" {
			id = cp.ToolUseID
		}
		return BackgroundStart, "shell:" + id
	case "KillShell", "TaskStop":
		if id = in.id(); id == "" {
			return "", ""
		}
		return BackgroundEnd, "shell:" + id
	}
	return "", ""
}

// toolEditWire and toolInputWire are the parts of a PreToolUse call's
// tool_input worth reading, in Claude Code's own wire shape (snake_case for
// Bash/Edit/Write's fields; AskUserQuestion's are already spelled the way
// AskQuestion below reads them, since that tool's schema is Flockdeck's own
// concern nowhere else).
type toolEditWire struct {
	OldString string `json:"old_string"`
	NewString string `json:"new_string"`
}

type toolInputWire struct {
	Command     string         `json:"command"`
	Description string         `json:"description"`
	FilePath    string         `json:"file_path"`
	OldString   string         `json:"old_string"`
	NewString   string         `json:"new_string"`
	Content     string         `json:"content"`
	Edits       []toolEditWire `json:"edits"`
	Questions   []AskQuestion  `json:"questions"`
}

// AskQuestion and AskOption are one question of an AskUserQuestion call, read
// straight off its tool_input -- Claude Code's own field names already match
// these tags, so there is nothing to translate.
type AskQuestion struct {
	Header      string      `json:"header,omitempty"`
	Question    string      `json:"question"`
	MultiSelect bool        `json:"multiSelect,omitempty"`
	Options     []AskOption `json:"options"`
}

type AskOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// toolInputFields is the capped, agent-agnostic shape Event.ToolInput
// marshals to. Every string in it has already been through clip, so
// marshalling it can never produce anything larger than the fields allow --
// unlike clipping the marshalled JSON itself, which can slice a truncated
// string in half and leave invalid JSON at the other end of the wire.
type toolInputFields struct {
	Command     string         `json:"command,omitempty"`
	Description string         `json:"description,omitempty"`
	FilePath    string         `json:"filePath,omitempty"`
	OldString   string         `json:"oldString,omitempty"`
	NewString   string         `json:"newString,omitempty"`
	Content     string         `json:"content,omitempty"`
	Edits       []toolEditWire `json:"edits,omitempty"`
	Questions   []AskQuestion  `json:"questions,omitempty"`
}

// maxToolInputFieldBytes bounds each string field of a tool_input carried
// onward -- a Write's content, say -- so that a permission prompt never
// carries an arbitrarily large payload any further than this process needs
// to. It matches the cap the phone protocol already puts on an inline diff.
const maxToolInputFieldBytes = 64 << 10

// buildToolInput reads a PreToolUse call's tool_input and returns it as
// compact JSON in the capped shape above, or "" when there was nothing in it
// worth carrying on (not PreToolUse, or a tool_input with none of these
// fields -- Read's file_path is not one of them, say, since Read asks
// nothing of the user).
func buildToolInput(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var w toolInputWire
	if json.Unmarshal(raw, &w) != nil {
		return ""
	}
	out := toolInputFields{
		Command:     clip(w.Command, maxToolInputFieldBytes),
		Description: clip(w.Description, maxToolInputFieldBytes),
		FilePath:    w.FilePath,
		OldString:   clip(w.OldString, maxToolInputFieldBytes),
		NewString:   clip(w.NewString, maxToolInputFieldBytes),
		Content:     clip(w.Content, maxToolInputFieldBytes),
		Questions:   w.Questions,
	}
	for _, e := range w.Edits {
		out.Edits = append(out.Edits, toolEditWire{
			OldString: clip(e.OldString, maxToolInputFieldBytes),
			NewString: clip(e.NewString, maxToolInputFieldBytes),
		})
	}
	if out.Command == "" && out.FilePath == "" && len(out.Questions) == 0 {
		return ""
	}
	data, err := json.Marshal(out)
	if err != nil {
		return ""
	}
	return string(data)
}

// Interrupted is what a PostToolUseFailure is reported as when the tool failed
// because the user stopped it. Claude Code 2.1.269, read from its executable,
// says so in the payload's is_interrupt, and then goes back to its prompt
// without a Stop: the turn is over, which the failure of a tool on its own is
// not, so the two are told apart here, where the payload is read.
const Interrupted = "Interrupted"

// finishedNotifications are the kinds of Notification that report something
// done rather than something waiting on the user: a login that succeeded, and
// an MCP server's question that has just been answered. Every Notification
// turns a pane amber, so passing these on put a pane back in the count of
// agents waiting on you the moment the user had dealt with it.
//
// Claude Code 2.1.269, read from its executable, sends two more through the
// same hook: "agent_completed" when a background agent it is keeping an eye
// on finishes or fails, and "computer_use_exit" -- "Claude is done using your
// computer" -- when a turn that used the computer ends. Both are news of
// something over. "agent_needs_input" and a push notification the model asks
// for itself are left to turn the pane amber: somebody is waiting on the user.
var finishedNotifications = map[string]bool{
	"auth_success":         true,
	"elicitation_complete": true,
	"elicitation_response": true,
	"agent_completed":      true,
	"computer_use_exit":    true,
}

// Server receives hook events on the loopback interface.
type Server struct {
	ln    net.Listener
	srv   *http.Server
	token string
	on    func(Event)

	mu         sync.RWMutex
	onSpawn    func(SpawnRequest) (SpawnResult, error)
	onContext  func(sessionID string) string
	onUsage    func(spend.Report)
	onReview   func(sessionID string, ev Event) (allow bool, reason string)
	onPeerName func(PeerNameRequest) error
	onClose    func(CloseRequest) (CloseResult, error)
}

// SessionStart is the lifecycle event a pane's agent fires as it starts,
// resumes or is compacted. It is the one event whose reply matters: the
// response body carries the text describing the pane the session is running
// in, which the hook then prints for the agent to read.
//
// Answering it is the better of the two ways a briefing is delivered, and the
// reason is the compaction: a briefing put in front of an opening prompt is
// summarised away with everything else that was said early on, where this one
// is asked for again and returned afresh.
const SessionStart = "SessionStart"

// SetContextHandler installs the function that describes a pane to the agent
// running in it. Returning an empty string leaves the session as it was.
//
// It serves only the agents that fire hooks. An agent that fires none is
// briefed through its opening prompt, which is built from the same
// description and never reaches this server.
func (s *Server) SetContextHandler(fn func(sessionID string) string) {
	s.mu.Lock()
	s.onContext = fn
	s.mu.Unlock()
}

// SetReviewHandler installs auto-review approvals: the function consulted on
// every PreToolUse call, which answers whether the pane it names is safe to
// let this one through without asking. It is not asked at all for a pane
// where auto-review is off, which is the handler's own decision to make --
// this server only ever forwards what it is told.
//
// Returning allow == false is answered exactly as no handler being installed
// at all is: the request falls through to Claude Code's own permission
// prompt, unchanged from every build before this one. See Response.Allow.
func (s *Server) SetReviewHandler(fn func(sessionID string, ev Event) (allow bool, reason string)) {
	s.mu.Lock()
	s.onReview = fn
	s.mu.Unlock()
}

// Serve starts a hook server on a random loopback port. Events are delivered
// to on, which may be called from multiple goroutines.
func Serve(on func(Event)) (*Server, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen on loopback: %w", err)
	}

	tok := make([]byte, 16)
	if _, err := rand.Read(tok); err != nil {
		ln.Close()
		return nil, fmt.Errorf("generate token: %w", err)
	}

	s := &Server{ln: ln, token: hex.EncodeToString(tok), on: on}
	mux := http.NewServeMux()
	mux.HandleFunc("/hook", s.handle)
	mux.HandleFunc("/spawn", s.handleSpawn)
	mux.HandleFunc("/usage", s.handleUsage)
	mux.HandleFunc("/peer-name", s.handlePeerName)
	mux.HandleFunc("/close", s.handleClose)
	s.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}

	go func() { _ = s.srv.Serve(ln) }()
	return s, nil
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var p payload
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&p); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	// The port is loopback-only but still reachable by any local process, so
	// require the token we handed to our own panes.
	if subtle.ConstantTimeCompare([]byte(p.Token), []byte(s.token)) != 1 {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if s.on != nil && p.SessionID != "" {
		s.on(p.Event)
	}

	s.mu.RLock()
	ctxFn, reviewFn := s.onContext, s.onReview
	s.mu.RUnlock()

	var rep reply
	answered := false
	// Only SessionStart has a context to say back. Building it costs a trip
	// through the goroutine that owns the workspace, so it is not done for
	// the events that fire on every tool call.
	if p.Event.Event == SessionStart && ctxFn != nil && p.SessionID != "" {
		rep.Context = ctxFn(p.SessionID)
		answered = true
	}
	// Only PreToolUse is worth reviewing: it is the one event Claude Code
	// still reads a reply from once its own permission prompt would open, so
	// it is the only place a decision can spare the prompt at all.
	if p.Event.Event == "PreToolUse" && reviewFn != nil && p.SessionID != "" {
		if allow, reason := reviewFn(p.SessionID, p.Event); allow {
			rep.PermissionDecision, rep.PermissionReason = "allow", reason
			answered = true
		}
	}
	if !answered {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(rep)
}

// reply is what the server answers a hook with.
type reply struct {
	Context string `json:"context,omitempty"`
	// PermissionDecision is "allow" where auto-review has decided a
	// PreToolUse call is safe to let through unasked, and PermissionReason
	// says why -- see Response.Allow. Never anything else: this protocol has
	// no way to say "deny", on purpose.
	PermissionDecision string `json:"permissionDecision,omitempty"`
	PermissionReason   string `json:"permissionReason,omitempty"`
}

// Response is what Emit returns once a hook's request has been answered.
type Response struct {
	// Context is the pane description a SessionStart hook is answered with,
	// empty for every other event.
	Context string
	// Allow is true where auto-review has decided a PreToolUse call needs no
	// prompt at all -- see SetReviewHandler. False is not a refusal, only
	// "answered as it always has been": whatever Claude Code's own permission
	// settings would otherwise do with this call still happens.
	Allow bool
	// Reason is why, shown to the agent as the permission decision's reason.
	// Empty when Allow is false.
	Reason string
}

// Endpoint is the URL panes should post their events to.
func (s *Server) Endpoint() string {
	return "http://" + s.ln.Addr().String() + "/hook"
}

// Token is the shared secret panes must present.
func (s *Server) Token() string { return s.token }

// Close shuts the server down.
func (s *Server) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	return s.srv.Shutdown(ctx)
}

// maxHookPayload bounds what a hook will read from Claude Code.
//
// A PreToolUse payload carries the whole tool input, which for a Write is the
// file being written and for an Edit is the text on both sides of it. A
// megabyte is a size real work reaches, and a payload cut short is not a
// payload with a field missing: the JSON no longer parses, so the tool name,
// the directory and the source are lost together — which is the pane's status
// for that tool call. This is a generous ceiling on something a subprocess
// reads once and throws away.
const maxHookPayload = 64 << 20

// maxPromptBytes is how much of a prompt is worth carrying back. All the
// receiving end does with it is name a tab after the first few words, and the
// server that receives it reads a bounded body — so a prompt with a pasted
// file in it would push the whole event past that limit and lose the event
// along with the prompt, leaving the pane's status stuck on the turn before.
const maxPromptBytes = 4 << 10

// clip cuts s to at most n bytes, on a rune boundary.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// Emit is the client half, run inside the hook subprocess. It reads Claude's
// hook JSON from stdin for the tool, the directory, the prompt and the source,
// and posts the event -- unless it is a Notification of something finished,
// which is not reported at all.
//
// What it returns is the pane briefing and, for a PreToolUse call, whether
// auto-review has decided it needs no prompt at all -- see Response and
// SetReviewHandler. An agent reporting its own lifecycle rather than being
// wrapped in a hook command — Flockdeck's chat client does — calls this with
// a nil stdin and shows what comes back the same way.
//
// It is deliberately forgiving: a hook that fails must never block or break
// the session it is reporting on.
func Emit(stdin io.Reader, endpoint, token, sessionID, event string) (Response, error) {
	// Which start of the pane this is comes from the environment, where the
	// pane put it: the hook and Flockdeck's chat client both run inside it.
	p := payload{Event: Event{SessionID: sessionID, Event: event, Launch: os.Getenv(LaunchEnv)}, Token: token}

	if stdin != nil {
		var cp claudePayload
		if json.NewDecoder(io.LimitReader(stdin, maxHookPayload)).Decode(&cp) == nil {
			p.Tool = cp.ToolName
			p.Cwd = cp.Cwd
			p.ToolInput = buildToolInput(cp.ToolInput)
			p.Conversation = cp.SessionID
			p.Prompt = clip(cp.Prompt, maxPromptBytes)
			p.Source = cp.Source
			if p.Source == "" {
				p.Source = cp.How
			}
			if event == "Notification" && finishedNotifications[cp.NotificationType] {
				return Response{}, nil
			}
			p.NotificationType = cp.NotificationType
			p.Background, p.BackgroundID = backgroundOf(event, cp)
			if event == "PostToolUseFailure" && cp.IsInterrupt {
				p.Event.Event = Interrupted
			}
		}
	}

	body, err := json.Marshal(p)
	if err != nil {
		return Response{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return Response{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Response{}, err
	}
	defer resp.Body.Close()
	// A refusal is silent otherwise, and a hook that is being refused looks
	// exactly like one that is not running: panes whose status simply stops
	// changing. The caller only writes this to stderr, where Claude Code shows
	// it under --debug, so saying so cannot disturb the session either.
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		if text := strings.TrimSpace(string(msg)); text != "" {
			return Response{}, fmt.Errorf("the application refused the %s hook: %s", event, text)
		}
		return Response{}, fmt.Errorf("the application refused the %s hook: %s", event, resp.Status)
	}
	var out reply
	if resp.StatusCode == http.StatusOK {
		_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return Response{Context: out.Context, Allow: out.PermissionDecision == "allow", Reason: out.PermissionReason}, nil
}

// SpawnRequest is a pane asking the application to start another agent.
//
// This is what makes a lead agent able to hand work to helpers: it runs
// `flockdeck spawn`, which posts here using the address and token its pane
// was given in its environment.
type SpawnRequest struct {
	Parent string `json:"parent"`
	Task   string `json:"task"`
	Branch string `json:"branch,omitempty"`
	Split  bool   `json:"split,omitempty"`
	Shell  bool   `json:"shell,omitempty"`
	// Agent and Model are which agent the helper should be, for an agent
	// handing work to one that is better at it than itself. Both empty is what
	// every earlier build sent and means the project's default, so a helper
	// asked for without an opinion is started exactly as it always was.
	Agent string `json:"agent,omitempty"`
	Model string `json:"model,omitempty"`
	Token string `json:"token"`
}

// SpawnResult is what the application answers a spawn with.
type SpawnResult struct {
	PaneID string `json:"paneId"`
	// Cwd is where the new agent is working. It is worth saying back because
	// the caller cannot work it out: --worktree asks for a branch, and which
	// directory that becomes is the application's decision, not the caller's.
	Cwd string `json:"cwd,omitempty"`
}

// BaseURL is the address panes call back on.
func (s *Server) BaseURL() string { return "http://" + s.ln.Addr().String() }

// SetSpawnHandler installs the function that starts a child agent. It returns
// the new pane and where it is working, or an error explaining why it could not.
func (s *Server) SetSpawnHandler(fn func(SpawnRequest) (SpawnResult, error)) {
	s.mu.Lock()
	s.onSpawn = fn
	s.mu.Unlock()
}

func (s *Server) handleSpawn(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req SpawnRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if subtle.ConstantTimeCompare([]byte(req.Token), []byte(s.token)) != 1 {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	s.mu.RLock()
	fn := s.onSpawn
	s.mu.RUnlock()
	if fn == nil {
		http.Error(w, "spawning is not available", http.StatusServiceUnavailable)
		return
	}
	res, err := fn(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(res)
}

// spawnTimeout is how long `flockdeck spawn` waits for the application to
// answer. It is a variable so a test does not have to wait that long.
var spawnTimeout = 60 * time.Second

// spawnFailure says what a spawn that got no answer means, for the agent that
// asked, which acts on what it reads.
//
// "context deadline exceeded" reads as a failure, and an agent told that asks
// again -- while the application, which only answers once the worktree is
// made and the pane is started, may be doing exactly what it was asked. A
// refused connection is the application having gone, which no retry will fix,
// and one dropped before any answer is the same thing seen a step later: the
// application closing as the request reached it, which Linux reports as a
// reset and Windows as a failed read.
func spawnFailure(err error, api string) error {
	var op *net.OpError
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("Flockdeck did not answer within %s; the helper may still be starting, so look for its pane before asking again", spawnTimeout)
	case errors.As(err, &op) && (op.Op == "dial" || op.Op == "read"):
		return fmt.Errorf("Flockdeck is not answering at %s; it may have been closed since this pane started", api)
	}
	return err
}

// Spawn is the client half, used by the `spawn` subcommand inside a pane.
func Spawn(api, token, parent string, req SpawnRequest) (SpawnResult, error) {
	req.Token = token
	req.Parent = parent
	var none SpawnResult
	body, err := json.Marshal(req)
	if err != nil {
		return none, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), spawnTimeout)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, api+"/spawn", bytes.NewReader(body))
	if err != nil {
		return none, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return none, spawnFailure(err, api)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		// A refusal usually explains itself in the body; when it does not,
		// the status is all the caller has to go on, so never return an
		// error that prints as nothing.
		if text := strings.TrimSpace(string(msg)); text != "" {
			return none, fmt.Errorf("%s", text)
		}
		return none, fmt.Errorf("the application refused: %s", resp.Status)
	}
	var out SpawnResult
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return none, fmt.Errorf("unreadable answer from the application: %w", err)
	}
	if out.PaneID == "" {
		return none, fmt.Errorf("the application accepted the request but named no pane")
	}
	return out, nil
}

// PeerNameRequest is a pane reporting the name another Claude session would
// use to address it -- SendMessage's "to", the way ListAgents shows it.
//
// Flockdeck cannot learn this on its own: it is assigned by infrastructure
// entirely outside this application, and known only to whichever agent asks
// it directly (typically by calling its own ListAgents tool). `flockdeck
// peer-name <name>` is how a pane hands that answer back, posting here with
// the address and token its environment was given, the same way
// SpawnRequest is.
type PeerNameRequest struct {
	Pane  string `json:"pane"`
	Name  string `json:"name"`
	Token string `json:"token"`
}

// SetPeerNameHandler installs the function that records a pane's own report
// of its peer name. It returns an error explaining why the report could not
// be kept -- an unknown pane, most likely, one closed between starting the
// command and it answering.
func (s *Server) SetPeerNameHandler(fn func(PeerNameRequest) error) {
	s.mu.Lock()
	s.onPeerName = fn
	s.mu.Unlock()
}

func (s *Server) handlePeerName(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req PeerNameRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if subtle.ConstantTimeCompare([]byte(req.Token), []byte(s.token)) != 1 {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		http.Error(w, "a name is required", http.StatusBadRequest)
		return
	}

	s.mu.RLock()
	fn := s.onPeerName
	s.mu.RUnlock()
	if fn == nil {
		http.Error(w, "reporting a peer name is not available", http.StatusServiceUnavailable)
		return
	}
	if err := fn(req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// peerNameTimeout is how long `flockdeck peer-name` waits for the
// application to answer. Recording a report touches nothing slow, so this is
// far shorter than spawnTimeout: a pane that gets no answer within it is one
// the application is not keeping up with at all.
var peerNameTimeout = 5 * time.Second

// peerNameFailure says what a peer-name report that got no answer means, the
// same shape as spawnFailure but worded for this command: there is no helper
// pane to go looking for, only a report that did not land.
func peerNameFailure(err error, api string) error {
	var op *net.OpError
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("Flockdeck did not answer within %s; it may be busy, so try again shortly", peerNameTimeout)
	case errors.As(err, &op) && (op.Op == "dial" || op.Op == "read"):
		return fmt.Errorf("Flockdeck is not answering at %s; it may have been closed since this pane started", api)
	}
	return err
}

// PeerName is the client half, used by the `peer-name` subcommand inside a
// pane.
func PeerName(api, token, pane, name string) error {
	body, err := json.Marshal(PeerNameRequest{Pane: pane, Name: name, Token: token})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), peerNameTimeout)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, api+"/peer-name", bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return peerNameFailure(err, api)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if text := strings.TrimSpace(string(msg)); text != "" {
			return fmt.Errorf("%s", text)
		}
		return fmt.Errorf("the application refused: %s", resp.Status)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

// CloseRequest is a pane asking the application to close another pane --
// what `flockdeck close` posts, the same pattern as SpawnRequest and
// PeerNameRequest before it.
//
// This is the other half of what makes a lead agent able to hand work to
// helpers: Spawn starts one, and this is how the lead cleans one up again once
// it is done, without a person having to find it and press Ctrl+Shift+W
// themselves. Pane is the caller's own id -- FLOCKDECK_PANE, exactly as
// SpawnRequest's Parent is -- carried so the application can refuse a pane
// asking to close itself; see handleClose.
//
// A single pane is named in Target. Finished asks for a different thing
// entirely -- close every idle or exited pane across every open project, the
// same as the "Close finished panes" command -- and Target is left empty for
// it.
type CloseRequest struct {
	Pane     string `json:"pane"`
	Target   string `json:"target,omitempty"`
	Finished bool   `json:"finished,omitempty"`
	// Force closes Target even while it is still working. Without it, closing
	// a pane that is not idle or exited is refused -- the same "finished"
	// rule CloseFinishedPanes applies to every pane -- so that a confused or
	// misbehaving agent naming the wrong id cannot silently cut off work still
	// under way; see hooks.SetCloseHandler's installed function. Meaningless
	// alongside Finished, which never closes a busy pane no matter what Force
	// says.
	Force bool   `json:"force,omitempty"`
	Token string `json:"token"`
}

// CloseResult is what the application answers a close with. Closed is set for
// a single-pane close; Panes and Tabs are set for Finished, the same counts
// CloseFinishedPanes itself returns.
type CloseResult struct {
	Closed bool `json:"closed,omitempty"`
	Panes  int  `json:"panes,omitempty"`
	Tabs   int  `json:"tabs,omitempty"`
}

// SetCloseHandler installs the function that closes a pane, or every finished
// one, on a request from another pane. It returns an error explaining why the
// request could not be carried out -- an unknown pane, one still working with
// no -force, or one asking to close itself.
func (s *Server) SetCloseHandler(fn func(CloseRequest) (CloseResult, error)) {
	s.mu.Lock()
	s.onClose = fn
	s.mu.Unlock()
}

func (s *Server) handleClose(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req CloseRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if subtle.ConstantTimeCompare([]byte(req.Token), []byte(s.token)) != 1 {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if !req.Finished && strings.TrimSpace(req.Target) == "" {
		http.Error(w, "a pane to close is required, or -finished for every finished one", http.StatusBadRequest)
		return
	}

	s.mu.RLock()
	fn := s.onClose
	s.mu.RUnlock()
	if fn == nil {
		http.Error(w, "closing a pane is not available", http.StatusServiceUnavailable)
		return
	}
	res, err := fn(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(res)
}

// closeTimeout is how long `flockdeck close` waits for the application to
// answer. Longer than peerNameTimeout: -finished can tear down every idle or
// exited pane across every open project, not touch one record in memory.
var closeTimeout = 15 * time.Second

// closeFailure says what a close that got no answer means, the same shape as
// spawnFailure and peerNameFailure but worded for this command.
func closeFailure(err error, api string) error {
	var op *net.OpError
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("Flockdeck did not answer within %s; it may be busy, so try again shortly", closeTimeout)
	case errors.As(err, &op) && (op.Op == "dial" || op.Op == "read"):
		return fmt.Errorf("Flockdeck is not answering at %s; it may have been closed since this pane started", api)
	}
	return err
}

// Close is the client half, used by the `close` subcommand inside a pane.
func Close(api, token, pane string, req CloseRequest) (CloseResult, error) {
	req.Token = token
	req.Pane = pane
	var none CloseResult
	body, err := json.Marshal(req)
	if err != nil {
		return none, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, api+"/close", bytes.NewReader(body))
	if err != nil {
		return none, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return none, closeFailure(err, api)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if text := strings.TrimSpace(string(msg)); text != "" {
			return none, fmt.Errorf("%s", text)
		}
		return none, fmt.Errorf("the application refused: %s", resp.Status)
	}
	var out CloseResult
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return none, fmt.Errorf("unreadable answer from the application: %w", err)
	}
	return out, nil
}
