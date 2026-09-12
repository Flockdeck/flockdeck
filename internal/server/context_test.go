package server

import (
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/hooks"
)

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

	ctx, err := hooks.Emit(nil, hookSrv.Endpoint(), hookSrv.Token(), pane, hooks.SessionStart)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	if !strings.Contains(ctx, "Flockdeck") {
		t.Errorf("context does not describe the application:\n%s", ctx)
	}
	if !strings.Contains(ctx, root) {
		t.Errorf("context does not mention the pane's directory %q:\n%s", root, ctx)
	}
}

// TestSessionStartForAClosedPaneIsHarmless guards the race where a pane's hook
// arrives after the pane has gone: the agent must still start, with nothing
// added to its session.
func TestSessionStartForAClosedPaneIsHarmless(t *testing.T) {
	_, ws := newTestServer(t)

	hookSrv := ws.HookServer()
	ctx, err := hooks.Emit(nil, hookSrv.Endpoint(), hookSrv.Token(), "gone", hooks.SessionStart)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	if ctx != "" {
		t.Errorf("context = %q, want nothing for an unknown pane", ctx)
	}
}
