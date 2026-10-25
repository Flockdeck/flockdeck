package server

import (
	"strconv"
	"strings"
	"time"

	"github.com/jmwri/flockdeck/internal/hooks"
	"github.com/jmwri/flockdeck/internal/team"
	"github.com/jmwri/flockdeck/internal/workspace"
)

// This file is the desktop side of `flockdeck team`, `flockdeck msg`,
// `flockdeck inbox` and `flockdeck task`. Each request names the pane it comes from, and the
// workspace refuses one whose pane is not open; see workspace.Roster. Every
// handler runs on the workspace goroutine, like the spawn and close handlers.

// installTeamHandlers lets a pane list its project's agents, exchange messages
// with them and use the project's task board.
func (s *Server) installTeamHandlers() {
	hookSrv := s.ws.HookServer()
	if hookSrv == nil {
		return
	}
	type outcome[T any] struct {
		res T
		err error
	}
	// Mail is told to a working agent in the reply to its next prompt or tool
	// call. An idle agent is woken by the workspace's wake service with one
	// fixed typed line. HookNotice is safe from any goroutine.
	hookSrv.SetNoticeHandler(s.ws.HookNotice)
	hookSrv.SetTeamHandler(func(req hooks.TeamRequest) (hooks.TeamResult, error) {
		out, ok := ask(s, func() outcome[[]team.Member] {
			m, err := s.ws.Roster(req.Pane)
			return outcome[[]team.Member]{m, err}
		})
		if !ok {
			return hooks.TeamResult{}, errShuttingDown
		}
		return hooks.TeamResult{Members: out.res}, out.err
	})
	hookSrv.SetMsgHandler(func(req hooks.MsgRequest) (hooks.MsgResult, error) {
		out, ok := ask(s, func() outcome[hooks.MsgResult] {
			r, err := s.ws.SendMessage(req.Pane, req.Target, req.Text)
			return outcome[hooks.MsgResult]{r, err}
		})
		if !ok {
			return hooks.MsgResult{}, errShuttingDown
		}
		// A Team panel that is open shows the message.
		s.Wake()
		return out.res, out.err
	})
	hookSrv.SetInboxHandler(func(req hooks.InboxRequest) (hooks.InboxResult, error) {
		out, ok := ask(s, func() outcome[[]team.Message] {
			if req.Read > 0 {
				m, err := s.ws.ReadMessage(req.Pane, req.Read)
				return outcome[[]team.Message]{[]team.Message{m}, err}
			}
			m, err := s.ws.Inbox(req.Pane, req.All)
			return outcome[[]team.Message]{m, err}
		})
		if !ok {
			return hooks.InboxResult{}, errShuttingDown
		}
		if req.Read > 0 {
			s.Wake()
		}
		return hooks.InboxResult{Messages: out.res}, out.err
	})
	hookSrv.SetPaneNameHandler(func(req hooks.PaneNameRequest) (hooks.PaneNameResult, error) {
		out, ok := ask(s, func() outcome[hooks.PaneNameResult] {
			target := strings.TrimSpace(req.Target)
			if target == "" {
				target = req.Pane
			}
			was := paneDisplayName(s.ws, target)
			r, err := s.ws.PaneNamed(req)
			if err == nil && r.Pane != req.Pane && s.renameToast(r.Pane) {
				// A pane named another: the person at the window is told who, which pane and what it is called now.
				s.noticeDesk(renameNotice(req.Pane, paneDisplayName(s.ws, req.Pane), r.Pane, was, r.Label), false)
			}
			return outcome[hooks.PaneNameResult]{r, err}
		})
		if !ok {
			return hooks.PaneNameResult{}, errShuttingDown
		}
		s.Wake()
		return out.res, out.err
	})
	hookSrv.SetTaskHandler(func(req hooks.TaskRequest) (hooks.TaskResult, error) {
		out, ok := ask(s, func() outcome[hooks.TaskResult] {
			r, err := s.ws.Task(req)
			return outcome[hooks.TaskResult]{r, err}
		})
		if !ok {
			return hooks.TaskResult{}, errShuttingDown
		}
		if req.Op != hooks.TaskList && req.Op != hooks.TaskShow {
			s.Wake()
		}
		return out.res, out.err
	})
}

// renameToastWindow is how long after a pane's rename was shown to the person that
// another rename of it is not shown again: a loop of renames is one notice, not a flood.
const renameToastWindow = time.Minute

// renameToast says whether the person is told of a rename of pane id now, and notes
// that they were. It runs on the workspace goroutine, so it takes no lock of its own.
func (s *Server) renameToast(id string) bool {
	now := time.Now()
	if at, ok := s.renameToasts[id]; ok && now.Sub(at) < renameToastWindow {
		return false
	}
	if s.renameToasts == nil || len(s.renameToasts) > 256 {
		s.renameToasts = map[string]time.Time{}
	}
	s.renameToasts[id] = now
	return true
}

// paneDisplayName is how the pane is called now: its label, else its name, cut.
func paneDisplayName(ws *workspace.Workspace, id string) string {
	if p := ws.Pane(id); p != nil {
		return team.PlainText(team.Member{Name: p.Name, Peer: ws.PeerNameOf(p), Called: ws.LabelOf(p)}.Label(), 40)
	}
	return id
}

// renameNotice is the sentence the desk is shown when a pane names another. The
// actor is the pane id the command reported, which another pane could have given;
// the names are the panes' own words and are cut.
func renameNotice(actor, actorName, target, was, label string) string {
	who := "pane " + actor + " (" + actorName + ")"
	if label == "" {
		return who + " cleared the name of pane " + target + ", " + strconv.Quote(was)
	}
	return who + " renamed pane " + target + " from " + strconv.Quote(was) + " to " + strconv.Quote(label)
}
