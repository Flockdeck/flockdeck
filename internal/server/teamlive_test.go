package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/tasks"
)

// watcher registers a window of the test as one the server pushes to.
func (e *teamEnv) watcher(t *testing.T, queue int) *controlClient {
	t.Helper()
	c := &controlClient{out: make(chan []byte, queue)}
	e.srv.mu.Lock()
	e.srv.clients[c] = true
	e.srv.mu.Unlock()
	t.Cleanup(func() {
		e.srv.mu.Lock()
		delete(e.srv.clients, c)
		e.srv.mu.Unlock()
	})
	return c
}

func typeOf(raw []byte) string {
	var head struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(raw, &head)
	return head.Type
}

// A window whose queue was full when a picture was pushed is sent it again: the
// push is not skipped as unchanged next time.
func TestAPushToAFullQueueIsSentAgain(t *testing.T) {
	e := newTeamEnv(t)
	c := e.watcher(t, 1)
	c.out <- []byte(`{"type":"filler"}`)
	ask(e.srv, func() bool {
		c.teamRoot = e.root
		e.srv.teamAt = time.Time{}
		e.srv.pushTeam()
		return true
	})
	if sent, _ := ask(e.srv, func() bool { return c.teamSent != nil }); sent {
		t.Fatal("a picture the queue refused was marked as sent")
	}
	// The window catches up; with no other change the server comes back for it.
	<-c.out
	select {
	case raw := <-c.out:
		if typeOf(raw) != "team" {
			t.Fatalf("got %s", raw)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the picture was never sent again")
	}
	if sent, _ := ask(e.srv, func() bool { return c.teamSent != nil }); !sent {
		t.Error("the picture that was queued is not marked as sent")
	}
}

func TestAWatchedProjectThatClosesIsToldToThePanel(t *testing.T) {
	e := newTeamEnv(t)
	e.withOtherProject(t)
	c := e.watcher(t, 16)
	e.srv.teamCommand(c, command{Cmd: "teamWatch", Root: e.otherRoot})
	if raw := <-c.out; typeOf(raw) != "team" {
		t.Fatalf("first = %s", raw)
	}
	ask(e.srv, func() bool {
		if err := e.ws.CloseProject(e.otherRoot); err != nil {
			t.Error(err)
		}
		e.srv.teamAt = time.Time{}
		e.srv.pushTeam()
		return true
	})
	select {
	case raw := <-c.out:
		var gone struct {
			Type, Root, Error string
		}
		if err := json.Unmarshal(raw, &gone); err != nil || gone.Type != "teamGone" || gone.Root != e.otherRoot || !strings.Contains(gone.Error, "no project is open") {
			t.Fatalf("got %s", raw)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the window was not told the project had closed")
	}
	// Once, not on every push.
	ask(e.srv, func() bool { e.srv.teamAt = time.Time{}; e.srv.pushTeam(); return true })
	select {
	case raw := <-c.out:
		t.Fatalf("told again: %s", raw)
	case <-time.After(300 * time.Millisecond):
	}
}

// writeBoard writes a board file of n open tasks, as another process might.
func writeBoard(t *testing.T, path string, n int) {
	t.Helper()
	list := make([]tasks.Task, n)
	base := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range list {
		list[i] = tasks.Task{
			ID: fmt.Sprintf("T%d", i+1), Title: fmt.Sprintf("task number %d with a title of ordinary length", i+1),
			Body: strings.Repeat("b", 500), State: tasks.Planned, OwnerRole: "worker",
			Creator: tasks.Actor{Pane: "p", Role: "manager", Name: "boss"}, Created: base, Updated: base.Add(time.Duration(i) * time.Minute),
		}
	}
	data, err := json.Marshal(map[string]any{"version": 1, "next": n + 1, "tasks": list})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestABigBoardIsCappedInThePushAndAnUnchangedPictureReadsNothing(t *testing.T) {
	e := newTeamEnv(t)
	board, _ := ask(e.srv, func() *tasks.Board { b, _ := e.ws.TeamBoard(e.root); return b })
	writeBoard(t, board.Path, 2000)

	data := func() []byte {
		type out struct {
			data []byte
			err  error
		}
		r, _ := ask(e.srv, func() out { _, d, err := e.srv.teamData(e.root); return out{d, err} })
		if r.err != nil {
			t.Fatal(r.err)
		}
		return r.data
	}
	first := data()
	var m teamMsg
	if err := json.Unmarshal(first, &m); err != nil {
		t.Fatal(err)
	}
	if m.Counts.Open != 2000 || len(m.Tasks) != maxTeamOpen || m.OpenHidden != 2000-maxTeamOpen || m.Counts.Tasks["planned"] != 2000 {
		t.Errorf("open %d, listed %d, hidden %d, by state %v", m.Counts.Open, len(m.Tasks), m.OpenHidden, m.Counts.Tasks)
	}
	if len(first) > 200*1024 {
		t.Errorf("the push of a 2000-task board is %d bytes", len(first))
	}
	// The newest are the ones listed.
	if m.Tasks[len(m.Tasks)-1].ID != "T2000" || m.Tasks[0].ID != fmt.Sprintf("T%d", 2000-maxTeamOpen+1) {
		t.Errorf("listed %s to %s", m.Tasks[0].ID, m.Tasks[len(m.Tasks)-1].ID)
	}

	// Asking again with nothing changed reads nothing and builds nothing.
	loads := board.Loads()
	for i := 0; i < 20; i++ {
		if got := data(); string(got) != string(first) {
			t.Fatal("an unchanged picture came out different")
		}
	}
	if board.Loads() != loads {
		t.Errorf("the board file was read %d times for an unchanged picture", board.Loads()-loads)
	}
	// The picture is cached as built, not marshalled again.
	a, b := data(), data()
	if &a[0] != &b[0] {
		t.Error("an unchanged picture was encoded again")
	}

	// A change through the window shows. The board takes 2000, so it is one short.
	writeBoard(t, board.Path, 1999)
	e.mustDo(t, command{Cmd: "taskAdd", Title: "one more"})
	if err := json.Unmarshal(data(), &m); err != nil || m.Counts.Open != 2000 {
		t.Errorf("after an add: open %d, %v", m.Counts.Open, err)
	}
	// A change by another process shows too.
	writeBoard(t, board.Path, 5)
	m = teamMsg{}
	if err := json.Unmarshal(data(), &m); err != nil || m.Counts.Open != 5 || m.OpenHidden != 0 {
		t.Errorf("after the file was rewritten: open %d hidden %d, %v", m.Counts.Open, m.OpenHidden, err)
	}
	// A roster change shows without the board changing.
	ask(e.srv, func() bool { e.ws.Pane(e.dev).Sess.SetStatus(session.StatusWorking, ""); return true })
	if err := json.Unmarshal(data(), &m); err != nil {
		t.Fatal(err)
	}
	for _, r := range m.Roster {
		if r.Pane == e.dev && r.Status != "working" {
			t.Errorf("a status change was not in the picture: %+v", r)
		}
	}
	// All of them are one command away.
	writeBoard(t, board.Path, 2000)
	e.srv.teamCommand(e.c, command{Cmd: "taskList", Root: e.root, State: "all"})
	var list teamTasksMsg
	e.next(t, "tasks", &list)
	if len(list.Tasks) != 2000 {
		t.Errorf("taskList all has %d tasks", len(list.Tasks))
	}
}

func TestCloseFinishedJudgesEachPaneAgainWhenItIsAsked(t *testing.T) {
	e := newTeamEnv(t)
	e.withOtherProject(t)
	// Idle panes: dev, eyes and boss. Between the window's last picture and the
	// click, dev went back to work, boss was locked and one id is not this
	// project's.
	ask(e.srv, func() bool {
		e.ws.Pane(e.dev).Sess.SetStatus(session.StatusWorking, "")
		e.ws.SetPaneLocked(e.boss, true)
		return true
	})
	res := e.mustDo(t, command{Cmd: "teamClose", Panes: []string{e.dev, e.eyes, e.boss, e.otherAgent, "forged"}})
	if res.Closed != 1 || len(res.Skipped) != 4 {
		t.Fatalf("closed %d, skipped %q", res.Closed, res.Skipped)
	}
	want := []string{"not finished any more", "locked", "not an agent pane of this project", "not an agent pane of this project"}
	for i, w := range want {
		if !strings.Contains(res.Skipped[i], w) {
			t.Errorf("skipped[%d] = %q, want %q", i, res.Skipped[i], w)
		}
	}
	open := func(id string) bool { v, _ := ask(e.srv, func() bool { return e.ws.Pane(id) != nil }); return v }
	if open(e.eyes) || !open(e.dev) || !open(e.boss) || !open(e.otherAgent) {
		t.Errorf("open after: eyes %v dev %v boss %v other %v", open(e.eyes), open(e.dev), open(e.boss), open(e.otherAgent))
	}
	// Once it is idle again it is closed.
	ask(e.srv, func() bool { e.ws.Pane(e.dev).Sess.SetStatus(session.StatusIdle, ""); return true })
	if res := e.mustDo(t, command{Cmd: "teamClose", Panes: []string{e.dev}}); res.Closed != 1 {
		t.Errorf("an idle pane was not closed: %+v", res)
	}
	// Too many at once, and a closed project.
	ids := make([]string, 101)
	if res := e.do(t, command{Cmd: "teamClose", Panes: ids}); res.OK || !strings.Contains(res.Error, "at most") {
		t.Errorf("101 panes = %+v", res)
	}
	if res := e.do(t, command{Cmd: "teamClose", Root: t.TempDir(), Panes: []string{e.boss}}); res.OK || !strings.Contains(res.Error, "no project is open") {
		t.Errorf("a project that is not open = %+v", res)
	}
}

// The command arrives through handleCommand from a full-role remote device, the
// way a phone's would, and is refused there.
func TestAFullRemoteDeviceIsRefusedTheTeamCommandsThroughHandleCommand(t *testing.T) {
	env := newArtifactEnv(t)
	phone, err := dialRemoteControl(env.ts, env.ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer phone.CloseNow()
	root := env.srv.ws.ActiveRoot()
	for _, cmd := range []command{
		{Cmd: "taskAdd", Root: root, Title: "x", Req: "r"},
		{Cmd: "msgSend", Root: root, Target: "worker", Text: "x", Req: "r"},
		{Cmd: "teamWatch", Root: root, Req: "r"},
		{Cmd: "teamClose", Root: root, Panes: []string{"p"}, Req: "r"},
		{Cmd: "msgSeen", Root: root, ID: "p", MsgID: 1, Req: "r"},
	} {
		sendCmd(t, phone, cmd)
		var res teamResultMsg
		readUntil(t, phone, "teamResult", &res)
		if res.OK || !strings.Contains(res.Error, "machine Flockdeck runs on") || res.Op != cmd.Cmd {
			t.Errorf("%s from a full remote device = %+v", cmd.Cmd, res)
		}
	}
	if m, _ := ask(env.srv, func() int {
		b, _ := env.srv.ws.TeamBoard(root)
		l, _, _ := b.List(tasks.Filter{All: true})
		return len(l)
	}); m != 0 {
		t.Errorf("the board has %d tasks after refused commands", m)
	}
}
