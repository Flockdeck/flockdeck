package session

import (
	"os"
	"testing"
	"time"
)

// The tests in this file walk docs/status-matrix.md row by row: every
// situation a pane can be in, and the status it should show for it. Each case
// names its row, so a failure here says which line of the matrix no longer
// holds, and a change to the matrix says which case to change with it.
//
// A row whose correct behaviour the code does not have yet is still written
// down here as the behaviour it should have, and skipped through knownBug, so
// that the suite stays green and the fix has its test waiting for it. Set
// FLOCKDECK_STATUS_BUGS=1 to run those as well and watch them fail.

// knownBug skips a case the matrix records as a confirmed bug, unless
// FLOCKDECK_STATUS_BUGS asks for the known bugs to be run.
func knownBug(t *testing.T, row, why string) {
	t.Helper()
	if os.Getenv("FLOCKDECK_STATUS_BUGS") == "" {
		t.Skipf("known bug, docs/status-matrix.md row %s: %s (FLOCKDECK_STATUS_BUGS=1 runs it)", row, why)
	}
}

// hookEv is one lifecycle event as workspace.handleHook hands it to a
// session: the event, the tool it names, a Notification's type, and the
// tool input it carries.
type hookEv struct {
	event, tool, ntype, input string
	// source is a SessionStart's source, or a compaction's trigger.
	source string
}

// feed applies events the way workspace.handleHook does -- through
// ApplyEvent, which runs StatusForEvent, ResolveBlocked and SetStatusFull
// against the turn the event belongs to -- and returns the status and
// detail the pane ends up showing. The background bookkeeping handleHook also
// does is the workspace's to test.
func feed(s *Session, evs ...hookEv) (Status, string) {
	for _, e := range evs {
		s.ApplyEvent(Event{Name: e.event, Tool: e.tool, NotificationType: e.ntype, Source: e.source, ToolInput: e.input})
	}
	return s.Status()
}

// Shorthands for the events the sequences below are made of.
var (
	prompt      = hookEv{event: "UserPromptSubmit"}
	stop        = hookEv{event: "Stop"}
	stopFailure = hookEv{event: "StopFailure"}
	idleNudge   = hookEv{event: "Notification", ntype: "idle_prompt"}
	permAsk     = hookEv{event: "Notification", ntype: "permission_prompt"}
)

func pre(tool string) hookEv         { return hookEv{event: "PreToolUse", tool: tool} }
func post(tool string) hookEv        { return hookEv{event: "PostToolUse", tool: tool} }
func postFail(tool string) hookEv    { return hookEv{event: "PostToolUseFailure", tool: tool} }
func interrupted(tool string) hookEv { return hookEv{event: "Interrupted", tool: tool} }
func permRequest(tool string) hookEv { return hookEv{event: "PermissionRequest", tool: tool} }
func denied(tool string) hookEv      { return hookEv{event: "PermissionDenied", tool: tool} }

// sessionStart is a SessionStart of any source: which one it was matters to
// the workspace's background bookkeeping, and to the status not at all.
func sessionStart(source string) hookEv { return hookEv{event: "SessionStart", source: source} }

