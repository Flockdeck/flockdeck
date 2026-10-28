package server

import (
	"errors"
	"sync"

	"github.com/jmwri/flockdeck/internal/structured"
	"github.com/jmwri/flockdeck/internal/workspace"
)

// Going back to an earlier prompt of a structured pane, and forking the
// conversation at one (API 1.24). Both name the prompt by the id of its entry in
// the conversation, taken from the push's rewindPoints; a place in the list is
// never used, since the list changes as turns end. Like the other structured
// commands they are for a window on this machine and refused to a viewer.
//
// Both also carry rewindToken, the one the push they were chosen from had. It
// names the pane's runtime and how many times it has gone back, so a window that
// is out of date (the pane was restarted, or went back from another window) is
// refused instead of acting on entry ids that now mean something else.
//
// structuredRewind removes the prompt and everything after it from the
// conversation and from the agent's own history, so it asks for confirm: true
// and refuses without it. It answers with the prompt's text, for the window to
// put back in the prompt box; cut is true when the text was longer than is sent
// back. structuredFork starts a new pane beside the original with the
// conversation up to that point, or all of it when entry is empty, and answers
// with the new pane's id. The original is not changed. Neither passes on
// anything the agent said about a failure.

const (
	structuredNeedConfirm   = "Confirm to go back: the prompt and everything after it, including messages other agents sent after it, are removed from the conversation and from the agent"
	structuredNoRewind      = "This agent cannot go back to an earlier point"
	structuredRewindBusy    = "Stop the turn and clear the waiting prompts first"
	structuredNoPoint       = "There is no earlier point before that prompt"
	structuredRewindFailed  = "The agent could not go back"
	structuredNoFork        = "This agent cannot fork a conversation"
	structuredForkFailed    = "The conversation could not be forked"
	structuredRewindEntry   = "Name the prompt to go back to"
	structuredRewindStale   = "The conversation changed since this window last showed it. Look at the list again and choose once more"
	structuredRewindUnknown = "The agent did not answer in time, so Flockdeck restarted the pane. The conversation so far is kept above, and the agent starts a new session"
	structuredTooManyForks  = "This pane already has 4 forks open. Close one first"
	structuredRewindPointsN = 200
)

// buildStructuredRewind adds the rewind points and the token the push carries.
func buildStructuredRewind(m *structuredMsg, sn structured.Snapshot) {
	pts := sn.RewindPoints
	if len(pts) > structuredRewindPointsN {
		pts = pts[len(pts)-structuredRewindPointsN:]
	}
	m.RewindPoints = append([]string(nil), pts...)
	m.RewindToken = sn.RewindToken
}

// forking holds the panes with a fork or a rewind under way, so that one click
// sent twice does not make two panes.
var forking sync.Map

// structuredRewind runs structuredRewind and structuredFork. It must run on the
// workspace goroutine; the agent is asked on a goroutine of its own.
func (s *Server) structuredRewind(c *controlClient, cmd command) {
	rt := s.ws.StructuredRuntime(cmd.ID)
	if rt == nil {
		refuse(c, cmd, errStructured(structuredNotPane))
		return
	}
	fork := cmd.Cmd == "structuredFork"
	if !fork {
		if cmd.Entry == "" {
			refuse(c, cmd, errStructured(structuredRewindEntry))
			return
		}
		if !cmd.Confirm {
			refuse(c, cmd, errStructured(structuredNeedConfirm))
			return
		}
	}
	if _, busy := forking.LoadOrStore(cmd.ID, struct{}{}); busy {
		refuse(c, cmd, errStructured(structuredRewindBusy))
		return
	}
	go func() {
		defer forking.Delete(cmd.ID)
		if fork {
			s.doFork(c, cmd, rt)
			return
		}
		text, err := rt.Rewind(cmd.Entry, cmd.RewindToken)
		if errors.Is(err, structured.ErrRewindUnknown) {
			// Whether the agent went back is not known, so the window and the
			// agent could disagree about what the conversation is. The pane
			// starts over; its history stays.
			ask(s, func() bool { return s.ws.RestartPaneByID(cmd.ID) })
		}
		if err != nil {
			refuse(c, cmd, errStructured(rewindRefusal(err, false)))
			return
		}
		cut, wasCut := clipStructured(text, structuredRewindTextCut)
		c.sendJSON(teamResultMsg{Type: "teamResult", Req: cmd.Req, Op: cmd.Cmd, OK: true, Text: cut, Cut: wasCut})
	}()
}

// structuredRewindTextCut is the most of a prompt's text that comes back. A
// longer prompt is cut; the person's own box then holds the start of it.
const structuredRewindTextCut = 16 << 10

func (s *Server) doFork(c *controlClient, cmd command, rt *structured.Runtime) {
	from, err := rt.ForkSource(cmd.Entry, cmd.RewindToken)
	if err != nil {
		refuse(c, cmd, errStructured(rewindRefusal(err, true)))
		return
	}
	type out struct {
		id  string
		err error
	}
	r, ok := ask(s, func() out {
		id, err := s.ws.SpawnFork(cmd.ID, from)
		return out{id, err}
	})
	if !ok {
		return
	}
	if errors.Is(r.err, workspace.ErrTooManyForks) {
		refuse(c, cmd, errStructured(structuredTooManyForks))
		return
	}
	if r.err != nil {
		refuse(c, cmd, errStructured(structuredForkFailed))
		return
	}
	c.sendJSON(teamResultMsg{Type: "teamResult", Req: cmd.Req, Op: cmd.Cmd, OK: true, Pane: r.id})
}

// rewindRefusal is the fixed text for an error of the runtime.
func rewindRefusal(err error, fork bool) string {
	switch {
	case errors.Is(err, structured.ErrNotRunning):
		return structuredSetNotReady
	case errors.Is(err, structured.ErrRewindNotOffered):
		return structuredNoRewind
	case errors.Is(err, structured.ErrForkNotOffered):
		return structuredNoFork
	case errors.Is(err, structured.ErrRewindStale):
		return structuredRewindStale
	case errors.Is(err, structured.ErrRewindUnknown):
		return structuredRewindUnknown
	case errors.Is(err, structured.ErrRewindBusy):
		return structuredRewindBusy
	case errors.Is(err, structured.ErrNoPoint):
		return structuredNoPoint
	}
	if fork {
		return structuredForkFailed
	}
	return structuredRewindFailed
}
