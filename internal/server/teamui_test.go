package server

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/jmwri/flockdeck/internal/hooks"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/tasks"
	"github.com/jmwri/flockdeck/internal/team"
	"github.com/jmwri/flockdeck/internal/workspace"
)

// The Team panel's commands: what a window may ask of a project's roster, task
// board and messages, and the answers it is sent.

// teamEnv is a project with a manager, a worker and a reviewer, and a window to
// ask from.
type teamEnv struct {
	srv                   *Server
	ws                    *workspace.Workspace
	root                  string
	boss, dev, eyes       string
	c                     *controlClient
	otherRoot, otherAgent string
}

func newTeamEnv(t *testing.T) *teamEnv {
	t.Helper()
	srv, ws := newTestServer(t)
	e := &teamEnv{srv: srv, ws: ws, root: ws.ActiveRoot(), c: &controlClient{out: make(chan []byte, 64)}}
	e.boss = agentPane(t, srv, ws, "boss", "manager")
	e.dev = agentPane(t, srv, ws, "dev", "worker")
	e.eyes = agentPane(t, srv, ws, "eyes", "reviewer")
	return e
}

// withOtherProject opens a second project with an agent pane of its own.
func (e *teamEnv) withOtherProject(t *testing.T) {
	t.Helper()
	e.otherRoot = t.TempDir()
	if _, ok := ask(e.srv, func() bool {
		if err := e.ws.OpenProject(e.otherRoot); err != nil {
			t.Error(err)
		}
		return true
	}); !ok {
		t.Fatal("the workspace did not answer")
	}
	e.otherAgent = agentPane(t, e.srv, e.ws, "stranger", "worker")
}

// next reads what the window was sent until a message of the type arrives.
func (e *teamEnv) next(t *testing.T, typ string, out any) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case raw := <-e.c.out:
			var head struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(raw, &head) != nil || head.Type != typ {
				continue
			}
			if err := json.Unmarshal(raw, out); err != nil {
				t.Fatalf("decode %s: %v", typ, err)
			}
			return
		case <-deadline:
			t.Fatalf("no %q message arrived", typ)
		}
	}
}

// do sends a command from the window and returns its teamResult.
func (e *teamEnv) do(t *testing.T, cmd command) teamResultMsg {
	t.Helper()
	cmd.Req = "r1"
	if cmd.Root == "" {
		cmd.Root = e.root
	}
	e.srv.teamCommand(e.c, cmd)
	var res teamResultMsg
	e.next(t, "teamResult", &res)
	if res.Op != cmd.Cmd || res.Req != "r1" {
		t.Fatalf("answer to %s = %+v", cmd.Cmd, res)
	}
	return res
}

func (e *teamEnv) mustDo(t *testing.T, cmd command) teamResultMsg {
	t.Helper()
	res := e.do(t, cmd)
	if !res.OK {
		t.Fatalf("%s: %s", cmd.Cmd, res.Error)
	}
	return res
}

func (e *teamEnv) snapshot(t *testing.T) teamMsg {
	t.Helper()
	e.srv.teamCommand(e.c, command{Cmd: "teamSnapshot", Root: e.root})
	var m teamMsg
	e.next(t, "team", &m)
	return m
}

// agentInbox is what the agent itself sees in its inbox.
func (e *teamEnv) agentInbox(t *testing.T, pane string, all bool) []team.Message {
	t.Helper()
	msgs, ok := ask(e.srv, func() []team.Message {
		m, _ := e.ws.Inbox(pane, all)
		return m
	})
	if !ok {
		t.Fatal("the workspace did not answer")
	}
	return msgs
}

// systemTexts are the notices Flockdeck wrote into a pane's inbox.
func (e *teamEnv) systemTexts(t *testing.T, pane string) []string {
	t.Helper()
	msgs, ok := ask(e.srv, func() []team.Message {
		m, _ := e.ws.Inbox(pane, true)
		return m
	})
	if !ok {
		t.Fatal("the workspace did not answer")
	}
	var out []string
	for _, m := range msgs {
		if m.System() {
			out = append(out, m.Text)
		}
	}
	return out
}

