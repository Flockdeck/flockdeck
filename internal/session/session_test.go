package session

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/agent"
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

	if err := s.WriteString("echo flockdeck_marker_ok\r"); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, ok := collect(t, out, replay, "flockdeck_marker_ok", 20*time.Second)
	if !ok {
		t.Fatalf("input was not echoed back; saw:\n%s", got)
	}
}

// TestOnChangeIsInstalledBeforeTheReaderStarts covers the callback a pane
// reports on. The reader starts with the process and a pane's first output is
// already a change -- from starting to working -- so a callback that can only
// be assigned once Start has returned races the reader that calls it.
func TestOnChangeIsInstalledBeforeTheReaderStarts(t *testing.T) {
	changed := make(chan struct{}, 1)
	s, err := Start(Config{
		ID:   "on-change",
		Kind: KindShell,
		Cwd:  t.TempDir(),
		Argv: ShellArgs(),
		Env:  Env(),
		OnChange: func() {
			select {
			case changed <- struct{}{}:
			default:
			}
		},
	})
	if err != nil {
		t.Skipf("cannot start a shell in this environment: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	select {
	case <-changed:
	case <-time.After(30 * time.Second):
		t.Fatal("the pane never reported a change on the callback it was started with")
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
		// A question for the user is a tool call, and the pane is waiting on
		// the answer.
		{"PreToolUse", "AskUserQuestion", StatusWaiting, true},
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
		if c.event == "PreToolUse" && detail != c.tool {
			t.Errorf("PreToolUse should surface the tool name %q, got %q", c.tool, detail)
		}
	}
}

// TestEnvStripsInheritedSessionMarkers guards the fix that stops panes from
// believing they are nested child sessions.
func TestEnvStripsInheritedSessionMarkers(t *testing.T) {
	t.Setenv("CLAUDE_CODE_CHILD_SESSION", "1")
	t.Setenv("CLAUDECODE", "1")
	t.Setenv("FLOCKDECK_KEEP", "yes")

	var sawStripped, sawKept bool
	for _, kv := range Env() {
		switch {
		case strings.HasPrefix(kv, "CLAUDE_CODE_CHILD_SESSION="), strings.HasPrefix(kv, "CLAUDECODE="):
			sawStripped = true
		case kv == "FLOCKDECK_KEEP=yes":
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
	t.Setenv("FLOCKDECK_PANE", "the-parent-pane")

	var seen []string
	for _, kv := range Env("FLOCKDECK_PANE=this-pane") {
		if strings.HasPrefix(kv, "FLOCKDECK_PANE=") {
			seen = append(seen, kv)
		}
	}
	if len(seen) != 1 {
		t.Fatalf("FLOCKDECK_PANE appears %d times: %q", len(seen), seen)
	}
	if seen[0] != "FLOCKDECK_PANE=this-pane" {
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
		startedAt:   time.Now(),
		// Long enough that nothing settles underneath a bell assertion.
		idleAfter: time.Minute,
		history:   newRing(4096),
		subs:      map[int]*subscriber{},
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

// TestBellWhileStartingUpIsNotAttention covers a pane that has only just
// opened: Claude rings while it starts up, and a pane that announces it needs
// attention before anything has happened in it is noise.
func TestBellWhileStartingUpIsNotAttention(t *testing.T) {
	s := claudePane()
	s.sawInput = false
	s.startedAt = time.Now()

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
		subs:        map[int]*subscriber{},
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

// TestARepeatedEventIsNotReported covers the lifecycle events that say what a
// pane already said: Claude nudges about the same unanswered question, and
// every report rebuilds and sends the whole workspace.
func TestARepeatedEventIsNotReported(t *testing.T) {
	s := claudePane()
	var wakes int64
	s.OnChange = func() { atomic.AddInt64(&wakes, 1) }

	s.SetStatus(StatusWaiting, "")
	s.SetStatus(StatusWaiting, "")
	s.SetStatus(StatusWaiting, "")
	if got := atomic.LoadInt64(&wakes); got != 1 {
		t.Errorf("three identical events were reported %d times, want once", got)
	}
	s.SetStatus(StatusWorking, "Bash")
	s.SetStatus(StatusWorking, "Edit")
	if got := atomic.LoadInt64(&wakes); got != 3 {
		t.Errorf("a change of status or tool was not reported: %d reports, want 3", got)
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
			b.StopTimer()

			// Without this the benchmark can quietly stop measuring the read
			// path -- a pseudo-terminal that drops what it cannot queue makes
			// it report several terabytes a second and nobody looks twice.
			if got := f.readsDone.Load(); got != int64(b.N) {
				b.Fatalf("the reader saw %d of %d chunks; this is not measuring the read path", got, b.N)
			}
		})
	}
}

// TestExitReleasesThePseudoTerminal covers a pane whose process ends on its
// own and is left on screen showing that it has. Nothing closes the pane, so
// if the exit does not release the pseudo-terminal the reader stays blocked on
// it forever, holding the handle and its buffer.
func TestExitReleasesThePseudoTerminal(t *testing.T) {
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

	select {
	case <-s.pumped:
	case <-time.After(10 * time.Second):
		t.Fatal("the reader is still blocked on the pseudo-terminal after the pane exited")
	}
}

// TestBellReachesAPaneNobodyHasTypedInto covers the two panes that are never
// typed at: an agent spawned with its task on the command line, and a pane
// restored from a saved layout. Both had no bell fallback at all, which is
// what is left when a pane's lifecycle hooks do not report.
func TestBellReachesAPaneNobodyHasTypedInto(t *testing.T) {
	s := claudePane()
	s.sawInput = false
	s.startedAt = time.Now().Add(-time.Minute)

	s.publish([]byte("may I run this?\x07"))
	if st, _ := s.Status(); st != StatusWaiting {
		t.Errorf("status = %v; a pane past its startup should report the bell", st)
	}
}

// TestResizeAfterReleaseDoesNotReachThePTY covers a pane that is closed, or
// whose process exits, while the layout is still measuring it. Both of those
// release the pseudo-terminal, and a resize that arrives afterwards must not
// be handed to it: the handle it would be given has been freed, and what
// follows is not an error return but a crash of the whole application.
func TestResizeAfterReleaseDoesNotReachThePTY(t *testing.T) {
	f := newFakePTY()
	s := fakeSession(f)

	s.Resize(100, 30)
	if got := f.lastApplied(); got != [2]int{100, 30} {
		t.Fatalf("a resize before the close was applied as %v", got)
	}

	if err := s.releasePTY(); err != nil {
		t.Fatalf("release: %v", err)
	}
	s.Resize(120, 40) // must not panic
	if got := f.lastApplied(); got != [2]int{100, 30} {
		t.Errorf("a resize reached the pseudo-terminal after it was released: %v", got)
	}

	// And a release cannot slip in while a resize is inside the PTY, which is
	// the same crash arrived at from the other side.
	g := newFakePTY()
	g.resizeDelay = func(int) time.Duration { return 100 * time.Millisecond }
	s2 := fakeSession(g)
	done := make(chan struct{})
	go func() {
		defer close(done)
		s2.Resize(90, 20)
	}()
	time.Sleep(20 * time.Millisecond)
	if err := s2.releasePTY(); err != nil {
		t.Fatalf("release: %v", err)
	}
	<-done

	// Typing into a pane whose pseudo-terminal has gone is answered by name
	// rather than by whatever the closed handle says.
	err := s2.WriteString("hello?")
	if err == nil || !strings.Contains(err.Error(), "has exited") {
		t.Errorf("write after release = %v; it should name the pane", err)
	}
}

// TestAnsweringAPaneClearsAnInferredWait covers the whole of the bell
// fallback, which is what a pane runs on when its lifecycle hooks are not
// reporting. The bell rings again on the next question, not on this one being
// answered, and the guess made from output leaves "waiting" alone on purpose,
// so typing is the only thing that can end the wait. Without it a pane sits in
// the count of agents needing you from its first question until it exits.
func TestAnsweringAPaneClearsAnInferredWait(t *testing.T) {
	f := newFakePTY()
	t.Cleanup(func() { _ = f.Close() })
	s := fakeSession(f)
	s.Kind = KindClaude
	s.sawInput = true
	s.status = StatusIdle
	s.idleAfter = 40 * time.Millisecond

	s.publish([]byte("may I run this?\x07"))
	if st, _ := s.Status(); st != StatusWaiting {
		t.Fatalf("status = %v, want waiting", st)
	}

	if err := s.WriteString("y\r"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if st, _ := s.Status(); st != StatusWorking {
		t.Errorf("status = %v after answering; the pane is no longer blocked on you", st)
	}
	waitForStatus(t, s, StatusIdle, 5*time.Second)

	// Focusing the pane is not answering it. The terminal reports focus to an
	// application that asked for it, down the same path as typing.
	s.publish([]byte("and this one?\x07"))
	if err := s.WriteString("\x1b[I"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if st, _ := s.Status(); st != StatusWaiting {
		t.Errorf("status = %v after a focus report; nothing was answered", st)
	}
	if err := s.WriteString("\x1b[O\x1b[Iy"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if st, _ := s.Status(); st != StatusWorking {
		t.Errorf("status = %v after typing behind a focus report, want working", st)
	}
	waitForStatus(t, s, StatusIdle, 5*time.Second)

	// A hook that says the pane is waiting knows better than the keyboard
	// does: the next event will move it, and typing something the agent has
	// not acted on yet must not clear the one status worth surfacing.
	s.SetStatus(StatusWaiting, "")
	if err := s.WriteString("hmm"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if st, _ := s.Status(); st != StatusWaiting {
		t.Errorf("status = %v; a hook outranks the keyboard", st)
	}
}

// TestClosingAPaneRacesEverythingElse closes a pane while every other thing
// that can happen to one is happening: the reader publishing, the layout
// resizing, a viewer arriving and leaving, keystrokes, and lifecycle hooks.
// That is not a contrived arrangement -- closing a pane reflows the tab, which
// resizes its neighbours, and the pane being closed is on screen with a viewer
// attached and its agent still printing.
//
// The fake pseudo-terminal refuses to be used after it is released, so
// anything that reaches it late brings the test down rather than passing
// quietly. That is what a real one does on Windows, where the handle points
// into the console host and closing it frees what it points at.
func TestClosingAPaneRacesEverythingElse(t *testing.T) {
	for round := 0; round < 50; round++ {
		f := newFakePTY()
		s := fakeSession(f)
		s.Kind = KindClaude
		s.idleAfter = time.Millisecond
		go s.pumpOutput()

		var wg sync.WaitGroup
		start := make(chan struct{})
		stop := make(chan struct{})
		spawn := func(fn func(i int)) {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				for i := 0; ; i++ {
					select {
					case <-stop:
						return
					default:
						fn(i)
					}
				}
			}()
		}

		spawn(func(i int) { s.Resize(60+i%40, 20+i%10) })
		spawn(func(int) { _ = s.WriteString("keystroke") })
		spawn(func(int) {
			id, _, out := s.Subscribe()
			select {
			case <-out:
			default:
			}
			s.Unsubscribe(id)
		})
		spawn(func(i int) {
			if i%2 == 0 {
				s.SetStatus(StatusWorking, "Bash")
			} else {
				s.SetStatus(StatusWaiting, "")
			}
			_, _ = s.Status()
			_, _ = s.Size()
			_ = s.RecentText(64)
			_ = s.Exited()
		})
		spawn(func(int) { f.feed([]byte("output from the agent\x07\n")) })

		close(start)
		time.Sleep(time.Duration(round%5) * time.Millisecond)
		if err := s.Close(); err != nil {
			t.Fatalf("close: %v", err)
		}
		// Keep everything running for a moment past the close, which is where
		// a late call into a released pseudo-terminal would land.
		time.Sleep(2 * time.Millisecond)
		close(stop)
		wg.Wait()

		if err := s.Close(); err != nil {
			t.Fatalf("closing twice: %v", err)
		}
	}
}

// TestARealPaneReportsBusyThenIdle drives the status inference through a real
// process, which the tests around it deliberately do not: they build sessions
// by hand and set the quiet period themselves, so none of them would notice
// Start forgetting to set it. A pane with no quiet period drops back to idle
// between one line of output and the next, and the tab bar shows a working
// agent as finished.
func TestARealPaneReportsBusyThenIdle(t *testing.T) {
	s := startShell(t)

	// Whatever the shell prints coming up settles first.
	waitForStatus(t, s, StatusIdle, 60*time.Second)

	id, replay, out := s.Subscribe()
	t.Cleanup(func() { s.Unsubscribe(id) })
	if err := s.WriteString("echo busy_marker_ok\r"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, ok := collect(t, out, replay, "busy_marker_ok", 30*time.Second); !ok {
		t.Skip("shell did not echo in time")
	}

	waitForStatus(t, s, StatusWorking, 10*time.Second)

	// The pane holds that for the quiet period rather than dropping back
	// between one line and the next. This is the assertion that fails if the
	// period is left at zero.
	time.Sleep(quietBeforeIdle / 3)
	if st, _ := s.Status(); st != StatusWorking {
		t.Errorf("status = %v a moment after output, want working until the pane is quiet", st)
	}

	waitForStatus(t, s, StatusIdle, 60*time.Second)
}

// TestAStalledViewerIsBoundedInBytes covers a window that has stopped reading
// -- minimised, throttled, or on a machine that is paging -- while its panes
// carry on producing output. Counting chunks puts no bound on what that holds:
// a chunk is whatever one read returned, so five hundred of them is sixteen
// megabytes for one pane, and every pane stalls together.
func TestAStalledViewerIsBoundedInBytes(t *testing.T) {
	s := shellPane()
	s.idleAfter = time.Minute
	id, _, out := s.Subscribe()
	t.Cleanup(func() { s.Unsubscribe(id) })

	// Chunks the size of a whole read, never taken.
	chunk := make([]byte, 32<<10)
	accepted := 0
	for i := 0; i < subscriberQueue; i++ {
		s.publish(chunk)
		s.mu.Lock()
		_, live := s.subs[id]
		s.mu.Unlock()
		if !live {
			break
		}
		accepted += len(chunk)
	}

	s.mu.Lock()
	_, live := s.subs[id]
	s.mu.Unlock()
	if live {
		t.Fatalf("a viewer holding %d bytes was still being fed", accepted)
	}
	if accepted > subscriberBytes+len(chunk) {
		t.Errorf("the viewer was allowed to fall %d bytes behind, want at most %d",
			accepted, subscriberBytes+len(chunk))
	}
	// And it is dropped rather than left to wedge the pane, so the stream ends.
	drained := 0
	for range out {
		drained++
		if drained > subscriberQueue {
			t.Fatal("the subscription was never closed")
		}
	}
}

// TestAViewerKeepingUpIsNotDropped is the other half of it: the bound counts
// what is outstanding, not what has been sent, so a viewer reading as fast as
// the pane produces stays attached however much goes through it.
func TestAViewerKeepingUpIsNotDropped(t *testing.T) {
	s := shellPane()
	s.idleAfter = time.Minute
	id, _, out := s.Subscribe()
	t.Cleanup(func() { s.Unsubscribe(id) })

	chunk := make([]byte, 32<<10)
	total := 0
	for i := 0; i < 400; i++ {
		s.publish(chunk)
		select {
		case c := <-out:
			total += len(c)
		case <-time.After(5 * time.Second):
			t.Fatal("nothing arrived")
		}
	}

	s.mu.Lock()
	_, live := s.subs[id]
	s.mu.Unlock()
	if !live {
		t.Fatal("a viewer that kept up was dropped")
	}
	if want := 400 * len(chunk); total != want {
		t.Errorf("the viewer received %d bytes, want %d", total, want)
	}
}

// TestPatternsSharpenTheFallback is the status path for an agent that reports
// no lifecycle of its own. The bell and the quiet timer can tell that something
// happened; the lines the Spec names can tell what, which is the difference
// between a tab bar that says which pane wants you and one that says all of
// them are busy.
func TestPatternsSharpenTheFallback(t *testing.T) {
	f := newFakePTY()
	t.Cleanup(func() { _ = f.Close() })
	s := fakeSession(f)
	s.Kind = KindAgent
	s.sawInput = true
	s.startedAt = time.Now()
	s.status = StatusIdle
	s.idleAfter = time.Minute
	s.patterns = agent.Patterns{Waiting: []string{"(y/n)"}, Idle: []string{"ready."}}

	// Output nothing recognises is still only output: working, as before.
	s.publish([]byte("writing main.go\n"))
	if st, _ := s.Status(); st != StatusWorking {
		t.Fatalf("status = %v while it was printing, want working", st)
	}

	s.publish([]byte("overwrite main.go? (y/n) "))
	if st, _ := s.Status(); st != StatusWaiting {
		t.Errorf("status = %v at a question, want waiting", st)
	}

	// A bell cannot tell a question from the end of a turn, and this chunk is
	// the end of a turn with a bell on it. The patterns can, so they have the
	// last word: without this the pane would stay in the count of agents
	// needing you until somebody typed into it.
	s.publish([]byte("\nwrote main.go\nReady.\x07"))
	if st, _ := s.Status(); st != StatusIdle {
		t.Errorf("status = %v back at its prompt, want idle", st)
	}

	// A lifecycle event outranks anything read off the screen, for good: the
	// agent knows what it is doing and the screen is a guess at it.
	s.SetStatus(StatusWorking, "Bash")
	s.publish([]byte("\noverwrite config.go? (y/n) "))
	if st, detail := s.Status(); st != StatusWorking || detail != "Bash" {
		t.Errorf("status = %v/%q; a reported lifecycle beats a pattern", st, detail)
	}
}

// TestPatternsAreNotFoldedPerLine covers the cost of the pattern fallback,
// which runs in the reader on every chunk a pane prints. Lower-casing each
// pattern for each line it was compared with allocated dozens of times a chunk.
func TestPatternsAreNotFoldedPerLine(t *testing.T) {
	f := newFakePTY()
	t.Cleanup(func() { _ = f.Close() })
	s := fakeSession(f)
	s.Kind = KindAgent
	s.patterns = foldPatterns(agent.Patterns{
		Waiting: []string{"Do you want to proceed?", "Allow command?", "Approve?"},
		Idle:    []string{"Ready."},
	})
	chunk := []byte(strings.Repeat("\x1b[36mSome Output\x1b[m on a line\r\n", 40))
	allocs := testing.AllocsPerRun(100, func() { s.publish(chunk) })
	if allocs > 20 {
		t.Errorf("publishing a chunk allocated %.0f times; the patterns are being folded per line", allocs)
	}
}

// TestAPaneWithoutPatternsIsUnchanged pins the promise that nothing regresses
// for somebody who only ever runs Claude: Claude reports its own lifecycle and
// names no patterns, so its pane must be read exactly as it was -- from the
// bell, and from how long it has been quiet.
func TestAPaneWithoutPatternsIsUnchanged(t *testing.T) {
	f := newFakePTY()
	t.Cleanup(func() { _ = f.Close() })
	s := fakeSession(f)
	s.Kind = KindClaude
	s.sawInput = true
	s.startedAt = time.Now()
	s.status = StatusIdle
	s.idleAfter = 40 * time.Millisecond

	s.publish([]byte("may I run this?\x07"))
	if st, _ := s.Status(); st != StatusWaiting {
		t.Fatalf("status = %v after the bell, want waiting", st)
	}
	if err := s.WriteString("y\r"); err != nil {
		t.Fatalf("write: %v", err)
	}
	waitForStatus(t, s, StatusIdle, 5*time.Second)
}

// TestStartTakesWhatItNeedsOffTheSpec covers the seam between launching a pane
// and believing anything about it afterwards. The patterns are read on every
// chunk the pane prints, so they are copied once at the start; if they were not
// copied the pane would run with no fallback at all and nothing would say so.
func TestStartTakesWhatItNeedsOffTheSpec(t *testing.T) {
	spec := agent.Spec{
		ID:       "codex",
		Patterns: agent.Patterns{Waiting: []string{"(y/n)"}},
	}
	s, err := Start(Config{
		ID:   "spec-test",
		Kind: KindAgent,
		Spec: spec,
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

	s.mu.RLock()
	got := s.patterns
	s.mu.RUnlock()
	if len(got.Waiting) != 1 || got.Waiting[0] != "(y/n)" {
		t.Errorf("patterns = %+v, want the Spec's own", got)
	}
}
