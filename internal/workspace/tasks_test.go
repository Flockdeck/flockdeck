package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/hooks"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/tasks"
)

// call runs one task operation as the pane from.
func (c crew) call(from string, req hooks.TaskRequest) (hooks.TaskResult, error) {
	req.Pane = from
	return c.ws.Task(req)
}

func (c crew) mustCall(t *testing.T, from string, req hooks.TaskRequest) hooks.TaskResult {
	t.Helper()
	res, err := c.call(from, req)
	if err != nil {
		t.Fatalf("%s %s: %v", req.Op, req.ID, err)
	}
	return res
}

func (c crew) texts(t *testing.T, pane string) []string {
	t.Helper()
	var out []string
	for _, m := range c.inbox(t, pane, true) {
		if m.System() {
			out = append(out, m.Text)
		}
	}
	return out
}

func TestTheBoardLivesInTheProjectsMainCheckoutAndIsSharedByItsPanes(t *testing.T) {
	c := newCrew(t)
	res := c.mustCall(t, c.lead, hooks.TaskRequest{Op: hooks.TaskAdd, Title: "write the parser", Body: "details", Role: "worker"})
	if res.Task == nil || res.Task.ID != "T1" || res.Task.OwnerRole != "worker" || res.Task.Creator.Pane != c.lead || res.Task.Creator.Role != "manager" {
		t.Fatalf("add = %+v", res.Task)
	}
	want := filepath.Join(c.root, ".flockdeck", "team", "tasks.json")
	if _, err := os.Stat(want); err != nil {
		t.Errorf("no board file at %s: %v", want, err)
	}
	// The root is not a git repository, so nothing else was made in it.
	entries, _ := os.ReadDir(c.root)
	for _, e := range entries {
		if e.Name() != ".flockdeck" {
			t.Errorf("unexpected %s in the project", e.Name())
		}
	}
	// Every pane of the project sees the same task.
	for _, pane := range []string{c.w1, c.w2, c.reviewer} {
		got := c.mustCall(t, pane, hooks.TaskRequest{Op: hooks.TaskShow, ID: "t1"})
		if got.Task == nil || got.Task.Title != "write the parser" || got.Task.Body != "details" {
			t.Errorf("pane %s sees %+v", pane, got.Task)
		}
	}
	// A project that was opened elsewhere has a board of its own.
	other := t.TempDir()
	if err := c.ws.OpenProject(other); err != nil {
		t.Fatal(err)
	}
	c.ws.NewTabWith(Choice{Kind: session.KindClaude, Agent: "sleeper"}, other, "")
	var stranger string
	for _, id := range c.ws.Tabs[len(c.ws.Tabs)-1].Tree.Panes() {
		stranger = id
	}
	list := c.mustCall(t, stranger, hooks.TaskRequest{Op: hooks.TaskList, State: "all"})
	if len(list.Tasks) != 0 {
		t.Errorf("another project sees %+v", list.Tasks)
	}
}

func TestAPaneActsAsItselfAndAnUnknownPaneIsRefused(t *testing.T) {
	c := newCrew(t)
	c.mustCall(t, c.lead, hooks.TaskRequest{Op: hooks.TaskAdd, Title: "t"})
	for _, op := range []string{hooks.TaskAdd, hooks.TaskList, hooks.TaskShow, hooks.TaskStatus, hooks.TaskNote} {
		_, err := c.call("forged-pane", hooks.TaskRequest{Op: op, ID: "T1", Title: "x", State: "done", Text: "x"})
		if err == nil || !strings.Contains(err.Error(), `no pane "forged-pane" is open`) {
			t.Errorf("%s as an unknown pane: err = %v", op, err)
		}
	}
	// A shell is open but is not an agent, so it has no place on the board.
	shell := c.ws.Pane(c.ws.NewTab(session.KindShell, c.root, "shell").Tree.Panes()[0]).ID
	if _, err := c.call(shell, hooks.TaskRequest{Op: hooks.TaskList}); err == nil || !strings.Contains(err.Error(), "not an agent pane") {
		t.Errorf("a shell used the board: %v", err)
	}
	// The note's author and a task's creator are the calling pane, with the role
	// it was started with, whatever the request says.
	res := c.mustCall(t, c.w1, hooks.TaskRequest{Op: hooks.TaskNote, ID: "T1", Text: "from w1"})
	n := res.Task.Notes[0].Author
	if n.Pane != c.w1 || n.Role != "worker" {
		t.Errorf("author = %+v", n)
	}
	if _, err := c.call(c.w1, hooks.TaskRequest{Op: "drop-table", ID: "T1"}); err == nil {
		t.Error("an unknown operation was accepted")
	}
}