func TestTeamSnapshotHasTheRosterAndCountsAndEmptyListsAreArrays(t *testing.T) {
	e := newTeamEnv(t)
	m := e.snapshot(t)
	if m.Type != "team" || m.Root != e.root || !m.HasManager {
		t.Fatalf("snapshot = %+v", m)
	}
	roles := map[string]string{}
	for _, r := range m.Roster {
		roles[r.Pane] = r.Role
		if r.Status != "idle" || r.Activity != workspace.ActivityIdle || r.Name == "" {
			t.Errorf("roster entry %+v", r)
		}
	}
	if len(roles) < 3 || roles[e.boss] != "manager" || roles[e.dev] != "worker" || roles[e.eyes] != "reviewer" {
		t.Errorf("roster = %+v", m.Roster)
	}
	if m.Counts.Panes != len(m.Roster) || m.Counts.Live != len(m.Roster) || m.Counts.Open != 0 || m.Counts.Messages != 0 {
		t.Errorf("counts = %+v", m.Counts)
	}
	// The window walks these without checking them, so none may arrive as null.
	raw, _ := json.Marshal(m)
	for _, key := range []string{`"roster":[`, `"tasks":[`, `"messages":[`} {
		if !strings.Contains(string(raw), key) {
			t.Errorf("%s is not an array in %s", key, raw)
		}
	}
	if m.Project == "" {
		t.Error("the project has no name")
	}
}

func TestTeamTasksThroughTheWindow(t *testing.T) {
	e := newTeamEnv(t)
	res := e.mustDo(t, command{Cmd: "taskAdd", Title: "write the parser", Spec: "details here", Role: "worker", Branch: "feat/parser"})
	task := res.Task
	if task == nil || task.ID != "T1" || task.State != "planned" || task.Body != "details here" || task.Branch != "feat/parser" || !task.Creator.User {
		t.Fatalf("add = %+v", task)
	}
	if task.Owner != "any worker" || !task.CanAssign || !task.CanEdit {
		t.Errorf("owner %q, assign %v, edit %v", task.Owner, task.CanAssign, task.CanEdit)
	}

	// Assign to a pane: the worker is told, as it is when the manager pane does it.
	res = e.mustDo(t, command{Cmd: "taskAssign", ID: "T1", Target: e.dev})
	if res.Task.State != "assigned" || res.Task.OwnerPane != e.dev || !res.Task.OwnerOpen || res.Told != 1 {
		t.Fatalf("assign = %+v told %d", res.Task, res.Told)
	}
	if got := e.systemTexts(t, e.dev); len(got) != 1 || got[0] != "task T1 assigned to you: write the parser" {
		t.Errorf("the worker was told %q", got)
	}

	res = e.mustDo(t, command{Cmd: "taskStatus", ID: "T1", State: "in progress"})
	if res.Task.State != "in-progress" {
		t.Fatalf("status = %+v", res.Task)
	}
	res = e.mustDo(t, command{Cmd: "taskNote", ID: "T1", Text: "started by hand"})
	if len(res.Task.NoteList) != 1 || !res.Task.NoteList[0].Author.User || res.Task.NoteList[0].Text != "started by hand" {
		t.Errorf("note = %+v", res.Task.NoteList)
	}
	// The worker, who owns it, hears of the note; the user, who has no pane, does not.
	if got := e.systemTexts(t, e.dev); len(got) != 2 || !strings.Contains(got[1], "note on task T1 from the user (at the window): started by hand") {
		t.Errorf("the worker was told %q", got)
	}
	res = e.mustDo(t, command{Cmd: "taskBranch", ID: "T1", Branch: "feat/other"})
	if res.Task.Branch != "feat/other" {
		t.Errorf("branch = %q", res.Task.Branch)
	}

	// The reviewer is told of a review.
	e.mustDo(t, command{Cmd: "taskStatus", ID: "T1", State: "in-review"})
	if got := e.systemTexts(t, e.eyes); len(got) != 1 || !strings.Contains(got[0], "task T1 is ready for review") {
		t.Errorf("the reviewer was told %q", got)
	}
	got := e.mustDo(t, command{Cmd: "taskGet", ID: "T1"}).Task
	if got.Body != "details here" || len(got.NoteList) != 1 || len(got.HistoryList) < 5 || got.Created == "" {
		t.Errorf("detail = %+v", got)
	}
	if got.HistoryList[0].What != "added the task" || !got.HistoryList[0].Actor.User {
		t.Errorf("history = %+v", got.HistoryList)
	}
	// The snapshot has the card, with the note count, and the roster the pane's task.
	m := e.snapshot(t)
	if len(m.Tasks) != 1 || m.Tasks[0].Notes != 1 || m.Tasks[0].Body != "" || len(m.Tasks[0].NoteList) != 0 {
		t.Errorf("snapshot tasks = %+v", m.Tasks)
	}
	for _, r := range m.Roster {
		if r.Pane == e.dev && (r.Task == nil || r.Task.ID != "T1" || r.Task.State != "in-review" || r.Open != 1) {
			t.Errorf("worker row = %+v", r)
		}
		if r.Pane == e.eyes && r.Task != nil {
			t.Errorf("reviewer row has a task: %+v", r)
		}
	}
	if m.Counts.Tasks["in-review"] != 1 || m.Counts.Open != 1 {
		t.Errorf("counts = %+v", m.Counts)
	}

	// Done: the manager pane is told, since it was not the one that closed it.
	e.mustDo(t, command{Cmd: "taskStatus", ID: "T1", State: "done"})
	if got := e.systemTexts(t, e.boss); len(got) == 0 || !strings.Contains(got[len(got)-1], "task T1 is done, by the user (at the window)") {
		t.Errorf("the manager was told %q", got)
	}
	// Finished tasks are in the all list, not the open one.
	var list teamTasksMsg
	e.srv.teamCommand(e.c, command{Cmd: "taskList", Root: e.root})
	e.next(t, "tasks", &list)
	if len(list.Tasks) != 0 || list.Hidden != 1 {
		t.Errorf("open list = %+v", list)
	}
	e.srv.teamCommand(e.c, command{Cmd: "taskList", Root: e.root, State: "all"})
	e.next(t, "tasks", &list)
	if len(list.Tasks) != 1 || list.Tasks[0].State != "done" {
		t.Errorf("all list = %+v", list)
	}
	e.srv.teamCommand(e.c, command{Cmd: "taskList", Root: e.root, State: "done"})
	e.next(t, "tasks", &list)
	if len(list.Tasks) != 1 {
		t.Errorf("done list = %+v", list)
	}
}

