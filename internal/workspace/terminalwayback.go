package workspace

import (
	"errors"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/session"
)

// ErrNoTerminalWayBack is what RestartPaneAsTerminal says of a pane that is
// not a structured agent pane that has stopped working, or whose agent has no
// terminal entry.
var ErrNoTerminalWayBack = errors.New("that pane has no terminal version of its agent to restart as")

// TerminalWayBack is the id of the terminal entry a pane could be restarted as,
// or empty. It is offered for a structured pane that is not running: one that
// failed to start (the agent is unavailable, the version is outside the tested
// series, the directory is gone, or structured panes are off), one whose agent
// needs signing in, and one whose agent has ended. It is never offered for a
// pane that is working, and not for an agent that has no terminal sibling
// (Spec.Terminal), where there is nothing to restart as.
func (w *Workspace) TerminalWayBack(p *Pane) string {
	if p == nil || !p.IsAgent() {
		return ""
	}
	if p.Err == nil {
		sr, ok := p.setRuntime().(*structuredRuntime)
		if !ok {
			return ""
		}
		if st, _ := sr.Status(); st != session.StatusBlocked && st != session.StatusExited {
			return ""
		}
	}
	cat := w.agents()
	id := p.Agent
	if id == "" {
		// An empty agent is whichever one the pane would be opened with today, as
		// startPane reads it; one saved as a terminal stays one.
		if p.savedTransport != TransportStructured && p.setRuntime() == nil {
			return ""
		}
		id = w.defaultAgentFor(p.Root)
	}
	spec, ok := cat.Find(id)
	if !ok || spec.Runner != agent.RunnerACP {
		return ""
	}
	if t := cat.TerminalOf(id); t != id {
		return t
	}
	return ""
}

// RestartPaneAsTerminal ends a structured pane's agent, if one is running, and
// starts the terminal entry of the same agent in the same pane. The pane keeps
// its place, name and directory. It does not carry the structured session over:
// the terminal agent starts a conversation of its own, and the structured
// transcript goes with the old runtime. The model is cleared, because a model
// id the structured entry offered may not be one the terminal agent takes.
//
// It reports ErrNoTerminalWayBack for a pane that is not there, is not a
// structured pane that has stopped, or whose agent has no terminal entry. A
// terminal agent that is not installed leaves the pane in the unavailable state
// a terminal pane shows, which is a terminal pane's to fix.
func (w *Workspace) RestartPaneAsTerminal(id string) error {
	p := w.Pane(id)
	term := w.TerminalWayBack(p)
	if term == "" {
		return ErrNoTerminalWayBack
	}
	if sr, ok := p.setRuntime().(*structuredRuntime); ok {
		_ = sr.Close()
	}
	runtimeMu.Lock()
	p.rt = nil
	runtimeMu.Unlock()
	w.mu.Lock()
	p.Agent, p.Model = term, ""
	p.Err = nil
	p.savedTransport = TransportTerminal
	p.savedSession = ""
	p.forkFrom = nil
	w.mu.Unlock()
	p.history = nil
	w.startPane(p, false)
	w.wake()
	return nil
}
