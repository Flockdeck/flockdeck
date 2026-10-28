package server

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/acp/acptest"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/session/transcript"
	"github.com/jmwri/flockdeck/internal/structured"
)

// Going back to an earlier prompt and forking at one, over the control socket.

var rewindOps = []string{"structuredRewind", "structuredFork"}

func (e *structuredEnv) rewindOp(t *testing.T, c *controlClient, cmd command) teamResultMsg {
	t.Helper()
	if cmd.ID == "" {
		cmd.ID = e.child
	}
	cmd.Req = "rw1"
	if cmd.RewindToken == "" && cmd.ID == e.child {
		cmd.RewindToken = e.snapshotOf(t, cmd.ID).RewindToken
	}
	raw := structuredReply(t, e.srv, c, cmd, "teamResult")
	var res teamResultMsg
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatal(err)
	}
	if res.Op != cmd.Cmd {
		t.Fatalf("answer for %q to %q", res.Op, cmd.Cmd)
	}
	return res
}

// rewindEnv is a pane on the fake agent with the extension, after two prompts
// of the person's.
func rewindEnv(t *testing.T) (*structuredEnv, []string) {
	t.Helper()
	e := newStructuredEnvWith(t, "plain", structuredEnvOpts{Args: []string{acptest.ArgSessions + "rewind"}})
	e.status(t, session.StatusIdle)
	for i, txt := range []string{"plain one", "plain two"} {
		e.sendText(t, txt)
		e.waitPrompts(t, i+2) // the opening prompt is the first
		e.status(t, session.StatusIdle)
	}
	var ids []string
	for _, en := range e.entries(t) {
		if en.Kind == transcript.KindPrompt && (en.Text == "plain one" || en.Text == "plain two") {
			ids = append(ids, en.ID)
		}
	}
	if len(ids) != 2 {
		t.Fatalf("prompt entries = %v", ids)
	}
	return e, ids
}

func TestThePushNamesTheRewindPoints(t *testing.T) {
	e, ids := rewindEnv(t)
	m := e.snapshotOf(t, e.child)
	if m.Capabilities == nil || !m.Capabilities.Rewind || !m.Capabilities.ForkSession {
		t.Errorf("capabilities = %+v", m.Capabilities)
	}
	// Both prompts follow a turn that ended, the opening one among them.
	if len(m.RewindPoints) != 2 || m.RewindPoints[0] != ids[0] || m.RewindPoints[1] != ids[1] {
		t.Errorf("points = %v, prompts = %v", m.RewindPoints, ids)
	}
}

func TestRewindNeedsAConfirmAndTakesThePromptBack(t *testing.T) {
	e, ids := rewindEnv(t)
	before := len(e.entries(t))
	stream, ok := e.ws.StructuredStream(e.child)
	if !ok {
		t.Fatal("no stream")
	}
	stream.Snapshot()
	if stream.Reset() {
		t.Fatal("the stream asked for a reset before any rewind")
	}
	res := e.rewindOp(t, e.c, command{Cmd: "structuredRewind", Entry: ids[0]})
	if res.OK || res.Error != structuredNeedConfirm {
		t.Fatalf("without confirm: %+v", res)
	}
	if len(e.entries(t)) != before {
		t.Fatal("a refused rewind changed the conversation")
	}
	res = e.rewindOp(t, e.c, command{Cmd: "structuredRewind", Entry: ids[0], Confirm: true})
	if !res.OK || res.Text != "plain one" {
		t.Fatalf("rewind: %+v", res)
	}
	for _, en := range e.entries(t) {
		if en.ID == ids[0] || en.ID == ids[1] {
			t.Errorf("entry %s is still there", en.ID)
		}
	}
	if m := e.snapshotOf(t, e.child); len(m.RewindPoints) != 0 {
		t.Errorf("points after the rewind = %v", m.RewindPoints)
	}
	// The conversation stream tells a window to start again: entries were taken out.
	if !stream.Reset() {
		t.Error("the stream did not ask a window to start again after a rewind")
	}
}

