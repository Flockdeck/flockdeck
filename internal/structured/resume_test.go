package structured

import (
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/acp"

	"github.com/jmwri/flockdeck/internal/acp/acptest"
	"github.com/jmwri/flockdeck/internal/agent"
)

func resumeOptions(t *testing.T, id string, aopts ...acptest.Option) Options {
	o := fakeOptions(t, aopts...)
	o.ResumeSession = id
	return o
}

func TestLoadSessionReplaysTheHistoryOnce(t *testing.T) {
	r := begin(t, resumeOptions(t, "old-1", acptest.WithSessionLoad(), acptest.WithKnownSessions("old-1")))
	s := until(t, r, "ready", idle)
	if s.Resume != ResumeLoaded || s.SessionID != "old-1" {
		t.Fatalf("resume = %q, session = %q", s.Resume, s.SessionID)
	}
	if got := prompts(s); len(got) != 2 || got[0] != "first question" || got[1] != "second question" {
		t.Errorf("prompts = %q", got)
	}
	if got := replies(s); got != "first answer\nsecond answer\n" {
		t.Errorf("replies = %q", got)
	}
	if n := notices(s); n != "" {
		t.Errorf("a notice for a session that loaded: %q", n)
	}
	if s.Status != "idle" {
		t.Errorf("status = %q", s.Status)
	}
	// A prompt goes to the reopened session and adds to the transcript; the
	// replayed part is not shown again.
	if err := r.Send("whoami"); err != nil {
		t.Fatal(err)
	}
	s = until(t, r, "the reply", func(s Snapshot) bool { return idle(s) && strings.Contains(replies(s), "session=old-1") })
	if got := prompts(s); len(got) != 3 || got[2] != "whoami" {
		t.Errorf("prompts after a turn = %q", got)
	}
	if strings.Count(replies(s), "first answer") != 1 {
		t.Errorf("the history was doubled: %q", replies(s))
	}
}

func TestResumeWhenTheAgentHasNoLoad(t *testing.T) {
	r := begin(t, resumeOptions(t, "old-1", acptest.WithSessionResume(), acptest.WithKnownSessions("old-1")))
	s := until(t, r, "ready", idle)
	if s.Resume != ResumeResumed || s.SessionID != "old-1" {
		t.Fatalf("resume = %q, session = %q", s.Resume, s.SessionID)
	}
	if len(s.Entries) != 0 {
		t.Errorf("a resume made %d entries", len(s.Entries))
	}
	if err := r.Send("whoami"); err != nil {
		t.Fatal(err)
	}
	until(t, r, "the reply", func(s Snapshot) bool { return strings.Contains(replies(s), "session=old-1") })
}

func TestAgentWithNeitherCapabilityStartsANewSession(t *testing.T) {
	r := begin(t, resumeOptions(t, "old-1", acptest.WithKnownSessions("old-1")))
	s := until(t, r, "ready", idle)
	if s.Resume != ResumeFailed || s.SessionID != "sess-1" {
		t.Fatalf("resume = %q, session = %q", s.Resume, s.SessionID)
	}
	if n := notices(s); n != ResumeFailedText+"\n" {
		t.Errorf("notices = %q", n)
	}
	if err := r.Send("whoami"); err != nil {
		t.Fatal(err)
	}
	until(t, r, "the reply", func(s Snapshot) bool { return strings.Contains(replies(s), "session=sess-1") })
}

func TestStaleSessionIdStartsANewSession(t *testing.T) {
	for name, opts := range map[string][]acptest.Option{
		"load":   {acptest.WithSessionLoad(), acptest.WithKnownSessions("another")},
		"resume": {acptest.WithSessionResume(), acptest.WithKnownSessions("another")},
	} {
		t.Run(name, func(t *testing.T) {
			r := begin(t, resumeOptions(t, "stale", opts...))
			s := until(t, r, "ready", idle)
			if s.Resume != ResumeFailed || s.SessionID != "sess-1" {
				t.Fatalf("resume = %q, session = %q", s.Resume, s.SessionID)
			}
			if n := notices(s); n != ResumeFailedText+"\n" {
				t.Errorf("notices = %q", n)
			}
			// What the agent said about why is not shown anywhere.
			if all := notices(s) + s.Err + s.Agent; strings.Contains(all, "SECRET-AGENT-TEXT") || strings.Contains(all, "stale") {
				t.Errorf("the agent's error text reached the pane: %q", all)
			}
			if s.Phase != PhaseReady {
				t.Errorf("phase = %q", s.Phase)
			}
		})
	}
}

func TestAHalfReplayedSessionLeavesNoHistoryBehindTheNewOne(t *testing.T) {
	// The agent dies after sending its history: that is an exit, not a stale id.
	r := begin(t, resumeOptions(t, "old-1", acptest.WithSessionLoad(), acptest.WithKnownSessions("old-1"), acptest.WithReplayThenExit()))
	s := until(t, r, "an exit", func(s Snapshot) bool { return s.Phase == PhaseExited })
	if len(s.Entries) != 0 {
		t.Errorf("%d entries of a failed load are shown", len(s.Entries))
	}
	if strings.Contains(s.Err, "cut off") {
		t.Errorf("err = %q", s.Err)
	}
	// The id is still the one to resume the next time.
	if r.SessionID() != "old-1" {
		t.Errorf("session id = %q", r.SessionID())
	}
}