func TestRolesDecideWhoMayAssignAndMove(t *testing.T) {
	c := newCrew(t)
	c.mustCall(t, c.lead, hooks.TaskRequest{Op: hooks.TaskAdd, Title: "the work", Role: "worker"})

	if _, err := c.call(c.w1, hooks.TaskRequest{Op: hooks.TaskAssign, ID: "T1", Target: c.w1}); err == nil || !strings.Contains(err.Error(), "only the manager role assigns") {
		t.Errorf("a worker assigned a task to itself: %v", err)
	}
	if _, err := c.call(c.reviewer, hooks.TaskRequest{Op: hooks.TaskAssign, ID: "T1", Target: c.w1}); err == nil {
		t.Error("a reviewer assigned a task")
	}
	if _, err := c.call(c.lead, hooks.TaskRequest{Op: hooks.TaskAssign, ID: "T1", Target: "nobody-here"}); err == nil || !strings.Contains(err.Error(), "no pane, name or role") {
		t.Errorf("assign to nobody: %v", err)
	}
	res := c.mustCall(t, c.lead, hooks.TaskRequest{Op: hooks.TaskAssign, ID: "T1", Target: c.w1})
	if res.Task.State != tasks.Assigned || res.Task.OwnerPane != c.w1 || res.Task.OwnerRole != "worker" {
		t.Fatalf("assign = %+v", res.Task)
	}
	if _, err := c.call(c.w2, hooks.TaskRequest{Op: hooks.TaskStatus, ID: "T1", State: "in-progress"}); err == nil {
		t.Error("another worker started a task that is not theirs")
	}
	c.mustCall(t, c.w1, hooks.TaskRequest{Op: hooks.TaskStatus, ID: "T1", State: "in-progress"})
	c.mustCall(t, c.w1, hooks.TaskRequest{Op: hooks.TaskBranch, ID: "T1", Branch: "feat/parser"})
	c.mustCall(t, c.w1, hooks.TaskRequest{Op: hooks.TaskStatus, ID: "T1", State: "in-review"})
	if _, err := c.call(c.w1, hooks.TaskRequest{Op: hooks.TaskStatus, ID: "T1", State: "done"}); err == nil {
		t.Error("the owner closed a task that is in review")
	}
	if _, err := c.call(c.reviewer, hooks.TaskRequest{Op: hooks.TaskStatus, ID: "T1", State: "in-progress"}); err == nil || !strings.Contains(err.Error(), "needs a note") {
		t.Errorf("send back with no note: %v", err)
	}
	c.mustCall(t, c.reviewer, hooks.TaskRequest{Op: hooks.TaskStatus, ID: "T1", State: "in-progress", Note: "missing a test"})
	c.mustCall(t, c.w1, hooks.TaskRequest{Op: hooks.TaskStatus, ID: "T1", State: "in-review"})
	c.mustCall(t, c.lead, hooks.TaskRequest{Op: hooks.TaskStatus, ID: "T1", State: "done"})
	if _, err := c.call(c.w1, hooks.TaskRequest{Op: hooks.TaskStatus, ID: "T1", State: "in-progress", Note: "again"}); err == nil {
		t.Error("a worker reopened a done task")
	}
	if _, err := c.call(c.lead, hooks.TaskRequest{Op: hooks.TaskStatus, ID: "T1", State: "in-progress"}); err == nil || !strings.Contains(err.Error(), "needs a note") {
		t.Errorf("reopen with no note: %v", err)
	}
}

