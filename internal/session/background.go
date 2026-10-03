package session

import (
	"log/slog"
	"sort"
	"strings"
	"time"
)

// BackgroundUnverifiedGrace is how long a piece of background work can go
// without any sign that it is still running -- its start, Claude Code's list
// of what is still running at the end of a turn, a subagent's transcript being
// written to -- before it is marked unverified. An idle agent whose work is
// all unverified no longer counts as working (see workspace.PaneActivity),
// though the work is still counted and listed: nothing is dropped without
// evidence that it has ended.
//
// It is the one setting behind that choice: 30 minutes outlasts a monitor's
// default timeout, and a longer or shorter wait is a change to this line.
const BackgroundUnverifiedGrace = 30 * time.Minute

// backgroundEndedShown is how long work that has stopped being counted is
// kept, with what showed it had ended, for the header to say so.
const backgroundEndedShown = 2 * time.Minute

// maxBackgroundEnded bounds the ended work kept at once.
const maxBackgroundEnded = 8

// backgroundNow is the clock background work is timed by; a test replaces it.
var backgroundNow = time.Now

// BackgroundInfo is what was said of a piece of background work beyond its
// id -- hooks.BackgroundInfo, which this package does not import. Every field
// is optional and already fit to show.
type BackgroundInfo struct {
	Type, AgentType, Description, Command string
}

// merge is b with every empty field filled from older: what a later event
// leaves unsaid does not undo what an earlier one said.
func (b BackgroundInfo) merge(older BackgroundInfo) BackgroundInfo {
	if b.Type == "" {
		b.Type = older.Type
	}
	if b.AgentType == "" {
		b.AgentType = older.AgentType
	}
	if b.Description == "" {
		b.Description = older.Description
	}
	if b.Command == "" {
		b.Command = older.Command
	}
	return b
}

// BackgroundWork is one piece of background work an agent has left running,
// as far as its hooks have said.
type BackgroundWork struct {
	// ID is the work's name as counted, kind and id: "shell:b1".
	ID string
	BackgroundInfo
	// Since is when Flockdeck first heard of it: its start, or, when Listed,
	// the end of a turn that said it was still running.
	Since time.Time
	// Listed says it was first heard of in a turn end's list of what is
	// still running rather than from its start, so Since is when it was
	// first seen, not when it started.
	Listed bool
	// Confirmed is the latest sign that it is still running, and
	// ConfirmedBy what that sign was.
	Confirmed   time.Time
	ConfirmedBy string
	// Unverified says nothing has shown it to be running for
	// BackgroundUnverifiedGrace, as of the last RefreshBackground.
	Unverified bool
}

// BackgroundEnded is a piece of background work that has lately stopped being
// counted, and why.
type BackgroundEnded struct {
	BackgroundWork
	// Evidence is what showed it had ended, worded for the header:
	// "ended: Claude Code's task notification".
	Evidence string
	At       time.Time
}

// Evidence for an end that a hook event reports, by the event.
func hookEndEvidence(event string) string {
	switch event {
	case "SubagentStop":
		return "ended: the subagent's SubagentStop hook"
	case "UserPromptSubmit":
		return "ended: Claude Code's task notification"
	case "PostToolUse":
		return "ended: stopped by the agent (KillShell/TaskStop)"
	}
	return "ended: " + event
}

// endedLocked moves w to the ended list with evidence. s.mu is held.
func (s *Session) endedLocked(w BackgroundWork, evidence string, at time.Time) {
	slog.Debug("background work ended", "work", w.ID, "evidence", evidence)
	s.backgroundEnded = append(s.backgroundEnded, BackgroundEnded{BackgroundWork: w, Evidence: evidence, At: at})
	if n := len(s.backgroundEnded); n > maxBackgroundEnded {
		s.backgroundEnded = append([]BackgroundEnded(nil), s.backgroundEnded[n-maxBackgroundEnded:]...)
	}
}

// NoteBackground records a piece of background work starting (op "start") or
// ending (op "end") under id, as hooks.Event reports it. A SessionStart from a
// new or cleared conversation forgets all of it: work started in one
// conversation is not waited on by the next. An empty op or id changes nothing.
//
// An id is a kind and the id Claude Code gave the work, "shell:b1" or
// "agent:a7". An end ends whatever was counted under the same id of any kind:
// the <task-notification> Claude Code sends when a task ends does not say
// which kind it was in a form worth relying on, and so names none ("task:").
func (s *Session) NoteBackground(event, op, id string) {
	s.NoteBackgroundWork(event, op, id, BackgroundInfo{})
}

