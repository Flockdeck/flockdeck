package structured

import (
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/acp/acptest"
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