func TestTaskRequestsAreCheckedBeforeTheyReachTheBoard(t *testing.T) {
	c := newCrew(t)
	c.mustCall(t, c.lead, hooks.TaskRequest{Op: hooks.TaskAdd, Title: "t"})
	bad := []hooks.TaskRequest{
		{Op: hooks.TaskAdd, Title: "x", Role: "no-such-role"},
		{Op: hooks.TaskAdd, Title: strings.Repeat("x", 201)},
		{Op: hooks.TaskShow, ID: "nonsense"},
		{Op: hooks.TaskShow, ID: "T99"},
		{Op: hooks.TaskStatus, ID: "T1", State: "finished"},
		{Op: hooks.TaskList, State: "finished"},
		{Op: hooks.TaskBaton, ID: "T1", Baton: "not-a-baton"},
		{Op: hooks.TaskBaton, ID: "T1", Baton: "20260101-000000-abcdef"},
		{Op: hooks.TaskBranch, ID: "T1", Branch: "has space"},
	}
	for _, req := range bad {
		if _, err := c.call(c.lead, req); err == nil {
			t.Errorf("%+v was accepted", req)
		}
	}
}

func TestAChangeMessagesTheRightPanesFromFlockdeck(t *testing.T) {
	c := newCrew(t)
	// The manager creates, the worker owns, a reviewer is running.
	c.mustCall(t, c.lead, hooks.TaskRequest{Op: hooks.TaskAdd, Title: "fix the parser"})

	res := c.mustCall(t, c.lead, hooks.TaskRequest{Op: hooks.TaskAssign, ID: "T1", Target: c.w1})
	if res.Told != 1 {
		t.Errorf("told = %d", res.Told)
	}
	if got := c.texts(t, c.w1); len(got) != 1 || got[0] != "task T1 assigned to you: fix the parser" {
		t.Errorf("owner's mail = %q", got)
	}
	for _, id := range []string{c.lead, c.w2, c.reviewer} {
		if got := c.texts(t, id); len(got) != 0 {
			t.Errorf("pane %s was told %q", id, got)
		}
	}

	c.mustCall(t, c.w1, hooks.TaskRequest{Op: hooks.TaskStatus, ID: "T1", State: "in-progress"})
	c.mustCall(t, c.w1, hooks.TaskRequest{Op: hooks.TaskStatus, ID: "T1", State: "in-review"})
	if got := c.texts(t, c.reviewer); len(got) != 1 || !strings.Contains(got[0], "task T1 is ready for review: fix the parser") {
		t.Errorf("reviewer's mail = %q", got)
	}
	if got := c.texts(t, c.lead); len(got) != 0 {
		t.Errorf("the creator was told of a review that a reviewer has: %q", got)
	}

	// The author of a move is not told of it, and a message is from flockdeck.
	msgs := c.inbox(t, c.reviewer, true)
	if len(msgs) != 1 || !msgs[0].System() || msgs[0].From.Pane != "" {
		t.Errorf("message = %+v", msgs)
	}

	c.mustCall(t, c.w1, hooks.TaskRequest{Op: hooks.TaskStatus, ID: "T1", State: "blocked", Note: "waiting on the schema"})
	if got := c.texts(t, c.lead); len(got) != 1 || !strings.Contains(got[0], "task T1 is blocked") {
		t.Errorf("creator's mail after blocked = %q", got)
	}
	c.mustCall(t, c.lead, hooks.TaskRequest{Op: hooks.TaskStatus, ID: "T1", State: "in-progress"})
	c.mustCall(t, c.w1, hooks.TaskRequest{Op: hooks.TaskStatus, ID: "T1", State: "in-review"})
	if _, err := c.call(c.w1, hooks.TaskRequest{Op: hooks.TaskStatus, ID: "T1", State: "done"}); err == nil {
		t.Error("the owner closed its own task")
	}
	c.mustCall(t, c.reviewer, hooks.TaskRequest{Op: hooks.TaskStatus, ID: "T1", State: "done"})
	if got := c.texts(t, c.lead); len(got) != 2 || got[1] != "task T1 is done: fix the parser" {
		t.Errorf("creator's mail after done = %q", got)
	}

	// A note from someone who is neither owner nor creator reaches both; the
	// author alone is left out.
	before, revBefore := len(c.texts(t, c.w1)), len(c.texts(t, c.reviewer))
	res = c.mustCall(t, c.reviewer, hooks.TaskRequest{Op: hooks.TaskNote, ID: "T1", Text: "looks fine to me"})
	if res.Told != 2 {
		t.Errorf("a note told %d panes, want the owner and the creator", res.Told)
	}
	if got := c.texts(t, c.w1); len(got) != before+1 {
		t.Errorf("the owner has %d messages, want %d", len(got), before+1)
	}
	if got := c.texts(t, c.reviewer); len(got) != revBefore {
		t.Errorf("the note's author was told: %q", got)
	}
}

