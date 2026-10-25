package workspace

import (
	"log"
	"sync"
	"time"

	"github.com/jmwri/flockdeck/internal/claudestate"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/store"
)

// A Claude pane's status is built from its lifecycle hooks, and a turn that
// starts from another session's message or calls no tool sends none, so the
// status can stay idle through work or stay working after it. Claude Code's own
// session file (see claudestate) says which it is. The service checks each
// Claude pane against it: see session.Session.Reconcile for when the file wins.
//
// Only a pane whose hooks have reported is corrected. Until they have, the status
// is a guess from the pane's output, which other code already settles; a pane
// whose hooks are not reporting at all is not one this file should be trusted
// to describe, because nothing says it is the process the file is about.
const (
	// claudeStateTick is how often the files are read.
	claudeStateTick = 2 * time.Second
	// claudeStateSettle is how long a change in the file must have stood before
	// it replaces the hooks' status.
	claudeStateSettle = 3 * time.Second
	// claudeStartSkew is how far the file's start time may be from the start of
	// the process with its pid before the file is taken to be a different
	// process's, left by a pid that was used again. That check runs only where the
	// file gives a start time and the platform can say when a process started
	// (not macOS); otherwise a file is taken as stale if its last change is from
	// before the pane's own session began, less this much.
	claudeStartSkew = 30 * time.Second
)

// claudeStateService reads the session files and reconciles the panes.
//
// The pane's own process id is the key: Flockdeck starts claude directly (on
// Windows claude.exe is the child of flockdeck.exe, checked on a launched pane),
// so Session.Pid is the pid Claude Code names its file after. The typed wake uses a
// file only when it is the pane's own (ownEntry): its conversation, and the
// process that started when the file says it did where the file gives a start time
// and the platform reports one, otherwise a last change not before the session
// began, less 30 s. The status reconcile may also use a file found by conversation
// alone, since the hooks' newer word still beats it.
type claudeStateService struct {
	// panes is the live Claude panes: the session, its conversation id.
	panes func() []claudeStatePane
	// readPid reads <pid>.json; readAll reads the folder, for the conversation
	// fallback when a pid has no file; started is when a process started.
	readPid func(pid int) (claudestate.Entry, bool)
	readAll func() []claudestate.Entry
	started func(pid int) (time.Time, bool)
	// settle is claudeStateSettle, shorter in a test.
	settle time.Duration

	stop     chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

type claudeStatePane struct {
	Sess         *session.Session
	Conversation string
}

func newClaudeStateService(panes func() []claudeStatePane) *claudeStateService {
	dir := claudestate.Dir()
	return &claudeStateService{
		panes:   panes,
		readPid: func(pid int) (claudestate.Entry, bool) { return claudestate.ReadPid(dir, pid) },
		readAll: func() []claudestate.Entry { return claudestate.Read(dir) },
		started: store.ProcessStartedAt,
		settle:  claudeStateSettle,
		stop:    make(chan struct{}),
	}
}

func (s *claudeStateService) run() {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		t := time.NewTicker(claudeStateTick)
		defer t.Stop()
		for {
			select {
			case <-s.stop:
				return
			case <-t.C:
				s.safeTick()
			}
		}
	}()
}

// Stop ends the service.
func (s *claudeStateService) Stop() {
	s.stopOnce.Do(func() { close(s.stop) })
	s.wg.Wait()
}

// safeTick is tick with a panic held: this goroutine reads files another
// program writes, and nothing above it would catch one.
func (s *claudeStateService) safeTick() {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("claude state: a check panicked: %v", r)
		}
	}()
	s.tick()
}

// tick reconciles every Claude pane once. A pane with no file of its own is left
// to its hooks.
func (s *claudeStateService) tick() {
	var all []claudestate.Entry
	readAll := false
	for _, p := range s.panes() {
		e, ok := s.entryFor(p, func() []claudestate.Entry {
			if !readAll {
				all, readAll = s.readAll(), true
			}
			return all
		})
		if ok {
			p.Sess.Reconcile(e.Busy, e.Updated, s.settle)
		}
	}
}

