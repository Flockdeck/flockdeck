package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jmwri/flockdeck/internal/hooks"
	"github.com/jmwri/flockdeck/internal/tasks"
	"github.com/jmwri/flockdeck/internal/team"
	"github.com/jmwri/flockdeck/internal/workspace"
)

// This file is the desktop side of the Team panel: a project's roster, task
// board and messages, read for the window and changed by the person at it.
//
// The person acts as tasks.UserActor and team.UserSender. Every change goes
// through Workspace.UserTask, UserMessage and UserReadMessage, which run the
// operations the panes' own `flockdeck task` and `flockdeck msg` run, so who may
// do what, the size limits and the notices to the panes are not repeated here.
// What is here is the shape of the answers, the checks on what a window sends,
// and the subscription that keeps an open panel current.
//
// The panel is for the machine Flockdeck runs on. A window reached through the
// relay is refused every command here, and the remote protocol gains nothing.

// Limits on what the panel is sent. They bound a push, which goes out whenever
// anything in the project changes.
const (
	// maxTeamFinished is how many done or cancelled tasks a push lists; taskList
	// has all of them.
	maxTeamFinished = 100
	// maxTeamMessages is how many messages a push lists, newest first.
	maxTeamMessages = 100
	// maxTeamMessageList is the most a messageList may ask for.
	maxTeamMessageList = 200
	// teamTextCut is how much of a message a list carries. Longer messages are
	// marked cut, and msgList with an id has the whole text.
	teamTextCut = 600
	// teamInterval is the shortest gap between two pushes to the panel. The
	// state push is held to a tenth of a second and the panel does more work
	// for each, so a team in full flow is shown at most twice a second.
	teamInterval = 500 * time.Millisecond
)

// teamDeskOnly is what a window reached through the relay is told.
const teamDeskOnly = "The Team panel is used on the machine Flockdeck runs on, not from a window reached through the relay"

// teamActorView is who did something, as the panel draws it. User is the person
// at the window, whom the panel shows as "you".
type teamActorView struct {
	Name string `json:"name"`
	Role string `json:"role,omitempty"`
	Pane string `json:"pane,omitempty"`
	User bool   `json:"user,omitempty"`
}

func actorView(a tasks.Actor) teamActorView {
	return teamActorView{Name: team.PlainLabel(a.Name), Role: a.Role, Pane: a.Pane, User: a.User}
}

// teamMoveView is a move the person may make to a task.
type teamMoveView struct {
	To       string `json:"to"`
	NeedNote bool   `json:"needNote,omitempty"`
}

// teamNoteView is one note in a task's thread, and teamEventView one entry in
// its history.
type teamNoteView struct {
	Author teamActorView `json:"author"`
	Time   string        `json:"time"`
	Text   string        `json:"text"`
}

type teamEventView struct {
	Actor teamActorView `json:"actor"`
	Time  string        `json:"time"`
	What  string        `json:"what"`
}

// teamTaskView is a task as a card shows it. The detail fields are set only when
// one task is asked for.
type teamTaskView struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	State string `json:"state"`
	// Owner is the words for who it is for ("any worker", "worker api"), and the
	// parts it is made of are kept apart for the panel to link.
	Owner     string `json:"owner"`
	OwnerRole string `json:"ownerRole,omitempty"`
	OwnerPane string `json:"ownerPane,omitempty"`
	// OwnerOpen says the owner pane is on the roster, so a click can go to it.
	OwnerOpen bool          `json:"ownerOpen,omitempty"`
	Branch    string        `json:"branch,omitempty"`
	Notes     int           `json:"notes"`
	Creator   teamActorView `json:"creator"`
	Updated   string        `json:"updated"`
	// Moves are the moves the person may make now, by the rules table.
	Moves []teamMoveView `json:"moves"`
	// CanAssign and CanEdit say the person may assign the task and change its
	// branch: both are the manager's, and only while the task is open.
	CanAssign bool `json:"canAssign,omitempty"`
	CanEdit   bool `json:"canEdit,omitempty"`

	Body        string          `json:"body,omitempty"`
	Baton       string          `json:"baton,omitempty"`
	Created     string          `json:"created,omitempty"`
	NoteList    []teamNoteView  `json:"noteList,omitempty"`
	HistoryList []teamEventView `json:"historyList,omitempty"`
}

// teamTaskRef is the task a roster row names as the pane's current one.
type teamTaskRef struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	State string `json:"state"`
}