func TestTeamTaskViewsOfferOnlyTheMovesTheRulesAllow(t *testing.T) {
	e := newTeamEnv(t)
	e.mustDo(t, command{Cmd: "taskAdd", Title: "a"})
	moves := func() []string {
		m := e.snapshot(t)
		var out []string
		for _, mv := range m.Tasks[0].Moves {
			s := mv.To
			if mv.NeedNote {
				s += "+note"
			}
			out = append(out, s)
		}
		return out
	}
	// Unowned and planned: assigned needs an owner, so it is not offered.
	if got := strings.Join(moves(), ","); got != "in-progress,blocked,cancelled" {
		t.Errorf("planned and unowned: %s", got)
	}
	e.mustDo(t, command{Cmd: "taskAssign", ID: "T1", Target: "worker"})
	if got := strings.Join(moves(), ","); got != "planned,in-progress,blocked,cancelled" {
		t.Errorf("assigned: %s", got)
	}
	e.mustDo(t, command{Cmd: "taskStatus", ID: "T1", State: "in-progress"})
	e.mustDo(t, command{Cmd: "taskStatus", ID: "T1", State: "done"})
	if got := strings.Join(moves(), ","); got != "in-progress+note" {
		t.Errorf("done: %s", got)
	}
	m := e.snapshot(t)
	if m.Tasks[0].CanAssign || m.Tasks[0].CanEdit {
		t.Errorf("a finished task can be assigned or edited: %+v", m.Tasks[0])
	}
	// Whatever is offered is what the rules say, for every state.
	for _, st := range tasks.States {
		task := tasks.Task{ID: "T9", State: st, OwnerRole: "worker", Creator: tasks.UserActor()}
		v := taskViewOf(task, nil, true, false)
		want := tasks.Moves(task, tasks.UserActor(), true)
		if len(v.Moves) != len(want) {
			t.Errorf("%s: view %v, rules %v", st, v.Moves, want)
		}
	}
}

