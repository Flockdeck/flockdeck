package session

import (
	"testing"
	"time"
)

// TestBackgroundWorkKeepsWhatWasSaidOfIt covers NoteBackgroundWork and
// SetBackgroundWork: each piece of background work keeps what its start said
// of it and when it was first heard of, for the header's list of it, and
// loses both with the work -- on its end, on a new conversation, and when a
// turn end's list no longer names it.
func TestBackgroundWorkKeepsWhatWasSaidOfIt(t *testing.T) {
	s := &Session{}
	changes := 0
	s.OnChange = func() { changes++ }

	before := time.Now()
	s.NoteBackgroundWork("PostToolUse", "start", "shell:b1", BackgroundInfo{Type: "shell", Description: "Sleep then echo", Command: "sleep 9; echo done"})
	s.NoteBackground("SubagentStart", "start", "agent:a7")
	work := s.BackgroundWork()
	if len(work) != 2 || s.BackgroundTasks() != 2 {
		t.Fatalf("work = %+v, want two pieces", work)
	}
	byID := func() map[string]BackgroundWork {
		m := map[string]BackgroundWork{}
		for _, w := range s.BackgroundWork() {
			m[w.ID] = w
		}
		return m
	}
	sh := byID()["shell:b1"]
	if sh.ID != "shell:b1" || sh.Command != "sleep 9; echo done" || sh.Description != "Sleep then echo" || sh.Listed {
		t.Errorf("the shell's start is not kept as it was said: %+v", sh)
	}
	if sh.Since.Before(before) || sh.Since.After(time.Now()) {
		t.Errorf("the shell was first seen at %v, not when it started", sh.Since)
	}
	if a := byID()["agent:a7"]; a.ID != "agent:a7" || a.Description != "" {
		t.Errorf("a start that said nothing of the work was given something: %+v", a)
	}
	since := sh.Since

	// A turn's end names both, says more of the subagent, and adds a monitor
	// first heard of here.
	s.SetBackgroundWork([]string{"shell:b1", "agent:a7", "monitor:m1"}, map[string]BackgroundInfo{
		"agent:a7": {Type: "subagent", AgentType: "general-purpose", Description: "Run the tests"},
	})
	work = s.BackgroundWork()
	if len(work) != 3 {
		t.Fatalf("work = %+v, want three pieces", work)
	}
	got := byID()
	if w := got["shell:b1"]; !w.Since.Equal(since) || w.Command != "sleep 9; echo done" || w.Listed {
		t.Errorf("the list lost what the shell's start said: %+v", w)
	}
	if w := got["agent:a7"]; w.AgentType != "general-purpose" || w.Description != "Run the tests" || w.Listed {
		t.Errorf("the list's account of the subagent was not kept: %+v", w)
	}
	if w := got["monitor:m1"]; !w.Listed || w.Description != "" || w.Since.IsZero() {
		t.Errorf("work first heard of in a list is not marked as such: %+v", w)
	}

	// An end by a task notification, which names no kind.
	n := changes
	s.NoteBackground("UserPromptSubmit", "end", "task:b1")
	if w, still := byID()["shell:b1"]; still {
		t.Fatalf("the shell outlived its end: %+v", w)
	}
	if changes == n {
		t.Error("an end changed nothing anyone was told of")
	}

	// A list that no longer names the subagent drops it, info and all.
	s.SetBackgroundWork([]string{"monitor:m1"}, nil)
	if work = s.BackgroundWork(); len(work) != 1 || work[0].ID != "monitor:m1" {
		t.Fatalf("work after a turn end's list = %+v, want only the monitor", work)
	}
	// Started again under its old id, it is a new piece of work, with nothing
	// of the old one.
	s.NoteBackground("SubagentStart", "start", "agent:a7")
	for _, w := range s.BackgroundWork() {
		if w.ID == "agent:a7" && (w.Description != "" || w.AgentType != "") {
			t.Errorf("a dropped subagent's account came back with it: %+v", w)
		}
	}

	s.NoteBackground("SessionStart", "", "")
	if work = s.BackgroundWork(); len(work) != 0 || s.BackgroundTasks() != 0 {
		t.Fatalf("a new conversation kept %+v", work)
	}
}

// TestBackgroundWorkChangesAreAnnounced covers the change a turn end's list
// makes when it names as many pieces of work as were counted, but not the
// same ones: the count is the same and the work is not.
func TestBackgroundWorkChangesAreAnnounced(t *testing.T) {
	s := &Session{}
	changes := 0
	s.OnChange = func() { changes++ }
	s.NoteBackground("PostToolUse", "start", "shell:b1")
	n := changes
	s.SetBackgroundWork([]string{"shell:b2"}, nil)
	if changes == n {
		t.Error("swapping one piece of work for another announced nothing")
	}
	n = changes
	s.SetBackgroundWork([]string{"shell:b2"}, nil)
	if changes != n {
		t.Error("a list naming exactly what was counted announced a change")
	}
}