// teamRosterView is one agent pane of the project.
type teamRosterView struct {
	Pane   string `json:"pane"`
	Name   string `json:"name"`
	Peer   string `json:"peer,omitempty"`
	Role   string `json:"role,omitempty"`
	Agent  string `json:"agent,omitempty"`
	Model  string `json:"model,omitempty"`
	Status string `json:"status"`
	// Activity is what the pane is drawn as doing, as everywhere else.
	Activity workspace.Activity `json:"activity"`
	Branch   string             `json:"branch,omitempty"`
	// Finished says Close finished panes would close it.
	Finished bool `json:"finished,omitempty"`
	Locked   bool `json:"locked,omitempty"`
	// Unread is how many messages wait in its inbox, and Open how many open tasks
	// are assigned to it.
	Unread int `json:"unread,omitempty"`
	Open   int `json:"open,omitempty"`
	// Task is its current task: the one in progress, else the one in review, else
	// the first assigned.
	Task *teamTaskRef `json:"task,omitempty"`
}

// teamMessageView is one message. To is the pane it was delivered to.
type teamMessageView struct {
	ID     int           `json:"id"`
	From   teamActorView `json:"from"`
	System bool          `json:"system,omitempty"`
	To     string        `json:"to"`
	ToName string        `json:"toName,omitempty"`
	ToRole string        `json:"toRole,omitempty"`
	Time   string        `json:"time"`
	Text   string        `json:"text"`
	Cut    bool          `json:"cut,omitempty"`
	Read   bool          `json:"read,omitempty"`
}

// teamCounts is the roster and the board in numbers, for the tab labels.
type teamCounts struct {
	Panes    int            `json:"panes"`
	Live     int            `json:"live"`
	Tasks    map[string]int `json:"tasks"`
	Open     int            `json:"open"`
	Messages int            `json:"messages"`
	Unread   int            `json:"unread"`
}

// teamMsg is the Team panel's whole picture of a project: what a teamSnapshot is
// answered with and what a subscribed panel is pushed.
type teamMsg struct {
	Type    string `json:"type"`
	Root    string `json:"root"`
	Project string `json:"project"`
	// HasManager says a live pane is in the manager role.
	HasManager bool             `json:"hasManager"`
	Roster     []teamRosterView `json:"roster"`
	Counts     teamCounts       `json:"counts"`
	// Tasks are the open ones and the most recently changed finished ones, oldest
	// id first; FinishedHidden counts the finished ones left out.
	Tasks          []teamTaskView `json:"tasks"`
	FinishedHidden int            `json:"finishedHidden,omitempty"`
	// BoardError is why the board could not be read, if it could not.
	BoardError string            `json:"boardError,omitempty"`
	Messages   []teamMessageView `json:"messages"`
	// MessagesHidden counts the older messages left out.
	MessagesHidden int `json:"messagesHidden,omitempty"`
}

// teamTasksMsg answers taskList, and teamMessagesMsg answers msgList.
type teamTasksMsg struct {
	Type   string         `json:"type"`
	Root   string         `json:"root"`
	State  string         `json:"state"`
	Tasks  []teamTaskView `json:"tasks"`
	Hidden int            `json:"hidden,omitempty"`
}

type teamMessagesMsg struct {
	Type     string            `json:"type"`
	Root     string            `json:"root"`
	Messages []teamMessageView `json:"messages"`
	Hidden   int               `json:"hidden,omitempty"`
}

// teamResultMsg answers a command that changes something, or any that was
// refused. Error is a sentence for the panel to show beside what was asked.
type teamResultMsg struct {
	Type    string        `json:"type"`
	Req     string        `json:"req,omitempty"`
	Op      string        `json:"op"`
	OK      bool          `json:"ok"`
	Error   string        `json:"error,omitempty"`
	Task    *teamTaskView `json:"task,omitempty"`
	Told    int           `json:"told,omitempty"`
	Pruned  int           `json:"pruned,omitempty"`
	Sent    int           `json:"sent,omitempty"`
	Warning string        `json:"warning,omitempty"`
	// Pane and Message name the message a mark-read changed.
	Pane    string `json:"pane,omitempty"`
	Message int    `json:"message,omitempty"`
}

// cutAtRune cuts s to at most n bytes on a rune boundary and says whether it cut.
func cutAtRune(s string, n int) (string, bool) {
	if len(s) <= n {
		return s, false
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n], true
}