// TestStatusMatrixEventMapping is section A of the matrix: what each lifecycle
// event, on its own, says about a pane.
func TestStatusMatrixEventMapping(t *testing.T) {
	cases := []struct {
		row              string
		event, tool      string
		notificationType string
		want             Status
		wantDetail       string
		ok               bool
	}{
		{"A1", "UserPromptSubmit", "", "", StatusWorking, "", true},
		{"A2", "PreToolUse", "Bash", "", StatusWorking, "Bash", true},
		{"A2", "PreToolUse", "Task", "", StatusWorking, "Task", true},
		{"A3", "PreToolUse", "AskUserQuestion", "", StatusWaiting, "AskUserQuestion", true},
		{"A4", "PostToolUse", "Bash", "", StatusWorking, "", true},
		{"A5", "PostToolUseFailure", "Bash", "", StatusWorking, "", true},
		{"A6", "Interrupted", "Bash", "", StatusIdle, "", true},
		{"A7", "PermissionRequest", "Bash", "", StatusWaiting, "", true},
		{"A8", "PermissionDenied", "Bash", "", StatusWorking, "", true},
		{"A9", "Notification", "", "permission_prompt", StatusWaiting, "", true},
		{"A10", "Notification", "", "idle_prompt", StatusIdle, "", false},
		{"A11", "Notification", "", "elicitation_dialog", StatusWaiting, "", true},
		{"A11", "Notification", "", "agent_needs_input", StatusWaiting, "", true},
		{"A11", "Notification", "", "something_new", StatusWaiting, "", true},
		// Flockdeck's own chat client names the tool it asks about.
		{"A11", "Notification", "write_file", "", StatusWaiting, "write_file", true},
		{"A12", "Notification", "", "", StatusWaiting, "", true},
		{"A14", "Stop", "", "", StatusIdle, "", true},
		{"A15", "StopFailure", "", "", StatusIdle, "", true},
		{"A16", "SessionStart", "", "", StatusIdle, "", false},
		{"A17", "SessionEnd", "", "", StatusIdle, "", false},
		{"A18", "SubagentStart", "", "", StatusIdle, "", false},
		{"A18", "SubagentStop", "", "", StatusIdle, "", false},
		{"A19", "PreCompact", "", "", StatusIdle, "", false},
		{"A19", "", "", "", StatusIdle, "", false},
	}
	for _, c := range cases {
		t.Run(c.row+"/"+c.event+"/"+c.tool+c.notificationType, func(t *testing.T) {
			got, detail, ok := StatusForEvent(c.event, c.tool, c.notificationType)
			if ok != c.ok {
				t.Fatalf("ok = %v, want %v", ok, c.ok)
			}
			if !ok {
				return
			}
			if got != c.want || detail != c.wantDetail {
				t.Errorf("= %v %q, want %v %q", got, detail, c.want, c.wantDetail)
			}
			if got == StatusExited {
				t.Error("no lifecycle event may mark a pane exited; only the process going away does")
			}
		})
	}
}