func TestTeamCommandsRefuseWhatTheRulesAndLimitsRefuse(t *testing.T) {
	e := newTeamEnv(t)
	e.withOtherProject(t)
	e.mustDo(t, command{Cmd: "taskAdd", Title: "a", Role: "worker"})

	refused := func(cmd command, want string) {
		t.Helper()
		res := e.do(t, cmd)
		if res.OK || !strings.Contains(res.Error, want) {
			t.Errorf("%s %+v: ok %v, error %q, want %q", cmd.Cmd, cmd, res.OK, res.Error, want)
		}
	}
	refused(command{Cmd: "taskAdd", Title: "  "}, "title is empty")
	refused(command{Cmd: "taskAdd", Title: strings.Repeat("x", tasks.MaxTitle+1)}, "longer than")
	refused(command{Cmd: "taskAdd", Title: "x", Spec: strings.Repeat("x", tasks.MaxBody+1)}, "body is")
	refused(command{Cmd: "taskAdd", Title: "x", Role: "nonesuch"}, "no role")
	refused(command{Cmd: "taskAdd", Title: "x", Branch: "has space"}, "not a branch name")
	refused(command{Cmd: "taskStatus", ID: "T1", State: "finished"}, "is not a task state")
	refused(command{Cmd: "taskStatus", ID: "T1", State: "done"}, "cannot go straight to done")
	refused(command{Cmd: "taskStatus", ID: "T9", State: "done"}, "no task T9")
	refused(command{Cmd: "taskStatus", ID: "nonsense", State: "done"}, "is not a task id")
	refused(command{Cmd: "taskGet", ID: "T9"}, "no task T9")
	refused(command{Cmd: "taskNote", ID: "T1", Text: " "}, "note is empty")
	refused(command{Cmd: "taskNote", ID: "T1", Text: strings.Repeat("x", tasks.MaxNote+1)}, "note is")
	refused(command{Cmd: "taskAssign", ID: "T1", Target: "nobody-here"}, "no pane, name or role")
	refused(command{Cmd: "taskAssign", ID: "T1"}, "say who the task is for")
	// A pane of another project is not a pane of this one.
	refused(command{Cmd: "taskAssign", ID: "T1", Target: e.otherAgent}, "no pane, name or role")
	refused(command{Cmd: "taskBranch", ID: "T1", Branch: "a b"}, "not a branch name")
	refused(command{Cmd: "taskPrune", Size: -1}, "cannot be negative")
	refused(command{Cmd: "taskList", State: "finished"}, "is not a task state")
	// A project that is not open has no team.
	refused(command{Cmd: "teamSnapshot", Root: t.TempDir()}, "no project is open")
	refused(command{Cmd: "taskAdd", Root: t.TempDir(), Title: "x"}, "no project is open")
	// The other project has a board of its own.
	e.srv.teamCommand(e.c, command{Cmd: "taskList", Root: e.otherRoot, State: "all"})
	var list teamTasksMsg
	e.next(t, "tasks", &list)
	if len(list.Tasks) != 0 {
		t.Errorf("another project's board = %+v", list.Tasks)
	}
	if res := e.mustDo(t, command{Cmd: "taskPrune", Size: 30}); res.Pruned != 0 {
		t.Errorf("pruned %d", res.Pruned)
	}
	if res := e.mustDo(t, command{Cmd: "taskPrune"}); res.Pruned != 0 {
		t.Errorf("pruned %d", res.Pruned)
	}
	// The limits accept exactly their size.
	e.mustDo(t, command{Cmd: "taskAdd", Title: strings.Repeat("t", tasks.MaxTitle), Spec: strings.Repeat("b", tasks.MaxBody)})
	e.mustDo(t, command{Cmd: "taskNote", ID: "T1", Text: strings.Repeat("n", tasks.MaxNote)})
}

