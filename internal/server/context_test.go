package server

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/hooks"
)

// TestSessionStartIsAnsweredWhileTheWorkspaceIsBackedUp covers the promise the
// context handler makes, that a busy workspace cannot stall a pane's start. Its
// deadline began only once the question had been handed over, and handing it
// over waited for as long as the queue in front of the workspace stayed full --
// which is exactly when the workspace is slow. Claude Code's hook gave up
// first, with an error, rather than being answered with nothing in time.
func TestSessionStartIsAnsweredWhileTheWorkspaceIsBackedUp(t *testing.T) {
	srv, ws := newTestServer(t)
	pane := srv.firstPaneID(t)
	hookSrv := ws.HookServer()

	release := make(chan struct{})
	var backlog sync.WaitGroup
	defer backlog.Wait()
	stop := sync.OnceFunc(func() { close(release) })
	defer stop()
	srv.do(func() { <-release })
	for range cap(srv.cmds) * 2 {
		backlog.Add(1)
		go func() { defer backlog.Done(); srv.do(func() {}) }()
	}
	for deadline := time.Now().Add(10 * time.Second); len(srv.cmds) < cap(srv.cmds); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("could not fill the workspace queue: %d of %d", len(srv.cmds), cap(srv.cmds))
		}
	}

	start := time.Now()
	res, err := hooks.Emit(nil, hookSrv.Endpoint(), hookSrv.Token(), pane, hooks.SessionStart)
	if err != nil {
		t.Fatalf("the hook was not answered while the workspace was backed up: %v", err)
	}
	if res.Context != "" {
		t.Errorf("a workspace that could not be asked still described the pane: %q", res.Context)
	}
	if elapsed := time.Since(start); elapsed > contextDeadline+time.Second {
		t.Errorf("the hook was answered after %v, want within the %v the handler allows", elapsed, contextDeadline)
	}
}

// TestSessionStartHookAnswersWithPaneContext covers the whole path a pane's
// agent takes to learn where it is running: the SessionStart hook posts to the
// loopback server, the answer is built on the goroutine that owns the
// workspace, and the description of that pane comes back for Claude to read.
func TestSessionStartHookAnswersWithPaneContext(t *testing.T) {
	srv, ws := newTestServer(t)

	pane := srv.firstPaneID(t)
	root := srv.activeRoot()
	hookSrv := ws.HookServer()
	if hookSrv == nil {
		t.Fatal("the workspace has no hook server")
	}

	res, err := hooks.Emit(nil, hookSrv.Endpoint(), hookSrv.Token(), pane, hooks.SessionStart)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	if !strings.Contains(res.Context, "Flockdeck") {
		t.Errorf("context does not describe the application:\n%s", res.Context)
	}
	if !strings.Contains(res.Context, root) {
		t.Errorf("context does not mention the pane's directory %q:\n%s", root, res.Context)
	}
}

// TestSessionStartForAClosedPaneIsHarmless guards the race where a pane's hook
// arrives after the pane has gone: the agent must still start, with nothing
// added to its session.
func TestSessionStartForAClosedPaneIsHarmless(t *testing.T) {
	_, ws := newTestServer(t)

	hookSrv := ws.HookServer()
	res, err := hooks.Emit(nil, hookSrv.Endpoint(), hookSrv.Token(), "gone", hooks.SessionStart)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	if res.Context != "" {
		t.Errorf("context = %q, want nothing for an unknown pane", res.Context)
	}
}
