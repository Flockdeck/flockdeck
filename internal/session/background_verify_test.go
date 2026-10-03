package session

import (
	"strings"
	"testing"
	"time"
)

// fakeBackgroundClock sets the clock background work is timed by to a fixed
// time the test moves on itself.
func fakeBackgroundClock(t *testing.T) *time.Time {
	t.Helper()
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	backgroundNow = func() time.Time { return now }
	t.Cleanup(func() { backgroundNow = time.Now })
	return &now
}

// TestBackgroundWorkNothingShowsRunningIsUnverifiedNotDropped covers
// RefreshBackground: work with no sign of running for
// BackgroundUnverifiedGrace is marked unverified and stops counting as
// verified, but stays counted and listed; a fresh sign verifies it again.
func TestBackgroundWorkNothingShowsRunningIsUnverifiedNotDropped(t *testing.T) {
	now := fakeBackgroundClock(t)
	s := &Session{}
	s.NoteBackgroundWork("PostToolUse", "start", "shell:b1", BackgroundInfo{Type: "shell", Command: "make"})
	if s.BackgroundVerified() != 1 || s.BackgroundWork()[0].ConfirmedBy != "its start" {
		t.Fatalf("a started shell is not verified: %+v", s.BackgroundWork())
	}

	*now = now.Add(BackgroundUnverifiedGrace - time.Second)
	if s.RefreshBackground(*now) || s.BackgroundVerified() != 1 {
		t.Fatal("work was unverified before its grace was up")
	}
	*now = now.Add(time.Second)
	if !s.RefreshBackground(*now) {
		t.Error("marking work unverified announced nothing")
	}
	if s.BackgroundVerified() != 0 || s.BackgroundTasks() != 1 || !s.BackgroundWork()[0].Unverified {
		t.Fatalf("after the grace: verified %d, counted %d, %+v", s.BackgroundVerified(), s.BackgroundTasks(), s.BackgroundWork())
	}

	// Claude Code's list at the end of a turn names it again: verified.
	s.SetBackgroundWork([]string{"shell:b1"}, nil)
	w := s.BackgroundWork()[0]
	if w.Unverified || !w.Confirmed.Equal(*now) || !strings.Contains(w.ConfirmedBy, "list") {
		t.Fatalf("a turn end's list did not verify the work: %+v", w)
	}

	// A sign older than the last changes nothing; a newer one moves it on.
	if s.ConfirmBackgroundWork("shell:b1", now.Add(-time.Minute), "older") {
		t.Error("an older sign was taken")
	}
	*now = now.Add(time.Hour)
	s.RefreshBackground(*now)
	if !s.ConfirmBackgroundWork("shell:b1", now.Add(-time.Minute), "its transcript is being written") {
		t.Fatal("a fresh sign was not taken")
	}
	if w := s.BackgroundWork()[0]; w.Unverified || w.ConfirmedBy != "its transcript is being written" {
		t.Errorf("a fresh sign did not verify the work: %+v", w)
	}
}

// TestEndedBackgroundWorkSaysWhy covers the ended list: every way work stops
// being counted keeps it a while with what showed it had ended, and a new
// conversation forgets it all.
func TestEndedBackgroundWorkSaysWhy(t *testing.T) {
	now := fakeBackgroundClock(t)
	s := &Session{}
	for _, id := range []string{"shell:b1", "shell:b2", "agent:a1", "agent:a2"} {
		s.NoteBackground("PostToolUse", "start", id)
	}
	s.NoteBackground("UserPromptSubmit", "end", "task:b1")
	s.NoteBackground("SubagentStop", "end", "agent:a1")
	if !s.EndBackgroundWork("shell:b2", "ended: its task notification is in the conversation") {
		t.Fatal("ending counted work by evidence reported nothing counted")
	}
	if s.EndBackgroundWork("shell:nope", "ended: x") {
		t.Error("ending work never counted reported it counted")
	}
	s.SetBackgroundWork([]string{}, nil)

	got := map[string]string{}
	for _, e := range s.BackgroundEnded() {
		got[e.ID] = e.Evidence
	}
	want := map[string]string{
		"shell:b1": "ended: Claude Code's task notification",
		"agent:a1": "ended: the subagent's SubagentStop hook",
		"shell:b2": "ended: its task notification is in the conversation",
		"agent:a2": "ended: not in Claude Code's list of running work at the end of a turn",
	}
	for id, ev := range want {
		if got[id] != ev {
			t.Errorf("%s ended with %q, want %q", id, got[id], ev)
		}
	}
	if s.BackgroundTasks() != 0 {
		t.Fatalf("still counted: %+v", s.BackgroundWork())
	}

	*now = now.Add(backgroundEndedShown)
	if len(s.BackgroundEnded()) != 0 {
		t.Error("ended work outlived its showing")
	}
	if !s.RefreshBackground(*now) {
		t.Error("forgetting ended work announced nothing")
	}

	s.NoteBackground("PostToolUse", "start", "shell:b3")
	s.NoteBackground("PostToolUse", "end", "shell:b3")
	s.NoteBackground("SessionStart", "", "")
	if len(s.BackgroundEnded()) != 0 || s.BackgroundTasks() != 0 {
		t.Error("a new conversation kept background work or its ends")
	}
}