// TestStatusMatrixTurns is sections B and C of the matrix as far as one
// session sees them: a sequence of events, in the order the pane's hooks
// delivered them, and the status and detail it must be left showing.
func TestStatusMatrixTurns(t *testing.T) {
	cases := []struct {
		row        string
		name       string
		events     []hookEv
		want       Status
		wantDetail string
		// bug, when set, is why the current code gets this row wrong; the
		// case is skipped unless FLOCKDECK_STATUS_BUGS is set.
		bug string
	}{
		{row: "B1", name: "a turn with no tools ends idle",
			events: []hookEv{prompt, stop}, want: StatusIdle},
		{row: "B1", name: "mid-turn with no tool yet is working",
			events: []hookEv{prompt}, want: StatusWorking},
		{row: "B2", name: "a tool running is working, named",
			events: []hookEv{prompt, pre("Bash")}, want: StatusWorking, wantDetail: "Bash"},
		{row: "B2", name: "between tools is working, unnamed",
			events: []hookEv{prompt, pre("Bash"), post("Bash")}, want: StatusWorking},
		{row: "B2", name: "a turn with tools ends idle",
			events: []hookEv{prompt, pre("Read"), post("Read"), pre("Edit"), post("Edit"), stop}, want: StatusIdle},
		{row: "B3", name: "parallel tools interleaved end idle",
			events: []hookEv{prompt, pre("Read"), pre("Grep"), post("Grep"), post("Read"), stop}, want: StatusIdle},
		{row: "B6", name: "a permission dialog opening waits on the tool",
			events: []hookEv{prompt, pre("Bash"), permRequest("")}, want: StatusWaiting, wantDetail: "Bash"},
		{row: "B6", name: "the nudge six seconds later keeps the tool",
			events: []hookEv{prompt, pre("Bash"), permRequest(""), permAsk}, want: StatusWaiting, wantDetail: "Bash"},
		{row: "B6", name: "an older Claude Code's nudge alone names the tool",
			events: []hookEv{prompt, pre("Edit"), {event: "Notification"}}, want: StatusWaiting, wantDetail: "Edit"},
		{row: "B6", name: "the tool running after the answer is working",
			events: []hookEv{prompt, pre("Bash"), permRequest(""), post("Bash")}, want: StatusWorking},
		{row: "B8", name: "AskUserQuestion waits",
			events: []hookEv{prompt, pre("AskUserQuestion")}, want: StatusWaiting, wantDetail: "AskUserQuestion"},
		{row: "B8", name: "its nudge keeps the question",
			events: []hookEv{prompt, pre("AskUserQuestion"), permAsk}, want: StatusWaiting, wantDetail: "AskUserQuestion"},
		{row: "B8", name: "answered, the turn goes on",
			events: []hookEv{prompt, pre("AskUserQuestion"), post("AskUserQuestion")}, want: StatusWorking},
		{row: "B8", name: "answered, the turn ends idle",
			events: []hookEv{prompt, pre("AskUserQuestion"), post("AskUserQuestion"), stop}, want: StatusIdle},
		{row: "B9", name: "a denial the turn ends on is blocked",
			events: []hookEv{prompt, pre("Bash"), denied("Bash"), stop}, want: StatusBlocked, wantDetail: "Bash"},
		{row: "B9", name: "a denial naming no tool is blocked on 'a tool'",
			events: []hookEv{prompt, denied(""), stop}, want: StatusBlocked, wantDetail: "a tool"},
		{row: "B9", name: "a denial mid-turn is still working",
			events: []hookEv{prompt, pre("Bash"), denied("Bash")}, want: StatusWorking},
		{row: "B9", name: "a denial recovered from ends idle",
			events: []hookEv{prompt, pre("Bash"), denied("Bash"), pre("Edit"), post("Edit"), stop}, want: StatusIdle},
		{row: "B9", name: "a denial from a turn before is not this turn's",
			events: []hookEv{prompt, denied("Bash"), stop, prompt, stop}, want: StatusIdle},
		{row: "C18", name: "a fresh conversation forgets a denial",
			events: []hookEv{prompt, denied("Bash"), {event: "SessionStart"}, stop}, want: StatusIdle},
		{row: "B9", name: "an ordinary tool failure is not blocked",
			events: []hookEv{prompt, pre("Bash"), postFail("Bash"), stop}, want: StatusIdle},
		{row: "B10", name: "a turn ending on an API error is idle",
			events: []hookEv{prompt, pre("Bash"), post("Bash"), stopFailure}, want: StatusIdle},
		{row: "B10", name: "a turn ending on an API error after a denial is blocked",
			events: []hookEv{prompt, pre("Bash"), denied("Bash"), stopFailure}, want: StatusBlocked, wantDetail: "Bash"},
		{row: "B11", name: "the user stopping a tool is idle",
			events: []hookEv{prompt, pre("Bash"), interrupted("Bash")}, want: StatusIdle},
		{row: "B11", name: "the user stopping a tool after a denial is idle, not blocked",
			events: []hookEv{prompt, pre("Bash"), denied("Bash"), interrupted("Bash")}, want: StatusIdle},
		{row: "B12", name: "Esc while the model is streaming: idle once Claude Code's idle nudge arrives",
			events: []hookEv{prompt, idleNudge}, want: StatusIdle},
		{row: "B13", name: "a compaction mid-turn leaves the pane working",
			events: []hookEv{prompt, pre("Bash"), post("Bash"), sessionStart("compact")}, want: StatusWorking},
		{row: "B14", name: "/clear at the prompt leaves the pane idle",
			events: []hookEv{prompt, stop, {event: "SessionEnd"}, sessionStart("clear")}, want: StatusIdle},
		{row: "B16", name: "the idle nudge after a turn leaves it idle",
			events: []hookEv{prompt, stop, idleNudge}, want: StatusIdle},
		{row: "B16", name: "the idle nudge does not clear a question",
			events: []hookEv{prompt, pre("AskUserQuestion"), idleNudge}, want: StatusWaiting, wantDetail: "AskUserQuestion"},
		{row: "B16", name: "the idle nudge does not clear blocked",
			events: []hookEv{prompt, denied("Bash"), stop, idleNudge}, want: StatusBlocked, wantDetail: "Bash"},
		{row: "B17", name: "an older Claude Code's untyped idle nudge after a turn leaves the pane idle",
			events: []hookEv{prompt, stop, {event: "Notification"}}, want: StatusIdle},
		{row: "B17", name: "an untyped Notification in the middle of a turn is still a real ask",
			events: []hookEv{prompt, pre("Bash"), {event: "Notification"}}, want: StatusWaiting, wantDetail: "Bash"},
		{row: "B17", name: "an untyped Notification with a tool named (the chat client) is still a real ask",
			events: []hookEv{prompt, stop, {event: "Notification", tool: "Bash"}}, want: StatusWaiting, wantDetail: "Bash"},
		{row: "B19", name: "a manual /compact at an idle prompt shows working",
			events: []hookEv{prompt, stop, {event: "PreCompact", source: "manual"}}, want: StatusWorking, wantDetail: "/compact"},
		{row: "B19", name: "a manual /compact ends idle at its PostCompact",
			events: []hookEv{prompt, stop, {event: "PreCompact", source: "manual"}, {event: "PostCompact", source: "manual"}}, want: StatusIdle},
		{row: "B19", name: "a manual /compact ends idle at its SessionStart when there is no PostCompact",
			events: []hookEv{prompt, stop, {event: "PreCompact", source: "manual"}, sessionStart("compact")}, want: StatusIdle},
		{row: "B19", name: "an automatic compaction mid-turn stays working through PostCompact",
			events: []hookEv{prompt, pre("Bash"), post("Bash"), {event: "PreCompact", source: "auto"}, {event: "PostCompact", source: "auto"}, sessionStart("compact")}, want: StatusWorking},
		{row: "B19", name: "a compaction whose end never came does not idle a later auto-compaction",
			events: []hookEv{prompt, stop, {event: "PreCompact", source: "manual"}, prompt, pre("Bash"), sessionStart("compact")}, want: StatusWorking, wantDetail: "Bash"},
		{row: "B20", name: "a foreground subagent keeps the pane working until the turn ends",
			events: []hookEv{prompt, pre("Task"), pre("Read"), post("Read"), {event: "SubagentStop"}}, want: StatusWorking},
		{row: "B20", name: "a foreground subagent's turn ends idle",
			events: []hookEv{prompt, pre("Task"), pre("Read"), post("Read"), {event: "SubagentStop"}, post("Task"), stop}, want: StatusIdle},
		{row: "B21", name: "a background subagent finishing after the turn leaves the pane idle",
			events: []hookEv{prompt, pre("Task"), post("Task"), stop, pre("Read"), post("Read"), {event: "SubagentStop"}}, want: StatusIdle},
		{row: "B22", name: "a background shell leaves the pane idle once the turn ends",
			events: []hookEv{prompt, pre("Bash"), post("Bash"), stop}, want: StatusIdle},

		{row: "C1", name: "the last tool's PostToolUse delivered after the turn's Stop",
			events: []hookEv{prompt, pre("Bash"), stop, post("Bash")}, want: StatusIdle},
		{row: "C1", name: "a late PostToolUseFailure after the Stop",
			events: []hookEv{prompt, pre("Bash"), stop, postFail("Bash")}, want: StatusIdle},
		{row: "C2", name: "a late PreToolUse after the Stop, then the idle nudge",
			events: []hookEv{prompt, stop, pre("Bash"), idleNudge}, want: StatusIdle},
		{row: "C3", name: "a duplicate Stop is still idle",
			events: []hookEv{prompt, stop, stop}, want: StatusIdle},
		{row: "C4", name: "a duplicate Stop keeps a blocked pane blocked",
			events: []hookEv{prompt, pre("Bash"), denied("Bash"), stop, stop}, want: StatusBlocked, wantDetail: "Bash"},
		{row: "C5", name: "a permission nudge after the Stop turns the pane amber (accepted: a background subagent can ask)",
			events: []hookEv{prompt, pre("Bash"), post("Bash"), stop, permAsk}, want: StatusWaiting},
		{row: "C14", name: "a lost Stop, then the idle nudge",
			events: []hookEv{prompt, pre("Bash"), post("Bash"), idleNudge}, want: StatusIdle},
		{row: "C15", name: "a lost UserPromptSubmit: the first tool marks the pane working",
			events: []hookEv{stop, pre("Read")}, want: StatusWorking, wantDetail: "Read"},
	}
	for _, c := range cases {
		t.Run(c.row+"/"+c.name, func(t *testing.T) {
			if c.bug != "" {
				knownBug(t, c.row, c.bug)
			}
			s := claudePane()
			st, detail := feed(s, c.events...)
			if st != c.want || detail != c.wantDetail {
				t.Errorf("status = %v %q, want %v %q", st, detail, c.want, c.wantDetail)
			}
		})
	}
}

