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
			res, ok := ask(s, func() hooks.CloseResult {
				panes, tabs := s.ws.CloseFinishedPanes()
				return hooks.CloseResult{Panes: panes, Tabs: tabs}
			})
			if !ok {
				return hooks.CloseResult{}, errShuttingDown
			}
			return res, nil
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
			if s.ws.Pane(req.Target) == nil {
				return outcome{err: fmt.Errorf("no pane %q is open here", req.Target)}
			}
			// A pane still working is left alone unless the caller insists:
			// naming the wrong id must not be able to cut off work in
			// progress silently. CloseFinishedPanes applies the same rule to
			// every pane it considers; see Workspace.PaneFinished.
			if !req.Force && !s.ws.PaneFinished(req.Target) {
				return outcome{err: fmt.Errorf("pane %q is still working; pass -force to close it anyway, or wait until it is idle", req.Target)}
			}
			// Mirrors the "closePane" control command: a settled fan-out job
			// closed pane by pane is captured on the first of those closes,
			// while the rest are still there to read.
			if tid := s.ws.TabIDOf(req.Target); tid != "" {
				if t := s.ws.Tab(tid); t != nil {
					s.captureFanoutHistory(t)
				}
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
