package server

import (
	"fmt"

	"github.com/jmwri/flockdeck/internal/hooks"
)

// This file is the desktop side of `flockdeck close`: how a pane running
// inside Flockdeck asks the application to close a different pane, or every
// finished one, the way a coordinating agent cleans up helpers whose work is
// done without a person having to find them and press Ctrl+Shift+W. See
// hooks.CloseRequest for the wire shape and installSpawnHandler for the
// pattern this follows.

// installCloseHandler lets an agent close another pane, or every finished
// one, by running `flockdeck close` inside its pane.
func (s *Server) installCloseHandler() {
	hookSrv := s.ws.HookServer()
	if hookSrv == nil {
		return
	}
	hookSrv.SetCloseHandler(func(req hooks.CloseRequest) (hooks.CloseResult, error) {
		if req.Finished {
			// Only the caller's own project: a pane of another project is not
			// the caller's to close, so it is not even looked at.
			type finished struct {
				res hooks.CloseResult
				err error
			}
			res, ok := ask(s, func() finished {
				if s.ws.Pane(req.Pane) == nil {
					return finished{err: fmt.Errorf("no pane %q is open here", req.Pane)}
				}
				panes, tabs, locked := s.ws.CloseFinishedPanesOf(req.Pane)
				return finished{res: hooks.CloseResult{Panes: panes, Tabs: tabs, Locked: locked}}
			})
			if !ok {
				return hooks.CloseResult{}, errShuttingDown
			}
			return res.res, res.err
		}

		// Closing itself has no place going through this: the reply to this
		// very request would be racing the process it just asked to end, and
		// self-management -- ending its own turn, say -- has nothing to do
		// with what this command is for, which is one pane cleaning up
		// another.
		if req.Target == req.Pane {
			return hooks.CloseResult{}, fmt.Errorf("a pane can't close itself this way; end the turn or ask the user to close it")
		}

		type outcome struct {
			res hooks.CloseResult
			err error
		}
		out, ok := ask(s, func() outcome {
			// A pane of another project is refused the same way as one that is
			// not there, so the answer does not say whether it exists.
			if !s.ws.PaneInProject(req.Pane, req.Target) {
				return outcome{err: fmt.Errorf("no pane %q is open here", req.Target)}
			}
			// Checked before the working rule, and not something -force gets
			// past: the user locked it to keep it, which is theirs to undo.
			if s.ws.PaneLocked(req.Target) {
				return outcome{err: fmt.Errorf("pane %q is locked, so it can't be closed; -force does not override a lock. Ask the user to unlock it", req.Target)}
			}
			// A pane still working is left alone unless the caller insists:
			// naming the wrong id must not be able to cut off work in
			// progress silently. CloseFinishedPanes applies the same rule to
			// every pane it considers; see Workspace.PaneFinished.
			if !req.Force && !s.ws.PaneFinished(req.Target) {
				return outcome{err: fmt.Errorf("pane %q is still working; pass -force to close it anyway, or wait until it is idle", req.Target)}
			}
			if !s.ws.ClosePaneByID(req.Target) {
				return outcome{err: fmt.Errorf("no pane %q is open here", req.Target)}
			}
			return outcome{res: hooks.CloseResult{Closed: true}}
		})
		if !ok {
			return hooks.CloseResult{}, errShuttingDown
		}
		return out.res, out.err
	})
}
