package server

import (
	"strconv"

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
	s.ws.SetRenameNotifier(func(r workspace.RenameInfo) { s.noticeDesk(renameNotice(r), false) })
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
	// Each structured pane's Flockdeck MCP server gets a token for that pane
	// alone. The run's shared token is never handed to one.
	s.ws.UseMCPToken(hookSrv.IssuePaneToken)
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
			r, err := s.ws.PaneNamed(req)
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

// renameNotice is the sentence the desk is shown when a pane names another. The
// actor is the pane id the command reported, which another pane could have given;
// the names are the panes' own words and are cut.
func renameNotice(r workspace.RenameInfo) string {
	who := "pane " + r.Actor + " (" + r.ActorName + ")"
	if r.N > 1 {
		return who + " renamed pane " + r.Target + " " + strconv.Itoa(r.N) + " times: from " + strconv.Quote(r.Was) + ", now " + strconv.Quote(r.Now)
	}
	if r.Now == "" {
		return who + " cleared the name of pane " + r.Target + ", " + strconv.Quote(r.Was)
	}
	return who + " renamed pane " + r.Target + " from " + strconv.Quote(r.Was) + " to " + strconv.Quote(r.Now)
}