func TestNoMessageGoesToAPaneThatIsClosedOrExited(t *testing.T) {
	c := newCrew(t)
	c.mustCall(t, c.w1, hooks.TaskRequest{Op: hooks.TaskAdd, Title: "from a worker"})
	// The reviewer's pane is closed: a move to review falls back to the creator.
	c.mustCall(t, c.lead, hooks.TaskRequest{Op: hooks.TaskAssign, ID: "T1", Target: c.w1})
	c.mustCall(t, c.w1, hooks.TaskRequest{Op: hooks.TaskStatus, ID: "T1", State: "in-progress"})
	c.ws.ClosePaneByID(c.reviewer)
	res := c.mustCall(t, c.w1, hooks.TaskRequest{Op: hooks.TaskStatus, ID: "T1", State: "in-review"})
	// The creator is the worker itself here, so only the manager is told.
	if res.Told != 1 || len(c.texts(t, c.lead)) != 1 {
		t.Errorf("told %d panes, manager has %q", res.Told, c.texts(t, c.lead))
	}
	// Assigned to a pane that has since closed: the manager's own change tells no one.
	c.mustCall(t, c.lead, hooks.TaskRequest{Op: hooks.TaskAdd, Title: "second"})
	c.ws.ClosePaneByID(c.w2)
	if _, err := c.call(c.lead, hooks.TaskRequest{Op: hooks.TaskAssign, ID: "T2", Target: c.w2}); err == nil {
		t.Error("a task was assigned to a pane that is closed")
	}
	// A task whose owner pane was closed afterwards: a note tells only who is there.
	c.mustCall(t, c.lead, hooks.TaskRequest{Op: hooks.TaskAssign, ID: "T2", Target: "worker"})
	c.ws.ClosePaneByID(c.w1)
	res = c.mustCall(t, c.lead, hooks.TaskRequest{Op: hooks.TaskNote, ID: "T2", Text: "anyone?"})
	if res.Told != 0 {
		t.Errorf("a note on a task for an empty role told %d panes", res.Told)
	}
}

