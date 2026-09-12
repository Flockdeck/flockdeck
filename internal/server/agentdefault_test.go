package server

import (
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/agent"
)

// The picker sets the default every project runs as well as one project's
// own, and undoes a project's own. The first took agents.json edited by hand,
// and the second could not be done at all.
func TestThePickerSetsEitherDefaultAndUndoesAProjects(t *testing.T) {
	stateDir(t)
	srv, ws := newTestServer(t)
	conn := dialControl(t, srv)
	nextHello(t, conn)
	root := ws.ActiveRoot()

	// waitFor polls the file, because the command is answered on a goroutine
	// of the server's and says nothing back but a notice.
	waitFor := func(what string, ok func(*agent.Catalog) bool) {
		t.Helper()
		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
			if ok(agent.Load()) {
				return
			}
		}
		t.Fatalf("%s: agents.json holds %+v for every project and %+v for this one",
			what, agent.Load().DefaultsFor(""), agent.Load().DefaultsFor(root))
	}

	sendCmd(t, conn, command{Cmd: "setAgentDefault", Kind: "all", Agent: "claude", Model: "sonnet"})
	waitFor("the default for every project was not set", func(c *agent.Catalog) bool {
		return c.DefaultsFor("") == agent.Defaults{Agent: "claude", Model: "sonnet"}
	})

	sendCmd(t, conn, command{Cmd: "setAgentDefault", Agent: "claude", Model: "opus"})
	waitFor("this project's default was not set", func(c *agent.Catalog) bool {
		return c.DefaultsFor(root).Model == "opus"
	})

	sendCmd(t, conn, command{Cmd: "setAgentDefault"})
	waitFor("this project's default was not forgotten", func(c *agent.Catalog) bool {
		return c.DefaultsFor(root) == c.DefaultsFor("")
	})
}
