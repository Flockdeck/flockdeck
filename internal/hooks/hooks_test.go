package hooks

import (
	"errors"
	"strings"
	"testing"
	"time"
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

// TestBadTokenRejected checks that the loopback port cannot be driven by other
// local processes.
func TestBadTokenRejected(t *testing.T) {
	srv, r := newServer(t)

	if _, err := Emit(nil, srv.Endpoint(), "not-the-token", "pane-3", "Stop"); err != nil {
		t.Fatalf("emit returned a transport error: %v", err)
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

	ctx, err := Emit(nil, srv.Endpoint(), srv.Token(), "pane-5", SessionStart)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	if ctx != "you are pane pane-5" {
		t.Errorf("context = %q, want the handler's answer", ctx)
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

	ctx, err := Emit(nil, srv.Endpoint(), srv.Token(), "pane-6", "Stop")
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	if ctx != "" {
		t.Errorf("context = %q, want empty for a Stop hook", ctx)
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
// application will not start a helper: the reason has to survive back to the
// `spawn` subcommand, since it is the only thing the agent gets to read.
func TestSpawnReportsRefusal(t *testing.T) {
	srv, _ := newServer(t)
	srv.SetSpawnHandler(func(SpawnRequest) (string, error) {
		return "", errors.New("no such branch")
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
	srv.SetSpawnHandler(func(SpawnRequest) (string, error) {
		called <- struct{}{}
		return "pane-x", nil
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
	srv.SetSpawnHandler(func(req SpawnRequest) (string, error) {
		got = req
		return "pane-9", nil
	})

	id, err := Spawn(srv.BaseURL(), srv.Token(), "parent-pane", SpawnRequest{Task: "review the docs", Split: true})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if id != "pane-9" {
		t.Errorf("id = %q, want pane-9", id)
	}
	if got.Parent != "parent-pane" || got.Task != "review the docs" || !got.Split {
		t.Errorf("handler saw %+v, want the request as sent", got)
	}
}