func TestNoResumeAsksForANewSession(t *testing.T) {
	r := begin(t, fakeOptions(t, acptest.WithSessionLoad(), acptest.WithKnownSessions("old-1")))
	s := until(t, r, "ready", idle)
	if s.Resume != ResumeNone || s.SessionID != "sess-1" || len(s.Entries) != 0 {
		t.Errorf("resume = %q, session = %q, entries = %d", s.Resume, s.SessionID, len(s.Entries))
	}
}

func TestSessionIdIsKeptWhileTheAgentStarts(t *testing.T) {
	o := resumeOptions(t, "old-1", acptest.WithSessionLoad(), acptest.WithKnownSessions("old-1"))
	r := begin(t, o)
	// Whatever moment this is read at, the id to resume is not lost.
	if id := r.SessionID(); id != "old-1" {
		t.Errorf("session id = %q", id)
	}
	until(t, r, "ready", idle)
	if id := r.SessionID(); id != "old-1" {
		t.Errorf("session id = %q", id)
	}
}

func TestALateReplayAfterATimedOutLoadIsNotTaken(t *testing.T) {
	o := resumeOptions(t, "old-1", acptest.WithSessionLoad(), acptest.WithKnownSessions("old-1"), acptest.WithLateReplay())
	o.Client = func(c *acp.Config) { c.LoadTimeout = 300 * time.Millisecond }
	r := begin(t, o)
	s := until(t, r, "ready", idle)
	if s.Resume != ResumeFailed {
		t.Fatalf("resume = %q", s.Resume)
	}
	// The agent keeps streaming for another second; none of it is shown.
	time.Sleep(1500 * time.Millisecond)
	s = r.Snapshot()
	if all := entriesAll(s); strings.Contains(all, "LATE") || strings.Contains(all, "first") {
		t.Errorf("a late replay reached the transcript: %q", all)
	}
	if n := notices(s); n != ResumeFailedText+"\n" || len(s.Entries) != 1 {
		t.Errorf("entries = %+v", s.Entries)
	}
	// No answer came, so the layout keeps the id it had; the live session is the new one.
	if r.SessionID() != "old-1" || s.SessionID != "sess-1" {
		t.Errorf("save %q, live %q", r.SessionID(), s.SessionID)
	}
}

func entriesAll(s Snapshot) string {
	var b strings.Builder
	for _, e := range s.Entries {
		b.WriteString(e.Text + e.Markdown + "\n")
	}
	return b.String()
}

func TestAnAgentThatRefusesLoadIsAskedToResume(t *testing.T) {
	r := begin(t, resumeOptions(t, "old-1", acptest.WithSessionLoad(), acptest.WithSessionResume(), acptest.WithLoadRefused(), acptest.WithKnownSessions("old-1")))
	s := until(t, r, "ready", idle)
	if s.Resume != ResumeResumed || s.SessionID != "old-1" || len(s.Entries) != 0 {
		t.Fatalf("resume = %q, session = %q, entries = %d", s.Resume, s.SessionID, len(s.Entries))
	}
	if all := entriesAll(s) + s.Err; strings.Contains(all, "SECRET-AGENT-TEXT") {
		t.Error("the agent's error text reached the pane")
	}
}

func TestADefiniteRefusalReplacesTheSavedId(t *testing.T) {
	r := begin(t, resumeOptions(t, "stale", acptest.WithSessionLoad(), acptest.WithKnownSessions("other")))
	s := until(t, r, "ready", idle)
	if s.Resume != ResumeFailed || r.SessionID() != s.SessionID {
		t.Errorf("resume = %q, save %q, live %q", s.Resume, r.SessionID(), s.SessionID)
	}
}

// Flockdeck's own Claude adapter can move the id a pane is resumed by (after
// /clear, claude continues under a new one). Prompts still go to the session
// the pane has; the id for the layout is the new one. Another agent saying the
// same, or an id that is not a UUID, changes nothing.
func TestAdapterMovesTheSavedSessionID(t *testing.T) {
	const moved = "11111111-2222-4333-8444-555555555555"
	claude := func(t *testing.T) Options {
		o := fakeOptions(t)
		o.Spec.Runner = agent.RunnerACP
		o.Spec.ACP.Adapter = "claude"
		return o
	}
	t.Run("own claude adapter", func(t *testing.T) {
		r := begin(t, claude(t))
		first := until(t, r, "ready", idle).SessionID
		if err := r.Send("moved-session"); err != nil {
			t.Fatal(err)
		}
		until(t, r, "the turn", func(s Snapshot) bool { return idle(s) && strings.Contains(replies(s), "moved") })
		if got := r.SessionID(); got != moved {
			t.Errorf("SessionID() = %q, want %q", got, moved)
		}
		// prompts still use the original session
		if err := r.Send("whoami"); err != nil {
			t.Fatal(err)
		}
		s := until(t, r, "whoami", func(s Snapshot) bool { return idle(s) && strings.Contains(replies(s), "session=") })
		if !strings.Contains(replies(s), "session="+first) {
			t.Errorf("a prompt went to a different session than %q: %q", first, replies(s))
		}
	})
	t.Run("another agent", func(t *testing.T) {
		r := begin(t, fakeOptions(t))
		first := until(t, r, "ready", idle).SessionID
		if err := r.Send("moved-session"); err != nil {
			t.Fatal(err)
		}
		until(t, r, "the turn", func(s Snapshot) bool { return idle(s) && strings.Contains(replies(s), "moved") })
		if got := r.SessionID(); got != first {
			t.Errorf("SessionID() = %q, want the live %q", got, first)
		}
	})
	t.Run("not a UUID", func(t *testing.T) {
		r := begin(t, claude(t))
		first := until(t, r, "ready", idle).SessionID
		if err := r.Send("moved-bad"); err != nil {
			t.Fatal(err)
		}
		until(t, r, "the turn", func(s Snapshot) bool { return idle(s) && strings.Contains(replies(s), "moved") })
		if got := r.SessionID(); got != first {
			t.Errorf("SessionID() = %q, want the live %q", got, first)
		}
	})
}

