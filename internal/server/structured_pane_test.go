package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/acp/acptest"
	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/store"
	"github.com/jmwri/flockdeck/internal/structured"
	"github.com/jmwri/flockdeck/internal/workspace"
)

// These tests put a structured pane, backed by the fake agent, in a team with
// terminal panes, and read it through the windows' messages.

type structuredEnv struct {
	*graphEnv
	child string
}

// newStructuredEnv is the graph team (boss, dev, eyes, nested, lone, all
// terminal-shaped) with one structured helper of the boss started with task.
func newStructuredEnv(t *testing.T, task string) *structuredEnv {
	t.Helper()
	return newStructuredEnvArgs(t, task)
}

// newStructuredEnvArgs is newStructuredEnv with more arguments for the fake agent.
func newStructuredEnvArgs(t *testing.T, task string, more ...string) *structuredEnv {
	t.Helper()
	return newStructuredEnvWith(t, task, structuredEnvOpts{Args: more})
}

// structuredEnvOpts are the ways a test can change the structured helper
// newStructuredEnv starts. The zero value is the fake agent in the temporary
// directory.
type structuredEnvOpts struct {
	// Args follow acptest.ArgAgent when the program is this binary.
	Args []string
	// Program, when set, is started in place of this binary, with ProgramArgs.
	Program     string
	ProgramArgs []string
	// Cwd is where the pane works; empty is the boss's directory.
	Cwd string
	// BeforeSpawn runs after the workspace is made and before the pane starts,
	// for a test that changes the environment the agent is given.
	BeforeSpawn func()
}

func newStructuredEnvWith(t *testing.T, task string, o structuredEnvOpts) *structuredEnv {
	t.Helper()
	e := &structuredEnv{graphEnv: newGraphEnv(t)}
	if err := store.SavePrefs(store.Prefs{StructuredPanes: true}); err != nil {
		t.Fatal(err)
	}
	// The adapter's program must be on PATH for the agent to count as installed.
	// It is never run: the fake agent is started in its place.
	dir := t.TempDir()
	file, body := "opencode", "#!/bin/sh\nexit 0\n"
	if runtime.GOOS == "windows" {
		file, body = "opencode.cmd", "@exit /b 0\r\n"
	}
	if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	agent.Refresh()

	exe, _ := os.Executable()
	e.ws.UseStructuredCommand(func(agent.Spec) *structured.Command {
		if o.Program != "" {
			return &structured.Command{Path: o.Program, Args: o.ProgramArgs}
		}
		return &structured.Command{Path: exe, Args: append([]string{acptest.ArgAgent}, o.Args...)}
	})
	ask(e.srv, func() bool { e.ws.ReloadAgents(); return true })
	if o.BeforeSpawn != nil {
		o.BeforeSpawn()
	}
	id, ok := ask(e.srv, func() string {
		id, err := e.ws.Spawn(e.boss, workspace.SpawnOptions{Task: task, Cwd: o.Cwd, Role: "worker", Agent: "opencode-acp",
			Kind: session.KindClaude, SpawnedByAgent: true})
		if err != nil {
			t.Errorf("spawn: %v", err)
		}
		return id
	})
	if !ok || id == "" {
		t.Fatal("no structured pane")
	}
	e.child = id
	return e
}

func (e *structuredEnv) status(t *testing.T, want session.Status) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		st, _ := ask(e.srv, func() session.Status { st, _ := e.ws.Pane(e.child).Status(); return st })
		if st == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("status %v, want %v", st, want)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// The old window's pane list has the structured pane with its flag set, no
// terminal size, and every other pane as it was.
func TestTheOldWindowsPaneListMarksAStructuredPane(t *testing.T) {
	e := newStructuredEnv(t, "plain")
	e.status(t, session.StatusIdle)
	snap, _ := ask(e.srv, func() stateMsg { return e.srv.snapshot() })
	pv, ok := snap.Panes[e.child]
	if !ok {
		t.Fatal("the pane is not in the state")
	}
	if !pv.Structured || pv.Kind != "agent" || pv.Status != "idle" || pv.Cols != 0 || pv.Rows != 0 {
		t.Errorf("structured %v, kind %q, status %q, size %dx%d", pv.Structured, pv.Kind, pv.Status, pv.Cols, pv.Rows)
	}
	if pv.Last == nil || !strings.Contains(pv.Last.Text, "Hello world") {
		t.Errorf("last reply = %+v", pv.Last)
	}
	if snap.Panes[e.dev].Structured {
		t.Error("a terminal pane is marked structured")
	}
	// Flockdeck starts opencode with its permissions set to ask, so the manifest
	// no longer says it runs without asking and the push carries no warning.
	if pv.RunsUnasked {
		t.Error("the opencode pane is marked as running without asking")
	}
	if snap.Panes[e.dev].RunsUnasked {
		t.Error("a terminal pane is marked as running without asking")
	}
	// The agents list names it too, with the same activity.
	ask(e.srv, func() bool { e.srv.sendAgents(e.c); return true })
	var list agentsMsg
	e.next(t, "agents", &list)
	found := false
	for _, it := range list.Items {
		if it.PaneID == e.child {
			found = it.Activity == workspace.ActivityIdle
		}
	}
	if !found {
		t.Errorf("the agents list does not show the pane idle: %+v", list.Items)
	}
}

