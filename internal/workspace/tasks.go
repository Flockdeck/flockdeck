package workspace

import (
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/jmwri/flockdeck/internal/baton"
	"github.com/jmwri/flockdeck/internal/hooks"
	"github.com/jmwri/flockdeck/internal/tasks"
	"github.com/jmwri/flockdeck/internal/team"
)

// This file is the workspace side of the task board: who is acting, which
// project's board it is, and the messages a change sends. The board itself, its
// rules and its file are in internal/tasks.

// taskState is what the workspace keeps for the boards.
type taskState struct {
	mu sync.Mutex
	// boards holds one Board for each board file, so that two projects whose
	// panes reach the same file share a lock.
	boards map[string]*tasks.Board
	// open makes a board for a project directory; a test replaces it.
	open func(root string) *tasks.Board
}

// boardFor is the board of the project the pane self belongs to. A project is a
// group of repositories when it has one, and its board is in the group's primary
// checkout. It must run on the workspace goroutine.
func (w *Workspace) boardFor(self string) *tasks.Board { return w.boardOf(w.rootOf(self)) }

// boardOf is the board of the project whose directory is root.
func (w *Workspace) boardOf(root string) *tasks.Board {
	if g := w.groupOf(root); g != nil && g.Primary != "" {
		root = g.Primary
	}
	key := pathKey(tasks.MainCheckout(root))
	t := &w.tasks
	t.mu.Lock()
	defer t.mu.Unlock()
	if b, ok := t.boards[key]; ok {
		return b
	}
	open := t.open
	if open == nil {
		open = tasks.Open
	}
	b := open(root)
	if t.boards == nil {
		t.boards = map[string]*tasks.Board{}
	}
	t.boards[key] = b
	return b
}

// taskActor says who the pane self is, from the roster: it must be an open agent
// pane. The role is the one the pane was started with, which the caller cannot
// change by what it sends.
func (w *Workspace) taskActor(self string) (tasks.Actor, []team.Member, error) {
	members, err := w.Roster(self)
	if err != nil {
		return tasks.Actor{}, nil, err
	}
	for _, m := range members {
		if m.Self && m.Pane == self {
			return tasks.Actor{Pane: m.Pane, Role: m.Role, Name: m.Label()}, members, nil
		}
	}
	return tasks.Actor{}, nil, fmt.Errorf("pane %s is not an agent pane, so it is not part of the team and cannot use the task board", self)
}

// TaskCounts is what the board holds, for the briefing of the pane id. It reports
// false when the pane is not an agent or the board cannot be read.
func (w *Workspace) TaskCounts(id string) (tasks.Counts, bool) {
	p := w.Pane(id)
	if p == nil || !p.IsAgent() {
		return tasks.Counts{}, false
	}
	me := tasks.Actor{Pane: p.ID, Role: p.Role, Name: p.Name}
	c, err := w.boardFor(id).Counts(me)
	return c, err == nil
}

// Task runs one operation of `flockdeck task` for the pane req.Pane, as that pane.
// It must run on the workspace goroutine.
func (w *Workspace) Task(req hooks.TaskRequest) (hooks.TaskResult, error) {
	actor, members, err := w.taskActor(req.Pane)
	if err != nil {
		return hooks.TaskResult{}, err
	}
	return w.runTask(actor, members, w.boardFor(req.Pane), req)
}