func TestTeamMessagesThroughTheWindow(t *testing.T) {
	e := newTeamEnv(t)
	e.withOtherProject(t)
	hookSrv := e.ws.HookServer()
	api, token := hookSrv.BaseURL(), hookSrv.Token()

	long := strings.Repeat("é", teamTextCut) // twice the cut in bytes
	res := e.mustDo(t, command{Cmd: "msgSend", Target: "worker", Text: long})
	if res.Sent != 1 {
		t.Fatalf("send = %+v", res)
	}
	e.mustDo(t, command{Cmd: "msgSend", Target: e.eyes, Text: "please look at T1"})
	// The pane's own message to the manager is in the list too.
	if _, err := hooks.Msg(api, token, e.dev, "manager", "need a decision"); err != nil {
		t.Fatal(err)
	}

	var list teamMessagesMsg
	e.srv.teamCommand(e.c, command{Cmd: "msgList", Root: e.root})
	e.next(t, "messages", &list)
	if len(list.Messages) != 3 {
		t.Fatalf("messages = %+v", list.Messages)
	}
	// Newest first.
	if list.Messages[0].Text != "need a decision" || list.Messages[0].From.Pane != e.dev || list.Messages[0].From.User {
		t.Errorf("newest = %+v", list.Messages[0])
	}
	if m := list.Messages[1]; m.Text != "please look at T1" || !m.From.User || m.From.Pane != "" || m.To != e.eyes || m.ToRole != "reviewer" || m.Read {
		t.Errorf("the user's message = %+v", m)
	}
	cut := list.Messages[2]
	if !cut.Cut || len(cut.Text) > teamTextCut || !strings.HasPrefix(long, cut.Text) || cut.To != e.dev {
		t.Errorf("long message: cut %v, %d bytes", cut.Cut, len(cut.Text))
	}
	// The whole text on request.
	e.srv.teamCommand(e.c, command{Cmd: "msgList", Root: e.root, MsgID: cut.ID})
	e.next(t, "messages", &list)
	if len(list.Messages) != 1 || list.Messages[0].Text != long || list.Messages[0].Cut {
		t.Errorf("whole message = %d bytes", len(list.Messages[0].Text))
	}
	if res := e.do(t, command{Cmd: "msgList", MsgID: 999}); res.OK || !strings.Contains(res.Error, "no message #999") {
		t.Errorf("a message that is not there = %+v", res)
	}

	// Sizes, targets and the empty message.
	refused := func(cmd command, want string) {
		t.Helper()
		res := e.do(t, cmd)
		if res.OK || !strings.Contains(res.Error, want) {
			t.Errorf("%s %+v: ok %v, error %q, want %q", cmd.Cmd, cmd, res.OK, res.Error, want)
		}
	}
	refused(command{Cmd: "msgSend", Target: e.eyes, Text: "  "}, "message is empty")
	refused(command{Cmd: "msgSend", Target: e.eyes, Text: strings.Repeat("x", team.MaxText+1)}, "write it to a file")
	refused(command{Cmd: "msgSend", Target: "", Text: "x"}, "say who the message is for")
	refused(command{Cmd: "msgSend", Target: "nobody-here", Text: "x"}, "no pane, name or role matches")
	refused(command{Cmd: "msgSend", Target: e.otherAgent, Text: "x"}, "no pane, name or role matches")
	e.mustDo(t, command{Cmd: "msgSend", Target: e.eyes, Text: strings.Repeat("x", team.MaxText)})

	// Mark seen: only in this project's inboxes, and not the agent's read mark.
	e.srv.teamCommand(e.c, command{Cmd: "msgList", Root: e.root})
	e.next(t, "messages", &list)
	var forEyes teamMessageView
	for _, m := range list.Messages {
		if m.Text == "need a decision" {
			forEyes = m
		}
	}
	refused(command{Cmd: "msgSeen", ID: e.dev, MsgID: forEyes.ID}, "no message #")
	refused(command{Cmd: "msgSeen", ID: e.otherAgent, MsgID: forEyes.ID}, "not an agent pane of this project")
	refused(command{Cmd: "msgSeen", ID: "forged", MsgID: forEyes.ID}, "not an agent pane of this project")
	before := e.snapshot(t)
	if before.Counts.Unseen != 1 || before.Counts.Unread != 4 {
		t.Errorf("counts before = %+v", before.Counts)
	}
	if res := e.mustDo(t, command{Cmd: "msgSeen", ID: e.boss, MsgID: forEyes.ID}); res.Pane != e.boss || res.Message != forEyes.ID {
		t.Errorf("mark seen = %+v", res)
	}
	m := e.snapshot(t)
	// Seen by the user, still unread by the agent: its count, its inbox and its
	// roster row are what they were.
	if m.Counts.Messages != 4 || m.Counts.Unread != 4 || m.Counts.Unseen != 0 {
		t.Errorf("counts = %+v", m.Counts)
	}
	for _, r := range m.Roster {
		if r.Pane == e.boss && r.Unread != 1 {
			t.Errorf("manager unread = %d, the user's mark changed it", r.Unread)
		}
		if r.Pane == e.eyes && r.Unread != 2 {
			t.Errorf("reviewer unread = %d", r.Unread)
		}
	}
	if got := e.agentInbox(t, e.boss, false); len(got) != 1 || got[0].Read || !got[0].Seen {
		t.Errorf("the manager's inbox = %+v", got)
	}
	for _, mv := range func() []teamMessageView {
		var l teamMessagesMsg
		e.srv.teamCommand(e.c, command{Cmd: "msgList", Root: e.root})
		e.next(t, "messages", &l)
		return l.Messages
	}() {
		if mv.Text == "need a decision" && (!mv.Seen || mv.Read) {
			t.Errorf("view of the message = %+v", mv)
		}
	}
}

