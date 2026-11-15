package server

import (
	"errors"

	"github.com/jmwri/flockdeck/internal/structured"
)

// The conversations of a structured pane's folder, and moving the pane between
// them (API 1.36, cap structuredConversations). Like the other structured
// commands they are for a window on this machine: a window reached through the
// relay is refused, and so is a viewer.
//
// structuredSessions lists the folder's past conversations as the agent says
// them, newest first and at most structured.SessionsMax: id, a one-line title
// cut and cleaned, the time it last changed, and whether the pane is on it. It is
// refused for an agent that does not list them (capabilities.listSessions in the
// push says which). structuredResume restarts the pane on the conversation
// sessionId names, which must be one of that list as the agent gives it now; a
// session of another folder is never resumed. structuredNewConversation restarts
// the pane on a clean session and keeps the old entries above a line saying so.
//
// Both end a running turn and drop the prompts waiting behind it, so they are
// refused while one is running unless confirm is true. The pane keeps its id.
// Nothing the agent said about a failure is passed on.

const (
	structuredNoSessions     = "This agent does not list its past conversations"
	structuredSessionsFailed = "The agent could not list its conversations"
	structuredNoSuchSession  = "That conversation is not one of this folder's. Look at the list again and choose once more"
	structuredSessionCurrent = "The pane is on that conversation already"
	structuredSessionBusy    = "A turn is running, a permission card is open, a rewind is out or prompts are waiting. Confirm to end it all"
	structuredSessionMoveErr = "The pane could not be moved to another conversation"
	structuredSessionNoID    = "Name the conversation to resume"
	structuredMoveInFlight   = "This pane is already being moved, went back or forked a moment ago. Wait for it to finish and look at the list again"
)

// sessionView is one conversation of the list.
type sessionView struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Updated string `json:"updated,omitempty"`
	Current bool   `json:"current,omitempty"`
}

// structuredConversation runs structuredSessions, structuredResume and
// structuredNewConversation. It must run on the workspace goroutine; the agent
// is asked on a goroutine of its own.
func (s *Server) structuredConversation(c *controlClient, cmd command) {
	rt := s.ws.StructuredRuntime(cmd.ID)
	if rt == nil {
		refuse(c, cmd, errStructured(structuredNotPane))
		return
	}
	if cmd.Cmd == "structuredResume" && cmd.SessionID == "" {
		refuse(c, cmd, errStructured(structuredSessionNoID))
		return
	}
	if cmd.Cmd == "structuredSessions" {
		go s.doSessions(c, cmd, rt)
		return
	}
	// One move at a time, and not while a rewind or fork of the pane runs.
	if _, busy := forking.LoadOrStore(cmd.ID, struct{}{}); busy {
		refuse(c, cmd, errStructured(structuredMoveInFlight))
		return
	}
	go func() {
		defer forking.Delete(cmd.ID)
		s.doMove(c, cmd, rt)
	}()
}

func (s *Server) doSessions(c *controlClient, cmd command, rt *structured.Runtime) {
	items, err := rt.Sessions()
	if err != nil {
		refuse(c, cmd, errStructured(sessionRefusal(err)))
		return
	}
	views := make([]sessionView, 0, len(items))
	for _, it := range items {
		views = append(views, sessionView{ID: it.ID, Title: it.Title, Updated: it.Updated, Current: it.Current})
	}
	c.sendJSON(teamResultMsg{Type: "teamResult", Req: cmd.Req, Op: cmd.Cmd, OK: true, Sessions: views})
}

type moveResult int

const (
	moveDone moveResult = iota
	moveFailed
	moveBusy
)

func (s *Server) doMove(c *controlClient, cmd command, rt *structured.Runtime) {
	// move runs on the workspace goroutine. Without confirm it looks at Busy again
	// there, right before the restart, so a prompt that started a turn since the
	// first check is not dropped unasked.
	var move func() moveResult
	if cmd.Cmd == "structuredResume" {
		id, err := rt.PickSession(cmd.SessionID, cmd.Confirm)
		if err != nil {
			refuse(c, cmd, errStructured(sessionRefusal(err)))
			return
		}
		move = func() moveResult {
			if !cmd.Confirm && rt.Busy() {
				return moveBusy
			}
			if s.ws.ResumeStructuredConversation(cmd.ID, id) {
				return moveDone
			}
			return moveFailed
		}
	} else {
		if !cmd.Confirm && rt.Busy() {
			refuse(c, cmd, errStructured(structuredSessionBusy))
			return
		}
		move = func() moveResult {
			if !cmd.Confirm && rt.Busy() {
				return moveBusy
			}
			if s.ws.NewStructuredConversation(cmd.ID) {
				return moveDone
			}
			return moveFailed
		}
	}
	res, ok := ask(s, move)
	if !ok {
		return
	}
	if res == moveBusy {
		refuse(c, cmd, errStructured(structuredSessionBusy))
		return
	}
	if res != moveDone {
		refuse(c, cmd, errStructured(structuredSessionMoveErr))
		return
	}
	c.sendJSON(teamResultMsg{Type: "teamResult", Req: cmd.Req, Op: cmd.Cmd, OK: true})
}

// sessionRefusal is the fixed text for an error of the runtime.
func sessionRefusal(err error) string {
	switch {
	case errors.Is(err, structured.ErrNotRunning):
		return structuredSetNotReady
	case errors.Is(err, structured.ErrSessionsNotOffered):
		return structuredNoSessions
	case errors.Is(err, structured.ErrNoSession):
		return structuredNoSuchSession
	case errors.Is(err, structured.ErrSessionCurrent):
		return structuredSessionCurrent
	case errors.Is(err, structured.ErrSessionBusy):
		return structuredSessionBusy
	}
	return structuredSessionsFailed
}