// openMemberPanes is the set of panes on the roster.
func rosterPanes(members []team.Member) map[string]bool {
	out := make(map[string]bool, len(members))
	for _, m := range members {
		out[m.Pane] = true
	}
	return out
}

// taskViewOf describes a task for the person. full adds the body, the notes and
// the history.
func taskViewOf(t tasks.Task, members []team.Member, hasManager, full bool) teamTaskView {
	user := tasks.UserActor()
	v := teamTaskView{
		ID: t.ID, Title: t.Title, State: string(t.State),
		Owner: tasks.OwnerLabel(t), OwnerRole: t.OwnerRole, OwnerPane: t.OwnerPane,
		Branch: t.Branch, Notes: len(t.Notes), Creator: actorView(t.Creator),
		Updated: t.Updated.Format(time.RFC3339),
		Moves:   []teamMoveView{},
	}
	for _, m := range members {
		if m.Pane == t.OwnerPane && t.OwnerPane != "" {
			v.OwnerOpen = true
			// What the pane is called now, which may differ from the name it had when
			// the task was given to it.
			v.Owner = tasks.OwnerLabel(tasks.Task{OwnerPane: t.OwnerPane, OwnerRole: m.Role, OwnerName: m.Label()})
		}
	}
	for _, mv := range tasks.Moves(t, user, hasManager) {
		v.Moves = append(v.Moves, teamMoveView{To: string(mv.To), NeedNote: mv.NeedNote})
	}
	v.CanAssign = tasks.CheckAssign(t, user, hasManager) == nil
	v.CanEdit = tasks.CheckEdit(t, user, hasManager) == nil
	if full {
		v.Body, v.Baton = t.Body, t.Baton
		v.Created = t.Created.Format(time.RFC3339)
		for _, n := range t.Notes {
			v.NoteList = append(v.NoteList, teamNoteView{Author: actorView(n.Author), Time: n.Time.Format(time.RFC3339), Text: n.Text})
		}
		for _, e := range t.History {
			v.HistoryList = append(v.HistoryList, teamEventView{Actor: actorView(e.Actor), Time: e.Time.Format(time.RFC3339), What: e.What})
		}
	}
	return v
}

// messageViewOf describes a message. members names the panes it went to.
func messageViewOf(m team.Message, members []team.Member, cut int) teamMessageView {
	v := teamMessageView{
		ID: m.ID, To: m.To, Time: m.Time.Format(time.RFC3339), Read: m.Read, System: m.System(),
		From: teamActorView{Name: team.PlainLabel(m.From.Name), Role: m.From.Role, Pane: m.From.Pane, User: m.From.User},
	}
	v.Text, v.Cut = m.Text, false
	if cut > 0 {
		v.Text, v.Cut = cutAtRune(m.Text, cut)
	}
	for _, mem := range members {
		if mem.Pane == m.To {
			v.ToName, v.ToRole = mem.Label(), mem.Role
		}
	}
	return v
}

