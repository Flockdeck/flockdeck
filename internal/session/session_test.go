package session

import (
	"strings"
	"sync"
	"testing"
	"time"
)

// startShell starts a shell session for testing, skipping if none can run.
func startShell(t *testing.T) *Session {
	t.Helper()
	s, err := Start(Config{
		ID:   "test",
		Kind: KindShell,
		Cwd:  t.TempDir(),
		Argv: ShellArgs(),
		Env:  Env(),
		Cols: 80,
		Rows: 24,
	})
	if err != nil {
		t.Skipf("cannot start a shell in this environment: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// collect drains a subscription into a string until it contains want or the
// deadline passes.
func collect(t *testing.T, out <-chan []byte, seed []byte, want string, d time.Duration) (string, bool) {
	t.Helper()
	var b strings.Builder
	b.Write(seed)
	if strings.Contains(b.String(), want) {
		return b.String(), true
	}
	deadline := time.After(d)
	for {
		select {
		case chunk, ok := <-out:
			if !ok {
				return b.String(), strings.Contains(b.String(), want)
			}
			b.Write(chunk)
			if strings.Contains(b.String(), want) {
				return b.String(), true
			}
		case <-deadline:
			return b.String(), false
		}
	}
}

// TestSessionEchoesInput drives a real PTY end to end: text written to the
// session must come back on a subscription.
func TestSessionEchoesInput(t *testing.T) {
	s := startShell(t)
	id, replay, out := s.Subscribe()
	t.Cleanup(func() { s.Unsubscribe(id) })

	if err := s.WriteString("echo wrapper_marker_ok\r"); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, ok := collect(t, out, replay, "wrapper_marker_ok", 20*time.Second)
	if !ok {
		t.Fatalf("input was not echoed back; saw:\n%s", got)
	}
}

// TestSubscribeReplaysHistory covers what makes a browser reload survivable:
// a viewer that arrives late still receives everything printed so far.
func TestSubscribeReplaysHistory(t *testing.T) {
	s := startShell(t)

	first, replay, out := s.Subscribe()
	if err := s.WriteString("echo replay_marker_ok\r"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, ok := collect(t, out, replay, "replay_marker_ok", 20*time.Second); !ok {
		t.Skip("shell did not echo in time; nothing to replay")
	}
	s.Unsubscribe(first)

	// A brand new viewer, as if the page had been reloaded.
	second, replay2, _ := s.Subscribe()
	t.Cleanup(func() { s.Unsubscribe(second) })
	if !strings.Contains(string(replay2), "replay_marker_ok") {
		t.Errorf("replay buffer did not carry earlier output; got %q", tail(string(replay2), 200))
	}
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// TestSessionConcurrentAccess hammers a session the way the server does: a PTY
// reader fanning output out while viewers come and go and the pane resizes.
func TestSessionConcurrentAccess(t *testing.T) {
	s := startShell(t)

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Viewers subscribing, draining and leaving.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				id, _, out := s.Subscribe()
				select {
				case <-out:
				case <-time.After(10 * time.Millisecond):
				}
				s.Unsubscribe(id)
			}
		}
	}()

	// Resizer.
	wg.Add(1)
	go func() {
		defer wg.Done()
		w := 60
		for {
			select {
			case <-stop:
				return
			default:
				w++
				if w > 100 {
					w = 60
				}
				s.Resize(w, 24)
			}
		}
	}()

	// Input and status.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_ = s.WriteString("x")
				s.SetStatus(StatusWorking, "tool")
				_, _ = s.Status()
			}
		}
	}()

	time.Sleep(1500 * time.Millisecond)
	close(stop)
	wg.Wait()
}