func TestBriefingCarriesTheBoardAndTheCountsOfTheirPane(t *testing.T) {
	c := newCrew(t)
	for _, title := range []string{"one", "two", "three", "four"} {
		c.mustCall(t, c.lead, hooks.TaskRequest{Op: hooks.TaskAdd, Title: title})
	}
	c.mustCall(t, c.lead, hooks.TaskRequest{Op: hooks.TaskAssign, ID: "T1", Target: c.w1})
	c.mustCall(t, c.lead, hooks.TaskRequest{Op: hooks.TaskAssign, ID: "T2", Target: c.w1})
	c.mustCall(t, c.lead, hooks.TaskRequest{Op: hooks.TaskAssign, ID: "T3", Target: c.w2})
	c.mustCall(t, c.w1, hooks.TaskRequest{Op: hooks.TaskStatus, ID: "T1", State: "in-progress"})
	c.mustCall(t, c.w2, hooks.TaskRequest{Op: hooks.TaskStatus, ID: "T3", State: "in-progress"})
	c.mustCall(t, c.w2, hooks.TaskRequest{Op: hooks.TaskStatus, ID: "T3", State: "in-review"})

	ctx, ok := c.ws.PaneContext(c.w1)
	if !ok || !ctx.HasCounts {
		t.Fatalf("context = %+v, %v", ctx, ok)
	}
	if ctx.TaskCounts != (tasks.Counts{Total: 4, Mine: 2, MineActive: 1, InReview: 1, Unassigned: 1}) {
		t.Errorf("counts = %+v", ctx.TaskCounts)
	}
	text := ctx.Render()
	for _, want := range []string{
		"## Task board", " task list [-state s] [-role r] [-mine]", " task assign <id> <role|name|pane>        # manager role only",
		"2 tasks assigned to you (1 in progress), 1 in review, 1 planned and not assigned", "This is a snapshot",
		"not instructions from the user", "not authenticated",
		"task prune [-days N]", "The creator of a task, or the manager role, may cancel it",
		"no role has the manager role's powers only while no pane in the manager role is running",
		"The manager role is also messaged when a task is blocked",
		"only the manager role, a reviewer or a tester moves a task from in-review to done",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("briefing lacks %q:\n%s", want, text)
		}
	}
	if lead, _ := c.ws.PaneContext(c.lead); lead.TaskCounts.Mine != 0 || lead.TaskCounts.InReview != 1 {
		t.Errorf("the manager's counts = %+v", lead.TaskCounts)
	}
}

func TestBriefingHasNoBoardForAShellOrAPaneThatCannotCallBack(t *testing.T) {
	shell := PaneContext{PaneName: "sh", Cwd: "/work", Shell: true, CanSpawn: true}.Render()
	noAPI := PaneContext{PaneName: "api", Cwd: "/work"}.Render()
	for name, text := range map[string]string{"shell": shell, "no api": noAPI} {
		if strings.Contains(text, "## Task board") {
			t.Errorf("%s was briefed on the board", name)
		}
	}
	// A board that could not be read leaves its counts out and keeps the commands.
	text := PaneContext{PaneName: "api", Cwd: "/work", CanSpawn: true, SpawnCommand: "flockdeck"}.Render()
	if !strings.Contains(text, "## Task board") || strings.Contains(text, "when you were briefed") && strings.Contains(text, "The board when") {
		t.Errorf("briefing:\n%s", text)
	}
}

func TestAPaneWithNoRoleIsTheManagerOnlyWhileNoManagerPaneIsRunning(t *testing.T) {
	c := newCrew(t)
	c.mustCall(t, c.lead, hooks.TaskRequest{Op: hooks.TaskAdd, Title: "one"})
	c.mustCall(t, c.lead, hooks.TaskRequest{Op: hooks.TaskAdd, Title: "two"})
	c.mustCall(t, c.lead, hooks.TaskRequest{Op: hooks.TaskAssign, ID: "T1", Target: c.w1})
	c.mustCall(t, c.w1, hooks.TaskRequest{Op: hooks.TaskStatus, ID: "T1", State: "in-progress"})
	c.mustCall(t, c.w1, hooks.TaskRequest{Op: hooks.TaskStatus, ID: "T1", State: "in-review"})
	c.mustCall(t, c.reviewer, hooks.TaskRequest{Op: hooks.TaskStatus, ID: "T1", State: "done"})

	// A helper started with no role, beside a live manager.
	c.ws.NewTabWith(Choice{Kind: session.KindClaude, Agent: "sleeper"}, c.root, "")
	helper := c.ws.FocusedPane().ID
	if got := c.ws.Pane(helper).Role; got != "" {
		t.Fatalf("the helper has role %q", got)
	}
	refused := func(what string, req hooks.TaskRequest) {
		t.Helper()
		if _, err := c.call(helper, req); err == nil {
			t.Errorf("a role-less helper beside a manager was allowed to %s", what)
		}
	}
	refused("assign", hooks.TaskRequest{Op: hooks.TaskAssign, ID: "T2", Target: c.w2})
	refused("reopen a done task", hooks.TaskRequest{Op: hooks.TaskStatus, ID: "T1", State: "in-progress", Note: "again"})
	refused("cancel a task it did not create", hooks.TaskRequest{Op: hooks.TaskStatus, ID: "T2", State: "cancelled"})
	refused("prune", hooks.TaskRequest{Op: hooks.TaskPrune})
	// It can still add tasks and cancel its own.
	c.mustCall(t, helper, hooks.TaskRequest{Op: hooks.TaskAdd, Title: "mine"})
	c.mustCall(t, helper, hooks.TaskRequest{Op: hooks.TaskStatus, ID: "T3", State: "cancelled"})

	// The manager pane exits: the helper has the manager's powers.
	c.ws.Pane(c.lead).Err = errors.New("process exited")
	c.mustCall(t, helper, hooks.TaskRequest{Op: hooks.TaskAssign, ID: "T2", Target: c.w2})
	c.mustCall(t, helper, hooks.TaskRequest{Op: hooks.TaskStatus, ID: "T1", State: "in-progress", Note: "again"})
	if res := c.mustCall(t, helper, hooks.TaskRequest{Op: hooks.TaskPrune, Days: 1}); res.Pruned != 0 {
		t.Errorf("pruned %d fresh tasks", res.Pruned)
	}
}

