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
	// process's, left by a pid that was used again.
	claudeStartSkew = 30 * time.Second
)

// claudeStateService reads the session files and reconciles the panes.
//
// The pane's own process id is the key: Flockdeck starts claude directly (on
// Windows claude.exe is the child of flockdeck.exe, checked on a launched pane),
// so Session.Pid is the pid Claude Code names its file after. A file is used only
// when it is also the pane's conversation and the process that started when the
// file says it did.
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

// entryFor finds the file that is about this pane's process, or false.
func (s *claudeStateService) entryFor(p claudeStatePane, all func() []claudestate.Entry) (claudestate.Entry, bool) {
	pid := p.Sess.Pid()
	if e, ok := s.readPid(pid); ok {
		// The file named for the pid is about this conversation, and about a
		// process that started when this one did; otherwise the pid was used
		// again, or the conversation has changed under the pane (a clear), and the
		// hooks are the only word.
		if p.Conversation != "" && e.SessionID != p.Conversation {
			return claudestate.Entry{}, false
		}
		if !e.Started.IsZero() {
			if st, ok := s.started(pid); ok {
				d := e.Started.Sub(st)
				if d < 0 {
					d = -d
				}
				if d > claudeStartSkew {
					return claudestate.Entry{}, false
				}
			}
		}
		return e, true
	}
	return claudestate.ByConversation(all(), p.Conversation)
}

// claudeStatePanes lists the running Claude panes whose hooks have reported.
func (w *Workspace) claudeStatePanes() []claudeStatePane {
	w.mu.RLock()
	var ps []*Pane
	for _, p := range w.panes {
		if p.Kind == session.KindAgent && p.Agent == "claude" && p.Sess != nil && !p.Sess.Exited() && p.Sess.HooksReported() {
			ps = append(ps, p)
		}
	}
	w.mu.RUnlock()
	out := make([]claudeStatePane, 0, len(ps))
	for _, p := range ps {
		out = append(out, claudeStatePane{Sess: p.Sess, Conversation: w.conversationOf(p)})
	}
	return out
}

func (w *Workspace) startClaudeState() {
	w.claudeSvc = newClaudeStateService(w.claudeStatePanes)
	w.claudeSvc.run()
}