func TestTeamCommandsAreForTheDeskOnly(t *testing.T) {
	e := newTeamEnv(t)
	phone := &controlClient{out: make(chan []byte, 8), remote: true, device: "d1"}
	for name := range map[string]bool{"teamSnapshot": true, "teamWatch": true, "taskAdd": true, "taskStatus": true, "taskPrune": true, "msgSend": true, "msgSeen": true, "teamClose": true, "msgList": true, "taskList": true, "taskGet": true, "taskAssign": true, "taskNote": true, "taskBranch": true, "teamUnwatch": true} {
		e.srv.teamCommand(phone, command{Cmd: name, Root: e.root, Title: "x", Text: "x", Target: e.dev, ID: "T1", State: "done"})
		var raw []byte
		select {
		case raw = <-phone.out:
		default:
			t.Errorf("%s: a window through the relay was told nothing", name)
			continue
		}
		var res teamResultMsg
		if json.Unmarshal(raw, &res) != nil || res.OK || !strings.Contains(res.Error, "machine Flockdeck runs on") {
			t.Errorf("%s through the relay: %s", name, raw)
		}
		for len(phone.out) > 0 {
			<-phone.out
		}
	}
	if m := e.snapshot(t); len(m.Tasks) != 0 || len(m.Messages) != 0 || phone.teamRoot != "" {
		t.Errorf("a window through the relay changed the team: %+v", m)
	}
}

func TestAViewerCannotUseTheTeamPanel(t *testing.T) {
	e := newTeamEnv(t)
	setAccess(t, e.srv, "viewer-dev", "viewer", true)
	viewer := &controlClient{remote: true, device: "viewer-dev", out: make(chan []byte, 8)}
	e.srv.handleCommand(viewer, command{Cmd: "taskAdd", Root: e.root, Title: "x"})
	e.srv.handleCommand(viewer, command{Cmd: "msgSend", Root: e.root, Target: e.dev, Text: "x"})
	if m := e.snapshot(t); len(m.Tasks) != 0 || len(m.Messages) != 0 {
		t.Errorf("a viewer changed the team: %+v", m)
	}
	for _, cmd := range []string{"teamSnapshot", "teamWatch", "teamUnwatch", "taskList", "taskGet", "taskAdd", "taskAssign", "taskStatus", "taskNote", "taskBranch", "taskPrune", "msgList", "msgSend", "msgSeen", "teamClose"} {
		if commandAccess[cmd] != accessFull {
			t.Errorf("%s is not full-access only", cmd)
		}
	}
}

// ------------------------------------------------------------------- live push

// teamSocket is a window on the control socket, collecting the team pushes it is
// sent.
type teamSocket struct {
	t       *testing.T
	conn    *websocket.Conn
	msgs    chan teamMsg
	results chan teamResultMsg
}

