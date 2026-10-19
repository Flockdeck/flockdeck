package acp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/acp/acptest"
)

// The tests in this file are the contract between the client and an ACP agent,
// run against the fake agent in acptest. No real agent is started.

const wait = 10 * time.Second

// recorder is a Handler that keeps what it is given.
type recorder struct {
	mu      sync.Mutex
	updates []Update
	notify  chan struct{}
	perm    func(ctx context.Context, req PermissionRequest) string
	perms   []PermissionRequest
}

func newRecorder() *recorder { return &recorder{notify: make(chan struct{}, 4096)} }

func (r *recorder) Update(u Update) {
	r.mu.Lock()
	r.updates = append(r.updates, u)
	r.mu.Unlock()
	select {
	case r.notify <- struct{}{}:
	default:
	}
}

func (r *recorder) Permission(ctx context.Context, req PermissionRequest) string {
	r.mu.Lock()
	r.perms = append(r.perms, req)
	f := r.perm
	r.mu.Unlock()
	if f == nil {
		return ""
	}
	return f(ctx, req)
}

func (r *recorder) all() []Update {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Update(nil), r.updates...)
}

func (r *recorder) reset() {
	r.mu.Lock()
	r.updates = nil
	r.mu.Unlock()
}

// text joins the text of the agent's message chunks.
func (r *recorder) text() string {
	var b strings.Builder
	for _, u := range r.all() {
		if u.Kind == UpdateAgentMessageChunk && u.Content != nil {
			b.WriteString(u.Content.Text)
		}
	}
	return b.String()
}

// waitFor waits until cond holds over the updates so far.
func (r *recorder) waitFor(t *testing.T, what string, cond func([]Update) bool) {
	t.Helper()
	deadline := time.After(wait)
	for {
		if cond(r.all()) {
			return
		}
		select {
		case <-r.notify:
		case <-time.After(20 * time.Millisecond):
		case <-deadline:
			t.Fatalf("timed out waiting for %s; updates so far: %d", what, len(r.all()))
		}
	}
}

func ctxT(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// start launches the fake agent, initializes it and opens a session. The
// session's cwd is outside the user's profile.
func start(t *testing.T, r *recorder, opts ...acptest.Option) (*Client, string) {
	t.Helper()
	c := startRaw(t, r, nil, opts...)
	if _, err := c.Initialize(ctxT(t)); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	sid, err := c.NewSession(ctxT(t), acptest.SessionDir(t), nil)
	if err != nil {
		t.Fatalf("session/new: %v", err)
	}
	return c, sid
}

func startRaw(t *testing.T, r *recorder, stderr func(string), opts ...acptest.Option) *Client {
	t.Helper()
	path, args, env := acptest.Command(t, opts...)
	c, err := Start(Config{Path: path, Args: args, Env: env, Dir: acptest.SessionDir(t), Handler: r, Stderr: stderr})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func prompt(t *testing.T, c *Client, sid, text string) string {
	t.Helper()
	stop, err := c.Prompt(ctxT(t), sid, text)
	if err != nil {
		t.Fatalf("prompt %q: %v", text, err)
	}
	return stop
}

func TestAPlainTurn(t *testing.T) {
	r := newRecorder()
	path, args, env := acptest.Command(t)
	c, err := Start(Config{Path: path, Args: args, Env: env, Handler: r})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })

	init, err := c.Initialize(ctxT(t))
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if init.ProtocolVersion != 1 || init.AgentInfo == nil || init.AgentInfo.Name != "acptest" {
		t.Errorf("initialize result = %+v", init)
	}
	if init.AuthMethods == nil || len(init.AuthMethods) != 0 {
		t.Errorf("authMethods = %v, want an empty list", init.AuthMethods)
	}
	sid, err := c.NewSession(ctxT(t), acptest.SessionDir(t), nil)
	if err != nil {
		t.Fatalf("session/new (the agent refuses mcpServers that is not an array): %v", err)
	}
	if stop := prompt(t, c, sid, "plain"); stop != StopEndTurn {
		t.Errorf("stop reason = %q", stop)
	}
	if got := r.text(); got != "Hello world" {
		t.Errorf("message = %q, want %q", got, "Hello world")
	}
}