// NoteBackgroundWork is NoteBackground with what the start said of the work,
// which is kept with it until it ends. A start heard again keeps the time of
// the first.
func (s *Session) NoteBackgroundWork(event, op, id string, info BackgroundInfo) {
	now := backgroundNow()
	s.mu.Lock()
	changed := false
	switch {
	case event == "SessionStart":
		changed = len(s.background) > 0 || len(s.backgroundEnded) > 0
		s.background = nil
		s.backgroundEnded = nil
	case id == "":
	case op == "start":
		if s.background == nil {
			s.background = map[string]BackgroundWork{}
		}
		w, had := s.background[id]
		if !had {
			w = BackgroundWork{ID: id, Since: now}
		}
		w.BackgroundInfo = info.merge(w.BackgroundInfo)
		w.Confirmed, w.ConfirmedBy, w.Unverified = now, "its start", false
		s.background[id] = w
		changed = true
	case op == "end":
		changed = s.endLocked(id, hookEndEvidence(event), now)
	}
	s.mu.Unlock()
	if changed {
		s.changed()
	}
}

// endLocked ends whatever is counted under id, or under the same id of any
// kind, with evidence. s.mu is held.
func (s *Session) endLocked(id, evidence string, now time.Time) bool {
	bare := backgroundBareID(id)
	ended := false
	for k, w := range s.background {
		if k == id || backgroundBareID(k) == bare {
			delete(s.background, k)
			s.endedLocked(w, evidence, now)
			ended = true
		}
	}
	return ended
}

// EndBackgroundWork stops counting the work under id (of any kind) on the
// strength of evidence, which the header shows for a while. It reports
// whether anything was counted under id.
func (s *Session) EndBackgroundWork(id, evidence string) bool {
	s.mu.Lock()
	ended := s.endLocked(id, evidence, backgroundNow())
	s.mu.Unlock()
	if ended {
		s.changed()
	}
	return ended
}

// EndBackgroundWorkSeen stops counting the work under id (of any kind) on the
// strength of evidence that Claude Code recorded at at -- but only work that
// evidence can be about: work first heard of before at, with no sign of
// running since. An end older than the work is about an earlier task under
// the same id, or a subagent since resumed; an end older than a later sign of
// running -- a turn end's list naming the work, say -- has been overtaken by
// it. Both are decided here, under the lock, so a sign that lands while the
// evidence was being read still wins. It reports whether anything ended.
func (s *Session) EndBackgroundWorkSeen(id string, at time.Time, evidence string) bool {
	if at.IsZero() {
		return false
	}
	bare := backgroundBareID(id)
	now := backgroundNow()
	s.mu.Lock()
	ended := false
	for k, w := range s.background {
		if backgroundBareID(k) != bare || !at.After(w.Since) || !at.After(w.Confirmed) {
			continue
		}
		delete(s.background, k)
		s.endedLocked(w, evidence, now)
		ended = true
	}
	s.mu.Unlock()
	if ended {
		s.changed()
	}
	return ended
}

// ConfirmBackgroundWork records a sign, seen at at, that the work under id
// is still running. A sign older than the latest already had changes
// nothing.
func (s *Session) ConfirmBackgroundWork(id string, at time.Time, by string) bool {
	s.mu.Lock()
	w, ok := s.background[id]
	changed := false
	if ok && at.After(w.Confirmed) {
		w.Confirmed, w.ConfirmedBy = at, by
		if w.Unverified && backgroundNow().Sub(at) < BackgroundUnverifiedGrace {
			w.Unverified = false
		}
		s.background[id] = w
		changed = true
	}
	s.mu.Unlock()
	if changed {
		s.changed()
	}
	return changed
}

// SetBackground replaces what is counted as the agent's background work with
// ids, named the way NoteBackground's are: what Claude Code itself says is
// still in flight at the end of a turn. It is the whole truth where the
// starts and ends counted one at a time can miss something -- a command that
// ended in the middle of a turn, one stopped from Claude Code's own task
// list -- so it replaces them rather than adding to them.
func (s *Session) SetBackground(ids []string) {
	s.SetBackgroundWork(ids, nil)
}