// teamPayload reads the project at root. It must run on the workspace goroutine.
func (s *Server) teamPayload(root string) (teamMsg, error) {
	ws := s.ws
	root, err := ws.TeamRoot(root)
	if err != nil {
		return teamMsg{}, err
	}
	members, err := ws.TeamMembers(root)
	if err != nil {
		return teamMsg{}, err
	}
	msg := teamMsg{Type: "team", Root: root, HasManager: tasks.HasManager(members),
		Roster: []teamRosterView{}, Tasks: []teamTaskView{}, Messages: []teamMessageView{},
		Counts: teamCounts{Tasks: map[string]int{}}}
	for _, p := range ws.Projects() {
		for _, m := range p.Members {
			if m.Root == root {
				msg.Project = p.Name
			}
		}
		if p.Root == root && msg.Project == "" {
			msg.Project = p.Name
		}
	}

	var board []tasks.Task
	if b, err := ws.TeamBoard(root); err != nil {
		msg.BoardError = err.Error()
	} else if board, _, err = b.List(tasks.Filter{All: true}); err != nil {
		msg.BoardError = err.Error()
	}

	// The board, with the finished tasks cut to the newest.
	var finished []tasks.Task
	for _, t := range board {
		msg.Counts.Tasks[string(t.State)]++
		if t.State.Open() {
			msg.Counts.Open++
			msg.Tasks = append(msg.Tasks, taskViewOf(t, members, msg.HasManager, false))
		} else {
			finished = append(finished, t)
		}
	}
	if len(finished) > maxTeamFinished {
		slices.SortStableFunc(finished, func(a, b tasks.Task) int { return b.Updated.Compare(a.Updated) })
		msg.FinishedHidden = len(finished) - maxTeamFinished
		finished = finished[:maxTeamFinished]
	}
	for _, t := range finished {
		msg.Tasks = append(msg.Tasks, taskViewOf(t, members, msg.HasManager, false))
	}
	slices.SortStableFunc(msg.Tasks, func(a, b teamTaskView) int { return taskNumber(a.ID) - taskNumber(b.ID) })

	// The messages, newest first.
	all, _, err := ws.TeamMessages(root)
	if err != nil {
		return teamMsg{}, err
	}
	msg.Counts.Messages = len(all)
	for _, m := range all {
		if !m.Read {
			msg.Counts.Unread++
		}
	}
	unread := map[string]int{}
	for _, m := range all {
		if !m.Read {
			unread[m.To]++
		}
	}
	slices.Reverse(all)
	if len(all) > maxTeamMessages {
		msg.MessagesHidden = len(all) - maxTeamMessages
		all = all[:maxTeamMessages]
	}
	for _, m := range all {
		msg.Messages = append(msg.Messages, messageViewOf(m, members, teamTextCut))
	}

	// The roster, each pane with its current task.
	for _, m := range members {
		rv := teamRosterView{
			Pane: m.Pane, Name: m.Label(), Role: m.Role, Agent: m.Agent, Model: m.Model, Status: m.Status,
			Unread: unread[m.Pane],
		}
		if m.Peer != "" && m.Name != m.Peer {
			rv.Peer = m.Name
		}
		if p := ws.Pane(m.Pane); p != nil {
			rv.Activity = workspace.PaneActivity(p)
			rv.Branch = p.Branch
			rv.Locked = p.Locked
			rv.Finished = ws.PaneFinished(m.Pane)
		}
		var cur *tasks.Task
		rank := func(t tasks.Task) int {
			switch t.State {
			case tasks.InProgress:
				return 0
			case tasks.InReview:
				return 1
			case tasks.Assigned:
				return 2
			case tasks.Blocked:
				return 3
			}
			return 4
		}
		for i, t := range board {
			if t.OwnerPane != m.Pane || !t.State.Open() {
				continue
			}
			rv.Open++
			if cur == nil || rank(t) < rank(*cur) {
				cur = &board[i]
			}
		}
		if cur != nil {
			rv.Task = &teamTaskRef{ID: cur.ID, Title: cur.Title, State: string(cur.State)}
		}
		msg.Roster = append(msg.Roster, rv)
		msg.Counts.Panes++
		if m.Status != "exited" {
			msg.Counts.Live++
		}
	}
	return msg, nil
}

func taskNumber(id string) int {
	var n int
	fmt.Sscanf(id, "T%d", &n)
	return n
}

// teamCommand answers the commands of the Team panel. A window reached through
// the relay is refused all of them.
func (s *Server) teamCommand(c *controlClient, cmd command) {
	if c.remote {
		c.sendJSON(teamResultMsg{Type: "teamResult", Req: cmd.Req, Op: cmd.Cmd, Error: teamDeskOnly})
		c.notify(teamDeskOnly, true)
		return
	}
	switch cmd.Cmd {
	case "teamSnapshot":
		s.teamSnapshot(c, cmd)
	case "teamWatch":
		s.teamWatch(c, cmd)
	case "teamUnwatch":
		s.do(func() { c.teamRoot, c.teamSent = "", nil })
	case "taskList":
		s.taskList(c, cmd)
	case "taskGet":
		s.teamTask(c, cmd, hooks.TaskRequest{Op: hooks.TaskShow, ID: cmd.ID})
	case "taskAdd":
		s.teamTask(c, cmd, hooks.TaskRequest{Op: hooks.TaskAdd, Title: cmd.Title, Body: cmd.Spec, Role: cmd.Role, Branch: cmd.Branch})
	case "taskAssign":
		s.teamTask(c, cmd, hooks.TaskRequest{Op: hooks.TaskAssign, ID: cmd.ID, Target: cmd.Target})
	case "taskStatus":
		s.teamTask(c, cmd, hooks.TaskRequest{Op: hooks.TaskStatus, ID: cmd.ID, State: cmd.State, Note: cmd.Text})
	case "taskNote":
		s.teamTask(c, cmd, hooks.TaskRequest{Op: hooks.TaskNote, ID: cmd.ID, Text: cmd.Text})
	case "taskBranch":
		s.teamTask(c, cmd, hooks.TaskRequest{Op: hooks.TaskBranch, ID: cmd.ID, Branch: cmd.Branch})
	case "taskPrune":
		s.teamTask(c, cmd, hooks.TaskRequest{Op: hooks.TaskPrune, Days: cmd.Size})
	case "msgList":
		s.msgList(c, cmd)
	case "msgSend":
		s.msgSend(c, cmd)
	case "msgRead":
		s.msgRead(c, cmd)
	}
}

