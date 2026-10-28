package workspace

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/acp/bridge"
	"github.com/jmwri/flockdeck/internal/hooks"
	"github.com/jmwri/flockdeck/internal/session"
)

// A terminal pane answers every question as its session does, and a pane given
// another runtime answers with that and not with its session.
func TestTerminalRuntimeReadsThePaneAndSetRuntimeReplacesIt(t *testing.T) {
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	tab := ws.NewTab(session.KindShell, root, "one")
	p := ws.Pane(tab.Tree.Panes()[0])
	rt := ws.Runtime(p)

	if rt.Transport() != TransportTerminal || rt.Caps() != (PaneCaps{}) {
		t.Errorf("kind %q, caps %+v", rt.Transport(), rt.Caps())
	}
	p.Sess.SetStatusFull(session.StatusWaiting, "Bash", `{"command":"ls"}`)
	st, detail := rt.Status()
	if want, wd := p.Status(); st != want || detail != wd {
		t.Errorf("status %v %q, the pane says %v %q", st, detail, want, wd)
	}
	if on := rt.Waiting(); on.Detail != "Bash" || on.ToolInput != `{"command":"ls"}` {
		t.Errorf("waiting on %+v", on)
	}
	if rt.Activity() != PaneActivity(p) || !rt.StatusSince().Equal(p.Sess.StatusSince()) {
		t.Errorf("activity %q since %v", rt.Activity(), rt.StatusSince())
	}
	if rt.HasRun() {
		t.Error("a shell has no spend to expect")
	}
	if got := rt.PermissionOptions("Bash"); got == nil || len(got) != 0 {
		t.Errorf("options for a pane that is not Claude = %#v", got)
	}
	p.Agent = "claude"
	if got := rt.PermissionOptions("Bash"); len(got) != 2 || got[0].Kind != OptionAllowOnce {
		t.Errorf("options for a Claude pane = %+v", got)
	}

	other := stubRuntime{terminalRuntime{w: ws, p: p}}
	if !ws.SetRuntime(p.ID, other) || ws.Runtime(p).Transport() != TransportStructured {
		t.Error("the pane did not take the runtime it was given")
	}
	if ws.SetRuntime("nobody", other) {
		t.Error("a pane that is not there took a runtime")
	}
	if ws.Runtime(nil).Activity() != ActivityNone {
		t.Error("no pane is nothing")
	}
}

type stubRuntime struct{ terminalRuntime }

func (stubRuntime) Transport() Transport { return TransportStructured }

// agentPaneOf is a pane of a one-pane project that counts as an agent.
func agentPaneOf(t *testing.T) (*Workspace, *Tab, *Pane) {
	t.Helper()
	isolateConfig(t)
	root := t.TempDir()
	ws := newTestWorkspace(t, root)
	tab := ws.NewTab(session.KindShell, root, "one")
	p := ws.Pane(tab.Tree.Panes()[0])
	p.Kind = session.KindAgent
	return ws, tab, p
}

// SetRuntime and Runtime may run at once, from the workspace goroutine and
// the server's.
func TestRuntimeCanBeSetWhileItIsRead(t *testing.T) {
	ws, _, p := agentPaneOf(t)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				ws.SetRuntime(p.ID, stubRuntime{terminalRuntime{w: ws, p: p}})
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				_ = ws.Runtime(p).Transport()
			}
		}()
	}
	wg.Wait()
}

