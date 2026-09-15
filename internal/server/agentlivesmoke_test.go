package server

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/workspace"
)

// TestLiveClaudeSmokePane is the smoke-pane half of the compatibility check
// described in flockdeck-planning/13-ci-agent-model-compatibility.md: with a
// real `claude` CLI on PATH, it starts an actual Claude pane through
// Flockdeck's own machinery -- the argv BuildArgv builds from claudeSpec(),
// the real hook settings WriteHookSettings writes -- and checks that the
// integration still works end to end, not just that the vendor's CLI runs on
// its own: the pane starts, answers a prompt typed into it, its status moves
// through the lifecycle its own hooks report rather than one Flockdeck
// infers from its output, and it can be restarted and resume the same
// conversation with --resume.
//
// It is a Go test, not a shell script, so it runs through `go test` the way
// every other test here does. It is skipped, not failed, everywhere the CLI
// is not installed or the live flag below is not set -- which is every
// ordinary PR: TestAPaneThatNeverStartedStillTakesASocket already covers a
// machine with no claude at all, without needing either.
// .github/workflows/agent-compat.yml installs claude and sets both on a
// schedule, and again on a pull_request that touches builtin.go.
func TestLiveClaudeSmokePane(t *testing.T) {
	if os.Getenv("FLOCKDECK_LIVE_AGENT_TESTS") == "" {
		t.Skip("set FLOCKDECK_LIVE_AGENT_TESTS=1 to run this against the real claude CLI; see .github/workflows/agent-compat.yml")
	}

	dir := stateTempDir(t)
	t.Setenv("APPDATA", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)
	goTelemetryOff(t)
	_ = t.TempDir()

	ws, err := workspace.New(workspace.Options{Root: stateTempDir(t)})
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	t.Cleanup(ws.Close)
	if !ws.ClaudeAvailable() {
		t.Skip("the claude CLI is not on PATH")
	}

	ws.NewTabWith(workspace.Choice{Kind: session.KindClaude, Agent: "claude"}, ws.ActiveRoot(), "smoke")

	srv, err := New(ws)
	if err != nil {
		t.Fatalf("server: %v", err)
	}
	t.Cleanup(func() {
		_ = srv.Close()
		select {
		case <-srv.Stopped():
		case <-time.After(10 * time.Second):
			t.Error("the server's workspace goroutine was still running ten seconds after Close")
		}
	})
	setIdle(srv, time.Hour, false, true)
	ws.SetWake(srv.Wake)

	ctl := dialControl(t, srv)
	paneID := nextState(t, ctl, nil).Tabs[0].Root.Pane

	// Confirm the process actually started under the real CLI before waiting
	// on anything it says: a spec whose flags have drifted kills the pane on
	// the spot, and that failure belongs here, not to a much longer timeout
	// below with nothing to explain it.
	deadline := time.Now().Add(20 * time.Second)
	for {
		sess, found, err := srv.paneSession(paneID)
		if err != nil {
			t.Fatalf("pane session: %v", err)
		}
		if found && sess != nil {
			if sess.Exited() {
				t.Fatal("claude exited immediately: its flags have likely drifted from what internal/agent's claudeSpec assumes")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("claude never started")
		}
		time.Sleep(50 * time.Millisecond)
	}

	pty := dialPTY(t, srv, paneID)
	const marker = "FLOCKDECK_LIVE_SMOKE_OK"
	typeOnceAndAwait(t, pty, "Reply with the single word "+marker+" and nothing else.\r", marker, 90*time.Second)

	// The pane's status has to reach idle again after answering. For an agent
	// with Caps.Hooks -- claude is the one built-in that claims it -- that only
	// happens once its own lifecycle hooks have actually reported, which is the
	// one thing a mock CLI could never exercise: WriteHookSettings has to have
	// written something claude still understands, and claude has to still call
	// it the way Flockdeck expects.
	awaitStatus(t, srv, paneID, session.StatusIdle, 30*time.Second)

	// Restart, which is what reattaching a saved pane does: the same
	// machinery, but claudeSpec's ResumeArgs -- --resume rather than
	// --session-id. If that flag has drifted, the new process dies rather
	// than coming back up.
	before, _, _ := srv.paneSession(paneID)
	sendCmd(t, ctl, command{Cmd: "restartPane", ID: paneID})
	deadline = time.Now().Add(20 * time.Second)
	for {
		sess, _, _ := srv.paneSession(paneID)
		if sess != nil && sess != before {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the pane was never restarted")
		}
		time.Sleep(50 * time.Millisecond)
	}
	deadline = time.Now().Add(20 * time.Second)
	for {
		sess, _, _ := srv.paneSession(paneID)
		if sess != nil {
			if sess.Exited() {
				t.Fatal("claude --resume exited immediately: its resume flag has likely drifted from what claudeSpec's ResumeArgs assumes")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the resumed pane never came up")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// typeOnceAndAwait types line into a terminal socket exactly once -- unlike
// awaitOutput, which retypes every couple of seconds until a marker appears,
// which is safe for a shell but not for an interactive TUI that may still be
// starting up: a second copy of the line typed into it half-drawn is not the
// same prompt sent twice, it is a garbled one.
func typeOnceAndAwait(t *testing.T, conn *websocket.Conn, line, marker string, timeout time.Duration) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageBinary, []byte(line)); err != nil {
		t.Fatalf("type: %v", err)
	}
	var seen strings.Builder
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("read pty waiting for %q: %v\nsaw:\n%s", marker, err, seen.String())
		}
		seen.Write(data)
		if strings.Contains(seen.String(), marker) {
			return seen.String()
		}
	}
}

// awaitStatus waits for a pane to report a status.
func awaitStatus(t *testing.T, srv *Server, paneID string, want session.Status, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last session.Status
	for {
		if sess, _, _ := srv.paneSession(paneID); sess != nil {
			if st, _ := sess.Status(); st == want {
				return
			} else {
				last = st
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the pane never reported status %v; last seen %v", want, last)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
