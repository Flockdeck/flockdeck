package server

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/hooks"
)

// A task is given to a team with taskTeam, and the link reaches the team push
// (as the task's team) and the graph (as the team's task).
func TestATaskIsGivenToATeam(t *testing.T) {
	e := newGraphEnv(t)
	hookSrv := e.ws.HookServer()
	api, token := hookSrv.BaseURL(), hookSrv.Token()
	for _, title := range []string{"basket", "checkout"} {
		if _, err := hooks.Task(api, token, e.boss, hooks.TaskRequest{Op: hooks.TaskAdd, Title: title}); err != nil {
			t.Fatal(err)
		}
	}
	cards := func() []teamTaskView {
		_, data, err := e.srv.teamData(e.root)
		if err != nil {
			t.Fatal(err)
		}
		var m teamMsg
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatal(err)
		}
		return m.Tasks
	}
	if c := cards()[0]; c.Team != "" || c.TeamGiven {
		t.Errorf("a task nobody has is in team %q", c.Team)
	}

	// The team of a pane that owns the task is the card's team before one is given.
	if _, err := hooks.Task(api, token, e.boss, hooks.TaskRequest{Op: hooks.TaskAssign, ID: "T2", Target: e.nested}); err != nil {
		t.Fatal(err)
	}
	if c := cards()[1]; c.Team != e.boss || c.TeamGiven {
		t.Errorf("an owned task has team %q given=%v, want %q derived", c.Team, c.TeamGiven, e.boss)
	}

	// Given to the team, an unowned task goes to its manager as well.
	if _, err := hooks.Task(api, token, e.boss, hooks.TaskRequest{Op: hooks.TaskTeam, ID: "T1", Team: e.boss}); err != nil {
		t.Fatal(err)
	}
	c := cards()[0]
	if c.Team != e.boss || !c.TeamGiven || c.OwnerPane != e.boss {
		t.Errorf("T1 is %+v, want the boss's team and the boss", c)
	}
	g := e.snapshotGraph(t)
	for _, tm := range g.Teams {
		if tm.ID == e.boss && tm.TaskID != "T1" {
			t.Errorf("the team's task is %q, want T1", tm.TaskID)
		}
	}

	// A pane another agent started is not the root of a team, and a pane that
	// is not there is not one at all.
	if _, err := hooks.Task(api, token, e.boss, hooks.TaskRequest{Op: hooks.TaskTeam, ID: "T1", Team: e.dev}); err == nil {
		t.Error("a task was given to a pane that is not a team root")
	}
	if _, err := hooks.Task(api, token, e.boss, hooks.TaskRequest{Op: hooks.TaskTeam, ID: "T1", Team: "p99"}); err == nil {
		t.Error("a task was given to a pane that does not exist")
	}

	// Cleared, the card has the owner's team again and the graph keeps the
	// manager's own task.
	if _, err := hooks.Task(api, token, e.boss, hooks.TaskRequest{Op: hooks.TaskTeam, ID: "T1"}); err != nil {
		t.Fatal(err)
	}
	if c := cards()[0]; c.TeamGiven {
		t.Errorf("T1 is still given: %+v", c)
	}
	raw, _ := json.Marshal(cards()[0])
	if !strings.Contains(string(raw), `"team":"`+e.boss+`"`) {
		t.Errorf("the owner's team is not on the card: %s", raw)
	}

	if commandAccess["taskTeam"] != accessFull {
		t.Error("taskTeam is not accessFull")
	}
	if scrubForRemote([]byte(`{"type":"team","tasks":[{"id":"T1","team":"p1"}]}`)) != nil {
		t.Error("a relay window is sent the team push")
	}
}