func TestAToolCallWithUpdates(t *testing.T) {
	r := newRecorder()
	c, sid := start(t, r)
	prompt(t, c, sid, "tool")

	us := r.all()
	var kinds []string
	for _, u := range us {
		kinds = append(kinds, u.Kind)
	}
	want := []string{UpdateToolCall, UpdateToolCallUpdate, UpdateToolCallUpdate, UpdateAgentMessageChunk, UpdateUsage}
	if strings.Join(kinds, ",") != strings.Join(want, ",") {
		t.Fatalf("update kinds = %v, want %v", kinds, want)
	}

	call := us[0]
	if call.ToolCallID != "call-1" || call.ToolKind != "execute" || call.Status != "pending" || call.Title != "Run echo" {
		t.Errorf("tool_call = %+v", call)
	}
	if len(call.Locations) != 1 || call.Locations[0].Path != "/work/a.txt" || call.Locations[0].Line == nil || *call.Locations[0].Line != 3 {
		t.Errorf("locations = %+v", call.Locations)
	}
	if string(call.RawInput) != `{"command":"echo hi"}` {
		t.Errorf("rawInput = %s", call.RawInput)
	}

	running := us[1]
	if running.Status != "in_progress" || len(running.ToolContent) != 1 || running.ToolContent[0].Content == nil || running.ToolContent[0].Content.Text != "hi\n" {
		t.Errorf("in-progress update = %+v", running)
	}
	done := us[2]
	if done.Status != "completed" || len(done.ToolContent) != 2 || done.ToolContent[1].Type != "diff" || done.ToolContent[1].NewText != "hi\n" || done.ToolContent[1].OldText != nil {
		t.Errorf("completed update = %+v", done)
	}
	if string(done.RawOutput) != `{"stdout":"hi\n"}` {
		t.Errorf("rawOutput = %s", done.RawOutput)
	}

	usage := us[4]
	if usage.Used == nil || *usage.Used != 1234 || usage.Size == nil || *usage.Size != 200000 || usage.Cost == nil || usage.Cost.Currency != "USD" {
		t.Errorf("usage_update = %+v", usage)
	}
}

func TestPermissionRequestsAnsweredAllowRejectAndCancelled(t *testing.T) {
	for _, tc := range []struct {
		name   string
		answer string
		report string
		status string
	}{
		{"allow", "allow-1", "outcome=selected option=allow-1", "completed"},
		{"allow always", "allow-all", "outcome=selected option=allow-all", "completed"},
		{"reject", "reject-1", "outcome=selected option=reject-1", "failed"},
		{"cancelled", "", "outcome=cancelled option=", "failed"},
		{"an option the agent did not offer", "made-up", "outcome=cancelled option=", "failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRecorder()
			r.perm = func(context.Context, PermissionRequest) string { return tc.answer }
			c, sid := start(t, r)
			prompt(t, c, sid, "perm")

			if got := r.text(); got != tc.report {
				t.Errorf("the agent saw %q, want %q", got, tc.report)
			}
			var last string
			for _, u := range r.all() {
				if u.Kind == UpdateToolCallUpdate {
					last = u.Status
				}
			}
			if last != tc.status {
				t.Errorf("tool status after the answer = %q, want %q", last, tc.status)
			}
			if len(r.perms) != 1 {
				t.Fatalf("permission requests = %d", len(r.perms))
			}
			req := r.perms[0]
			if req.SessionID != sid || req.ToolCall.Title != "Write notes.txt" || req.ToolCall.Kind != "edit" || len(req.Options) != 3 || req.Options[1].OptionID != "allow-all" || req.Options[1].Kind != "allow_always" {
				t.Errorf("request = %+v", req)
			}
		})
	}
}

func TestCancelStopsACommandInTheMiddle(t *testing.T) {
	r := newRecorder()
	c, sid := start(t, r)

	stop := make(chan string, 1)
	go func() {
		s, err := c.Prompt(ctxT(t), sid, "slow")
		if err != nil {
			t.Errorf("prompt: %v", err)
		}
		stop <- s
	}()
	r.waitFor(t, "the first chunk", func(us []Update) bool { return len(us) > 0 })

	started := time.Now()
	if err := c.Cancel(sid); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	select {
	case s := <-stop:
		if s != StopCancelled {
			t.Errorf("stop reason = %q, want %q", s, StopCancelled)
		}
		if took := time.Since(started); took > 5*time.Second {
			t.Errorf("cancel took %v", took)
		}
	case <-time.After(wait):
		t.Fatal("the turn did not end after cancel")
	}
}