func dialTeam(t *testing.T, srv *Server) *teamSocket {
	t.Helper()
	conn := dialControlPage(t, srv)
	nextHello(t, conn)
	ts := &teamSocket{t: t, conn: conn, msgs: make(chan teamMsg, 256), results: make(chan teamResultMsg, 16)}
	go func() {
		for {
			_, data, err := conn.Read(context.Background())
			if err != nil {
				return
			}
			var head struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(data, &head) != nil {
				continue
			}
			if head.Type == "teamResult" {
				var r teamResultMsg
				if json.Unmarshal(data, &r) == nil {
					ts.results <- r
				}
				continue
			}
			if head.Type != "team" {
				continue
			}
			var m teamMsg
			if json.Unmarshal(data, &m) == nil {
				ts.msgs <- m
			}
		}
	}()
	return ts
}

// until reads pushes until one satisfies ok, and fails after a few seconds.
func (ts *teamSocket) until(what string, ok func(teamMsg) bool) teamMsg {
	ts.t.Helper()
	deadline := time.After(8 * time.Second)
	for {
		select {
		case m := <-ts.msgs:
			if ok(m) {
				return m
			}
		case <-deadline:
			ts.t.Fatalf("no push with %s", what)
		}
	}
}

func TestAWatchingPanelIsPushedEveryChangeToTheProject(t *testing.T) {
	e := newTeamEnv(t)
	hookSrv := e.ws.HookServer()
	api, token := hookSrv.BaseURL(), hookSrv.Token()
	ts := dialTeam(t, e.srv)
	sendCmd(t, ts.conn, command{Cmd: "teamWatch", Root: e.root})
	first := ts.until("the first picture", func(m teamMsg) bool { return len(m.Roster) >= 3 })
	if first.Root != e.root || len(first.Tasks) != 0 {
		t.Fatalf("first = %+v", first)
	}

	// A task a pane adds through the command line shows up with nobody asking.
	if _, err := hooks.Task(api, token, e.boss, hooks.TaskRequest{Op: hooks.TaskAdd, Title: "from the cli"}); err != nil {
		t.Fatal(err)
	}
	ts.until("the task", func(m teamMsg) bool { return len(m.Tasks) == 1 && m.Tasks[0].Title == "from the cli" })

	// So does a message.
	if _, err := hooks.Msg(api, token, e.dev, "manager", "a question"); err != nil {
		t.Fatal(err)
	}
	ts.until("the message", func(m teamMsg) bool { return len(m.Messages) == 1 && m.Messages[0].Text == "a question" })

	// A change to a pane's status shows in the roster.
	ask(e.srv, func() bool { e.ws.Pane(e.dev).Sess.SetStatus(session.StatusWorking, ""); return true })
	e.srv.Wake()
	ts.until("the status", func(m teamMsg) bool {
		for _, r := range m.Roster {
			if r.Pane == e.dev {
				return r.Status == "working"
			}
		}
		return false
	})

	// A pane that starts is added.
	late := agentPane(t, e.srv, e.ws, "late", "tester")
	e.srv.Wake()
	ts.until("the new pane", func(m teamMsg) bool {
		for _, r := range m.Roster {
			if r.Pane == late {
				return true
			}
		}
		return false
	})

	// A change the window makes itself comes back too.
	sendCmd(t, ts.conn, command{Cmd: "taskAssign", Root: e.root, ID: "T1", Target: e.dev, Req: "x"})
	ts.until("the assignment", func(m teamMsg) bool { return len(m.Tasks) == 1 && m.Tasks[0].OwnerPane == e.dev })
}