// refuse answers a team command that cannot be done, to the window that sent it.
func refuse(c *controlClient, cmd command, err error) {
	c.sendJSON(teamResultMsg{Type: "teamResult", Req: cmd.Req, Op: cmd.Cmd, Error: err.Error()})
}

// teamSnapshot answers with the whole picture of the project.
func (s *Server) teamSnapshot(c *controlClient, cmd command) {
	type out struct {
		msg teamMsg
		err error
	}
	r, ok := ask(s, func() out {
		m, err := s.teamPayload(cmd.Root)
		return out{m, err}
	})
	if !ok {
		return
	}
	if r.err != nil {
		refuse(c, cmd, r.err)
		return
	}
	c.sendJSON(r.msg)
}

// teamWatch subscribes the window to the project: it is sent the picture now and
// again whenever the project's roster, board or messages change, at most every
// teamInterval. One project at a time; watching another replaces it.
func (s *Server) teamWatch(c *controlClient, cmd command) {
	s.do(func() {
		m, err := s.teamPayload(cmd.Root)
		if err != nil {
			c.teamRoot, c.teamSent = "", nil
			refuse(c, cmd, err)
			return
		}
		c.teamRoot = m.Root
		data, err := json.Marshal(m)
		if err != nil {
			return
		}
		c.teamSent = data
		c.send(data)
	})
}

// pushTeam sends every subscribed window the picture of its project when it has
// changed. It must run on the workspace goroutine, and is called with each state
// push; the picture is built at most once every teamInterval, and a push that
// came too soon is made when the interval is up.
func (s *Server) pushTeam() {
	watching := false
	for _, c := range s.greetedClients() {
		if c.teamRoot != "" {
			watching = true
		}
	}
	if !watching {
		return
	}
	if wait := teamInterval - time.Since(s.teamAt); wait > 0 {
		if !s.teamArmed {
			s.teamArmed = true
			time.AfterFunc(wait, func() {
				s.do(func() {
					s.teamArmed = false
					s.pushTeam()
				})
			})
		}
		return
	}
	s.teamAt = time.Now()
	built := map[string][]byte{}
	for _, c := range s.greetedClients() {
		if c.teamRoot == "" {
			continue
		}
		data, ok := built[c.teamRoot]
		if !ok {
			if m, err := s.teamPayload(c.teamRoot); err == nil {
				data, _ = json.Marshal(m)
			}
			built[c.teamRoot] = data
		}
		if data == nil || bytes.Equal(data, c.teamSent) {
			continue
		}
		c.teamSent = data
		c.send(data)
	}
}

// taskList answers with the tasks in a state, or every task with state "all".
// An empty state is the open tasks.
func (s *Server) taskList(c *controlClient, cmd command) {
	type out struct {
		msg teamTasksMsg
		err error
	}
	r, ok := ask(s, func() out {
		var o out
		members, err := s.ws.TeamMembers(cmd.Root)
		if err != nil {
			o.err = err
			return o
		}
		b, err := s.ws.TeamBoard(cmd.Root)
		if err != nil {
			o.err = err
			return o
		}
		flt := tasks.Filter{}
		switch st := strings.ToLower(strings.TrimSpace(cmd.State)); st {
		case "":
		case "all":
			flt.All = true
		default:
			if flt.State, err = tasks.ParseState(st); err != nil {
				o.err = err
				return o
			}
		}
		list, hidden, err := b.List(flt)
		if err != nil {
			o.err = err
			return o
		}
		hasManager := tasks.HasManager(members)
		o.msg = teamTasksMsg{Type: "tasks", Root: cmd.Root, State: cmd.State, Hidden: hidden, Tasks: []teamTaskView{}}
		for _, t := range list {
			o.msg.Tasks = append(o.msg.Tasks, taskViewOf(t, members, hasManager, false))
		}
		return o
	})
	if !ok {
		return
	}
	if r.err != nil {
		refuse(c, cmd, r.err)
		return
	}
	c.sendJSON(r.msg)
}