// TestStatusMatrixToolInputFollowsTheWait is row B6/B8's other half: what a
// wait is about -- the tool input the phone shows -- is carried forward the
// same way the tool's name is, and replaced once the pane moves on.
func TestStatusMatrixToolInputFollowsTheWait(t *testing.T) {
	s := claudePane()
	cmd := `{"command":"rm -rf build"}`
	feed(s, prompt, hookEv{event: "PreToolUse", tool: "Bash", input: cmd}, permRequest(""), permAsk)
	if got := s.ToolInput(); got != cmd {
		t.Errorf("ToolInput = %q while waiting, want %q", got, cmd)
	}
	feed(s, post("Bash"))
	if got := s.ToolInput(); got != "" {
		t.Errorf("ToolInput = %q once the tool ran, want empty", got)
	}
}

// TestStatusMatrixWaitClock is row B18: a repeated nudge about the same wait
// neither reports a change nor restarts how long the pane has been waiting.
func TestStatusMatrixWaitClock(t *testing.T) {
	s := claudePane()
	changes := 0
	s.OnChange = func() { changes++ }
	feed(s, prompt, pre("Bash"), permRequest(""))
	since, before := s.StatusSince(), changes
	time.Sleep(5 * time.Millisecond)
	feed(s, permAsk, permAsk)
	if !s.StatusSince().Equal(since) {
		t.Error("a repeated nudge restarted the wait clock")
	}
	if changes != before {
		t.Errorf("a repeated nudge was reported %d times, want 0", changes-before)
	}
	// Row C3: a duplicate Stop is not reported either.
	feed(s, stop)
	before = changes
	feed(s, stop)
	if changes != before {
		t.Error("a duplicate Stop was reported as a change")
	}
}