// SetBackgroundWork is SetBackground with what the list says of each piece of
// work, by id. Work already counted keeps when it was first heard of and what
// was said of it before; work first heard of here is marked Listed. All of it
// is confirmed running, and what the list leaves out has ended.
func (s *Session) SetBackgroundWork(ids []string, info map[string]BackgroundInfo) {
	now := backgroundNow()
	s.mu.Lock()
	old := s.background
	var next map[string]BackgroundWork
	used := map[string]bool{}
	for _, id := range ids {
		if id == "" {
			continue
		}
		if next == nil {
			next = map[string]BackgroundWork{}
		}
		w, had := old[id]
		from := id
		if !had {
			// The same work counted under another kind: a start named by its
			// kind, listed now with none, or the other way about.
			bare := backgroundBareID(id)
			for k, o := range old {
				if backgroundBareID(k) == bare {
					w, had, from = o, true, k
					break
				}
			}
			w.ID = id
		}
		if !had {
			w = BackgroundWork{ID: id, Since: now, Listed: true}
		}
		used[from] = true
		w.BackgroundInfo = info[id].merge(w.BackgroundInfo)
		w.Confirmed, w.ConfirmedBy, w.Unverified = now, "Claude Code's list at the end of a turn", false
		next[id] = w
	}
	// A list that names just what was counted, saying nothing new of it,
	// changes nothing shown but when it was last confirmed.
	changed := len(next) != len(old)
	for k, w := range next {
		o, ok := old[k]
		if !ok || o.BackgroundInfo != w.BackgroundInfo || o.Unverified {
			changed = true
		}
	}
	for k, w := range old {
		if !used[k] {
			s.endedLocked(w, "ended: not in Claude Code's list of running work at the end of a turn", now)
		}
	}
	s.background = next
	s.mu.Unlock()
	if changed {
		s.changed()
	}
}

// RefreshBackground marks the work nothing has shown to be running for
// BackgroundUnverifiedGrace as unverified (and the rest as not), and forgets
// ended work once it has been shown long enough, as of now. Nothing counted is
// dropped. It reports whether anything changed, which is then announced.
func (s *Session) RefreshBackground(now time.Time) bool {
	s.mu.Lock()
	changed := false
	for k, w := range s.background {
		unverified := now.Sub(w.Confirmed) >= BackgroundUnverifiedGrace
		if unverified != w.Unverified {
			if unverified {
				slog.Debug("background work unverified", "work", w.ID, "lastConfirmed", w.Confirmed, "by", w.ConfirmedBy)
			}
			w.Unverified = unverified
			s.background[k] = w
			changed = true
		}
	}
	kept := s.backgroundEnded[:0]
	for _, e := range s.backgroundEnded {
		if now.Sub(e.At) < backgroundEndedShown {
			kept = append(kept, e)
		}
	}
	if len(kept) != len(s.backgroundEnded) {
		changed = true
	}
	s.backgroundEnded = kept
	s.mu.Unlock()
	if changed {
		s.changed()
	}
	return changed
}

// backgroundBareID is a background work id without the kind in front of it.
func backgroundBareID(id string) string {
	if _, bare, ok := strings.Cut(id, ":"); ok {
		return bare
	}
	return id
}

// BackgroundWork is the background work the agent has left running, as far
// as its hooks have said, oldest first.
func (s *Session) BackgroundWork() []BackgroundWork {
	s.mu.RLock()
	out := make([]BackgroundWork, 0, len(s.background))
	for _, w := range s.background {
		out = append(out, w)
	}
	s.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Since.Equal(out[j].Since) {
			return out[i].Since.Before(out[j].Since)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// BackgroundEnded is the work that has stopped being counted within the last
// backgroundEndedShown, oldest first, with what showed it had ended.
func (s *Session) BackgroundEnded() []BackgroundEnded {
	now := backgroundNow()
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []BackgroundEnded
	for _, e := range s.backgroundEnded {
		if now.Sub(e.At) < backgroundEndedShown {
			out = append(out, e)
		}
	}
	return out
}

// BackgroundTasks is how many pieces of background work the agent has left
// running, as far as its hooks have said.
func (s *Session) BackgroundTasks() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.background)
}

// BackgroundVerified is how many of them have been shown to be running within
// BackgroundUnverifiedGrace, as of the last RefreshBackground.
func (s *Session) BackgroundVerified() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := 0
	for _, w := range s.background {
		if !w.Unverified {
			n++
		}
	}
	return n
}