func TestTheBoardSurvivesAWorkspaceRestartAndKeepsCountingIDs(t *testing.T) {
	c := newCrew(t)
	c.mustCall(t, c.lead, hooks.TaskRequest{Op: hooks.TaskAdd, Title: "one"})
	c.mustCall(t, c.lead, hooks.TaskRequest{Op: hooks.TaskAdd, Title: "two"})
	// A second workspace over the same project has never opened the board.
	ws2 := newTestWorkspace(t, c.root)
	ws2.NewTabWith(Choice{Kind: session.KindClaude, Agent: "sleeper"}, c.root, "")
	p := ws2.FocusedPane()
	p.Role = "manager"
	res, err := ws2.Task(hooks.TaskRequest{Auth: hooks.Auth{Pane: p.ID}, Op: hooks.TaskAdd, Title: "three"})
	if err != nil || res.Task.ID != "T3" {
		t.Fatalf("add after a restart = %+v, %v", res.Task, err)
	}
}

// The built-in role briefings describe what each role does on the board. These
// are the moves they describe, checked against the rule table, so a briefing and
// the rules cannot drift apart.
func TestRoleBriefingsDescribeMovesTheRulesAllow(t *testing.T) {
	byID := map[string]string{}
	for _, r := range agent.BuiltinRoles() {
		byID[r.ID] = r.Briefing
	}
	for role, phrases := range map[string][]string{
		"manager":  {"flockdeck task add", "flockdeck task assign <id> <pane id or role>", "flockdeck task status <id> done", "cannot close it", "before the worker moves it to in-progress"},
		"planner":  {"flockdeck task add <title>", "assigning and moving them is the manager's"},
		"worker":   {"task status <id> in-progress", "task branch <id> <branch>", "task status <id> in-review", "task status <id> blocked -note"},
		"reviewer": {"task list -state in-review", "task status <id> in-progress -note", "only if the manager asked"},
		"tester":   {"task status <id> in-progress -note"},
		"docs":     {"task status <id> in-progress", "task status <id> in-review"},
	} {
		for _, want := range phrases {
			if !strings.Contains(byID[role], want) {
				t.Errorf("the %s briefing lacks %q", role, want)
			}
		}
	}

	actor := func(pane, role string) tasks.Actor { return tasks.Actor{Pane: pane, Role: role} }
	mgr, wk, rev, tst := actor("m", "manager"), actor("w", "worker"), actor("r", "reviewer"), actor("t", "tester")
	task := func(s tasks.State) tasks.Task {
		return tasks.Task{ID: "T1", State: s, OwnerPane: "w", OwnerRole: "worker", Creator: actor("p", "planner")}
	}
	allow := func(who tasks.Actor, from, to tasks.State, note string) {
		t.Helper()
		if err := tasks.CheckMove(task(from), to, who, true, note); err != nil {
			t.Errorf("%s: %s -> %s: %v", who.Role, from, to, err)
		}
	}
	refuse := func(who tasks.Actor, from, to tasks.State, note string) {
		t.Helper()
		if err := tasks.CheckMove(task(from), to, who, true, note); err == nil {
			t.Errorf("%s: %s -> %s was allowed", who.Role, from, to)
		}
	}
	allow(wk, tasks.Assigned, tasks.InProgress, "")
	allow(wk, tasks.InProgress, tasks.InReview, "")
	allow(wk, tasks.InProgress, tasks.Blocked, "why")
	allow(rev, tasks.InReview, tasks.InProgress, "findings")
	allow(tst, tasks.InReview, tasks.InProgress, "failure")
	refuse(rev, tasks.InReview, tasks.InProgress, "")
	// The briefings say a reviewer or tester may close a task in review, and the
	// owner may not.
	allow(rev, tasks.InReview, tasks.Done, "")
	allow(tst, tasks.InReview, tasks.Done, "")
	refuse(wk, tasks.InReview, tasks.Done, "")
	refuse(wk, tasks.InProgress, tasks.Done, "")
	refuse(rev, tasks.InProgress, tasks.Done, "")
	allow(mgr, tasks.InReview, tasks.Done, "")
	allow(mgr, tasks.InProgress, tasks.Done, "")
	refuse(wk, tasks.Done, tasks.InProgress, "again")
}

