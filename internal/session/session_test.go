package session

import (
	"strings"
	"sync"
	"sync/atomic"
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

	if err := s.WriteString("echo perch_marker_ok\r"); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, ok := collect(t, out, replay, "perch_marker_ok", 20*time.Second)
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
	t.Setenv("PERCH_KEEP", "yes")

	var sawStripped, sawKept bool
	for _, kv := range Env() {
		switch {
		case strings.HasPrefix(kv, "CLAUDE_CODE_CHILD_SESSION="), strings.HasPrefix(kv, "CLAUDECODE="):
			sawStripped = true
		case kv == "PERCH_KEEP=yes":
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
// instance: the pane identity it inherits must not outrank the one it is given,
// because the first copy of a duplicated name is the one that takes effect.
func TestEnvExtrasReplaceInheritedValues(t *testing.T) {
	t.Setenv("PERCH_PANE", "the-parent-pane")

	var seen []string
	for _, kv := range Env("PERCH_PANE=this-pane") {
		if strings.HasPrefix(kv, "PERCH_PANE=") {
			seen = append(seen, kv)
		}
	}
	if len(seen) != 1 {
		t.Fatalf("PERCH_PANE appears %d times: %q", len(seen), seen)
	}
	if seen[0] != "PERCH_PANE=this-pane" {
		t.Errorf("environment says %q; the given value should win", seen[0])
	}
}

// TestWriteToAnExitedPaneSaysSo covers typing into a pane whose process has
// gone. The PTY's own error names a closed handle and nothing else, which is
// not something to show anybody.
func TestWriteToAnExitedPaneSaysSo(t *testing.T) {
	s := startShell(t)
	if err := s.WriteString("exit\r"); err != nil {
		t.Fatalf("write: %v", err)
	}

	deadline := time.Now().Add(30 * time.Second)
	for !s.Exited() {
		if time.Now().After(deadline) {
			t.Fatal("session never reported the process as exited")
		}
		time.Sleep(20 * time.Millisecond)
	}

	err := s.WriteString("hello?")
	if err == nil {
		t.Fatal("writing to an exited pane should fail")
	}
	if !strings.Contains(err.Error(), "has exited") {
		t.Errorf("error = %q; it should say the pane has exited", err)
	}
	if !strings.Contains(err.Error(), s.ID) {
		t.Errorf("error = %q; it should name the pane", err)
	}
}

// claudePane builds a Claude session without a process behind it, for the
// parts of the status machinery that only look at the output stream.
func claudePane() *Session {
	return &Session{
		ID:          "claude-pane",
		Kind:        KindClaude,
		status:      StatusIdle,
		statusSince: time.Now(),
		sawInput:    true,
		// Long enough that nothing settles underneath a bell assertion.
		idleAfter: time.Minute,
		history:   newRing(4096),
		subs:      map[int]chan []byte{},
	}
}

// TestRepeatedBellsDoNotRestartTheWaitClock covers the number the tab bar uses
// to say how long an agent has been blocked. Claude rings again each time it
// nudges about the same unanswered question.
func TestRepeatedBellsDoNotRestartTheWaitClock(t *testing.T) {
	s := claudePane()

	s.publish([]byte("please choose\x07"))
	if st, _ := s.Status(); st != StatusWaiting {
		t.Fatalf("status = %v, want waiting", st)
	}
	since := s.StatusSince()

	time.Sleep(10 * time.Millisecond)
	s.publish([]byte("still waiting\x07"))
	if got := s.StatusSince(); !got.Equal(since) {
		t.Errorf("the wait clock restarted on a second bell: %v then %v", since, got)
	}

	// A genuine change of status does start it again.
	time.Sleep(10 * time.Millisecond)
	s.SetStatus(StatusWorking, "")
	if got := s.StatusSince(); !got.After(since) {
		t.Errorf("moving to a different status should restart the clock: %v", got)
	}
}

// TestBellIsIgnoredOnceHooksReport pins the fallback's place: lifecycle hooks
// are the only source of truth once they have reported, because Claude rings
// the bell when a turn merely ends as well as when it needs an answer.
func TestBellIsIgnoredOnceHooksReport(t *testing.T) {
	s := claudePane()
	s.SetStatus(StatusWorking, "Bash")

	s.publish([]byte("done\x07"))
	if st, detail := s.Status(); st != StatusWorking || detail != "Bash" {
		t.Errorf("status = %v %q; a bell must not overrule a hook", st, detail)
	}
}

// TestBellBeforeAnyInputIsNotAttention covers a pane nobody has spoken to yet:
// Claude rings while it starts up, and a pane that announces it needs
// attention before being asked anything is noise.
func TestBellBeforeAnyInputIsNotAttention(t *testing.T) {
	s := claudePane()
	s.sawInput = false

	s.publish([]byte("welcome\x07"))
	if st, _ := s.Status(); st == StatusWaiting {
		t.Error("a startup bell should not mark an untouched pane as waiting")
	}
}

// TestResizeIsClampedAndValidated covers dimensions arriving from a browser,
// which measures them itself and is free to send anything at all.
func TestResizeIsClampedAndValidated(t *testing.T) {
	s := startShell(t)

	s.Resize(120, 40)
	if cols, rows := s.Size(); cols != 120 || rows != 40 {
		t.Fatalf("size = %dx%d, want 120x40", cols, rows)
	}

	// Nonsense is ignored rather than applied.
	s.Resize(0, 40)
	s.Resize(120, -1)
	if cols, rows := s.Size(); cols != 120 || rows != 40 {
		t.Errorf("size = %dx%d after an invalid resize, want 120x40", cols, rows)
	}

	// A figure no display could produce is clamped, not handed to the PTY to
	// allocate a cell for.
	s.Resize(1<<20, 1<<20)
	if cols, rows := s.Size(); cols != maxCols || rows != maxRows {
		t.Errorf("size = %dx%d, want the clamp %dx%d", cols, rows, maxCols, maxRows)
	}
}

// TestStartClampsItsInitialSize covers the size a pane opens at, which comes
// from the same measurement a resize does and is kept in the saved layout, so
// a bad one outlives the session that produced it.
func TestStartClampsItsInitialSize(t *testing.T) {
	s, err := Start(Config{
		ID:   "oversized",
		Kind: KindShell,
		Cwd:  t.TempDir(),
		Argv: ShellArgs(),
		Env:  Env(),
		Cols: 1 << 20,
		Rows: 1 << 20,
	})
	if err != nil {
		t.Skipf("cannot start a shell in this environment: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	if cols, rows := s.Size(); cols != maxCols || rows != maxRows {
		t.Errorf("size = %dx%d, want the clamp %dx%d", cols, rows, maxCols, maxRows)
	}

	// The ordinary defaults are untouched.
	d := startShell(t)
	if cols, rows := d.Size(); cols != 80 || rows != 24 {
		t.Errorf("size = %dx%d, want 80x24", cols, rows)
	}
}

// shellPane builds a shell session with no process behind it and a short quiet
// period, for the status a pane is given when nothing is reporting for it.
func shellPane() *Session {
	return &Session{
		ID:          "shell-pane",
		Kind:        KindShell,
		status:      StatusStarting,
		statusSince: time.Now(),
		idleAfter:   40 * time.Millisecond,
		history:     newRing(4096),
		subs:        map[int]chan []byte{},
	}
}

// waitForStatus waits for a session to reach a status, which the settling
// goroutine arrives at on its own clock.
func waitForStatus(t *testing.T, s *Session, want Status, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for {
		got, _ := s.Status()
		if got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("status = %v after %v, want %v", got, d, want)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// TestOutputMarksAPaneWorkingUntilItGoesQuiet covers a pane no lifecycle hook
// reports for -- every shell pane, and a Claude pane before its first event.
// Without it a build running flat out and a prompt nobody has typed at are the
// same dot in the tab bar.
func TestOutputMarksAPaneWorkingUntilItGoesQuiet(t *testing.T) {
	s := shellPane()
	changed := make(chan struct{}, 32)
	s.OnChange = func() {
		select {
		case changed <- struct{}{}:
		default:
		}
	}

	s.publish([]byte("compiling one.go\n"))
	if st, _ := s.Status(); st != StatusWorking {
		t.Fatalf("status = %v, want working while a pane is printing", st)
	}
	busy := s.StatusSince()

	// Output arriving inside the quiet period keeps the pane working, and does
	// not restart the clock: how long it has been busy is the useful number.
	for i := 0; i < 5; i++ {
		time.Sleep(15 * time.Millisecond)
		s.publish([]byte("compiling.\n"))
		if st, _ := s.Status(); st != StatusWorking {
			t.Fatalf("status = %v part way through a run of output, want working", st)
		}
	}
	if got := s.StatusSince(); !got.Equal(busy) {
		t.Errorf("the busy clock restarted mid-run: %v then %v", busy, got)
	}

	waitForStatus(t, s, StatusIdle, 5*time.Second)

	// The transition has to be announced or nothing redraws the tab bar.
	select {
	case <-changed:
	default:
		t.Error("going idle was never reported")
	}

	// And the pane goes back to working when it starts printing again.
	s.publish([]byte("running tests\n"))
	if st, _ := s.Status(); st != StatusWorking {
		t.Errorf("status = %v after output resumed, want working", st)
	}
}

// TestOutputDoesNotOverruleALifecycleHook keeps the guess out of the way of
// the thing that knows. A finished Claude pane still redraws its prompt, and
// calling that work would show every idle agent as busy.
func TestOutputDoesNotOverruleALifecycleHook(t *testing.T) {
	s := claudePane()
	s.idleAfter = 40 * time.Millisecond
	s.SetStatus(StatusIdle, "")

	s.publish([]byte("\x1b[2K> \n"))
	if st, _ := s.Status(); st != StatusIdle {
		t.Errorf("status = %v; a hook that said idle outranks the output", st)
	}

	s.SetStatus(StatusWaiting, "")
	s.publish([]byte("still asking\n"))
	time.Sleep(100 * time.Millisecond)
	if st, _ := s.Status(); st != StatusWaiting {
		t.Errorf("status = %v; a pane blocked on the user must stay that way", st)
	}
}

// TestOutputDoesNotOverruleTheBell covers the fallback a Claude pane with no
// hooks runs on: the bell says it is blocked, and the bytes around it say only
// that something was printed.
func TestOutputDoesNotOverruleTheBell(t *testing.T) {
	s := claudePane()
	s.idleAfter = 40 * time.Millisecond

	s.publish([]byte("choose one\x07"))
	if st, _ := s.Status(); st != StatusWaiting {
		t.Fatalf("status = %v, want waiting", st)
	}
	s.publish([]byte("(y/n) "))
	time.Sleep(100 * time.Millisecond)
	if st, _ := s.Status(); st != StatusWaiting {
		t.Errorf("status = %v; more output after the bell does not answer it", st)
	}
}

// TestStreamingOutputDoesNotWakeTheInterface covers what a pane printing hard
// costs the rest of the application. Every report rebuilds the workspace
// snapshot, encodes it and pushes it to the browser, and a streaming agent
// produces thousands of chunks a second while saying nothing new about
// itself.
func TestStreamingOutputDoesNotWakeTheInterface(t *testing.T) {
	s := shellPane()
	s.idleAfter = time.Minute
	var wakes int64
	s.OnChange = func() { atomic.AddInt64(&wakes, 1) }

	const chunks = 5000
	for i := 0; i < chunks; i++ {
		s.publish([]byte("a line of build output\n"))
	}

	// One report, for going from starting to working. The rest said nothing.
	if got := atomic.LoadInt64(&wakes); got > 2 {
		t.Errorf("%d chunks of output woke the interface %d times", chunks, got)
	}

	// A real transition still reports, or nothing would ever redraw.
	before := atomic.LoadInt64(&wakes)
	s.SetStatus(StatusWaiting, "")
	if atomic.LoadInt64(&wakes) == before {
		t.Error("a status change must still be reported")
	}
}

// BenchmarkPublish measures the fan-out path a pane's output takes, with a
// viewer attached and the interface listening for changes.
func BenchmarkPublish(b *testing.B) {
	s := shellPane()
	s.idleAfter = time.Minute
	s.OnChange = func() {}
	id, _, out := s.Subscribe()
	defer s.Unsubscribe(id)
	go func() {
		for range out {
		}
	}()

	chunk := []byte(strings.Repeat("x", 4096))
	b.SetBytes(int64(len(chunk)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.publish(chunk)
	}
}

// TestConcurrentResizesLeaveThePaneTheSizeItReports covers two resizes in
// flight at once, which is the ordinary case: the browser measures a pane on
// every layout change, so one arrives on the terminal socket while another
// comes from the layout.
func TestConcurrentResizesLeaveThePaneTheSizeItReports(t *testing.T) {
	f := newFakePTY()
	// The narrower resize takes longer to apply, so an unordered second one
	// overtakes it inside the pseudo-terminal.
	f.resizeDelay = func(cols int) time.Duration {
		if cols == 100 {
			return 150 * time.Millisecond
		}
		return 0
	}
	s := fakeSession(f)
	t.Cleanup(func() { _ = f.Close() })

	done := make(chan struct{})
	go func() {
		defer close(done)
		s.Resize(100, 24)
	}()
	// Long enough for the slow resize to be under way, short enough that it
	// has not finished.
	time.Sleep(30 * time.Millisecond)
	s.Resize(120, 40)
	<-done

	if got, want := f.lastApplied(), [2]int{120, 40}; got != want {
		t.Errorf("the pane was left at %v, want %v", got, want)
	}
	cols, rows := s.Size()
	if applied := f.lastApplied(); applied != [2]int{cols, rows} {
		t.Errorf("session reports %dx%d but the pane is %v", cols, rows, applied)
	}
}

// TestReplayAndViewersDoNotShareTheReadBuffer covers the reader handing its
// own buffer to publish. The buffer is reused on the very next read, so
// anything kept beyond the call -- the replay history, a viewer's queue --
// has to have taken a copy of it.
func TestReplayAndViewersDoNotShareTheReadBuffer(t *testing.T) {
	f := newFakePTY()
	s := fakeSession(f)
	id, _, out := s.Subscribe()
	t.Cleanup(func() { s.Unsubscribe(id) })

	go s.pumpOutput()
	f.feed([]byte("first chunk\n"))
	f.feed([]byte("second chunk\n"))

	var got []string
	for i := 0; i < 2; i++ {
		select {
		case chunk := <-out:
			got = append(got, string(chunk))
		case <-time.After(5 * time.Second):
			t.Fatalf("only %d chunks arrived: %q", len(got), got)
		}
	}
	if got[0] != "first chunk\n" || got[1] != "second chunk\n" {
		t.Errorf("viewer saw %q; the second read overwrote the first", got)
	}

	_, replay, _ := s.Subscribe()
	if want := "first chunk\nsecond chunk\n"; string(replay) != want {
		t.Errorf("replay = %q, want %q", replay, want)
	}
	_ = f.Close()
}

// BenchmarkPumpOutput measures the whole read path, which is where a pane's
// output costs the application something: read, record for replay, fan out.
func BenchmarkPumpOutput(b *testing.B) {
	for _, viewers := range []int{0, 1} {
		name := "no viewers"
		if viewers > 0 {
			name = "one viewer"
		}
		b.Run(name, func(b *testing.B) {
			f := newFakePTY()
			s := fakeSession(f)
			for i := 0; i < viewers; i++ {
				_, _, out := s.Subscribe()
				go func() {
					for range out {
					}
				}()
			}
			chunk := make([]byte, 32<<10)
			go func() {
				for i := 0; i < b.N; i++ {
					f.feed(chunk)
				}
				_ = f.Close()
			}()
			b.SetBytes(int64(len(chunk)))
			b.ResetTimer()
			s.pumpOutput()
		})
	}
}