// entryFor finds the file that is about this pane's process, or failing that its
// conversation, for checking the status against. It is not good enough to act on
// (see ownEntry).
func (s *claudeStateService) entryFor(p claudeStatePane, all func() []claudestate.Entry) (claudestate.Entry, bool) {
	if e, found, ok := s.ownEntry(p); found {
		return e, ok
	}
	// No file of the pane's own. The conversation finds one, with no start time
	// to check it against, so a second Claude process on the same conversation
	// (the user resuming it in another terminal) can answer for a pane whose own
	// file is missing. That is rare, and it is only ever a busy/idle word that the
	// hooks' newer word still beats; the typed wake does not use it.
	return claudestate.ByConversation(all(), p.Conversation)
}

// ownEntry reads the file named for the pane's own process and nothing else. found
// says there was such a file; ok says it is the pane's: its session is the pane's
// conversation, and the process it was written by started when this one did.
// Otherwise the pid was used again, the conversation has changed under the pane (a
// clear), or the file is left from a process that is gone, and the hooks are the
// only word.
func (s *claudeStateService) ownEntry(p claudeStatePane) (e claudestate.Entry, found, ok bool) {
	pid := p.Sess.Pid()
	e, found = s.readPid(pid)
	if !found {
		return claudestate.Entry{}, false, false
	}
	if p.Conversation != "" && e.SessionID != p.Conversation {
		return claudestate.Entry{}, true, false
	}
	checked := false
	if !e.Started.IsZero() {
		if st, known := s.started(pid); known {
			d := e.Started.Sub(st)
			if d < 0 {
				d = -d
			}
			if d > claudeStartSkew {
				return claudestate.Entry{}, true, false
			}
			checked = true
		}
	}
	// Without that check, a file of the same conversation left on a pid that
	// was used again is told apart only by when it last changed: not before
	// this pane's session began.
	if !checked && e.Updated.Before(p.Sess.StartedAt().Add(-claudeStartSkew)) {
		return claudestate.Entry{}, true, false
	}
	return e, true, true
}

// claudeStatePanes lists the running Claude panes whose hooks have reported.
func (w *Workspace) claudeStatePanes() []claudeStatePane {
	w.mu.RLock()
	defer w.mu.RUnlock()
	var out []claudeStatePane
	for _, p := range w.panes {
		if p.Kind == session.KindAgent && p.Agent == "claude" && p.Sess != nil && !p.Sess.Exited() && p.Sess.HooksReported() {
			// The conversation as conversationOf has it, read here because that
			// takes the lock this holds.
			conv := p.Conversation
			if conv == "" {
				conv = p.ID
			}
			out = append(out, claudeStatePane{Sess: p.Sess, Conversation: conv})
		}
	}
	return out
}

func (w *Workspace) startClaudeState() {
	w.claudeSvc = newClaudeStateService(w.claudeStatePanes)
	w.claudeSvc.run()
}

// claudeAgentIdle is the wake candidate's AgentIdle for a pane: nil unless it is a
// Claude pane whose hooks have reported. w.mu may be held by the caller.
func (w *Workspace) claudeAgentIdle(p *Pane) func() (AgentWord, bool) {
	if p.Agent != "claude" || p.Sess == nil || !p.Sess.HooksReported() {
		return nil
	}
	conv := p.Conversation
	if conv == "" {
		conv = p.ID
	}
	return w.agentIdle(claudeStatePane{Sess: p.Sess, Conversation: conv})
}

// agentIdle is the wake service's view of a Claude pane's session file: whether
// Claude says it is idle and since when. ok is false unless the file is the
// pane's own (see ownEntry): the one named for its process, for its conversation,
// from a process that started when it did. A file found only by conversation, a
// previous process's file, or none at all is no word, and the pane is then woken
// only by a rule that has been tested, which for Claude Code there is none.
func (w *Workspace) agentIdle(p claudeStatePane) func() (AgentWord, bool) {
	return func() (AgentWord, bool) {
		svc := w.claudeSvc
		if svc == nil {
			return AgentWord{}, false
		}
		e, _, ok := svc.ownEntry(p)
		if !ok {
			return AgentWord{}, false
		}
		return AgentWord{Idle: !e.Busy, Since: e.Updated, Pid: e.Pid, SessionID: e.SessionID, From: "pid file"}, true
	}
}