func TestAPlanUpdate(t *testing.T) {
	r := newRecorder()
	c, sid := start(t, r)
	prompt(t, c, sid, "plan")

	us := r.all()
	if len(us) != 1 || us[0].Kind != UpdatePlan || len(us[0].Plan) != 3 {
		t.Fatalf("updates = %+v", us)
	}
	if e := us[0].Plan[0]; e.Content != "Edit the file" || e.Priority != "high" || e.Status != "in_progress" {
		t.Errorf("entry 0 = %+v", e)
	}
	// Values outside the schema are kept as sent.
	if e := us[0].Plan[2]; e.Priority != "urgent" || e.Status != "paused" {
		t.Errorf("entry 2 = %+v", e)
	}
}

// TestALatePermissionAnswerAfterCancelIsDropped covers a person answering
// after the turn was cancelled. The request was already answered cancelled, as
// the protocol requires, and the agent must not get a second response.
func TestALatePermissionAnswerAfterCancelIsDropped(t *testing.T) {
	r := newRecorder()
	asked := make(chan struct{})
	release := make(chan struct{})
	returned := make(chan struct{})
	ctxDone := make(chan struct{})
	r.perm = func(ctx context.Context, req PermissionRequest) string {
		close(asked)
		select {
		case <-ctx.Done():
			close(ctxDone)
		case <-time.After(wait):
		}
		<-release
		defer close(returned)
		return "allow-1"
	}
	c, sid := start(t, r)

	stop := make(chan string, 1)
	go func() {
		s, _ := c.Prompt(ctxT(t), sid, "slow-perm")
		stop <- s
	}()
	select {
	case <-asked:
	case <-time.After(wait):
		t.Fatal("no permission request")
	}
	if err := c.Cancel(sid); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	select {
	case <-ctxDone:
	case <-time.After(wait):
		t.Fatal("the handler's context was not cancelled by Cancel")
	}
	if s := <-stop; s != StopCancelled {
		t.Errorf("stop reason = %q", s)
	}

	close(release) // the person answers now, too late
	<-returned
	time.Sleep(200 * time.Millisecond)

	r.reset()
	prompt(t, c, sid, "report")
	if got := r.text(); got != "responses: 2001=1" {
		t.Errorf("the agent saw %q, want exactly one response to its request", got)
	}
}

func TestAnAuthFailureAtSessionNew(t *testing.T) {
	r := newRecorder()
	c := startRaw(t, r, nil, acptest.WithAuth("new"))
	init, err := c.Initialize(ctxT(t))
	if err != nil || len(init.AuthMethods) != 2 || init.AuthMethods[0].ID != "api-key" || init.AuthMethods[1].Name != "Log in" {
		t.Fatalf("initialize = %+v, %v", init, err)
	}

	_, err = c.NewSession(ctxT(t), acptest.SessionDir(t), nil)
	var auth *AuthRequiredError
	if !errors.As(err, &auth) || !IsAuthRequired(err) {
		t.Fatalf("session/new error = %v, want an AuthRequiredError", err)
	}
	if auth.Method != "session/new" || auth.Message != "Authentication required" || len(auth.Methods) != 2 {
		t.Errorf("auth error = %+v", auth)
	}
	var rpc *RPCError
	if !errors.As(err, &rpc) || rpc.Code != -32000 {
		t.Errorf("the RPCError is not reachable: %v", err)
	}

	if err := c.Authenticate(ctxT(t), auth.Methods[0].ID); err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if _, err := c.NewSession(ctxT(t), acptest.SessionDir(t), nil); err != nil {
		t.Fatalf("session/new after authenticate: %v", err)
	}
}