// The terminal runtime of an agent pane answers as the pane and its session do.
func TestTerminalRuntimeOfAnAgentPane(t *testing.T) {
	ws, _, p := agentPaneOf(t)
	rt := ws.Runtime(p)
	if !rt.HasRun() {
		t.Error("an agent with a session has run")
	}

	p.Sess.SetStatus(session.StatusIdle, "")
	p.Sess.NoteBackground("PostToolUse", hooks.BackgroundStart, "shell:b1")
	bg := rt.Background()
	if bg.Tasks != 1 || bg.Verified != 1 || len(bg.Running) != 1 {
		t.Errorf("background = %+v", bg)
	}
	if rt.Activity() != ActivityWorking {
		t.Errorf("an idle agent with work running is %q", rt.Activity())
	}
	// Nothing is left running once the process has gone, whatever was counted.
	p.Sess.SetStatus(session.StatusExited, "")
	if bg := rt.Background(); bg.Tasks != 0 || len(bg.Running) != 0 || bg.Verified != 0 {
		t.Errorf("an exited agent has background work %+v", bg)
	}

	if _, ok := rt.LastReply(); ok {
		t.Error("a reply with no lookup")
	}
	ws.SetReplyLookup(func(id string) (LastReply, bool) {
		return LastReply{Text: "done " + id, Kind: "text"}, id == p.ID
	})
	if r, ok := rt.LastReply(); !ok || r.Text != "done "+p.ID {
		t.Errorf("last reply = %+v, %v", r, ok)
	}

	p.Agent = "claude"
	ws.mu.RLock()
	c := rt.Conversation()
	ws.mu.RUnlock()
	if c.ID != p.ID || c.Spec.ID != "claude" || !c.Readable() {
		t.Errorf("conversation = %+v", c)
	}
	p.Conversation = "abc"
	ws.mu.RLock()
	c = rt.Conversation()
	ws.mu.RUnlock()
	if c.ID != "abc" {
		t.Errorf("conversation id = %q", c.ID)
	}

	if err := rt.Close(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for !p.Sess.Exited() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !p.Sess.Exited() {
		t.Error("Close did not end the session")
	}
}

// A pane that never started has no session and reads as exited, with no start
// time and no status time.
func TestTerminalRuntimeOfAPaneThatNeverStarted(t *testing.T) {
	ws, _, p := agentPaneOf(t)
	sess := p.Sess
	p.Sess, p.Err = nil, errTestStart
	t.Cleanup(func() { _ = sess.Close() })
	rt := ws.Runtime(p)
	if st, d := rt.Status(); st != session.StatusExited || d != "" {
		t.Errorf("status %v %q", st, d)
	}
	if !rt.StatusSince().IsZero() || !rt.StartedAt().IsZero() || rt.HasRun() {
		t.Error("a pane that never started has run")
	}
	if bg := rt.Background(); bg.Tasks != 0 {
		t.Errorf("background = %+v", bg)
	}
	if err := rt.Close(); err != nil {
		t.Error(err)
	}
	p.Err = nil
}

type testErr string

func (e testErr) Error() string { return string(e) }

const errTestStart = testErr("could not start")

// A send to a working agent says it is queued, which is read from the runtime's
// activity.
func TestSendToPaneSaysQueuedForAWorkingAgent(t *testing.T) {
	ws, _, p := agentPaneOf(t)
	p.Sess.SetStatus(session.StatusWorking, "")
	res, err := ws.SendToPane(p.ID, "hello", false)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Queued {
		t.Error("a send to a working agent is not queued")
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		ws.mu.RLock()
		sending := p.sending
		ws.mu.RUnlock()
		if !sending {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// waitingStub is a pane with no session that says it is waiting.
type waitingStub struct{ terminalRuntime }

func (waitingStub) Transport() Transport             { return TransportStructured }
func (waitingStub) Status() (session.Status, string) { return session.StatusWaiting, "Bash" }
func (waitingStub) Activity() Activity               { return ActivityWaiting }

// The project's counts and the attention count include a pane with no session
// when its runtime says it is waiting.
func TestAPaneWithNoSessionCountsThroughItsRuntime(t *testing.T) {
	ws, _, p := agentPaneOf(t)
	sess := p.Sess
	p.Sess = nil
	t.Cleanup(func() { _ = sess.Close() })
	ws.SetRuntime(p.ID, waitingStub{terminalRuntime{w: ws, p: p}})
	var got *Project
	for _, pr := range ws.Projects() {
		if pr.Active {
			pr := pr
			got = &pr
		}
	}
	if got == nil || got.Waiting != 1 || got.Activity != ActivityWaiting {
		t.Errorf("project = %+v", got)
	}
	if w, _ := ws.AttentionCount(); w != 1 {
		t.Errorf("attention waiting = %d", w)
	}
}

// A command of several lines is marked in the one line the roster and the graph
// show, so the first line is not taken for all of it.
func TestWaitingInputMarksACommandOfSeveralLines(t *testing.T) {
	dots := string(rune(0x2026))
	one := &bridge.Card{Command: "ls", CommandText: "ls", Summary: "execute: Bash", File: "f"}
	if got, want := waitingInput(one), `{"command":"ls","description":"execute: Bash","filePath":"f"}`; got != want {
		t.Errorf("one line: %s, want %s", got, want)
	}
	many := &bridge.Card{Command: "echo ok", CommandText: "echo ok" + string(rune(10)) + "rm -rf ~"}
	if got := waitingInput(many); !strings.Contains(got, `"command":"echo ok `+dots+`"`) {
		t.Errorf("several lines: %s", got)
	}
	if got := waitingInput(&bridge.Card{CommandText: "a" + string(rune(10)) + "b"}); strings.Contains(got, dots) {
		t.Errorf("no command, no mark: %s", got)
	}
}