func TestRewindRefusals(t *testing.T) {
	e, ids := rewindEnv(t)
	for name, c := range map[string]struct {
		cmd  command
		want string
	}{
		"no entry":      {command{Cmd: "structuredRewind", Confirm: true}, structuredRewindEntry},
		"unknown entry": {command{Cmd: "structuredRewind", Entry: "e9999", Confirm: true}, structuredNoPoint},
		"not a prompt":  {command{Cmd: "structuredRewind", Entry: "../x", Confirm: true}, structuredNoPoint},
		"not a pane":    {command{Cmd: "structuredRewind", ID: e.boss, Entry: ids[0], Confirm: true}, structuredNotPane},
		"fork unknown":  {command{Cmd: "structuredFork", Entry: "e9999"}, structuredNoPoint},
		"fork not pane": {command{Cmd: "structuredFork", ID: "nope"}, structuredNotPane},
	} {
		if res := e.rewindOp(t, e.c, c.cmd); res.OK || res.Error != c.want {
			t.Errorf("%s: %+v, want %q", name, res, c.want)
		}
	}
	if n := len(e.entries(t)); n < 6 {
		t.Errorf("a refusal changed the conversation: %d entries", n)
	}
}

func TestRewindIsRefusedWhileATurnRuns(t *testing.T) {
	e, ids := rewindEnv(t)
	e.sendText(t, "slow")
	e.waitStatus(t, e.child, session.StatusWorking)
	for _, cmd := range []command{
		{Cmd: "structuredRewind", Entry: ids[0], Confirm: true},
		{Cmd: "structuredFork", Entry: ids[1]},
	} {
		if res := e.rewindOp(t, e.c, cmd); res.OK || res.Error != structuredRewindBusy {
			t.Errorf("%s during a turn: %+v", cmd.Cmd, res)
		}
	}
	e.cancel(t, e.c, e.child)
}

func TestAgentWithoutTheExtensionCannotGoBack(t *testing.T) {
	e := newStructuredEnv(t, "plain")
	e.status(t, session.StatusIdle)
	e.sendText(t, "plain two")
	e.waitPrompts(t, 3)
	e.status(t, session.StatusIdle)
	if m := e.snapshotOf(t, e.child); len(m.RewindPoints) != 0 || (m.Capabilities != nil && m.Capabilities.Rewind) {
		t.Errorf("an agent without the extension offers points: %+v %v", m.Capabilities, m.RewindPoints)
	}
	var id string
	for _, en := range e.entries(t) {
		if en.Text == "plain two" {
			id = en.ID
		}
	}
	if res := e.rewindOp(t, e.c, command{Cmd: "structuredRewind", Entry: id, Confirm: true}); res.OK || res.Error != structuredNoRewind {
		t.Errorf("rewind: %+v", res)
	}
	if res := e.rewindOp(t, e.c, command{Cmd: "structuredFork", Entry: id}); res.OK || res.Error != structuredNoFork {
		t.Errorf("fork: %+v", res)
	}
}

