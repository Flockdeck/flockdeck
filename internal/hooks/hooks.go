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
	"strings"
	"sync"
	"time"
	"unicode/utf8"
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
}

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

	mu        sync.RWMutex
	onSpawn   func(SpawnRequest) (SpawnResult, error)
	onContext func(sessionID string) string
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

	// Only SessionStart has anything to say back. Building the context costs a
	// trip through the goroutine that owns the workspace, so it is not done
	// for the events that fire on every tool call.
	s.mu.RLock()
	fn := s.onContext
	s.mu.RUnlock()
	if p.Event.Event != SessionStart || fn == nil || p.SessionID == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(reply{Context: fn(p.SessionID)})
}

// reply is what the server answers a hook with.
type reply struct {
	Context string `json:"context,omitempty"`
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
// What it returns is the pane briefing, and only a SessionStart is answered
// with one: the caller prints it for the agent to read. An agent reporting its
// own lifecycle rather than being wrapped in a hook command — Flockdeck's chat
// client does — calls this with a nil stdin and shows what comes back the same
// way.
//
// It is deliberately forgiving: a hook that fails must never block or break
// the session it is reporting on.
func Emit(stdin io.Reader, endpoint, token, sessionID, event string) (string, error) {
	p := payload{Event: Event{SessionID: sessionID, Event: event}, Token: token}

	if stdin != nil {
		var cp claudePayload
		if json.NewDecoder(io.LimitReader(stdin, maxHookPayload)).Decode(&cp) == nil {
			p.Tool = cp.ToolName
			p.Cwd = cp.Cwd
			p.Conversation = cp.SessionID
			p.Prompt = clip(cp.Prompt, maxPromptBytes)
			p.Source = cp.Source
			if p.Source == "" {
				p.Source = cp.How
			}
			if event == "Notification" && finishedNotifications[cp.NotificationType] {
				return "", nil
			}
			if event == "PostToolUseFailure" && cp.IsInterrupt {
				p.Event.Event = Interrupted
			}
		}
	}

	body, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	// A refusal is silent otherwise, and a hook that is being refused looks
	// exactly like one that is not running: panes whose status simply stops
	// changing. The caller only writes this to stderr, where Claude Code shows
	// it under --debug, so saying so cannot disturb the session either.
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		if text := strings.TrimSpace(string(msg)); text != "" {
			return "", fmt.Errorf("the application refused the %s hook: %s", event, text)
		}
		return "", fmt.Errorf("the application refused the %s hook: %s", event, resp.Status)
	}
	var out reply
	if resp.StatusCode == http.StatusOK {
		_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return out.Context, nil
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
		// What comes back is read by the agent that asked, which acts on it.
		// "context deadline exceeded" reads as a failure, and an agent told
		// that asks again -- while the application, which only answers once
		// the worktree is made and the pane is started, may be doing exactly
		// what it was asked. A refused connection is the application having
		// gone, which no retry will fix.
		var dial *net.OpError
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return none, fmt.Errorf("Flockdeck did not answer within %s; the helper may still be starting, so look for its pane before asking again", spawnTimeout)
		// A connection dropped before any answer is the same thing seen a
		// step later -- the application closing as the request reached it --
		// which Linux reports as a reset and Windows as a failed read.
		case errors.As(err, &dial) && (dial.Op == "dial" || dial.Op == "read"):
			return none, fmt.Errorf("Flockdeck is not answering at %s; it may have been closed since this pane started", api)
		}
		return none, err
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