func TestAnAuthFailureAtPrompt(t *testing.T) {
	r := newRecorder()
	c := startRaw(t, r, nil, acptest.WithAuth("prompt"))
	if _, err := c.Initialize(ctxT(t)); err != nil {
		t.Fatal(err)
	}
	sid, err := c.NewSession(ctxT(t), acptest.SessionDir(t), nil)
	if err != nil {
		t.Fatalf("session/new: %v", err)
	}
	_, err = c.Prompt(ctxT(t), sid, "plain")
	var auth *AuthRequiredError
	if !errors.As(err, &auth) || auth.Method != "session/prompt" {
		t.Fatalf("prompt error = %v, want an AuthRequiredError at session/prompt", err)
	}
	if err := c.Authenticate(ctxT(t), "login"); err != nil {
		t.Fatal(err)
	}
	if stop := prompt(t, c, sid, "plain"); stop != StopEndTurn {
		t.Errorf("stop reason = %q", stop)
	}
}

// TestUnknownUpdatesAreKeptNotDropped covers update kinds, content block types
// and fields this client does not know.
func TestUnknownUpdatesAreKeptNotDropped(t *testing.T) {
	r := newRecorder()
	c, sid := start(t, r)
	if stop := prompt(t, c, sid, "unknown"); stop != StopEndTurn {
		t.Fatalf("stop reason = %q", stop)
	}
	us := r.all()
	if len(us) != 6 {
		t.Fatalf("got %d updates, want all 6", len(us))
	}

	if u := us[0]; u.Kind != "future_kind" || !strings.Contains(string(u.Raw), `"nested":{"a":[1,2]}`) || u.Content != nil {
		t.Errorf("unknown kind: %+v", u)
	}
	if u := us[1]; u.Kind != UpdateAgentMessageChunk || u.Content == nil || u.Content.Type != "hologram" ||
		!strings.Contains(string(u.Content.Raw), `"n":7`) || u.Content.Text != "" {
		t.Errorf("unknown content block type: %+v", u.Content)
	}
	if u := us[2]; u.ToolKind != "teleport" || u.ToolCallID != "call-9" || !strings.Contains(string(u.Raw), `"zzz":true`) {
		t.Errorf("unknown tool kind: %+v", u)
	}
	if u := us[3]; len(u.ToolContent) != 2 || u.ToolContent[0].Type != "hologram" || !strings.Contains(string(u.ToolContent[0].Raw), `"x":1`) ||
		u.ToolContent[1].Content == nil || u.ToolContent[1].Content.Text != "ok" {
		t.Errorf("unknown tool content: %+v", u.ToolContent)
	}
	if u := us[4]; u.Content == nil || u.Content.Text != "after unknown" {
		t.Errorf("an unknown field on a known kind: %+v", u)
	}
	// A known kind of the wrong shape is delivered with its bytes, not lost.
	if u := us[5]; u.Kind != UpdateAgentMessageChunk || u.Content == nil || u.Content.Type != "" || string(u.Content.Raw) != `"not an object"` {
		t.Errorf("a known kind of the wrong shape: %+v content=%+v", u, u.Content)
	}
}

func TestAVeryLargeToolResult(t *testing.T) {
	r := newRecorder()
	c, sid := start(t, r)
	prompt(t, c, sid, "big")
	us := r.all()
	if len(us) != 2 || len(us[0].ToolContent) != 1 || len(us[0].ToolContent[0].Content.Text) != 5<<20 {
		t.Fatalf("the large update was not delivered whole: %d updates", len(us))
	}
	if r.text() != "after big" {
		t.Errorf("text after the large update = %q", r.text())
	}
}

// TestRequestIDsComeBackAsSent covers an agent that uses string ids.
func TestRequestIDsComeBackAsSent(t *testing.T) {
	r := newRecorder()
	r.perm = func(context.Context, PermissionRequest) string { return "allow-1" }
	c, sid := start(t, r)

	prompt(t, c, sid, "perm-str")
	if got := r.text(); got != `outcome=selected option=allow-1id-echo="perm-abc"` {
		t.Errorf("the agent saw %q", got)
	}
	if r.perms[0].RequestID != `"perm-abc"` {
		t.Errorf("RequestID = %s", r.perms[0].RequestID)
	}

	prompt(t, c, sid, "perm")
	if id := r.perms[1].RequestID; id != "1002" {
		t.Errorf("a numeric id arrived as %s, want 1002", id)
	}
}

