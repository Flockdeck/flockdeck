package server

import (
	"time"

	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/workspace"
)

// This file is the desktop side of the fan-out history panel: a per-project
// record of past fan-out jobs, kept in memory only (see
// Workspace.AddFanoutHistory), captured the moment a settled job starts
// going away for good and would otherwise be lost. It reuses the same
// reading of a pane's outcome the live summary card's outcomeOf draws in the
// webui, built here from the same sources the snapshot's own pane view
// already reads: Pane.Status, Pane.Err, waitingViews and the preview cache.

// fanoutJobView is one past fan-out job as the history panel shows it. Ago is
// computed the same way a stored conversation's own is (see conversationView
// and humanAgo), rather than the raw timestamp being left for the page to
// turn into words itself.
type fanoutJobView struct {
	ID    string              `json:"id"`
	Title string              `json:"title"`
	At    string              `json:"at"`
	Ago   string              `json:"ago"`
	Panes []fanoutJobPaneView `json:"panes"`
}

// fanoutJobPaneView is one pane's outcome inside a fanoutJobView. Kind is
// one of "done", "needs" or "failed" -- see workspace.FanoutJobPane.
type fanoutJobPaneView struct {
	Name   string `json:"name"`
	Branch string `json:"branch"`
	Task   string `json:"task"`
	Kind   string `json:"kind"`
	Detail string `json:"detail"`
}

type fanoutHistoryMsg struct {
	Type  string          `json:"type"`
	Root  string          `json:"root"`
	Items []fanoutJobView `json:"items"`
}

// captureFanoutHistory records t's job against its project the first time a
// close finds it still settled -- see Workspace.FanoutTabSettled -- and does
// nothing otherwise: a tab still working, or one an ordinary split built the
// same shape of by hand, has no finished job to remember.
//
// It is called ahead of both CloseTab and ClosePaneByID, and reads whatever
// is still in t's tree at the moment it runs. CloseTab calls it while every
// pane is still there; a settled job dismantled pane by pane instead is
// caught on the first of those closes, while the rest are still there to
// read too, which is also the last moment FanoutTabSettled's own two-or-more
// rule can still hold -- one pane on from there, it is down to the one
// closing and reads as a lone helper's tab, not a job. Either way, once a
// job is captured its tab is marked no longer delegated, the same flag
// FanoutTabSettled itself requires, so panes closing one after another out
// of what is left never captures it a second time.
//
// Must run on the workspace goroutine, which every caller reaches it from
// already, since it reads and writes pane and tab state that only that
// goroutine may touch.
func (s *Server) captureFanoutHistory(t *workspace.Tab) {
	ws := s.ws
	if !ws.FanoutTabSettled(t) {
		return
	}
	ids := t.Tree.Panes()
	job := workspace.FanoutJob{
		ID:    t.ID,
		Title: t.Title,
		At:    time.Now(),
		Panes: make([]workspace.FanoutJobPane, 0, len(ids)),
	}
	for _, id := range ids {
		p := ws.Pane(id)
		if p == nil {
			continue
		}
		kind, detail := s.fanoutOutcome(p)
		job.Panes = append(job.Panes, workspace.FanoutJobPane{
			Name:   p.Name,
			Branch: p.Branch,
			Task:   p.Task,
			Kind:   kind,
			Detail: detail,
		})
	}
	// t.Delegated is FanoutTabSettled's own gate, so clearing it here is what
	// keeps a job dismantled pane by pane from being captured again on its
	// next pane's close -- and, since nothing is left to show a card for
	// once a job has started being taken apart, it also stops the remaining
	// panes being drawn as one.
	t.Delegated = false
	if len(job.Panes) == 0 {
		return
	}
	ws.AddFanoutHistory(t.Root, job)
}

// fanoutOutcome reads a settled pane's outcome the way the webui's outcomeOf
// reads a live one: failed when the pane carries an error, needing input
// while it waits on a question or a permission prompt, done otherwise, with
// the same one-line detail that goes with each -- what the pane wants, why
// it failed, or its agent's latest reply.
func (s *Server) fanoutOutcome(p *workspace.Pane) (kind, detail string) {
	st, statusDetail := p.Status()
	kind, detail = "done", ""
	if last, ok := s.preview.get(p.ID); ok {
		detail = last.Text
	}
	if st == session.StatusWaiting {
		kind, detail = "needs", statusDetail
		if p.Sess != nil {
			if label := waitingLabel(statusDetail, p.Sess.ToolInput()); label != "" {
				detail = "Wants " + label + "."
			}
		}
	}
	// Refused outright rather than asked: nothing here is waiting on an
	// answer, but it did not finish either, so it reads as failed rather
	// than done -- see outcomeOf's own StatusBlocked case.
	if st == session.StatusBlocked {
		kind, detail = "failed", "A tool call was denied."
		if statusDetail != "" {
			detail = "A tool call was denied: " + statusDetail + "."
		}
	}
	// Closed before it finished: a fan-out's history only ever reads a
	// settled pane, but a todo step's attempt is read on any close (see
	// captureTodoStepOutcome), and "done" there ticks the step.
	switch st {
	case session.StatusWorking:
		kind, detail = "failed", "Closed while the agent was still working."
	case session.StatusStarting:
		kind, detail = "failed", "Closed before the agent had started."
	}
	if p.Err != nil {
		kind, detail = "failed", p.Err.Error()
	}
	if why, failed := p.Failed(); failed {
		kind, detail = "failed", "The process ended: "+why+"."
	}
	return kind, detail
}

// waitingLabel is outcomeOf's kindLabel in the webui, read here off the same
// ask/permission views the phone's own waiting card is built from, so a
// history entry's "needs input" row reads the same way the live one did.
func waitingLabel(tool, toolInputJSON string) string {
	ask, perm := waitingViews(tool, toolInputJSON)
	switch {
	case ask != nil:
		return "a question"
	case perm != nil:
		switch perm.Tool {
		case "Bash":
			return "a command to run"
		case "Edit", "MultiEdit", "Write":
			return "a file write"
		default:
			return "to use " + perm.Tool
		}
	default:
		return ""
	}
}

// fanoutHistory answers a window's request for a project's past fan-out
// jobs. Reading it is a plain map lookup, so unlike recents or conversations
// it needs neither a goroutine of its own nor the newest-answer bookkeeping
// those make room for a disk read to finish out of order.
func (s *Server) fanoutHistory(c *controlClient, root string) {
	msg, ok := ask(s, func() fanoutHistoryMsg {
		r := root
		if r == "" {
			r = s.ws.ActiveRoot()
		}
		jobs := s.ws.FanoutHistory(r)
		m := fanoutHistoryMsg{Type: "fanoutHistory", Root: r, Items: make([]fanoutJobView, 0, len(jobs))}
		for _, j := range jobs {
			jv := fanoutJobView{
				ID: j.ID, Title: j.Title, At: j.At.Format(time.RFC3339), Ago: humanAgo(time.Since(j.At)),
				Panes: make([]fanoutJobPaneView, 0, len(j.Panes)),
			}
			for _, p := range j.Panes {
				jv.Panes = append(jv.Panes, fanoutJobPaneView{
					Name: p.Name, Branch: p.Branch, Task: p.Task, Kind: p.Kind, Detail: p.Detail,
				})
			}
			m.Items = append(m.Items, jv)
		}
		return m
	})
	if !ok {
		return
	}
	c.sendJSON(msg)
}