func TestForkStartsANewPaneBesideTheOriginal(t *testing.T) {
	e, ids := rewindEnv(t)
	before := len(e.entries(t))
	res := e.rewindOp(t, e.c, command{Cmd: "structuredFork", Entry: ids[1]})
	if !res.OK || res.Pane == "" || res.Pane == e.child {
		t.Fatalf("fork: %+v", res)
	}
	if len(e.entries(t)) != before {
		t.Error("the original was changed by a fork")
	}
	type view struct {
		label, agent string
		together     bool
		session      string
	}
	v, _ := ask(e.srv, func() view {
		p := e.ws.Pane(res.Pane)
		if p == nil {
			return view{}
		}
		v := view{label: p.Label, agent: p.Agent}
		for _, tab := range e.ws.Tabs {
			if tab.Tree.Find(e.child) != nil && tab.Tree.Find(res.Pane) != nil {
				v.together = true
			}
		}
		return v
	})
	if v.label != "Fork" || v.agent == "" || !v.together {
		t.Errorf("fork pane = %+v", v)
	}
	// It opens the forked session: the fake agent names it after the point.
	deadline := time.Now().Add(20 * time.Second)
	for {
		sn, ok := e.srv.structuredSnapshotOf(res.Pane)
		_ = ok
		if sn.SessionID == "fork-of-sess-1-at-2" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the fork's session is %q", sn.SessionID)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func TestRewindAndForkAreRefusedToTheRelayAndViewers(t *testing.T) {
	e, ids := rewindEnv(t)
	remote := &controlClient{out: make(chan []byte, 8), remote: true, device: "full-dev"}
	before := len(e.entries(t))
	for _, op := range rewindOps {
		res := e.rewindOp(t, remote, command{Cmd: op, Entry: ids[0], Confirm: true})
		if res.OK || res.Error != structuredDeskOnly {
			t.Errorf("%s from the relay: %+v", op, res)
		}
		if commandAccess[op] != accessFull {
			t.Errorf("%s is not accessFull, so a viewer could send it", op)
		}
	}
	if len(e.entries(t)) != before {
		t.Error("a command from the relay changed the conversation")
	}
}

// A window that holds an old token is refused: the pane went back (or was
// restarted) since it last showed the list, and its entry ids mean other things.
func TestRewindAndForkWithAStaleTokenAreRefused(t *testing.T) {
	e, ids := rewindEnv(t)
	old := e.snapshotOf(t, e.child).RewindToken
	if old == "" {
		t.Fatal("the push has no rewind token")
	}
	res := e.rewindOp(t, e.c, command{Cmd: "structuredRewind", Entry: ids[1], Confirm: true})
	if !res.OK {
		t.Fatalf("rewind: %+v", res)
	}
	if now := e.snapshotOf(t, e.child).RewindToken; now == old {
		t.Fatal("the token did not change after a rewind")
	}
	for _, cmd := range []command{
		{Cmd: "structuredRewind", Entry: ids[0], Confirm: true, RewindToken: old},
		{Cmd: "structuredFork", Entry: ids[0], RewindToken: old},
		{Cmd: "structuredFork", Entry: ids[0], RewindToken: "garbage"},
		{Cmd: "structuredFork", Entry: ids[0], RewindToken: "0.0"},
	} {
		if res := e.rewindOp(t, e.c, cmd); res.OK || res.Error != structuredRewindStale {
			t.Errorf("%s with token %q: %+v", cmd.Cmd, cmd.RewindToken, res)
		}
	}
	// A restart of the pane makes a new runtime: the token it had is no good.
	tok := e.snapshotOf(t, e.child).RewindToken
	if ok, _ := ask(e.srv, func() bool { return e.ws.RestartPaneByID(e.child) }); !ok {
		t.Fatal("restart refused")
	}
	e.status(t, session.StatusIdle)
	if res := e.rewindOp(t, e.c, command{Cmd: "structuredFork", Entry: ids[0], RewindToken: tok}); res.OK || res.Error != structuredRewindStale {
		t.Errorf("fork with the token of the runtime before the restart: %+v", res)
	}
}

// When the agent does not answer a rewind, whether it went back is not known:
// the pane is restarted so that the window and the agent cannot disagree.
func TestARewindTheAgentNeverAnswersRestartsThePane(t *testing.T) {
	e := newStructuredEnvWith(t, "plain", structuredEnvOpts{Args: []string{acptest.ArgSessions + "rewind,rewindhang,resume,id=sess-1"}, RewindTimeout: 700 * time.Millisecond})
	e.status(t, session.StatusIdle)
	e.sendText(t, "plain one")
	e.waitPrompts(t, 2)
	e.status(t, session.StatusIdle)
	e.sendText(t, "plain two")
	e.waitPrompts(t, 3)
	e.status(t, session.StatusIdle)
	var id string
	for _, en := range e.entries(t) {
		if en.Kind == transcript.KindPrompt && en.Text == "plain two" {
			id = en.ID
		}
	}
	first, _ := ask(e.srv, func() any { return e.ws.StructuredRuntime(e.child) })
	before, _ := e.srv.structuredSnapshotOf(e.child)
	res := e.rewindOp(t, e.c, command{Cmd: "structuredRewind", Entry: id, Confirm: true})
	if res.OK || res.Error != structuredRewindUnknown {
		t.Fatalf("rewind: %+v", res)
	}
	now, _ := ask(e.srv, func() any { return e.ws.StructuredRuntime(e.child) })
	if now == nil || now == first {
		t.Error("the pane was not restarted")
	}
	// It reopens the conversation it had, and does not start a new one.
	e.status(t, session.StatusIdle)
	after, _ := e.srv.structuredSnapshotOf(e.child)
	if before.SessionID == "" || after.SessionID != before.SessionID {
		t.Errorf("the restarted pane has session %q, want %q", after.SessionID, before.SessionID)
	}
	if after.Resume != structured.ResumeResumed && after.Resume != structured.ResumeLoaded {
		t.Errorf("the restarted pane started a new session (resume = %q)", after.Resume)
	}
}

// A pane keeps four forks at most; the fifth gets a fixed text and no pane.
func TestAFifthForkOfOnePaneIsRefused(t *testing.T) {
	e, ids := rewindEnv(t)
	for i := 0; i < 4; i++ {
		if res := e.rewindOp(t, e.c, command{Cmd: "structuredFork", Entry: ids[1]}); !res.OK {
			t.Fatalf("fork %d: %+v", i+1, res)
		}
	}
	res := e.rewindOp(t, e.c, command{Cmd: "structuredFork", Entry: ids[1]})
	if res.OK || res.Pane != "" || res.Error != structuredTooManyForks {
		t.Errorf("fifth fork: %+v", res)
	}
}
