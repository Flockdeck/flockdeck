package workspace

import (
	"sync"
	"time"

	"github.com/jmwri/flockdeck/internal/claudestate"
	"github.com/jmwri/flockdeck/internal/session"
)

// A Claude pane's status is built from its lifecycle hooks, and a turn that
// starts from another session's message or calls no tool sends none, so the
// status can stay idle through work or stay working after it. Claude Code's own
// session file (see claudestate) says which it is. The service checks each
// Claude pane against it: see session.Session.Reconcile for when the file wins.
const (
	// claudeStateTick is how often the files are read.
	claudeStateTick = 2 * time.Second
	// claudeStateSettle is how long a change in the file must have stood before
	// it replaces the hooks' status.
	claudeStateSettle = 3 * time.Second
)

// claudeStateService reads the session files and reconciles the panes.
type claudeStateService struct {
	// panes is the live Claude panes: the session, its conversation id.
	panes func() []claudeStatePane
	read  func() []claudestate.Entry
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
		panes:  panes,
		read:   func() []claudestate.Entry { return claudestate.Read(dir) },
		settle: claudeStateSettle,
		stop:   make(chan struct{}),
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
				s.tick()
			}
		}
	}()
}

// Stop ends the service.
func (s *claudeStateService) Stop() {
	s.stopOnce.Do(func() { close(s.stop) })
	s.wg.Wait()
}

// tick reconciles every Claude pane once. A pane with no file is left to its
// hooks.
func (s *claudeStateService) tick() {
	panes := s.panes()
	if len(panes) == 0 {
		return
	}
	entries := s.read()
	if len(entries) == 0 {
		return
	}
	for _, p := range panes {
		if e, ok := claudestate.Find(entries, p.Sess.Pid(), p.Conversation); ok {
			p.Sess.Reconcile(e.Busy, e.Updated, s.settle)
		}
	}
}

// claudeStatePanes lists the running Claude panes.
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