func TestPushesAreHeldToTheIntervalAndUnchangedPicturesAreNotRepeated(t *testing.T) {
	e := newTeamEnv(t)
	hookSrv := e.ws.HookServer()
	api, token := hookSrv.BaseURL(), hookSrv.Token()
	ts := dialTeam(t, e.srv)
	sendCmd(t, ts.conn, command{Cmd: "teamWatch", Root: e.root})
	ts.until("the first picture", func(m teamMsg) bool { return len(m.Roster) >= 3 })

	// A burst of changes, far faster than the interval.
	start := time.Now()
	const n = 25
	for i := 0; i < n; i++ {
		if _, err := hooks.Task(api, token, e.boss, hooks.TaskRequest{Op: hooks.TaskAdd, Title: "burst"}); err != nil {
			t.Fatal(err)
		}
	}
	pushes := 0
	deadline := time.After(10 * time.Second)
	var last teamMsg
	for len(last.Tasks) < n {
		select {
		case last = <-ts.msgs:
			pushes++
		case <-deadline:
			t.Fatalf("only %d of %d tasks arrived after %d pushes", len(last.Tasks), n, pushes)
		}
	}
	elapsed := time.Since(start)
	if limit := int(elapsed/teamInterval) + 2; pushes > limit {
		t.Errorf("%d pushes in %v for %d changes; the interval allows %d", pushes, elapsed, n, limit)
	}
	if pushes >= n {
		t.Errorf("every change was pushed: %d pushes for %d changes", pushes, n)
	}

	// With nothing changing, waking the server again sends nothing.
	for i := 0; i < 5; i++ {
		e.srv.Wake()
	}
	select {
	case m := <-ts.msgs:
		t.Errorf("an unchanged picture was pushed again: %d tasks", len(m.Tasks))
	case <-time.After(3 * teamInterval):
	}
}

func TestUnwatchingStopsThePushesAndWatchingAnotherProjectReplacesTheFirst(t *testing.T) {
	e := newTeamEnv(t)
	e.withOtherProject(t)
	hookSrv := e.ws.HookServer()
	api, token := hookSrv.BaseURL(), hookSrv.Token()
	ts := dialTeam(t, e.srv)

	sendCmd(t, ts.conn, command{Cmd: "teamWatch", Root: e.otherRoot})
	m := ts.until("the other project", func(m teamMsg) bool { return m.Root != "" })
	// The project opened with a pane of its own besides the one the test added.
	var panes []string
	for _, r := range m.Roster {
		panes = append(panes, r.Pane)
	}
	if m.Root != e.otherRoot || !slices.Contains(panes, e.otherAgent) || slices.Contains(panes, e.boss) || slices.Contains(panes, e.dev) {
		t.Fatalf("other project's picture = %+v", m)
	}
	// A change in the first project is not pushed to a window watching the other.
	if _, err := hooks.Task(api, token, e.boss, hooks.TaskRequest{Op: hooks.TaskAdd, Title: "elsewhere"}); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-ts.msgs:
		if len(got.Tasks) != 0 || got.Root != e.otherRoot {
			t.Errorf("pushed %+v about the other project", got)
		}
	case <-time.After(3 * teamInterval):
	}

	sendCmd(t, ts.conn, command{Cmd: "teamWatch", Root: e.root})
	ts.until("the first project", func(m teamMsg) bool { return m.Root == e.root && len(m.Tasks) == 1 })
	sendCmd(t, ts.conn, command{Cmd: "teamUnwatch"})
	// Commands on one socket are handled in order, so the picture asked for next
	// is the sign that the unwatch has been done.
	sendCmd(t, ts.conn, command{Cmd: "teamSnapshot", Root: e.root})
	ts.until("the snapshot", func(m teamMsg) bool { return len(m.Tasks) == 1 })
	if _, err := hooks.Task(api, token, e.boss, hooks.TaskRequest{Op: hooks.TaskAdd, Title: "after unwatch"}); err != nil {
		t.Fatal(err)
	}
	e.srv.Wake()
	for {
		select {
		case m := <-ts.msgs:
			if len(m.Tasks) > 1 {
				t.Fatalf("pushed after the panel stopped watching: %d tasks", len(m.Tasks))
			}
			continue
		case <-time.After(3 * teamInterval):
		}
		break
	}

	// A window that watches a project that is not open is told so and watches none.
	sendCmd(t, ts.conn, command{Cmd: "teamWatch", Root: t.TempDir()})
	select {
	case res := <-ts.results:
		if res.OK || !strings.Contains(res.Error, "no project is open") {
			t.Errorf("watching a project that is not open = %+v", res)
		}
	case <-time.After(5 * time.Second):
		t.Error("no answer to a watch of a project that is not open")
	}
}