func claudeRuntime(session, keep string) (*Runtime, *int) {
	calls := new(int)
	return &Runtime{
		opts: Options{
			Spec:           agent.Spec{Runner: agent.RunnerACP, ACP: agent.ACPSpec{Adapter: "claude"}},
			OnSessionMoved: func() { *calls++ },
		},
		session: session, keep: keep,
	}, calls
}

// noteMoved accepts the id in any UUID spelling and keeps the canonical one, and
// tells the owner only when the id to save changed. It is called outside the
// lock: the callback reads SessionID.
func TestNoteMovedNormalisesAndTellsOnChange(t *testing.T) {
	const canon = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	r, calls := claudeRuntime("live", "")
	var seen string
	r.opts.OnSessionMoved = func() { *calls++; seen = r.SessionID() }
	r.noteMoved("{AAAAAAAA-BBBB-4CCC-8DDD-EEEEEEEEEEEE}")
	r.noteMoved(canon)
	if *calls != 1 || seen != canon || r.SessionID() != canon {
		t.Errorf("calls %d, seen %q, SessionID %q; want one call and %q", *calls, seen, r.SessionID(), canon)
	}
	r.noteMoved("11111111-2222-4333-8444-555555555555")
	if *calls != 2 {
		t.Errorf("a different id was not told: %d calls", *calls)
	}
}

// With no live session there is nothing to move, and nobody is told.
func TestNoteMovedNeedsALiveSession(t *testing.T) {
	r, calls := claudeRuntime("", "")
	r.opts.ResumeSession = "asked-for"
	r.noteMoved("11111111-2222-4333-8444-555555555555")
	if r.SessionID() != "asked-for" || *calls != 0 {
		t.Errorf("SessionID %q, %d calls", r.SessionID(), *calls)
	}
}

// A pane whose reopen was never answered keeps that id in the layout, until the
// live session moves.
func TestMovedWinsOverKeep(t *testing.T) {
	r, _ := claudeRuntime("live", "kept-old")
	if r.SessionID() != "kept-old" {
		t.Fatalf("SessionID = %q", r.SessionID())
	}
	r.noteMoved("11111111-2222-4333-8444-555555555555")
	if r.SessionID() != "11111111-2222-4333-8444-555555555555" {
		t.Errorf("SessionID = %q after the move", r.SessionID())
	}
}

type recHandler struct {
	acp.Handler
	got []acp.Update
}

func (h *recHandler) Update(u acp.Update) { h.got = append(h.got, u) }

// The update is taken by the runtime and not passed to the transcript bridge.
func TestSessionIDUpdateIsNotPassedOn(t *testing.T) {
	r, _ := claudeRuntime("live", "")
	rec := &recHandler{}
	h := handler{Handler: rec, r: r}
	h.Update(acp.Update{SessionID: "live", Kind: acp.UpdateSessionID, NewSessionID: "11111111-2222-4333-8444-555555555555"})
	h.Update(acp.Update{SessionID: "live", Kind: acp.UpdateAgentMessageChunk})
	if len(rec.got) != 1 || rec.got[0].Kind != acp.UpdateAgentMessageChunk {
		t.Errorf("passed on: %+v", rec.got)
	}
	if r.SessionID() != "11111111-2222-4333-8444-555555555555" {
		t.Errorf("SessionID = %q", r.SessionID())
	}
}

func TestAdapterUpperCaseIDIsNormalised(t *testing.T) {
	o := fakeOptions(t)
	o.Spec.Runner = agent.RunnerACP
	o.Spec.ACP.Adapter = "claude"
	r := begin(t, o)
	until(t, r, "ready", idle)
	if err := r.Send("moved-upper"); err != nil {
		t.Fatal(err)
	}
	until(t, r, "the turn", func(s Snapshot) bool { return idle(s) && strings.Contains(replies(s), "moved") })
	if got := r.SessionID(); got != "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee" {
		t.Errorf("SessionID() = %q", got)
	}
}
