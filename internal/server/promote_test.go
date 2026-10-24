package server

import (
	"strings"
	"testing"
	"time"
)

func promoteNotices(t *testing.T, c *controlClient) []noticeMsg {
	t.Helper()
	untilExport(t, "a notice", func() bool { return len(c.out) > 0 })
	time.Sleep(20 * time.Millisecond)
	return exportNotices(c)
}

// The person makes an agent they started by hand the manager. It takes the role
// and is told so; the team then has a manager.
func TestPromoteMakesAnAgentTheManager(t *testing.T) {
	srv, ws := newTestServer(t)
	id := agentPane(t, srv, ws, "solo", "")
	c := &controlClient{out: make(chan []byte, 8)}
	srv.handleCommand(c, command{Cmd: "promote", ID: id, Role: "manager"})
	untilExport(t, "the role", func() bool {
		role, _ := ask(srv, func() string { return ws.Pane(id).Role })
		return role == "manager"
	})
	if n, _ := ask(srv, func() int { return ws.Unread(id) }); n != 1 {
		t.Errorf("the agent has %d unread, want the one notice that it was promoted", n)
	}
	// A second manager in the same project is refused.
	other := agentPane(t, srv, ws, "other", "")
	srv.handleCommand(c, command{Cmd: "promote", ID: other, Role: "manager"})
	n := promoteNotices(t, c)
	if len(n) != 1 || !n[0].Error || !strings.Contains(n[0].Text, "already has a manager") {
		t.Errorf("notices = %+v", n)
	}
	if role, _ := ask(srv, func() string { return ws.Pane(other).Role }); role != "" {
		t.Errorf("the refused agent has the role %q", role)
	}
	// But any other role is fine for it.
	srv.handleCommand(c, command{Cmd: "promote", ID: other, Role: "reviewer"})
	untilExport(t, "the reviewer role", func() bool {
		role, _ := ask(srv, func() string { return ws.Pane(other).Role })
		return role == "reviewer"
	})
}

// Only an agent with no role that was started by hand can be promoted.
func TestPromoteRefusesWhatItShould(t *testing.T) {
	e := newGraphEnv(t)
	c := &controlClient{out: make(chan []byte, 8)}
	for name, tc := range map[string]struct {
		id, role, want string
		remote         bool
	}{
		"has a role":          {e.boss, "manager", "already the manager", false},
		"started by an agent": {e.nested, "worker", "started by another agent", false},
		"unknown role":        {e.lone, "wizard", "no role called", false},
		"not there":           {"nope", "worker", "not open", false},
		"relay window":        {e.lone, "worker", "not from a window reached through the relay", true},
	} {
		cl := c
		if tc.remote {
			cl = &controlClient{out: make(chan []byte, 8), remote: true, device: "full-dev"}
		}
		e.srv.handleCommand(cl, command{Cmd: "promote", ID: tc.id, Role: tc.role})
		n := promoteNotices(t, cl)
		if len(n) != 1 || !n[0].Error || !strings.Contains(n[0].Text, tc.want) {
			t.Errorf("%s: notices = %+v, want %q", name, n, tc.want)
		}
	}
	if role, _ := ask(e.srv, func() string { return e.ws.Pane(e.lone).Role }); role != "" {
		t.Errorf("a refused promote left the role %q", role)
	}
	if commandAccess["promote"] != accessFull {
		t.Error("promote is not accessFull")
	}
}