// runTask runs one operation as the actor on the board of the project whose
// roster is members. It is the one place the operations are carried out, for a
// pane and for the person at the window alike, so the rules, the limits and the
// notices are the same for both.
func (w *Workspace) runTask(actor tasks.Actor, members []team.Member, board *tasks.Board, req hooks.TaskRequest) (hooks.TaskResult, error) {
	var err error
	// A pane with no role has the manager's powers only while no pane in the
	// manager role is running.
	hasManager := tasks.HasManager(members)

	var id string
	switch req.Op {
	case hooks.TaskAdd, hooks.TaskList, hooks.TaskPrune:
	case hooks.TaskShow, hooks.TaskAssign, hooks.TaskStatus, hooks.TaskNote, hooks.TaskBranch, hooks.TaskBaton, hooks.TaskDepends, hooks.TaskEstimate, hooks.TaskTeam:
		if id, err = tasks.ParseID(req.ID); err != nil {
			return hooks.TaskResult{}, err
		}
	default:
		return hooks.TaskResult{}, fmt.Errorf("unknown task operation %q", team.PlainLabel(req.Op))
	}

	var ch tasks.Change
	switch req.Op {
	case hooks.TaskList:
		flt := tasks.Filter{All: req.All, Mine: req.Mine, Me: actor}
		if req.State != "" && !strings.EqualFold(strings.TrimSpace(req.State), "all") {
			if flt.State, err = tasks.ParseState(req.State); err != nil {
				return hooks.TaskResult{}, err
			}
		} else if req.State != "" {
			flt.All = true
		}
		if req.Role != "" {
			if flt.Role, err = tasks.CleanRole(req.Role); err != nil {
				return hooks.TaskResult{}, err
			}
		}
		list, hidden, lerr := board.List(flt)
		return hooks.TaskResult{Tasks: list, Hidden: hidden}, lerr

	case hooks.TaskPrune:
		days := req.Days
		if days == 0 {
			days = tasks.DefaultPruneDays
		}
		n, perr := board.Prune(actor, hasManager, days)
		return hooks.TaskResult{Pruned: n}, perr

	case hooks.TaskShow:
		t, gerr := board.Get(id)
		if gerr != nil {
			return hooks.TaskResult{}, gerr
		}
		return hooks.TaskResult{Task: &t}, nil

	case hooks.TaskAdd:
		if req.Role != "" {
			if _, err := w.agents().CheckRole(strings.ToLower(strings.TrimSpace(req.Role))); err != nil {
				return hooks.TaskResult{}, err
			}
		}
		ch, err = board.Add(actor, tasks.NewTask{Title: req.Title, Body: req.Body, Role: req.Role, Branch: req.Branch})

	case hooks.TaskAssign:
		// A manager is checked before the target is looked up, so a pane that may
		// not assign learns that and not something about the roster.
		if err := tasks.CheckAssign(tasks.Task{ID: id, State: tasks.Planned}, actor, hasManager); err != nil {
			return hooks.TaskResult{}, err
		}
		var as tasks.Assignee
		if as, err = tasks.ResolveAssignee(members, w.agents().RoleIDs(), req.Target); err != nil {
			return hooks.TaskResult{}, err
		}
		ch, err = board.Assign(actor, hasManager, id, as)

	case hooks.TaskStatus:
		var to tasks.State
		if to, err = tasks.ParseState(req.State); err != nil {
			return hooks.TaskResult{}, err
		}
		ch, err = board.SetState(actor, hasManager, id, to, req.Note)

	case hooks.TaskNote:
		ch, err = board.AddNote(actor, id, req.Text)

	case hooks.TaskBranch:
		ch, err = board.SetBranch(actor, hasManager, id, req.Branch)

	case hooks.TaskDepends:
		ch, err = board.SetDepends(actor, hasManager, id, req.Depends)

	case hooks.TaskEstimate:
		ch, err = board.SetEstimate(actor, hasManager, id, req.Minutes)

	case hooks.TaskTeam:
		teamID := strings.TrimSpace(req.Team)
		if teamID != "" {
			if err := w.checkTeamRoot(members, teamID); err != nil {
				return hooks.TaskResult{}, err
			}
		}
		if ch, err = board.SetTeam(actor, hasManager, id, teamID); err == nil && teamID != "" && ch.Task.OwnerPane == "" && ch.Task.OwnerRole == "" {
			// A task given a team and to nobody goes to the team's manager.
			if as, aerr := tasks.ResolveAssignee(members, w.agents().RoleIDs(), teamID); aerr == nil {
				if ach, aerr := board.Assign(actor, hasManager, id, as); aerr == nil {
					ch = ach
				}
			}
		}

	case hooks.TaskBaton:
		bid := strings.TrimSpace(req.Baton)
		if !baton.ValidID(bid) {
			return hooks.TaskResult{}, fmt.Errorf("%q is not a baton id; `flockdeck baton list` shows them", team.PlainLabel(req.Baton))
		}
		if path := batonPath(bid); path != "" {
			if _, err := os.Stat(path); err != nil {
				return hooks.TaskResult{}, fmt.Errorf("no baton %s on this machine; `flockdeck baton list` shows them", bid)
			}
		}
		ch, err = board.SetBaton(actor, hasManager, id, bid)
	}
	if err != nil {
		return hooks.TaskResult{}, err
	}
	res := hooks.TaskResult{Task: &ch.Task, Told: w.sendTaskNotices(ch, actor, members)}
	if ch.Overrode != "" {
		res.Warning = fmt.Sprintf("%s was in progress under %s, who no longer owns it; assign a task before its owner starts it", ch.Task.ID, team.PlainLabel(ch.Overrode))
	}
	return res, nil
}

