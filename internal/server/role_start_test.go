package server

import (
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/workspace"
)

// The window's role start goes through startAgent, which shares its checks
// with `flockdeck spawn -role` (chooseInRole): the role must exist, it is not
// valid with a shell, and its agent applies only where the window named none.
func TestStartAgentInARole(t *testing.T) {
	srv, ws := newTestServer(t)
	rolesAgent(t, srv)
	root := ws.ActiveRoot()
	paneOf := func(id string) (role, agentID string) {
		got, _ := ask(srv, func() [2]string {
			p := ws.Pane(id)
			return [2]string{p.Role, p.Agent}
		})
		return got[0], got[1]
	}

	reply := runStartAgent(t, srv, command{Root: root, Task: "x", Role: "scuot"})
	if !reply.isErr || !strings.Contains(reply.notice, `no role called "scuot"`) || !strings.Contains(reply.notice, "scout") {
		t.Errorf("unknown role: notice = %+v", reply)
	}
	reply = runStartAgent(t, srv, command{Root: root, Task: "x", Role: "worker", Kind: "shell"})
	if !reply.isErr || !strings.Contains(reply.notice, "-shell starts a shell") {
		t.Errorf("a role with a shell: notice = %+v", reply)
	}

	// The role's agent applies when the window named neither agent nor model.
	reply = runStartAgent(t, srv, command{Root: root, Task: "look around", Role: "scout"})
	if reply.notice != "" {
		t.Fatalf("startAgent said %q", reply.notice)
	}
	if role, agentID := paneOf(reply.paneID); role != "scout" || agentID != "gocli2" {
		t.Errorf("pane has role %q on %q, want scout on gocli2", role, agentID)
	}
	// An agent the window named wins over the role's.
	reply = runStartAgent(t, srv, command{Root: root, Task: "look around", Role: "scout", Agent: "gocli"})
	if reply.notice != "" {
		t.Fatalf("startAgent said %q", reply.notice)
	}
	if role, agentID := paneOf(reply.paneID); role != "scout" || agentID != "gocli" {
		t.Errorf("pane has role %q on %q, want the window's own agent gocli", role, agentID)
	}
	// A window is not a pane in a role: the scout lists no roles to start, and
	// the window may still start a manager.
	reply = runStartAgent(t, srv, command{Root: root, Task: "run things", Role: "manager", Agent: "gocli"})
	if reply.notice != "" {
		t.Fatalf("startAgent said %q", reply.notice)
	}
	if role, _ := paneOf(reply.paneID); role != "manager" {
		t.Errorf("pane has role %q, want manager", role)
	}
	// With no role the pane has none, as before.
	reply = runStartAgent(t, srv, command{Root: root, Task: "plain", Agent: "gocli"})
	if role, _ := paneOf(reply.paneID); reply.notice != "" || role != "" {
		t.Errorf("a start with no role gave role %q (%q)", role, reply.notice)
	}

	// The role reaches the state the window draws, and the pane info.
	msg, _ := ask(srv, func() stateMsg { return srv.snapshot() })
	var found bool
	for _, v := range msg.Panes {
		if v.Role == "manager" {
			found = true
		}
	}
	if !found {
		t.Error("no pane in the snapshot carries role manager")
	}
	reply = runStartAgent(t, srv, command{Root: root, Task: "again", Role: "manager", Agent: "gocli"})
	d, _ := ask(srv, func() workspace.PaneDetails { d, _ := ws.PaneDetailsOf(reply.paneID, false); return d })
	var info string
	for _, f := range paneInfoFields(d, "p") {
		if f.Label == "Role" {
			info = f.Value
		}
	}
	if info != "manager" {
		t.Errorf("pane info Role = %q, want manager", info)
	}
}

// A pane in a role may start only the roles it lists; the check is the CLI's.
func TestChooseInRoleKeepsSpawnRights(t *testing.T) {
	srv, _ := newTestServer(t)
	rolesAgent(t, srv)
	cat := srv.ws.Catalog()
	if _, _, err := chooseInRole(cat, "scout", "worker", "", "", "gocli"); err == nil || !strings.Contains(err.Error(), "scout role may not start") {
		t.Errorf("scout starting a worker: error = %v", err)
	}
	if _, _, err := chooseInRole(cat, "manager", "worker", "", "", "gocli"); err != nil {
		t.Errorf("manager starting a worker: %v", err)
	}
	if _, _, err := chooseInRole(cat, "manager", "manager", "", "", "gocli"); err == nil {
		t.Error("a manager started a manager, which it does not list")
	}
	agentID, _, err := chooseInRole(cat, "", "scout", "", "", "gocli")
	if err != nil || agentID != "gocli2" {
		t.Errorf("scout with nothing named = %q, %v; want its own agent gocli2", agentID, err)
	}
}

// The catalog a window draws its role picker from lists every role with the
// line that says what it is for, built-ins first.
func TestTheCatalogListsRolesWithDescriptions(t *testing.T) {
	srv, _ := newTestServer(t)
	rolesAgent(t, srv)
	cat := buildCatalog(srv.ws.Catalog(), srv.ws.ActiveRoot())
	got := map[string]catalogRole{}
	for _, r := range cat.Roles {
		got[r.ID] = r
	}
	for _, id := range []string{"manager", "planner", "worker", "reviewer", "tester", "docs"} {
		if got[id].Name == "" || got[id].Description == "" {
			t.Errorf("role %s in the catalog: %+v, want a name and a description", id, got[id])
		}
	}
	if s := got["scout"]; s.Agent != "gocli2" || s.Description != "" {
		t.Errorf("user role scout = %+v, want agent gocli2 and no description", s)
	}
	if len(cat.Roles) != 7 || cat.Roles[0].ID != "manager" || cat.Roles[6].ID != "scout" {
		t.Errorf("roles = %+v, want the built-ins in order and then the user's", cat.Roles)
	}
}