// teamTask runs one task operation as the person and answers with the task.
func (s *Server) teamTask(c *controlClient, cmd command, req hooks.TaskRequest) {
	type out struct {
		res     hooks.TaskResult
		view    *teamTaskView
		err     error
		changed bool
	}
	r, ok := ask(s, func() out {
		res, err := s.ws.UserTask(cmd.Root, req)
		o := out{res: res, err: err}
		if err != nil {
			return o
		}
		if res.Task != nil {
			members, _ := s.ws.TeamMembers(cmd.Root)
			v := taskViewOf(*res.Task, members, tasks.HasManager(members), true)
			o.view = &v
		}
		o.changed = req.Op != hooks.TaskShow
		return o
	})
	if !ok {
		return
	}
	if r.err != nil {
		refuse(c, cmd, r.err)
		return
	}
	c.sendJSON(teamResultMsg{Type: "teamResult", Req: cmd.Req, Op: cmd.Cmd, OK: true, Task: r.view,
		Told: r.res.Told, Pruned: r.res.Pruned, Warning: r.res.Warning})
	if r.changed {
		// The person is watching for it, and so is every other window.
		s.wakeAsked()
	}
}

// msgList answers with the project's messages, newest first, or with the whole
// text of the one message named by MsgID.
func (s *Server) msgList(c *controlClient, cmd command) {
	type out struct {
		msg teamMessagesMsg
		err error
	}
	r, ok := ask(s, func() out {
		all, members, err := s.ws.TeamMessages(cmd.Root)
		if err != nil {
			return out{err: err}
		}
		m := teamMessagesMsg{Type: "messages", Root: cmd.Root, Messages: []teamMessageView{}}
		if cmd.MsgID > 0 {
			for _, x := range all {
				if x.ID == cmd.MsgID {
					m.Messages = append(m.Messages, messageViewOf(x, members, 0))
				}
			}
			if len(m.Messages) == 0 {
				return out{err: fmt.Errorf("no message #%d in this project", cmd.MsgID)}
			}
			return out{msg: m}
		}
		limit := cmd.Size
		if limit <= 0 || limit > maxTeamMessageList {
			limit = maxTeamMessages
		}
		slices.Reverse(all)
		if len(all) > limit {
			m.Hidden = len(all) - limit
			all = all[:limit]
		}
		for _, x := range all {
			m.Messages = append(m.Messages, messageViewOf(x, members, teamTextCut))
		}
		return out{msg: m}
	})
	if !ok {
		return
	}
	if r.err != nil {
		refuse(c, cmd, r.err)
		return
	}
	c.sendJSON(r.msg)
}

// msgSend sends a message from the person to a pane, a name or a role of the
// project.
func (s *Server) msgSend(c *controlClient, cmd command) {
	type out struct {
		res hooks.MsgResult
		err error
	}
	r, ok := ask(s, func() out {
		res, err := s.ws.UserMessage(cmd.Root, cmd.Target, cmd.Text)
		return out{res, err}
	})
	if !ok {
		return
	}
	if r.err != nil {
		refuse(c, cmd, r.err)
		return
	}
	warn := ""
	if len(r.res.Failed) > 0 {
		warn = "Not delivered to " + strings.Join(r.res.Failed, "; ")
	}
	c.sendJSON(teamResultMsg{Type: "teamResult", Req: cmd.Req, Op: cmd.Cmd, OK: true, Sent: len(r.res.Sent), Warning: warn})
	s.wakeAsked()
}

// msgRead marks one message read: ID is the pane whose inbox it is in and MsgID
// the message.
func (s *Server) msgRead(c *controlClient, cmd command) {
	r, ok := ask(s, func() error {
		_, err := s.ws.UserReadMessage(cmd.Root, cmd.ID, cmd.MsgID)
		return err
	})
	if !ok {
		return
	}
	if r != nil {
		refuse(c, cmd, r)
		return
	}
	c.sendJSON(teamResultMsg{Type: "teamResult", Req: cmd.Req, Op: cmd.Cmd, OK: true, Pane: cmd.ID, Message: cmd.MsgID})
	s.wakeAsked()
}
