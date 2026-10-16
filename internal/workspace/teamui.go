package workspace

import (
	"fmt"

	"github.com/jmwri/flockdeck/internal/hooks"
	"github.com/jmwri/flockdeck/internal/tasks"
	"github.com/jmwri/flockdeck/internal/team"
)

// This file is the window's side of the team: what the Team panel reads and does
// for a project, as the person at the window and not as a pane. The panes' own
// side is in team.go and tasks.go, and everything here goes through the same
// functions (runTask, deliver, the board and the inbox store), so the rules,
// limits and notices are one set.
//
// The person is no pane and has no role. Tasks see them as tasks.UserActor and
// messages as team.UserSender. Every function here must run on the workspace
// goroutine.

// teamRoot resolves the directory the window names to the root of an open
// project. Empty is the project on screen.
func (w *Workspace) teamRoot(root string) (string, error) {
	if root == "" {
		root = w.activeRoot
	}
	open, ok := w.openRootFor(root)
	if !ok {
		return "", fmt.Errorf("no project is open at %s", root)
	}
	return open, nil
}

// TeamRoot is the root of the open project the window's directory names; empty
// names the project on screen. It is how a window keeps to one spelling of a
// project.
func (w *Workspace) TeamRoot(root string) (string, error) { return w.teamRoot(root) }

// TeamMembers lists the agent panes of the project at root, in the order the
// tabs show them. No member is marked Self.
func (w *Workspace) TeamMembers(root string) ([]team.Member, error) {
	open, err := w.teamRoot(root)
	if err != nil {
		return nil, err
	}
	return w.rosterOf(open, ""), nil
}

// TeamBoard is the task board of the project at root.
func (w *Workspace) TeamBoard(root string) (*tasks.Board, error) {
	open, err := w.teamRoot(root)
	if err != nil {
		return nil, err
	}
	return w.boardOf(open), nil
}

// UserTask runs one operation of the task board for the project at root as the
// person at the window. The pane in req is ignored. Who may do what, the limits
// and the messages sent to the panes are those of the same operation from a pane
// in the manager role, except that the person is not one of the panes told.
func (w *Workspace) UserTask(root string, req hooks.TaskRequest) (hooks.TaskResult, error) {
	open, err := w.teamRoot(root)
	if err != nil {
		return hooks.TaskResult{}, err
	}
	req.Pane = ""
	return w.runTask(tasks.UserActor(), w.rosterOf(open, ""), w.boardOf(open), req)
}

// UserMessage sends a message from the person at the window to a pane, a name or
// a role of the project at root. The text never reaches a terminal: the pane is
// told it has mail by the same notice, when its agent can be, and reads the text
// with `flockdeck inbox`.
func (w *Workspace) UserMessage(root, target, text string) (hooks.MsgResult, error) {
	open, err := w.teamRoot(root)
	if err != nil {
		return hooks.MsgResult{}, err
	}
	return w.deliver(w.rosterOf(open, ""), team.UserSender(), target, text)
}

// TeamMessages lists every message in the inboxes of the project at root's agent
// panes, oldest first, with the roster they were found by.
func (w *Workspace) TeamMessages(root string) ([]team.Message, []team.Member, error) {
	members, err := w.TeamMembers(root)
	if err != nil {
		return nil, nil, err
	}
	panes := make([]string, len(members))
	for i, m := range members {
		panes[i] = m.Pane
	}
	return w.team.inbox.Messages(panes), members, nil
}

// UserReadMessage marks one message in the inbox of pane read, as the person at
// the window. The pane must be one of the project at root, so a window cannot
// reach an inbox of another project. The agent then no longer sees the message
// as unread.
func (w *Workspace) UserReadMessage(root, pane string, id int) (team.Message, error) {
	members, err := w.TeamMembers(root)
	if err != nil {
		return team.Message{}, err
	}
	for _, m := range members {
		if m.Pane == pane {
			msg, err := w.team.inbox.Read(pane, id)
			if err != nil {
				return team.Message{}, fmt.Errorf("no message #%d in the inbox of pane %s", id, pane)
			}
			return msg, nil
		}
	}
	return team.Message{}, fmt.Errorf("pane %q is not an agent pane of this project", pane)
}
