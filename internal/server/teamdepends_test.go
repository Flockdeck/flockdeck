package server

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/hooks"
)

// The tasks a task waits on and its estimate reach the team push as plain
// facts, set from the command line or from a window, and are not sent to a
// relay window.
func TestTeamPushCarriesDependsAndEstimate(t *testing.T) {
	e := newTeamEnv(t)
	hookSrv := e.ws.HookServer()
	api, token := hookSrv.BaseURL(), hookSrv.Token()
	ts := dialTeam(t, e.srv)
	sendCmd(t, ts.conn, command{Cmd: "teamWatch", Root: e.root})
	ts.until("the first picture", func(m teamMsg) bool { return len(m.Roster) >= 3 })

	for _, title := range []string{"parser", "lexer"} {
		if _, err := hooks.Task(api, token, e.boss, hooks.TaskRequest{Op: hooks.TaskAdd, Title: title}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := hooks.Task(api, token, e.boss, hooks.TaskRequest{Op: hooks.TaskDepends, ID: "T2", Depends: []string{"T1"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := hooks.Task(api, token, e.boss, hooks.TaskRequest{Op: hooks.TaskEstimate, ID: "T2", Minutes: 45}); err != nil {
		t.Fatal(err)
	}
	m := ts.until("the dependency", func(m teamMsg) bool {
		return len(m.Tasks) == 2 && slices.Equal(m.Tasks[1].Depends, []string{"T1"}) && m.Tasks[1].EstimateMin == 45
	})
	if len(m.Tasks[0].Depends) != 0 || m.Tasks[0].EstimateMin != 0 {
		t.Errorf("T1 has %+v", m.Tasks[0])
	}
	raw, _ := json.Marshal(m.Tasks[0])
	if strings.Contains(string(raw), `"depends"`) || strings.Contains(string(raw), `"estimateMin"`) {
		t.Errorf("a task with none sends the fields anyway: %s", raw)
	}

	// A loop is refused and says so.
	if _, err := hooks.Task(api, token, e.boss, hooks.TaskRequest{Op: hooks.TaskDepends, ID: "T1", Depends: []string{"T2"}}); err == nil {
		t.Error("a dependency loop was accepted")
	}

	// A window sets them with commands, and a relay window may not.
	sendCmd(t, ts.conn, command{Cmd: "taskDepends", Root: e.root, ID: "T2", Depends: []string{}, Req: "r1"})
	ts.until("cleared", func(m teamMsg) bool { return len(m.Tasks) == 2 && len(m.Tasks[1].Depends) == 0 })
	sendCmd(t, ts.conn, command{Cmd: "taskEstimate", Root: e.root, ID: "T2", Size: 10, Req: "r2"})
	ts.until("estimate", func(m teamMsg) bool { return len(m.Tasks) == 2 && m.Tasks[1].EstimateMin == 10 })
	for _, name := range []string{"taskDepends", "taskEstimate"} {
		if commandAccess[name] != accessFull {
			t.Errorf("%s is not accessFull", name)
		}
	}
	for _, typ := range []string{"team", "tasks"} {
		if scrubForRemote([]byte(`{"type":"`+typ+`","tasks":[{"id":"T2","depends":["T1"],"estimateMin":45}]}`)) != nil {
			t.Errorf("a relay window is sent a %s message", typ)
		}
	}
}