// The graph shows a parent with a structured child and a terminal child, the
// structured one's kind and status from its runtime, and a pending request as
// a permission need with the options the agent named.
func TestTheGraphShowsAStructuredPane(t *testing.T) {
	e := newStructuredEnv(t, "hold-perm")
	e.status(t, session.StatusWaiting)
	g := e.snapshotGraph(t)
	n, ok := g.node(e.child)
	if !ok {
		t.Fatal("no node")
	}
	if n.Kind != graphKindStructured || n.Status != "waiting" || n.Activity != "waiting" || n.Parent != e.boss {
		t.Errorf("kind %q, status %q, activity %q, parent %q", n.Kind, n.Status, n.Activity, n.Parent)
	}
	if n.WaitingOn.Kind != "user" {
		t.Errorf("waiting on %+v", n.WaitingOn)
	}
	if dev, _ := g.node(e.dev); dev.Kind != graphKindTerminal || dev.Parent != e.boss {
		t.Errorf("the terminal sibling is %+v", dev)
	}
	needed := false
	for _, it := range g.Teams[0].Needs {
		if it.Pane == e.child && it.Kind == "permission" {
			needed = true
		}
	}
	if !needed {
		t.Errorf("no permission need for the pane: %+v", g.Teams[0].Needs)
	}
	snap, _ := ask(e.srv, func() stateMsg { return e.srv.snapshot() })
	perm := snap.Panes[e.child].Permission
	if perm == nil || len(perm.Options) == 0 {
		t.Fatalf("permission = %+v", perm)
	}
	for _, o := range perm.Options {
		if o.ID == "" || o.Name == "" || o.Kind == "" {
			t.Errorf("option %+v", o)
		}
	}
}

// A window on the desk answers the request with one of the agent's options and
// the agent goes on; one reached through the relay is refused.
func TestAnAnswerFromTheDeskReachesTheStructuredPane(t *testing.T) {
	e := newStructuredEnv(t, "hold-perm")
	e.status(t, session.StatusWaiting)
	card := e.ws.StructuredRuntime(e.child).Snapshot().Pending
	if card == nil || len(card.Options) == 0 {
		t.Fatalf("card = %+v", card)
	}
	cmd := answerCmd(e.child, card.Seq, card.RequestID, card.Options[0].OptionID)
	remote := &controlClient{out: make(chan []byte, 16), remote: true}
	e.srv.permissionAnswer(remote, cmd)
	var refused permissionAnswerMsg
	for raw := range remote.out {
		if typeOf(raw) == "permissionAnswer" {
			if err := json.Unmarshal(raw, &refused); err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	if refused.OK || refused.Error != permissionAnswerDeskOnly {
		t.Errorf("a relayed window got %+v", refused)
	}
	if e.ws.StructuredRuntime(e.child).Snapshot().Pending == nil {
		t.Fatal("the relayed answer was taken")
	}
	if r, _ := send(t, e.srv, &controlClient{out: make(chan []byte, 32)}, cmd); !r.OK {
		t.Fatalf("the desk's answer: %+v", r)
	}
	e.status(t, session.StatusIdle)
}

// No old-window route reaches a terminal that is not there: the terminal socket
// is refused with a message, and a prompt typed in the old window goes nowhere.
func TestTheOldWindowCannotTypeIntoAStructuredPane(t *testing.T) {
	e := newStructuredEnv(t, "plain")
	e.status(t, session.StatusIdle)
	req := httptest.NewRequest(http.MethodGet, "/ws/pty?id="+e.child+"&t="+e.srv.token, nil)
	rec := httptest.NewRecorder()
	e.srv.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "structured") {
		t.Errorf("the terminal socket answered %d %q", rec.Code, rec.Body.String())
	}
	entries := func() int {
		n, _ := ask(e.srv, func() int { n, _ := e.ws.StructuredEntries(e.child); return len(n) })
		return n
	}
	before := entries()
	e.srv.handleCommand(e.c, command{Cmd: "sendPrompt", ID: e.child, Text: "typed from the prompt bar", Submit: true})
	time.Sleep(300 * time.Millisecond)
	if after := entries(); after != before {
		t.Errorf("the pane showed %d entries, then %d after the old window sent a prompt", before, after)
	}
}

// The adapter the server gives permissionAnswer takes exactly one answer for
// the card that is open and calls any other stale.
func TestTheStructuredPaneAdapterTakesOneAnswerPerCard(t *testing.T) {
	e := newStructuredEnv(t, "hold-perm")
	e.status(t, session.StatusWaiting)
	pane, ok := e.srv.workspaceStructuredPane(e.child)
	if !ok {
		t.Fatal("no adapter")
	}
	if _, ok := e.srv.workspaceStructuredPane(e.dev); ok {
		t.Error("a terminal pane has an adapter")
	}
	card := pane.PendingPermission()
	if card == nil {
		t.Fatal("no card")
	}
	if err := pane.AnswerPermission(card.Seq+100, card.RequestID, card.Options[0].OptionID); err != ErrPermissionStale {
		t.Errorf("an answer for another card: %v", err)
	}
	if err := pane.AnswerPermission(card.Seq, card.RequestID, "not-an-option"); err == nil || err == ErrPermissionStale {
		t.Errorf("an answer with an option that was not offered: %v", err)
	}
	if err := pane.AnswerPermission(card.Seq, card.RequestID, card.Options[0].OptionID); err != nil {
		t.Fatal(err)
	}
	if err := pane.AnswerPermission(card.Seq, card.RequestID, card.Options[0].OptionID); err != ErrPermissionStale {
		t.Errorf("a second answer: %v", err)
	}
}