// TestStatusMatrixExited is rows C10 and C11: nothing but the process going
// away marks a pane exited, and nothing after that brings it back.
func TestStatusMatrixExited(t *testing.T) {
	for _, evs := range [][]hookEv{
		{prompt},
		{pre("Bash")},
		{pre("AskUserQuestion")},
		{permAsk},
		{denied("Bash"), stop},
		{stop},
		{{event: "SessionEnd"}},
	} {
		s := claudePane()
		feed(s, prompt)
		s.SetStatus(StatusExited, "")
		if st, _ := feed(s, evs...); st != StatusExited {
			t.Errorf("%v after exit: status = %v, want exited", evs, st)
		}
	}
	// C11: SessionEnd while the process lives says nothing -- /clear fires it
	// and carries straight on.
	s := claudePane()
	if st, _ := feed(s, prompt, stop, hookEv{event: "SessionEnd"}); st != StatusIdle {
		t.Errorf("status = %v after SessionEnd on a live pane, want idle", st)
	}
}

// TestStatusMatrixKeyboard is rows B4, B5, B7 and B8's keyboard half: what
// typing into a hook-reported pane does to a wait.
func TestStatusMatrixKeyboard(t *testing.T) {
	setup := func(t *testing.T) *Session {
		f := newFakePTY()
		t.Cleanup(func() { _ = f.Close() })
		s := fakeSession(f)
		s.Kind = KindAgent
		s.sawInput = true
		s.answerQuiet = 40 * time.Millisecond
		return s
	}
	settle := func(s *Session) Status {
		deadline := time.Now().Add(2 * time.Second)
		for {
			if st, _ := s.Status(); st != StatusWorking || time.Now().After(deadline) {
				return st
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	press := func(t *testing.T, s *Session, keys string) {
		t.Helper()
		if err := s.WriteString(keys); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	t.Run("B4/Enter on a permission prompt is working on the tool", func(t *testing.T) {
		s := setup(t)
		feed(s, prompt, pre("Bash"), permRequest(""))
		press(t, s, "\r")
		if st, detail := s.Status(); st != StatusWorking || detail != "Bash" {
			t.Errorf("status = %v %q, want working on Bash", st, detail)
		}
		// Allowed: the tool's PostToolUse and the Stop follow.
		if st, _ := feed(s, post("Bash"), stop); st != StatusIdle {
			t.Errorf("status = %v at the end of the turn, want idle", st)
		}
	})
	t.Run("B4/an arrow key moves between choices and answers nothing", func(t *testing.T) {
		s := setup(t)
		feed(s, prompt, pre("Bash"), permRequest(""))
		press(t, s, "\x1b[B")
		if st, _ := s.Status(); st != StatusWaiting {
			t.Errorf("status = %v, want still waiting", st)
		}
	})
	t.Run("B5/No and Enter, then silence, is idle", func(t *testing.T) {
		s := setup(t)
		feed(s, prompt, pre("Bash"), permRequest(""))
		press(t, s, "\x1b[B\x1b[B\r")
		if st := settle(s); st != StatusIdle {
			t.Errorf("status = %v after a refusal and silence, want idle", st)
		}
	})
	t.Run("B7/Esc on a permission prompt, then silence, is idle", func(t *testing.T) {
		s := setup(t)
		feed(s, prompt, pre("Bash"), permRequest(""))
		press(t, s, "\x1b")
		if st := settle(s); st != StatusIdle {
			t.Errorf("status = %v after Esc refused the tool, want idle", st)
		}
	})
	t.Run("B8/Enter on AskUserQuestion keeps it waiting", func(t *testing.T) {
		s := setup(t)
		feed(s, prompt, pre("AskUserQuestion"))
		press(t, s, "\r")
		if st, _ := s.Status(); st != StatusWaiting {
			t.Errorf("status = %v, want waiting until the tool reports", st)
		}
	})
	t.Run("B16/Enter at the idle prompt changes nothing until its hook", func(t *testing.T) {
		s := setup(t)
		feed(s, prompt, stop)
		press(t, s, "fix it\r")
		if st, _ := s.Status(); st != StatusIdle {
			t.Errorf("status = %v, want idle until UserPromptSubmit says otherwise", st)
		}
	})
	spin := func(s *Session) {
		for end := time.Now().Add(60 * time.Millisecond); time.Now().Before(end); time.Sleep(2 * time.Millisecond) {
			s.publish([]byte("\r* Thinking\n"))
		}
	}
	t.Run("C15/a lost UserPromptSubmit: Enter, then a spinner, is working until quiet", func(t *testing.T) {
		s := setup(t)
		feed(s, prompt, stop)
		press(t, s, "explain this\r")
		spin(s)
		if st, _ := s.Status(); st != StatusWorking {
			t.Fatalf("status = %v while the turn draws, want working", st)
		}
		if st := settle(s); st != StatusIdle {
			t.Errorf("status = %v once it fell quiet, want idle", st)
		}
	})
	t.Run("C15/a lost UserPromptSubmit: the Stop still ends the guessed turn", func(t *testing.T) {
		s := setup(t)
		feed(s, prompt, stop)
		press(t, s, "explain this\r")
		spin(s)
		if st, _ := feed(s, stop); st != StatusIdle {
			t.Errorf("status = %v after the Stop, want idle", st)
		}
		time.Sleep(120 * time.Millisecond)
		if st, _ := s.Status(); st != StatusIdle {
			t.Errorf("status = %v after the guess's timer, want idle", st)
		}
	})
	t.Run("C15/Enter that draws once and stops is not a turn", func(t *testing.T) {
		s := setup(t)
		feed(s, prompt, stop)
		press(t, s, "/help\r")
		s.publish([]byte("help text\n"))
		time.Sleep(120 * time.Millisecond)
		if st, _ := s.Status(); st != StatusIdle {
			t.Errorf("status = %v, want idle", st)
		}
	})
	t.Run("C15/a prompt whose UserPromptSubmit arrives is not disturbed by the guess", func(t *testing.T) {
		s := setup(t)
		feed(s, prompt, stop)
		press(t, s, "explain this\r")
		feed(s, prompt)
		spin(s)
		if st, _ := feed(s, stop); st != StatusIdle {
			t.Errorf("status = %v after the Stop, want idle", st)
		}
	})
}

// TestStatusMatrixOutputFallback is section D: a pane nothing reports for --
// a shell, an agent with no lifecycle, a Claude pane before its first event --
// read from its output.
func TestStatusMatrixOutputFallback(t *testing.T) {
	t.Run("D1/D2/a shell printing is working, then idle once quiet", func(t *testing.T) {
		s := shellPane()
		if st, _ := s.Status(); st != StatusStarting {
			t.Fatalf("status = %v before any output, want starting", st)
		}
		s.publish([]byte("building\n"))
		if st, _ := s.Status(); st != StatusWorking {
			t.Fatalf("status = %v while printing, want working", st)
		}
		waitForStatus(t, s, StatusIdle, 5*time.Second)
	})
	t.Run("D4/an agent's bell after startup is waiting", func(t *testing.T) {
		s := claudePane()
		s.publish([]byte("may I?\x07"))
		if st, _ := s.Status(); st != StatusWaiting {
			t.Errorf("status = %v, want waiting", st)
		}
	})
	t.Run("D4/a bell while starting up is not attention", func(t *testing.T) {
		s := claudePane()
		s.sawInput = false
		s.startedAt = time.Now()
		s.publish([]byte("\x07"))
		if st, _ := s.Status(); st == StatusWaiting {
			t.Error("a bell during startup marked the pane waiting")
		}
	})
	t.Run("D5/a shell's bell is never waiting", func(t *testing.T) {
		s := shellPane()
		s.sawInput = true
		s.publish([]byte("done\x07"))
		if st, _ := s.Status(); st == StatusWaiting {
			t.Error("a shell's bell marked it waiting")
		}
	})
	t.Run("D8/output does not clear an inferred wait", func(t *testing.T) {
		s := claudePane()
		s.idleAfter = 20 * time.Millisecond
		s.publish([]byte("may I?\x07"))
		s.publish([]byte("redraw\n"))
		time.Sleep(60 * time.Millisecond)
		if st, _ := s.Status(); st != StatusWaiting {
			t.Errorf("status = %v, want still waiting", st)
		}
	})
	t.Run("D9/once a hook reports, output and the bell are ignored", func(t *testing.T) {
		s := claudePane()
		feed(s, prompt, stop)
		s.publish([]byte("\x1b[2K> \x07"))
		if st, _ := s.Status(); st != StatusIdle {
			t.Errorf("status = %v, want idle as the Stop left it", st)
		}
	})
	t.Run("D9/once a hook reports, a working pane does not settle idle on quiet", func(t *testing.T) {
		// The flip side of C14: this is why a lost Stop is never recovered.
		s := claudePane()
		s.idleAfter = 10 * time.Millisecond
		feed(s, prompt)
		s.publish([]byte("thinking…"))
		time.Sleep(60 * time.Millisecond)
		if st, _ := s.Status(); st != StatusWorking {
			t.Errorf("status = %v, want working until a hook says otherwise", st)
		}
	})
	t.Run("D12/SessionStart alone hands nothing to the hooks", func(t *testing.T) {
		s := claudePane()
		s.status = StatusStarting
		s.idleAfter = 20 * time.Millisecond
		feed(s, sessionStart("startup"))
		s.publish([]byte("Welcome to Claude Code\n"))
		if st, _ := s.Status(); st != StatusWorking {
			t.Fatalf("status = %v while drawing its first screen, want working", st)
		}
		waitForStatus(t, s, StatusIdle, 5*time.Second)
	})
	t.Run("D11/an exited pane stays exited whatever it prints", func(t *testing.T) {
		s := shellPane()
		s.SetStatus(StatusExited, "")
		s.publish([]byte("goodbye\x07"))
		if st, _ := s.Status(); st != StatusExited {
			t.Errorf("status = %v, want exited", st)
		}
	})
}

// TestStatusMatrixAttention is section F's base: which statuses count as
// needing the user.
func TestStatusMatrixAttention(t *testing.T) {
	want := map[Status]bool{
		StatusStarting: false, StatusWorking: false, StatusWaiting: true,
		StatusBlocked: true, StatusIdle: false, StatusExited: false,
	}
	for st, needs := range want {
		if st.NeedsAttention() != needs {
			t.Errorf("%v.NeedsAttention() = %v, want %v", st, !needs, needs)
		}
		if st.String() == "unknown" || st.Symbol() == "·" {
			t.Errorf("%v has no name or glyph of its own", st)
		}
	}
}
