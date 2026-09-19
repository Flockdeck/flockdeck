package hooks

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// recv collects events delivered to a server.
type recv struct {
	ch chan Event
}

func newServer(t *testing.T) (*Server, *recv) {
	t.Helper()
	r := &recv{ch: make(chan Event, 8)}
	srv, err := Serve(func(e Event) { r.ch <- e })
	if err != nil {
		t.Fatalf("serve: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	return srv, r
}

func (r *recv) next(t *testing.T) Event {
	t.Helper()
	select {
	case e := <-r.ch:
		return e
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for a hook event")
		return Event{}
	}
}

// TestEmitDeliversEvent covers the whole transport: the subprocess half parses
// Claude's stdin payload and the server half receives the event.
func TestEmitDeliversEvent(t *testing.T) {
	srv, r := newServer(t)

	stdin := strings.NewReader(`{"session_id":"claude-side-id","tool_name":"Bash","cwd":"/repo"}`)
	if _, err := Emit(stdin, srv.Endpoint(), srv.Token(), "pane-1", "PreToolUse"); err != nil {
		t.Fatalf("emit: %v", err)
	}

	got := r.next(t)
	if got.SessionID != "pane-1" {
		t.Errorf("session = %q, want pane-1", got.SessionID)
	}
	if got.Event != "PreToolUse" {
		t.Errorf("event = %q, want PreToolUse", got.Event)
	}
	// The tool name must come from Claude's stdin payload, since that is the
	// only place it appears.
	if got.Tool != "Bash" {
		t.Errorf("tool = %q, want Bash", got.Tool)
	}
	if got.Cwd != "/repo" {
		t.Errorf("cwd = %q, want /repo", got.Cwd)
	}
}

// TestEmitCarriesTheAgentsOwnConversationID covers /clear. Claude Code carries
// on under a new conversation id and reports the SessionStart that follows
// under it, while the pane keeps the id it was started with -- which now names
// the conversation before the clear. Passing Claude's id on is the only way the
// application can learn that the conversation to resume has changed.
func TestEmitCarriesTheAgentsOwnConversationID(t *testing.T) {
	srv, r := newServer(t)

	stdin := strings.NewReader(`{"session_id":"after-the-clear","source":"clear"}`)
	if _, err := Emit(stdin, srv.Endpoint(), srv.Token(), "pane-1", SessionStart); err != nil {
		t.Fatalf("emit: %v", err)
	}
	got := r.next(t)
	if got.SessionID != "pane-1" {
		t.Errorf("session = %q, want the pane's own id", got.SessionID)
	}
	if got.Conversation != "after-the-clear" || got.Source != "clear" {
		t.Errorf("conversation = %q, source = %q; want Claude's new id and \"clear\"", got.Conversation, got.Source)
	}
}

// TestEmitWithoutStdin covers events that carry no payload.
func TestEmitWithoutStdin(t *testing.T) {
	srv, r := newServer(t)

	if _, err := Emit(nil, srv.Endpoint(), srv.Token(), "pane-2", "Stop"); err != nil {
		t.Fatalf("emit: %v", err)
	}
	if got := r.next(t); got.Event != "Stop" || got.SessionID != "pane-2" {
		t.Errorf("got %+v, want Stop for pane-2", got)
	}
}

// TestEmitSaysWhichStartOfThePaneItIsFrom covers a pane restarted while a hook
// of the process before it was still on its way. The pane keeps its id, so the
// start its environment names is what lets the application drop the old one's.
func TestEmitSaysWhichStartOfThePaneItIsFrom(t *testing.T) {
	srv, r := newServer(t)
	t.Setenv(LaunchEnv, "second-start")

	if _, err := Emit(strings.NewReader(`{}`), srv.Endpoint(), srv.Token(), "pane-3", "Notification"); err != nil {
		t.Fatalf("emit: %v", err)
	}
	if got := r.next(t); got.Launch != "second-start" {
		t.Errorf("launch = %q, want the start the pane's environment names", got.Launch)
	}
}

// TestBadTokenRejected checks that the loopback port cannot be driven by other
// local processes, and that a hook being turned away says so. A refusal that
// reports nothing is indistinguishable from a hook that never ran, which is a
// pane whose status quietly stops changing.
func TestBadTokenRejected(t *testing.T) {
	srv, r := newServer(t)

	_, err := Emit(nil, srv.Endpoint(), "not-the-token", "pane-3", "Stop")
	if err == nil {
		t.Error("a rejected hook reported success")
	}
	select {
	case e := <-r.ch:
		t.Fatalf("event with a bad token was accepted: %+v", e)
	case <-time.After(300 * time.Millisecond):
	}
}

// TestEmitToDeadServerIsNotFatal makes sure a hook can never wedge the Claude
// session it reports on.
func TestEmitToDeadServerIsNotFatal(t *testing.T) {
	srv, _ := newServer(t)
	endpoint, token := srv.Endpoint(), srv.Token()
	_ = srv.Close()

	done := make(chan error, 1)
	go func() {
		_, err := Emit(nil, endpoint, token, "pane-4", "Stop")
		done <- err
	}()

	select {
	case <-done: // an error is fine; hanging is not
	case <-time.After(5 * time.Second):
		t.Fatal("Emit blocked against a dead server")
	}
}

// TestSessionStartReturnsContext covers the reply half of the transport: the
// SessionStart hook gets back the description of the pane it belongs to, which
// is what the subprocess prints for Claude to read.
func TestSessionStartReturnsContext(t *testing.T) {
	srv, r := newServer(t)
	srv.SetContextHandler(func(id string) string { return "you are pane " + id })

	res, err := Emit(nil, srv.Endpoint(), srv.Token(), "pane-5", SessionStart)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	if res.Context != "you are pane pane-5" {
		t.Errorf("context = %q, want the handler's answer", res.Context)
	}
	if got := r.next(t); got.Event != SessionStart {
		t.Errorf("event = %q, want SessionStart", got.Event)
	}
}

// TestOtherEventsReturnNoContext keeps the per-tool-call events cheap: only
// SessionStart is worth a trip through the goroutine that owns the workspace.
func TestOtherEventsReturnNoContext(t *testing.T) {
	srv, r := newServer(t)
	called := make(chan string, 4)
	srv.SetContextHandler(func(id string) string {
		called <- id
		return "should not be asked for"
	})

	res, err := Emit(nil, srv.Endpoint(), srv.Token(), "pane-6", "Stop")
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	if res.Context != "" {
		t.Errorf("context = %q, want empty for a Stop hook", res.Context)
	}
	r.next(t)
	select {
	case id := <-called:
		t.Errorf("context handler was called for %q on a Stop hook", id)
	default:
	}
}

// TestSessionStartSourceIsRead checks that the reason a session started is
// carried through, since a compaction reports as a start of its own.
func TestSessionStartSourceIsRead(t *testing.T) {
	srv, r := newServer(t)

	stdin := strings.NewReader(`{"session_id":"x","source":"compact"}`)
	if _, err := Emit(stdin, srv.Endpoint(), srv.Token(), "pane-7", SessionStart); err != nil {
		t.Fatalf("emit: %v", err)
	}
	if got := r.next(t); got.Source != "compact" {
		t.Errorf("source = %q, want compact", got.Source)
	}
}

// TestSpawnReportsRefusal covers the path a lead agent hits when the
// application will not start a helper: the reason has to survive back to Flockdeck.
// `spawn` subcommand, since it is the only thing the agent gets to read.
func TestSpawnReportsRefusal(t *testing.T) {
	srv, _ := newServer(t)
	srv.SetSpawnHandler(func(SpawnRequest) (SpawnResult, error) {
		return SpawnResult{}, errors.New("no such branch")
	})

	_, err := Spawn(srv.BaseURL(), srv.Token(), "pane-1", SpawnRequest{Task: "do a thing"})
	if err == nil || !strings.Contains(err.Error(), "no such branch") {
		t.Fatalf("err = %v, want the handler's reason", err)
	}
}

// TestSpawnWithoutHandlerExplainsItself checks the refusal that carries no
// body of its own still prints as something a reader can act on.
func TestSpawnWithoutHandlerExplainsItself(t *testing.T) {
	srv, _ := newServer(t)

	_, err := Spawn(srv.BaseURL(), srv.Token(), "pane-1", SpawnRequest{Task: "do a thing"})
	if err == nil || strings.TrimSpace(err.Error()) == "" {
		t.Fatalf("err = %v, want a message", err)
	}
}

// TestSpawnRejectsBadToken keeps the spawn endpoint as closed to other local
// processes as the hook endpoint is.
func TestSpawnRejectsBadToken(t *testing.T) {
	srv, _ := newServer(t)
	called := make(chan struct{}, 1)
	srv.SetSpawnHandler(func(SpawnRequest) (SpawnResult, error) {
		called <- struct{}{}
		return SpawnResult{PaneID: "pane-x"}, nil
	})

	if _, err := Spawn(srv.BaseURL(), "not-the-token", "pane-1", SpawnRequest{Task: "x"}); err == nil {
		t.Fatal("a spawn with the wrong token was accepted")
	}
	select {
	case <-called:
		t.Fatal("the spawn handler ran for an unauthenticated request")
	default:
	}
}

// TestSpawnReturnsPaneID is the happy path: the id comes back so the caller
// can tell the user which pane it started.
func TestSpawnReturnsPaneID(t *testing.T) {
	srv, _ := newServer(t)
	var got SpawnRequest
	srv.SetSpawnHandler(func(req SpawnRequest) (SpawnResult, error) {
		got = req
		return SpawnResult{PaneID: "pane-9", Cwd: `C:\repo-fix-auth`}, nil
	})

	res, err := Spawn(srv.BaseURL(), srv.Token(), "parent-pane", SpawnRequest{Task: "review the docs", Split: true})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if res.PaneID != "pane-9" {
		t.Errorf("id = %q, want pane-9", res.PaneID)
	}
	// The directory has to survive the round trip: with --worktree it is the
	// one thing the caller could not have worked out for itself.
	if res.Cwd != `C:\repo-fix-auth` {
		t.Errorf("cwd = %q, want the directory the handler named", res.Cwd)
	}
	if got.Parent != "parent-pane" || got.Task != "review the docs" || !got.Split {
		t.Errorf("handler saw %+v, want the request as sent", got)
	}
}

// TestPeerNameRoundTrip is the happy path: the pane and the name it reported
// reach the installed handler exactly as sent.
func TestPeerNameRoundTrip(t *testing.T) {
	srv, _ := newServer(t)
	var got PeerNameRequest
	srv.SetPeerNameHandler(func(req PeerNameRequest) error {
		got = req
		return nil
	})

	if err := PeerName(srv.BaseURL(), srv.Token(), "pane-1", "flockdeck-8d"); err != nil {
		t.Fatalf("peer name: %v", err)
	}
	if got.Pane != "pane-1" || got.Name != "flockdeck-8d" {
		t.Errorf("handler saw %+v, want the request as sent", got)
	}
}

// TestPeerNameReportsRefusal checks that a handler's reason for refusing --
// an unknown pane, most likely -- reaches the caller.
func TestPeerNameReportsRefusal(t *testing.T) {
	srv, _ := newServer(t)
	srv.SetPeerNameHandler(func(PeerNameRequest) error {
		return errors.New("no such pane")
	})

	err := PeerName(srv.BaseURL(), srv.Token(), "pane-1", "flockdeck-8d")
	if err == nil || !strings.Contains(err.Error(), "no such pane") {
		t.Fatalf("err = %v, want the handler's reason", err)
	}
}

// TestPeerNameWithoutHandlerExplainsItself checks the refusal that carries no
// body of its own still prints as something a reader can act on.
func TestPeerNameWithoutHandlerExplainsItself(t *testing.T) {
	srv, _ := newServer(t)

	err := PeerName(srv.BaseURL(), srv.Token(), "pane-1", "flockdeck-8d")
	if err == nil || strings.TrimSpace(err.Error()) == "" {
		t.Fatalf("err = %v, want a message", err)
	}
}

// TestPeerNameRejectsBadToken keeps this endpoint as closed to other local
// processes as every other one is.
func TestPeerNameRejectsBadToken(t *testing.T) {
	srv, _ := newServer(t)
	called := make(chan struct{}, 1)
	srv.SetPeerNameHandler(func(PeerNameRequest) error {
		called <- struct{}{}
		return nil
	})

	if err := PeerName(srv.BaseURL(), "not-the-token", "pane-1", "flockdeck-8d"); err == nil {
		t.Fatal("a report with the wrong token was accepted")
	}
	select {
	case <-called:
		t.Fatal("the peer-name handler ran for an unauthenticated request")
	default:
	}
}

// TestPeerNameRequiresAName checks the server refuses an empty name rather
// than handing the handler nothing worth keeping.
func TestPeerNameRequiresAName(t *testing.T) {
	srv, _ := newServer(t)
	called := make(chan struct{}, 1)
	srv.SetPeerNameHandler(func(PeerNameRequest) error {
		called <- struct{}{}
		return nil
	})

	if err := PeerName(srv.BaseURL(), srv.Token(), "pane-1", "   "); err == nil {
		t.Fatal("a blank name was accepted")
	}
	select {
	case <-called:
		t.Fatal("the peer-name handler ran for a blank name")
	default:
	}
}

// TestCloseRoundTrip is the happy path: the caller's pane, the target and the
// force flag reach the installed handler exactly as sent, and its result
// comes back.
func TestCloseRoundTrip(t *testing.T) {
	srv, _ := newServer(t)
	var got CloseRequest
	srv.SetCloseHandler(func(req CloseRequest) (CloseResult, error) {
		got = req
		return CloseResult{Closed: true}, nil
	})

	res, err := Close(srv.BaseURL(), srv.Token(), "pane-1", CloseRequest{Target: "pane-2", Force: true})
	if err != nil {
		t.Fatalf("close: %v", err)
	}
	if !res.Closed {
		t.Error("Closed = false, want true")
	}
	if got.Pane != "pane-1" || got.Target != "pane-2" || !got.Force {
		t.Errorf("handler saw %+v, want the request as sent", got)
	}
}

// TestCloseFinishedRoundTrip covers -finished: no target is required, and the
// counts the handler answers with come back.
func TestCloseFinishedRoundTrip(t *testing.T) {
	srv, _ := newServer(t)
	var got CloseRequest
	srv.SetCloseHandler(func(req CloseRequest) (CloseResult, error) {
		got = req
		return CloseResult{Panes: 3, Tabs: 1}, nil
	})

	res, err := Close(srv.BaseURL(), srv.Token(), "pane-1", CloseRequest{Finished: true})
	if err != nil {
		t.Fatalf("close: %v", err)
	}
	if res.Panes != 3 || res.Tabs != 1 {
		t.Errorf("res = %+v, want Panes=3 Tabs=1", res)
	}
	if !got.Finished || got.Target != "" {
		t.Errorf("handler saw %+v, want Finished with no target", got)
	}
}

// TestCloseReportsRefusal checks that a handler's reason for refusing -- a
// pane still working, most likely -- reaches the caller.
func TestCloseReportsRefusal(t *testing.T) {
	srv, _ := newServer(t)
	srv.SetCloseHandler(func(CloseRequest) (CloseResult, error) {
		return CloseResult{}, errors.New("pane is still working")
	})

	_, err := Close(srv.BaseURL(), srv.Token(), "pane-1", CloseRequest{Target: "pane-2"})
	if err == nil || !strings.Contains(err.Error(), "pane is still working") {
		t.Fatalf("err = %v, want the handler's reason", err)
	}
}

// TestCloseWithoutHandlerExplainsItself checks the refusal that carries no
// body of its own still prints as something a reader can act on.
func TestCloseWithoutHandlerExplainsItself(t *testing.T) {
	srv, _ := newServer(t)

	_, err := Close(srv.BaseURL(), srv.Token(), "pane-1", CloseRequest{Target: "pane-2"})
	if err == nil || strings.TrimSpace(err.Error()) == "" {
		t.Fatalf("err = %v, want a message", err)
	}
}

// TestCloseRejectsBadToken keeps this endpoint as closed to other local
// processes as every other one is.
func TestCloseRejectsBadToken(t *testing.T) {
	srv, _ := newServer(t)
	called := make(chan struct{}, 1)
	srv.SetCloseHandler(func(CloseRequest) (CloseResult, error) {
		called <- struct{}{}
		return CloseResult{Closed: true}, nil
	})

	if _, err := Close(srv.BaseURL(), "not-the-token", "pane-1", CloseRequest{Target: "pane-2"}); err == nil {
		t.Fatal("a close with the wrong token was accepted")
	}
	select {
	case <-called:
		t.Fatal("the close handler ran for an unauthenticated request")
	default:
	}
}

// TestCloseRequiresATargetOrFinished checks the server refuses a request that
// names neither a pane nor -finished, rather than handing the handler
// nothing worth acting on.
func TestCloseRequiresATargetOrFinished(t *testing.T) {
	srv, _ := newServer(t)
	called := make(chan struct{}, 1)
	srv.SetCloseHandler(func(CloseRequest) (CloseResult, error) {
		called <- struct{}{}
		return CloseResult{}, nil
	})

	if _, err := Close(srv.BaseURL(), srv.Token(), "pane-1", CloseRequest{}); err == nil {
		t.Fatal("a request naming neither a pane nor -finished was accepted")
	}
	select {
	case <-called:
		t.Fatal("the close handler ran for a request naming nothing to close")
	default:
	}
}

// TestCloseSaysWhatAFailureMeans mirrors TestSpawnSaysWhatAFailureMeans: a
// close that got no answer in time is explained as the instance being busy.
func TestCloseSaysWhatAFailureMeans(t *testing.T) {
	srv, _ := newServer(t)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	srv.SetCloseHandler(func(CloseRequest) (CloseResult, error) {
		<-release
		return CloseResult{Closed: true}, nil
	})
	old := closeTimeout
	closeTimeout = 50 * time.Millisecond
	t.Cleanup(func() { closeTimeout = old })

	_, err := Close(srv.BaseURL(), srv.Token(), "pane-1", CloseRequest{Target: "pane-2"})
	if err == nil || !strings.Contains(err.Error(), "may be busy") {
		t.Errorf("a close that timed out said %v; it should say it may be busy", err)
	}
}

// TestAnInterruptedToolIsReportedAsSuch covers Esc pressed while a tool runs:
// the tool fails, Claude Code goes back to its prompt, and no Stop follows. A
// tool that failed on its own is part of a turn that goes on.
func TestAnInterruptedToolIsReportedAsSuch(t *testing.T) {
	srv, r := newServer(t)
	for _, c := range []struct{ payload, want string }{
		{`{"session_id":"s","tool_name":"Bash","is_interrupt":true}`, Interrupted},
		{`{"session_id":"s","tool_name":"Bash","is_interrupt":false}`, "PostToolUseFailure"},
		{`{"session_id":"s","tool_name":"Bash"}`, "PostToolUseFailure"},
	} {
		if _, err := Emit(strings.NewReader(c.payload), srv.Endpoint(), srv.Token(), "pane-i", "PostToolUseFailure"); err != nil {
			t.Fatalf("emit: %v", err)
		}
		if got := r.next(t); got.Event != c.want || got.Tool != "Bash" {
			t.Errorf("%s: reported as %q (tool %q), want %q", c.payload, got.Event, got.Tool, c.want)
		}
	}
}

// TestFinishedNotificationsAreNotReported covers the Notifications that say
// something is done rather than waiting: every Notification turns a pane
// amber, and a login that succeeded, an MCP question just answered, a
// background agent that has finished or a turn done with the computer is the
// opposite of an agent needing you.
func TestFinishedNotificationsAreNotReported(t *testing.T) {
	srv, r := newServer(t)
	for _, kind := range []string{"auth_success", "elicitation_complete", "elicitation_response", "agent_completed", "computer_use_exit"} {
		stdin := strings.NewReader(`{"session_id":"s","notification_type":"` + kind + `"}`)
		if _, err := Emit(stdin, srv.Endpoint(), srv.Token(), "pane-n", "Notification"); err != nil {
			t.Fatalf("emit %s: %v", kind, err)
		}
	}
	select {
	case e := <-r.ch:
		t.Fatalf("a notification of something finished was reported: %+v", e)
	case <-time.After(200 * time.Millisecond):
	}

	// The ones that do mean the user is wanted still arrive, and so does one
	// from a Claude Code too old to say what it is about.
	for _, kind := range []string{"permission_prompt", "idle_prompt", "elicitation_dialog", "agent_needs_input", "push_notification", ""} {
		stdin := strings.NewReader(`{"session_id":"s","notification_type":"` + kind + `"}`)
		if _, err := Emit(stdin, srv.Endpoint(), srv.Token(), "pane-n", "Notification"); err != nil {
			t.Fatalf("emit %q: %v", kind, err)
		}
		if got := r.next(t); got.Event != "Notification" {
			t.Errorf("%q: event = %q, want Notification", kind, got.Event)
		}
	}
}

// TestEmitCarriesTheNotificationType covers what a real ask needs told apart
// from Claude Code's idle nudge: the notification_type Claude's payload
// carries has to reach the Event this server delivers, since that is the only
// place a helper's idle reminder can be told from a question nobody but a
// person can answer.
func TestEmitCarriesTheNotificationType(t *testing.T) {
	srv, r := newServer(t)
	for _, kind := range []string{"idle_prompt", "permission_prompt", "elicitation_dialog", "agent_needs_input", ""} {
		stdin := strings.NewReader(`{"session_id":"s","notification_type":"` + kind + `"}`)
		if _, err := Emit(stdin, srv.Endpoint(), srv.Token(), "pane-n", "Notification"); err != nil {
			t.Fatalf("emit %q: %v", kind, err)
		}
		if got := r.next(t); got.NotificationType != kind {
			t.Errorf("notification type = %q, want %q", got.NotificationType, kind)
		}
	}
}

// TestSpawnSaysWhatAFailureMeans covers what the agent that ran `flockdeck
// spawn` is told, since it acts on it. A timeout is not a refusal: the helper
// may be on its way, and an agent that reads a bare deadline error asks again.
// A refused connection is the application gone, which asking again cannot fix.
func TestSpawnSaysWhatAFailureMeans(t *testing.T) {
	srv, _ := newServer(t)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	srv.SetSpawnHandler(func(SpawnRequest) (SpawnResult, error) {
		<-release
		return SpawnResult{PaneID: "late"}, nil
	})
	old := spawnTimeout
	spawnTimeout = 50 * time.Millisecond
	t.Cleanup(func() { spawnTimeout = old })

	_, err := Spawn(srv.BaseURL(), srv.Token(), "pane-1", SpawnRequest{Task: "x"})
	if err == nil || !strings.Contains(err.Error(), "may still be starting") {
		t.Errorf("a spawn that timed out said %v; it should say the helper may still be starting", err)
	}

	// An application that has closed is given as what the client reports of
	// it rather than reached for real: whether a closed port refuses at once,
	// resets or hangs until the deadline depends on the machine, and a macOS
	// runner did each of those in turn.
	api := "http://127.0.0.1:1"
	for _, c := range []struct {
		what string
		err  error
		want string
	}{
		{"refused", &url.Error{Op: "Post", URL: api, Err: &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}}, "not answering"},
		{"dropped", &url.Error{Op: "Post", URL: api, Err: &net.OpError{Op: "read", Net: "tcp", Err: errors.New("wsarecv: an existing connection was forcibly closed")}}, "not answering"},
		{"slow", &url.Error{Op: "Post", URL: api, Err: context.DeadlineExceeded}, "may still be starting"},
	} {
		if got := spawnFailure(c.err, api); !strings.Contains(got.Error(), c.want) {
			t.Errorf("a spawn that was %s said %q; want it to say %q", c.what, got, c.want)
		}
	}
}

// A PreToolUse payload carries the whole tool input, so a Write of a generated
// file is megabytes of JSON. Reading a fixed slice of it leaves the payload
// unparseable and loses the tool name, the directory and the prompt together —
// which is the pane's status for that tool call.
func TestEmitReadsALargeToolPayload(t *testing.T) {
	srv, r := newServer(t)

	big := strings.Repeat("x", 4<<20)
	stdin := strings.NewReader(`{"session_id":"s","tool_name":"Write","cwd":"/repo","tool_input":{"content":"` + big + `"}}`)
	if _, err := Emit(stdin, srv.Endpoint(), srv.Token(), "pane-big", "PreToolUse"); err != nil {
		t.Fatalf("emit: %v", err)
	}
	got := r.next(t)
	if got.Tool != "Write" {
		t.Errorf("tool = %q, want Write", got.Tool)
	}
	if got.Cwd != "/repo" {
		t.Errorf("cwd = %q, want /repo", got.Cwd)
	}
}

// A prompt with a pasted file in it is bigger than the body the server will
// read, so forwarding it whole loses the event itself — and with it the change
// of status that says the pane is working.
func TestEmitClipsAHugePrompt(t *testing.T) {
	srv, r := newServer(t)

	prompt := "rewrite this: " + strings.Repeat("y", 2<<20)
	stdin := strings.NewReader(`{"session_id":"s","prompt":"` + prompt + `"}`)
	if _, err := Emit(stdin, srv.Endpoint(), srv.Token(), "pane-prompt", "UserPromptSubmit"); err != nil {
		t.Fatalf("emit: %v", err)
	}
	got := r.next(t)
	if got.Event != "UserPromptSubmit" {
		t.Errorf("event = %q, want UserPromptSubmit", got.Event)
	}
	if len(got.Prompt) > maxPromptBytes {
		t.Errorf("prompt is %d bytes, want at most %d", len(got.Prompt), maxPromptBytes)
	}
	// What survives has to be the start of it: that is what names the tab.
	if !strings.HasPrefix(got.Prompt, "rewrite this: ") {
		t.Errorf("prompt = %.40q…, want it to open on the words the user typed", got.Prompt)
	}
}

// TestEmitCarriesBashToolInput covers what a permission prompt needs to say
// what a Bash call wants to run, straight from PreToolUse rather than waiting
// on anything drawn to the screen.
func TestEmitCarriesBashToolInput(t *testing.T) {
	srv, r := newServer(t)
	stdin := strings.NewReader(`{"session_id":"s","tool_name":"Bash",
		"tool_input":{"command":"echo hi > out.txt","description":"Write hi to out.txt"}}`)
	if _, err := Emit(stdin, srv.Endpoint(), srv.Token(), "pane-bash", "PreToolUse"); err != nil {
		t.Fatalf("emit: %v", err)
	}
	got := r.next(t)
	if got.ToolInput == "" {
		t.Fatal("ToolInput is empty, want the Bash command carried through")
	}
	var out struct {
		Command     string `json:"command"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal([]byte(got.ToolInput), &out); err != nil {
		t.Fatalf("ToolInput is not valid JSON: %v (%s)", err, got.ToolInput)
	}
	if out.Command != "echo hi > out.txt" {
		t.Errorf("command = %q, want the command from tool_input", out.Command)
	}
	if out.Description != "Write hi to out.txt" {
		t.Errorf("description = %q, want the description from tool_input", out.Description)
	}
}

// TestEmitCarriesEditToolInput covers the file and both sides of an edit,
// which is what lets a permission prompt show the same diff a finished Edit
// row would, before the edit has even run.
func TestEmitCarriesEditToolInput(t *testing.T) {
	srv, r := newServer(t)
	stdin := strings.NewReader(`{"session_id":"s","tool_name":"Edit",
		"tool_input":{"file_path":"/repo/push.go","old_string":"a","new_string":"b"}}`)
	if _, err := Emit(stdin, srv.Endpoint(), srv.Token(), "pane-edit", "PreToolUse"); err != nil {
		t.Fatalf("emit: %v", err)
	}
	got := r.next(t)
	var out struct {
		FilePath  string `json:"filePath"`
		OldString string `json:"oldString"`
		NewString string `json:"newString"`
	}
	if err := json.Unmarshal([]byte(got.ToolInput), &out); err != nil {
		t.Fatalf("ToolInput is not valid JSON: %v (%s)", err, got.ToolInput)
	}
	if out.FilePath != "/repo/push.go" || out.OldString != "a" || out.NewString != "b" {
		t.Errorf("got %+v, want the file and both sides of the edit", out)
	}
}

// TestEmitCarriesAskUserQuestionInput covers AskUserQuestion's own shape,
// unmodified, since Claude Code's field names already match what the phone
// needs to draw the question itself.
func TestEmitCarriesAskUserQuestionInput(t *testing.T) {
	srv, r := newServer(t)
	stdin := strings.NewReader(`{"session_id":"s","tool_name":"AskUserQuestion",
		"tool_input":{"questions":[{"question":"Which colour?","header":"Colour","multiSelect":false,
		"options":[{"label":"Red","description":"Warm"},{"label":"Blue","description":"Calm"}]}]}}`)
	if _, err := Emit(stdin, srv.Endpoint(), srv.Token(), "pane-ask", "PreToolUse"); err != nil {
		t.Fatalf("emit: %v", err)
	}
	got := r.next(t)
	var out struct {
		Questions []AskQuestion `json:"questions"`
	}
	if err := json.Unmarshal([]byte(got.ToolInput), &out); err != nil {
		t.Fatalf("ToolInput is not valid JSON: %v (%s)", err, got.ToolInput)
	}
	if len(out.Questions) != 1 {
		t.Fatalf("got %d questions, want 1", len(out.Questions))
	}
	q := out.Questions[0]
	if q.Question != "Which colour?" || q.Header != "Colour" {
		t.Errorf("question = %+v, want the text and header carried through", q)
	}
	if len(q.Options) != 2 || q.Options[0].Label != "Red" || q.Options[0].Description != "Warm" {
		t.Errorf("options = %+v, want both options with their descriptions", q.Options)
	}
}

// TestEmitCapsHugeToolInputContent covers a Write of a generated file: the
// content is capped rather than carried on whole (a permission prompt built
// from it only ever needs a diff-sized preview), and what survives must
// still be valid JSON -- clipping the marshalled bytes instead of the string
// before marshalling would risk cutting a multi-byte rune, or an escape
// sequence, in half.
func TestEmitCapsHugeToolInputContent(t *testing.T) {
	srv, r := newServer(t)
	big := strings.Repeat("x", 4<<20)
	stdin := strings.NewReader(`{"session_id":"s","tool_name":"Write","tool_input":{"file_path":"/repo/out.txt","content":"` + big + `"}}`)
	if _, err := Emit(stdin, srv.Endpoint(), srv.Token(), "pane-write", "PreToolUse"); err != nil {
		t.Fatalf("emit: %v", err)
	}
	got := r.next(t)
	var out struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(got.ToolInput), &out); err != nil {
		t.Fatalf("ToolInput is not valid JSON: %v (%s)", err, got.ToolInput)
	}
	if len(out.Content) > maxToolInputFieldBytes {
		t.Errorf("content is %d bytes, want at most %d", len(out.Content), maxToolInputFieldBytes)
	}
	if len(out.Content) == 0 {
		t.Error("content is empty, want a capped preview of it")
	}
}

// TestEmitToolInputEmptyForAToolWithNothingToAsk covers Grep: none of its
// tool_input is a command, a file being written, or a question, so ToolInput
// should carry nothing forward.
func TestEmitToolInputEmptyForAToolWithNothingToAsk(t *testing.T) {
	srv, r := newServer(t)
	stdin := strings.NewReader(`{"session_id":"s","tool_name":"Grep","tool_input":{"pattern":"TODO"}}`)
	if _, err := Emit(stdin, srv.Endpoint(), srv.Token(), "pane-grep", "PreToolUse"); err != nil {
		t.Fatalf("emit: %v", err)
	}
	got := r.next(t)
	if got.ToolInput != "" {
		t.Errorf("ToolInput = %q, want empty for a tool_input with nothing to ask about", got.ToolInput)
	}
}

// TestReviewHandlerCanAnswerAPreToolUseHookWithAllow covers the one path
// SetReviewHandler exists for: a PreToolUse call the handler allows comes
// back from Emit saying so, with the reason carried along for the agent to
// read.
func TestReviewHandlerCanAnswerAPreToolUseHookWithAllow(t *testing.T) {
	srv, r := newServer(t)
	srv.SetReviewHandler(func(id string, ev Event) (bool, string) {
		if id != "pane-1" || ev.Tool != "Bash" {
			t.Errorf("review handler saw session %q tool %q, want pane-1 Bash", id, ev.Tool)
		}
		return true, "a read-only command"
	})

	stdin := strings.NewReader(`{"session_id":"s","tool_name":"Bash","tool_input":{"command":"git status"}}`)
	res, err := Emit(stdin, srv.Endpoint(), srv.Token(), "pane-1", "PreToolUse")
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	if !res.Allow {
		t.Fatal("Allow = false, want true from a handler that allowed it")
	}
	if res.Reason != "a read-only command" {
		t.Errorf("reason = %q, want the handler's own reason", res.Reason)
	}
	r.next(t) // the event itself still reaches the server
}

// TestReviewHandlerDecliningFallsThroughUnchanged covers the fallback: a
// handler that declines to allow a call is answered exactly as no handler at
// all would be, so the request goes on to Claude Code's own permission
// prompt.
func TestReviewHandlerDecliningFallsThroughUnchanged(t *testing.T) {
	srv, r := newServer(t)
	srv.SetReviewHandler(func(string, Event) (bool, string) { return false, "" })

	stdin := strings.NewReader(`{"session_id":"s","tool_name":"Bash","tool_input":{"command":"rm -rf /"}}`)
	res, err := Emit(stdin, srv.Endpoint(), srv.Token(), "pane-2", "PreToolUse")
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	if res.Allow {
		t.Error("Allow = true, want false from a handler that declined")
	}
	r.next(t)
}

// TestReviewHandlerNotConsultedOutsidePreToolUse checks that a handler
// installed only ever answers PreToolUse -- every other event is answered as
// it always was, whether or not a handler is installed.
func TestReviewHandlerNotConsultedOutsidePreToolUse(t *testing.T) {
	srv, r := newServer(t)
	called := make(chan string, 1)
	srv.SetReviewHandler(func(id string, ev Event) (bool, string) {
		called <- ev.Event
		return true, "should not matter"
	})

	res, err := Emit(nil, srv.Endpoint(), srv.Token(), "pane-3", "Stop")
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	if res.Allow {
		t.Error("Allow = true for a Stop event, want false: only PreToolUse is reviewed")
	}
	r.next(t)
	select {
	case ev := <-called:
		t.Errorf("review handler was consulted for %q", ev)
	default:
	}
}

// clip must not cut a multi-byte character in half: what it produces is put in
// a tab title and sent through JSON.
func TestClipKeepsRunesWhole(t *testing.T) {
	s := strings.Repeat("é", 8) // two bytes each
	for n := 0; n <= len(s); n++ {
		got := clip(s, n)
		if len(got) > n {
			t.Fatalf("clip(%d) is %d bytes", n, len(got))
		}
		if !utf8.ValidString(got) {
			t.Fatalf("clip(%d) = %q, which is not valid UTF-8", n, got)
		}
	}
}
