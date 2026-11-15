package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/acp/acptest"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/session/transcript"
	"github.com/jmwri/flockdeck/internal/structured"
)

// The conversation picker and New conversation, over the control socket.

var conversationOps = []string{"structuredSessions", "structuredResume", "structuredNewConversation"}

func (e *structuredEnv) convOp(t *testing.T, c *controlClient, cmd command) teamResultMsg {
	t.Helper()
	if cmd.ID == "" {
		cmd.ID = e.child
	}
	cmd.Req = "cv1"
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

func convEnv(t *testing.T) *structuredEnv {
	t.Helper()
	e := newStructuredEnvWith(t, "plain", structuredEnvOpts{Args: []string{acptest.ArgSessions + "load,resume,list,id=old-1,id=old-2"}})
	e.status(t, session.StatusIdle)
	return e
}

// waitSessionIs waits for the pane to be on a runtime that holds session id.
func (e *structuredEnv) waitSessionIs(t *testing.T, id string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		rt, _ := ask(e.srv, func() *structured.Runtime { return e.ws.StructuredRuntime(e.child) })
		if rt != nil && rt.SessionID() == id && rt.Snapshot().Phase == structured.PhaseReady {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the pane never moved to session %q", id)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func TestSessionsListIsCleanedBoundedAndKeptToTheFolder(t *testing.T) {
	e := convEnv(t)
	res := e.convOp(t, e.c, command{Cmd: "structuredSessions"})
	if !res.OK {
		t.Fatalf("list: %+v", res)
	}
	var ids []string
	for _, s := range res.Sessions {
		ids = append(ids, s.ID)
		if s.ID == "foreign-1" {
			t.Errorf("a session of another folder is listed")
		}
		if strings.ContainsAny(s.Title, "\x1b\n") || len([]rune(s.Title)) > structured.SessionTitleCap+1 {
			t.Errorf("title of %q is not clean and bounded: %q", s.ID, s.Title)
		}
	}
	// Newest first: old-2 is dated after old-1, and hostile-1 before both.
	if strings.Join(ids, ",") != "old-2,old-1,hostile-1" {
		t.Errorf("listed %v", ids)
	}
	for _, s := range res.Sessions {
		if s.ID == "hostile-1" && !strings.HasPrefix(s.Title, "[31mred[0m first line second line ") {
			t.Errorf("hostile title %q", s.Title)
		}
	}
}

func TestSessionsAreNotOfferedByAnAgentThatCannotListThem(t *testing.T) {
	e := newStructuredEnvWith(t, "plain", structuredEnvOpts{Args: []string{acptest.ArgSessions + "load,resume,id=old-1"}})
	e.status(t, session.StatusIdle)
	if res := e.convOp(t, e.c, command{Cmd: "structuredSessions"}); res.OK || res.Error != structuredNoSessions {
		t.Errorf("list: %+v", res)
	}
	if res := e.convOp(t, e.c, command{Cmd: "structuredResume", SessionID: "old-1"}); res.OK || res.Error != structuredNoSessions {
		t.Errorf("resume: %+v", res)
	}
	// A clean conversation needs nothing of the agent.
	if res := e.convOp(t, e.c, command{Cmd: "structuredNewConversation"}); !res.OK {
		t.Errorf("new conversation: %+v", res)
	}
}

func TestResumeMovesThePaneToTheChosenConversation(t *testing.T) {
	e := convEnv(t)
	res := e.convOp(t, e.c, command{Cmd: "structuredResume", SessionID: "old-1"})
	if !res.OK {
		t.Fatalf("resume: %+v", res)
	}
	e.waitSessionIs(t, "old-1")
	e.status(t, session.StatusIdle)
	// The pane is on it now, so the list says so and it cannot be picked again.
	list := e.convOp(t, e.c, command{Cmd: "structuredSessions"})
	for _, s := range list.Sessions {
		if s.Current != (s.ID == "old-1") {
			t.Errorf("session %q current = %v", s.ID, s.Current)
		}
	}
	if res := e.convOp(t, e.c, command{Cmd: "structuredResume", SessionID: "old-1"}); res.OK || res.Error != structuredSessionCurrent {
		t.Errorf("resume of the current session: %+v", res)
	}
	var notice string
	for _, en := range e.entries(t) {
		if en.Kind == transcript.KindNotice {
			notice = en.Text
		}
	}
	if notice != "Resumed an earlier conversation from this folder." {
		t.Errorf("notice %q", notice)
	}
}

func TestResumeRefusesASessionThatIsNotInThisFoldersList(t *testing.T) {
	e := convEnv(t)
	rt, _ := ask(e.srv, func() *structured.Runtime { return e.ws.StructuredRuntime(e.child) })
	before := rt.SessionID()
	// foreign-1 belongs to another folder; ../x and the empty id were never listed.
	for _, id := range []string{"foreign-1", "../x --dangerous", "never-listed"} {
		res := e.convOp(t, e.c, command{Cmd: "structuredResume", SessionID: id})
		if res.OK || res.Error != structuredNoSuchSession {
			t.Errorf("resume %q: %+v", id, res)
		}
	}
	if res := e.convOp(t, e.c, command{Cmd: "structuredResume"}); res.OK || res.Error != structuredSessionNoID {
		t.Errorf("resume with no id: %+v", res)
	}
	now, _ := ask(e.srv, func() *structured.Runtime { return e.ws.StructuredRuntime(e.child) })
	if now != rt || now.SessionID() != before {
		t.Error("a refused resume restarted the pane")
	}
}

func TestMovingAPaneNeedsConfirmWhileATurnOrAPromptIsWaiting(t *testing.T) {
	e := convEnv(t)
	// A prompt that stays running: the fake agent's "silent" scenario never answers.
	e.sendText(t, "silent")
	e.until(t, "the turn to run", 10*time.Second, func(m convView) bool { return m.Status == "working" })
	rt, _ := ask(e.srv, func() *structured.Runtime { return e.ws.StructuredRuntime(e.child) })
	for _, cmd := range []command{{Cmd: "structuredResume", SessionID: "old-1"}, {Cmd: "structuredNewConversation"}} {
		if res := e.convOp(t, e.c, cmd); res.OK || res.Error != structuredSessionBusy {
			t.Errorf("%s without confirm: %+v", cmd.Cmd, res)
		}
	}
	if now, _ := ask(e.srv, func() *structured.Runtime { return e.ws.StructuredRuntime(e.child) }); now != rt {
		t.Fatal("a refused move restarted the pane")
	}
	// With confirm the turn ends and the queue is dropped.
	e.sendText(t, "queued behind the turn")
	if res := e.convOp(t, e.c, command{Cmd: "structuredNewConversation", Confirm: true}); !res.OK {
		t.Fatalf("confirmed: %+v", res)
	}
	e.status(t, session.StatusIdle)
	if q := e.queue(t); len(q) != 0 {
		t.Errorf("the queue was kept: %+v", q)
	}
}

func TestNewConversationKeepsTheEarlierEntriesAboveANotice(t *testing.T) {
	e := convEnv(t)
	before := e.entries(t)
	res := e.convOp(t, e.c, command{Cmd: "structuredNewConversation"})
	if !res.OK {
		t.Fatalf("new conversation: %+v", res)
	}
	e.status(t, session.StatusIdle)
	got := e.entries(t)
	if len(got) < len(before)+1 {
		t.Fatalf("entries %v after %v", kindsOf(got), kindsOf(before))
	}
	n := got[len(before)]
	if n.Kind != transcript.KindNotice || n.Text != "New conversation. The agent does not remember the one above." {
		t.Errorf("notice %+v", n)
	}
	rt, _ := ask(e.srv, func() *structured.Runtime { return e.ws.StructuredRuntime(e.child) })
	if rt.SessionID() == "old-1" || rt.SessionID() == "old-2" {
		t.Errorf("the new conversation resumed %q", rt.SessionID())
	}
}

func TestConversationCommandsAreRefusedToTheRelayAndViewers(t *testing.T) {
	e := convEnv(t)
	remote := &controlClient{out: make(chan []byte, 8), remote: true, device: "full-dev"}
	rt, _ := ask(e.srv, func() *structured.Runtime { return e.ws.StructuredRuntime(e.child) })
	for _, op := range conversationOps {
		res := e.convOp(t, remote, command{Cmd: op, SessionID: "old-1", Confirm: true})
		if res.OK || res.Error != structuredDeskOnly {
			t.Errorf("%s from the relay: %+v", op, res)
		}
		if commandAccess[op] != accessFull {
			t.Errorf("%s is not accessFull, so a viewer could send it", op)
		}
	}
	if now, _ := ask(e.srv, func() *structured.Runtime { return e.ws.StructuredRuntime(e.child) }); now != rt {
		t.Error("a command from the relay restarted the pane")
	}
}
