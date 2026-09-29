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
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	nextHello(t, conn)
	root := srv.activeRoot()

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

// A default saved while a probe is already running is not lost. The probe
// read agents.json before the save, and the refresh the save asked for was
// skipped because that probe was running, so the catalog panes are started
// from went on holding the old default until the next probe, a minute on.
func TestADefaultSavedDuringAProbeIsUsed(t *testing.T) {
	stateDir(t)
	srv, ws := newTestServer(t)
	conn := dialControl(t, srv)
	nextHello(t, conn)
	root := srv.activeRoot()

	// A probe is under way, having read agents.json already.
	if _, ok := ask(srv, func() int {
		srv.catalog()
		srv.agentsProbing = true
		return 0
	}); !ok {
		t.Fatal("the workspace did not answer")
	}

	want := agent.Defaults{Agent: "claude", Model: "sonnet"}
	sendCmd(t, conn, command{Cmd: "setAgentDefault", Kind: "all", Agent: want.Agent, Model: want.Model})
	for deadline := time.Now().Add(10 * time.Second); agent.Load().DefaultsFor("") != want; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the default was not saved")
		}
	}
	// The save asks for its refresh before the connection reads its next
	// command, so once a later command has been answered from the workspace
	// goroutine, the refresh has been handled there too.
	sendCmd(t, conn, command{Cmd: "selectTab", ID: "no-such-tab"})
	for {
		var note noticeMsg
		readUntil(t, conn, "notice", &note)
		if note.Text == tabGone {
			break
		}
	}

	// The probe that was under way lands its answer, from before the save.
	if _, ok := ask(srv, func() int {
		srv.probeDone(srv.agents, root)
		return 0
	}); !ok {
		t.Fatal("the workspace did not answer")
	}
	for deadline := time.Now().Add(5 * time.Second); ws.Catalog().DefaultsFor("") != want; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("panes are still started from %+v, want the default just saved, %+v", ws.Catalog().DefaultsFor(""), want)
		}
	}
}
