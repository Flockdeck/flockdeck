package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/hooks"
)

// rolesAgent gives the catalog two agents that start on any machine and a role
// of the user's that names the second.
func rolesAgent(t *testing.T, srv *Server) {
	t.Helper()
	path, err := agent.ConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	body := `{"version": 1,
		"agents": [{"id": "gocli", "name": "Go", "exe": "go", "args": [{"value": "{{session}}"}], "caps": {"resume": true}},
			{"id": "gocli2", "name": "Go two", "exe": "go", "args": [{"value": "{{session}}"}]}],
		"roles": [{"id": "scout", "agent": "gocli2"}]}`
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	srv.ws.ReloadAgents()
}

// The role in `flockdeck spawn -role` is checked where the helper starts, since
// the command line's own check is only a courtesy: it must exist, the pane
// asking must be allowed it, and its agent applies only where none was asked for.
func TestSpawnInARoleOverTheCallbackAPI(t *testing.T) {
	srv, ws := newTestServer(t)
	rolesAgent(t, srv)
	pane := spawnGoPane(t, srv, ws)
	hook := ws.HookServer()
	if hook == nil {
		t.Skip("no hook server")
	}
	spawn := func(from string, req hooks.SpawnRequest) (hooks.SpawnResult, error) {
		return hooks.Spawn(hook.BaseURL(), hook.Token(), from, req)
	}
	paneOf := func(id string) (role, agentID string) {
		got, _ := ask(srv, func() [2]string {
			p := ws.Pane(id)
			return [2]string{p.Role, p.Agent}
		})
		return got[0], got[1]
	}

	// Unknown roles start nothing and say which ones there are.
	_, err := spawn(pane, hooks.SpawnRequest{Task: "x", Role: "scuot"})
	if err == nil || !strings.Contains(err.Error(), `no role called "scuot"`) || !strings.Contains(err.Error(), "scout") {
		t.Errorf("unknown role: error = %v", err)
	}
	if _, err := spawn(pane, hooks.SpawnRequest{Task: "x", Shell: true, Role: "worker"}); err == nil {
		t.Error("a shell was started in a role")
	}

	// The role's agent is used when the spawn names none, and the spawn's own
	// wins when it does.
	res, err := spawn(pane, hooks.SpawnRequest{Task: "look around", Role: "scout"})
	if err != nil {
		t.Fatal(err)
	}
	if role, agentID := paneOf(res.PaneID); res.Role != "scout" || role != "scout" || agentID != "gocli2" {
		t.Errorf("pane has role %q on %q (result %q), want scout on gocli2", role, agentID, res.Role)
	}
	res, err = spawn(pane, hooks.SpawnRequest{Task: "look around", Role: "scout", Agent: "gocli"})
	if err != nil {
		t.Fatal(err)
	}
	if role, agentID := paneOf(res.PaneID); role != "scout" || agentID != "gocli" {
		t.Errorf("pane has role %q on %q, want the spawn's own agent gocli", role, agentID)
	}

	// A pane in a role may start only what its role lists. The scout lists
	// nothing, so it cannot start a role; the manager can start a worker.
	scout, err := spawn(pane, hooks.SpawnRequest{Task: "look around", Role: "scout"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := spawn(scout.PaneID, hooks.SpawnRequest{Task: "x", Role: "worker"}); err == nil || !strings.Contains(err.Error(), "scout role may not start") {
		t.Errorf("scout starting a worker: error = %v", err)
	}
	if _, err := spawn(scout.PaneID, hooks.SpawnRequest{Task: "plain helper", Agent: "gocli"}); err != nil {
		t.Errorf("a role with no spawn rights was stopped from starting an agent with no role: %v", err)
	}
}