// TestSlowViewerIsDroppedNotBlocking checks that a viewer which stops reading
// cannot stall the process feeding it.
func TestSlowViewerIsDroppedNotBlocking(t *testing.T) {
	s := startShell(t)
	id, _, out := s.Subscribe()
	t.Cleanup(func() { s.Unsubscribe(id) })

	// Never read from out. Produce far more chunks than the queue holds.
	done := make(chan struct{})
	go func() {
		for i := 0; i < subscriberQueue*3; i++ {
			s.publish([]byte("noise"))
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("publishing blocked on a viewer that stopped reading")
	}

	// The abandoned subscription must have been closed rather than left to
	// wedge the session.
	drained := 0
	for range out {
		drained++
		if drained > subscriberQueue*4 {
			t.Fatal("subscription was never closed")
		}
	}
}

// TestStatusForEvent pins the mapping from Claude lifecycle events to statuses.
func TestStatusForEvent(t *testing.T) {
	cases := []struct {
		event string
		tool  string
		want  Status
		ok    bool
	}{
		{"UserPromptSubmit", "", StatusWorking, true},
		{"PreToolUse", "Bash", StatusWorking, true},
		{"Notification", "", StatusWaiting, true},
		{"Stop", "", StatusIdle, true},
		{"SomethingElse", "", StatusIdle, false},
	}
	for _, c := range cases {
		got, detail, ok := StatusForEvent(c.event, c.tool)
		if ok != c.ok {
			t.Errorf("%s: ok = %v, want %v", c.event, ok, c.ok)
			continue
		}
		if ok && got != c.want {
			t.Errorf("%s: status = %v, want %v", c.event, got, c.want)
		}
		if c.event == "PreToolUse" && detail != "Bash" {
			t.Errorf("PreToolUse should surface the tool name, got %q", detail)
		}
	}
}

// TestEnvStripsInheritedSessionMarkers guards the fix that stops panes from
// believing they are nested child sessions.
func TestEnvStripsInheritedSessionMarkers(t *testing.T) {
	t.Setenv("CLAUDE_CODE_CHILD_SESSION", "1")
	t.Setenv("CLAUDECODE", "1")
	t.Setenv("AGENT_WRAPPER_KEEP", "yes")

	var sawStripped, sawKept bool
	for _, kv := range Env() {
		switch {
		case strings.HasPrefix(kv, "CLAUDE_CODE_CHILD_SESSION="), strings.HasPrefix(kv, "CLAUDECODE="):
			sawStripped = true
		case kv == "AGENT_WRAPPER_KEEP=yes":
			sawKept = true
		}
	}
	if sawStripped {
		t.Error("inherited Claude session markers must not be passed to panes")
	}
	if !sawKept {
		t.Error("unrelated environment variables must be preserved")
	}
}

// TestExitDeliversFinalOutputThenCloses covers what a viewer sees when a pane's
// process ends: the last thing it printed, and only then the end of the
// stream. The exit is held back for the reader to drain the PTY, so this also
// pins that the hold is bounded and the pane really does reach "exited".
func TestExitDeliversFinalOutputThenCloses(t *testing.T) {
	s := startShell(t)
	id, replay, out := s.Subscribe()
	t.Cleanup(func() { s.Unsubscribe(id) })

	if err := s.WriteString("echo farewell_marker_ok\rexit\r"); err != nil {
		t.Fatalf("write: %v", err)
	}

	// collect returns when the subscription closes, which is the exit.
	got, ok := collect(t, out, replay, "farewell_marker_ok", 30*time.Second)
	if !ok {
		t.Errorf("the pane's last output never reached the viewer; saw:\n%s", tail(got, 400))
	}

	deadline := time.Now().Add(30 * time.Second)
	for !s.Exited() {
		if time.Now().After(deadline) {
			t.Fatal("session never reported the process as exited")
		}
		time.Sleep(20 * time.Millisecond)
	}
	_, _, after := s.Subscribe()
	if _, open := <-after; open {
		t.Error("a viewer arriving after the exit should get a closed stream")
	}
}

// TestEnvExtrasReplaceInheritedValues covers a pane opened from inside another
// wrapper: the pane identity it inherits must not outrank the one it is given,
// because the first copy of a duplicated name is the one that takes effect.
func TestEnvExtrasReplaceInheritedValues(t *testing.T) {
	t.Setenv("AGENT_WRAPPER_PANE", "the-parent-pane")

	var seen []string
	for _, kv := range Env("AGENT_WRAPPER_PANE=this-pane") {
		if strings.HasPrefix(kv, "AGENT_WRAPPER_PANE=") {
			seen = append(seen, kv)
		}
	}
	if len(seen) != 1 {
		t.Fatalf("AGENT_WRAPPER_PANE appears %d times: %q", len(seen), seen)
	}
	if seen[0] != "AGENT_WRAPPER_PANE=this-pane" {
		t.Errorf("environment says %q; the given value should win", seen[0])
	}
}