// TestCloseAnswersAnOpenPermissionRequestCancelled covers quitting, or closing
// a pane, with a request waiting on a person.
func TestCloseAnswersAnOpenPermissionRequestCancelled(t *testing.T) {
	marker := filepath.Join(acptest.SessionDir(t), "outcome.txt")
	r := newRecorder()
	asked := make(chan struct{})
	ctxDone := make(chan struct{})
	r.perm = func(ctx context.Context, req PermissionRequest) string {
		close(asked)
		<-ctx.Done()
		close(ctxDone)
		return "allow-1" // too late: the request was answered cancelled
	}
	c, sid := start(t, r, acptest.WithMarker(marker))

	prompted := make(chan error, 1)
	go func() {
		_, err := c.Prompt(context.Background(), sid, "hold-perm")
		prompted <- err
	}()
	select {
	case <-asked:
	case <-time.After(wait):
		t.Fatal("no permission request")
	}

	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	select {
	case <-ctxDone:
	case <-time.After(wait):
		t.Fatal("the handler's context was not cancelled by Close")
	}
	select {
	case <-prompted:
	case <-time.After(wait):
		t.Fatal("a prompt in flight was left waiting by Close")
	}
	raw, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("the agent never recorded an answer: %v", err)
	}
	if got := strings.TrimSpace(string(raw)); got != "cancelled" {
		t.Errorf("the agent was answered %q, want cancelled", got)
	}
}

func TestUpdatesArriveInWireOrder(t *testing.T) {
	r := newRecorder()
	c, sid := start(t, r)
	prompt(t, c, sid, "order")
	us := r.all()
	// The prompt's response follows the last update on the wire, so every
	// update has been handled by the time Prompt returns.
	if len(us) != 200 {
		t.Fatalf("got %d updates", len(us))
	}
	for i, u := range us {
		if u.Content == nil || u.Content.Text != strconv.Itoa(i) {
			t.Fatalf("update %d is %+v", i, u.Content)
		}
	}
}

func TestStderrIsDrained(t *testing.T) {
	r := newRecorder()
	var mu sync.Mutex
	lines := 0
	c := startRaw(t, r, func(string) { mu.Lock(); lines++; mu.Unlock() })
	if _, err := c.Initialize(ctxT(t)); err != nil {
		t.Fatal(err)
	}
	sid, err := c.NewSession(ctxT(t), acptest.SessionDir(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	// 1 MiB on stderr: an undrained pipe would hold the agent and the turn.
	prompt(t, c, sid, "stderr")
	deadline := time.Now().Add(wait)
	for {
		mu.Lock()
		n := lines
		mu.Unlock()
		if n >= 1024 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d stderr lines arrived", n)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if tail := c.StderrTail(); len(tail) == 0 || len(tail) > 4096 {
		t.Errorf("stderr tail is %d bytes", len(tail))
	}
}

func TestTheAgentExitingFailsTheCallWaiting(t *testing.T) {
	r := newRecorder()
	c, sid := start(t, r)
	_, err := c.Prompt(ctxT(t), sid, "exit")
	var exit *ExitError
	if !errors.As(err, &exit) || !strings.Contains(exit.Stderr, "exiting on request") {
		t.Fatalf("prompt error = %v, want an ExitError carrying stderr", err)
	}
	select {
	case <-c.Done():
	case <-time.After(wait):
		t.Fatal("Done was not closed")
	}
}

func TestStartAndSessionRefuseRelativePaths(t *testing.T) {
	r := newRecorder()
	if _, err := Start(Config{Path: "fakeagent.exe", Handler: r}); err == nil {
		t.Error("a relative agent path was accepted")
	}
	if _, err := Start(Config{Path: filepath.Join(t.TempDir(), "x")}); err == nil {
		t.Error("a missing handler was accepted")
	}
	c, _ := start(t, r)
	if _, err := c.NewSession(ctxT(t), "relative/dir", nil); err == nil {
		t.Error("a relative cwd was accepted")
	}
}

func TestCloseIsSafeTwiceAndCallsAfterItFail(t *testing.T) {
	r := newRecorder()
	c, sid := start(t, r)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Prompt(ctxT(t), sid, "plain"); !errors.Is(err, ErrClosed) {
		t.Errorf("prompt after close: %v, want ErrClosed", err)
	}
}

func TestMCPServersAreArraysNeverNull(t *testing.T) {
	b, err := json.Marshal(MCPServer{Name: "n", Command: "c"})
	if err != nil || string(b) != `{"name":"n","command":"c","args":[],"env":[]}` {
		t.Errorf("MCPServer = %s, %v", b, err)
	}
}