func TestAssignAfterTheOwnerStartedWarnsAndTakesTheTask(t *testing.T) {
	c := newCrew(t)
	c.mustCall(t, c.lead, hooks.TaskRequest{Op: hooks.TaskAdd, Title: "the work"})
	res := c.mustCall(t, c.lead, hooks.TaskRequest{Op: hooks.TaskAssign, ID: "T1", Target: c.w1})
	if res.Warning != "" {
		t.Errorf("an assign before the work started warned: %q", res.Warning)
	}
	c.mustCall(t, c.w1, hooks.TaskRequest{Op: hooks.TaskStatus, ID: "T1", State: "in-progress"})
	res = c.mustCall(t, c.lead, hooks.TaskRequest{Op: hooks.TaskAssign, ID: "T1", Target: c.w2})
	if res.Task.OwnerPane != c.w2 {
		t.Errorf("the assign was refused or ignored: %+v", res.Task)
	}
	if !strings.Contains(res.Warning, "T1 was in progress under") || !strings.Contains(res.Warning, c.w1) {
		t.Errorf("warning = %q", res.Warning)
	}
}

func TestBriefingSaysTasksForTheRoleAreNotClaimedYet(t *testing.T) {
	c := newCrew(t)
	for _, title := range []string{"one", "two"} {
		c.mustCall(t, c.lead, hooks.TaskRequest{Op: hooks.TaskAdd, Title: title})
	}
	c.mustCall(t, c.lead, hooks.TaskRequest{Op: hooks.TaskAssign, ID: "T1", Target: "worker"})
	c.mustCall(t, c.lead, hooks.TaskRequest{Op: hooks.TaskAssign, ID: "T2", Target: "worker"})
	ctx, _ := c.ws.PaneContext(c.w1)
	text := ctx.Render()
	if !strings.Contains(text, "2 tasks for your role, none claimed yet") || strings.Contains(text, "assigned to you") {
		t.Errorf("briefing before a claim:\n%s", text)
	}
	c.mustCall(t, c.w1, hooks.TaskRequest{Op: hooks.TaskStatus, ID: "T1", State: "in-progress"})
	ctx, _ = c.ws.PaneContext(c.w1)
	if text := ctx.Render(); !strings.Contains(text, "1 task assigned to you (1 in progress), 1 task for your role, not claimed yet") {
		t.Errorf("briefing after a claim:\n%s", text)
	}
}