// sendTaskNotices writes the messages a change calls for, from Flockdeck, into
// the inboxes of the panes concerned, and says how many it wrote. How a pane
// is told it has mail is the same as for any message: see HookNotice.
func (w *Workspace) sendTaskNotices(ch tasks.Change, actor tasks.Actor, members []team.Member) int {
	n := 0
	for _, note := range tasks.Notices(ch, actor, members) {
		if _, ok := w.team.inbox.SendSystem(note.Pane, note.Text); ok {
			n++
		}
	}
	return n
}

// checkTeamRoot checks that id names a running pane of the project that is the
// root of a team: a pane no other agent started.
func (w *Workspace) checkTeamRoot(members []team.Member, id string) error {
	for _, m := range members {
		if m.Pane != id {
			continue
		}
		if m.Exited() {
			return fmt.Errorf("pane %s has ended, so it cannot run a team", id)
		}
		if p := w.Pane(id); p == nil || p.Parent != "" {
			return fmt.Errorf("pane %s was started by another agent; a task goes to the root of a team, the pane that started it", id)
		}
		return nil
	}
	return fmt.Errorf("no pane %s in this project; `flockdeck team` lists the panes", team.PlainLabel(id))
}

// TaskOpening is the opening prompt for a helper started for a task of the
// board of the project pane parent is in, and the task's id as the board
// spells it. It must run on the workspace goroutine.
func (w *Workspace) TaskOpening(parent, taskID string) (prompt, id string, err error) {
	if id, err = tasks.ParseID(taskID); err != nil {
		return "", "", err
	}
	t, err := w.boardFor(parent).Get(id)
	if err != nil {
		return "", "", err
	}
	prompt = fmt.Sprintf("Work on task %s: %s", t.ID, t.Title)
	if t.Body != "" {
		prompt += "\n\n" + t.Body
	}
	prompt += fmt.Sprintf("\n\nRun `flockdeck task show %s` for the notes and history, and `flockdeck task status %s in-progress` when you start.", t.ID, t.ID)
	return prompt, t.ID, nil
}

// GiveTaskToPane gives a task to a pane and to the team the pane is in, as the
// pane parent that started it. The team is the root above the pane, the pane
// itself when it has no parent. It must run on the workspace goroutine.
func (w *Workspace) GiveTaskToPane(parent, taskID, pane string) error {
	root := pane
	for i := 0; i < 64; i++ {
		p := w.Pane(root)
		if p == nil || p.Parent == "" {
			break
		}
		root = p.Parent
	}
	base := hooks.TaskRequest{Auth: hooks.Auth{Pane: parent}, ID: taskID}
	assign := base
	assign.Op, assign.Target = hooks.TaskAssign, pane
	if _, err := w.Task(assign); err != nil {
		return err
	}
	give := base
	give.Op, give.Team = hooks.TaskTeam, root
	_, err := w.Task(give)
	return err
}
