package server

import (
	"encoding/json"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/jmwri/flockdeck/internal/acp"
	"github.com/jmwri/flockdeck/internal/acp/bridge"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/session/transcript"
	"github.com/jmwri/flockdeck/internal/store"
	"github.com/jmwri/flockdeck/internal/structured"
	"github.com/jmwri/flockdeck/internal/workspace"
)

// These tests read a structured pane through structuredSnapshot, structuredWatch
// and structuredUnwatch. The pane is the fake agent's.

var structuredCommands = []string{"structuredSnapshot", "structuredWatch", "structuredUnwatch", "structuredCancel", "structuredFiles",
	"structuredQueueEdit", "structuredQueueRemove", "structuredQueueClear", "structuredQueueSendNow", "structuredExport"}

// structuredReply sends a command from c and returns the first message of a type.
func structuredReply(t *testing.T, srv *Server, c *controlClient, cmd command, types ...string) []byte {
	t.Helper()
	srv.handleCommand(c, cmd)
	deadline := time.After(10 * time.Second)
	for {
		select {
		case raw := <-c.out:
			for _, ty := range types {
				if typeOf(raw) == ty {
					return raw
				}
			}
		case <-deadline:
			t.Fatalf("%s was not answered with %v", cmd.Cmd, types)
		}
	}
}

// entries are the pane's conversation as the workspace holds it, which is what
// the conversation commands page. A push carries none of it.
func (e *structuredEnv) entries(t *testing.T) []transcript.Entry {
	t.Helper()
	got, ok := ask(e.srv, func() []transcript.Entry {
		sn, _ := e.srv.structuredSnapshotOf(e.child)
		return sn.Entries
	})
	if !ok {
		t.Fatal("the server stopped")
	}
	return got
}

func (e *structuredEnv) snapshotOf(t *testing.T, id string) structuredMsg {
	t.Helper()
	var m structuredMsg
	raw := structuredReply(t, e.srv, e.c, command{Cmd: "structuredSnapshot", ID: id, Req: "r1"}, "structured")
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// push runs a push the way a state push does, past the interval.
func (e *structuredEnv) push() {
	ask(e.srv, func() bool { e.srv.structAt = time.Time{}; e.srv.pushStructured(); return true })
}

func drain(c *controlClient) (out [][]byte) {
	for {
		select {
		case raw := <-c.out:
			out = append(out, raw)
		default:
			return out
		}
	}
}

// A snapshot has the pane's phase, status and usage, and a request for
// anything that is not a structured pane is refused in the same words.
func TestStructuredSnapshotOfAPane(t *testing.T) {
	e := newStructuredEnv(t, "plain")
	e.status(t, session.StatusIdle)
	m := e.snapshotOf(t, e.child)
	if m.ID != e.child || m.Kind != "structured" || m.Req != "r1" || m.Phase != "ready" || m.Status != "idle" {
		t.Errorf("message = %+v", m)
	}
	var prompt, reply bool
	for _, en := range e.entries(t) {
		prompt = prompt || en.Kind == "prompt"
		reply = reply || (en.Kind == "reply" && strings.Contains(en.Markdown, "Hello world"))
	}
	if !prompt || !reply || m.Card != nil {
		t.Errorf("prompt %v reply %v card %+v", prompt, reply, m.Card)
	}
	if raw := structuredReply(t, e.srv, e.c, command{Cmd: "structuredSnapshot", ID: e.child, Req: "r1"}, "structured"); strings.Contains(string(raw), "Hello world") || strings.Contains(string(raw), "\"entries\"") {
		t.Errorf("the snapshot carries the conversation: %.300s", raw)
	}

	// A terminal pane, a pane that is not there and an empty id look the same.
	var seen []string
	for _, id := range []string{e.dev, "no-such-pane", ""} {
		e.srv.handleCommand(e.c, command{Cmd: "structuredSnapshot", ID: id, Req: "r2"})
		var res teamResultMsg
		e.next(t, "teamResult", &res)
		if res.OK || res.Error != structuredNotPane || res.Op != "structuredSnapshot" {
			t.Errorf("%q: %+v", id, res)
		}
		raw, _ := json.Marshal(res)
		seen = append(seen, string(raw))
	}
	if seen[0] != seen[1] || seen[1] != seen[2] {
		t.Errorf("the refusals differ: %q", seen)
	}
}

// A pane waiting on a request shows its card, with the agent's options.
func TestStructuredSnapshotShowsThePermissionCard(t *testing.T) {
	e := newStructuredEnv(t, "hold-perm")
	e.status(t, session.StatusWaiting)
	m := e.snapshotOf(t, e.child)
	if m.Status != "waiting" || m.Card == nil || m.Card.RequestID == "" || len(m.Card.Options) == 0 {
		t.Fatalf("message = %+v", m)
	}
	for _, o := range m.Card.Options {
		if o.ID == "" || o.Name == "" || o.Kind == "" {
			t.Errorf("option %+v", o)
		}
	}
}

// A watching window is sent the pane at once, and again when it changes; it is
// not sent a state that is the same, and not more than twice a second.
func TestStructuredWatchPushesOnChangeAndIsThrottled(t *testing.T) {
	e := newStructuredEnv(t, "plain")
	e.status(t, session.StatusIdle)
	c := e.watcher(t, 64)
	raw := structuredReply(t, e.srv, c, command{Cmd: "structuredWatch", ID: e.child, Req: "w1"}, "structured")
	var first structuredMsg
	_ = json.Unmarshal(raw, &first)
	if first.Req != "w1" || first.ID != e.child {
		t.Fatalf("first = %+v", first)
	}

	// Nothing changed: a push sends nothing.
	e.push()
	if got := drain(c); len(got) != 0 {
		t.Fatalf("%d messages with nothing changed", len(got))
	}

	// The agent works: the next push has what changed (a plan here) and no req.
	if err, _ := ask(e.srv, func() error { _, err := e.ws.SendToPane(e.child, "plan the work", false); return err }); err != nil {
		t.Fatal(err)
	}
	e.status(t, session.StatusIdle)
	deadline := time.Now().Add(20 * time.Second)
	var last structuredMsg
	for len(last.Plan) <= len(first.Plan) && time.Now().Before(deadline) {
		e.push()
		for _, raw := range drain(c) {
			last = structuredMsg{}
			_ = json.Unmarshal(raw, &last)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(last.Plan) <= len(first.Plan) || last.Req != "" {
		t.Fatalf("no push with the new plan: %d rows then %d, req %q", len(first.Plan), len(last.Plan), last.Req)
	}

	// A flood inside the interval makes no push now, and one when it is up.
	ask(e.srv, func() bool { e.srv.structAt = time.Now(); return true })
	// A turn that stays running changes the pane's status, which a push carries.
	e.ws.SendToPane(e.child, "slow flood", false)
	e.status(t, session.StatusWorking)
	for i := 0; i < 50; i++ {
		ask(e.srv, func() bool { e.srv.pushStructured(); return true })
	}
	if got := drain(c); len(got) != 0 {
		t.Fatalf("%d pushes inside the interval", len(got))
	}
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got := drain(c); len(got) == 1 {
			return
		} else if len(got) > 1 {
			t.Fatalf("%d pushes when the interval was up", len(got))
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Error("the push held back was never made")
}

// Unwatching ends the pushes, and a pane that closes is told once as gone.
func TestStructuredUnwatchAndAClosedPane(t *testing.T) {
	e := newStructuredEnv(t, "plain")
	e.status(t, session.StatusIdle)
	a, b := e.watcher(t, 64), e.watcher(t, 64)
	structuredReply(t, e.srv, a, command{Cmd: "structuredWatch", ID: e.child}, "structured")
	structuredReply(t, e.srv, b, command{Cmd: "structuredWatch", ID: e.child}, "structured")

	e.srv.handleCommand(a, command{Cmd: "structuredUnwatch"})
	ask(e.srv, func() bool { return true })
	if id, _ := ask(e.srv, func() string { return a.structID }); id != "" {
		t.Fatalf("still watching %q", id)
	}

	if _, ok := ask(e.srv, func() bool { return e.ws.ClosePaneByID(e.child) }); !ok {
		t.Fatal("not closed")
	}
	e.push()
	if got := drain(a); len(got) != 0 {
		t.Errorf("an unwatching window was sent %d messages", len(got))
	}
	got := drain(b)
	if len(got) != 1 {
		t.Fatalf("the watcher was sent %d messages", len(got))
	}
	var m structuredMsg
	_ = json.Unmarshal(got[0], &m)
	if !m.Gone || m.ID != e.child {
		t.Errorf("message = %+v", m)
	}
	e.push()
	if got := drain(b); len(got) != 0 {
		t.Errorf("%d messages after the pane was gone", len(got))
	}
	// A dead id no longer counts toward the bound.
	if id, _ := ask(e.srv, func() string { return b.structID }); id != "" {
		t.Errorf("still watching %q after gone", id)
	}
}

// No more than maxStructuredWatches panes are watched at once.
func TestStructuredWatchesAreBounded(t *testing.T) {
	e := newStructuredEnv(t, "plain")
	e.status(t, session.StatusIdle)
	fill := func(from, to int) {
		ask(e.srv, func() bool {
			for i := from; i < to; i++ {
				c := &controlClient{out: make(chan []byte, 4), structID: "pane-" + string(rune('a'+i))}
				e.srv.mu.Lock()
				e.srv.clients[c] = true
				e.srv.mu.Unlock()
				t.Cleanup(func() { e.srv.mu.Lock(); delete(e.srv.clients, c); e.srv.mu.Unlock() })
			}
			return true
		})
	}
	// With 15 other panes watched, the 16th is allowed.
	fill(0, maxStructuredWatches-1)
	first := e.watcher(t, 8)
	if raw := structuredReply(t, e.srv, first, command{Cmd: "structuredWatch", ID: e.child, Req: "w0"}, "structured", "teamResult"); typeOf(raw) != "structured" {
		t.Fatalf("the 16th watch was refused: %s", raw)
	}
	e.srv.handleCommand(first, command{Cmd: "structuredUnwatch"})
	ask(e.srv, func() bool { return true })
	// With 16 others, the 17th is not.
	fill(maxStructuredWatches-1, maxStructuredWatches)
	c := e.watcher(t, 8)
	var res teamResultMsg
	raw := structuredReply(t, e.srv, c, command{Cmd: "structuredWatch", ID: e.child, Req: "w"}, "teamResult")
	_ = json.Unmarshal(raw, &res)
	if res.Error != structuredTooMany {
		t.Errorf("result = %+v", res)
	}
	if id, _ := ask(e.srv, func() string { return c.structID }); id != "" {
		t.Error("the window is watching")
	}
}

// A window reached through the relay is refused each command, a full device
// included, and is not subscribed. A viewer is refused by the table, whether or
// not the desk let it watch panes, and is never sent the messages.
func TestStructuredCommandsAreForTheDeskOnly(t *testing.T) {
	e := newStructuredEnv(t, "plain")
	e.status(t, session.StatusIdle)
	remote := &controlClient{out: make(chan []byte, 32), remote: true, device: "full-dev"}
	for _, name := range structuredCommands {
		raw := structuredReply(t, e.srv, remote, command{Cmd: name, ID: e.child, Entry: "x", Req: "r"}, "teamResult", "structured")
		var res teamResultMsg
		if err := json.Unmarshal(raw, &res); err != nil || res.Type != "teamResult" || res.OK || res.Op != name || res.Error != structuredDeskOnly {
			t.Errorf("%s: %s", name, raw)
		}
		drain(remote)
	}
	if id, _ := ask(e.srv, func() string { return remote.structID }); id != "" {
		t.Error("a relay window is subscribed")
	}

	setAccess(t, e.srv, "viewer-dev", "viewer", true)
	viewer := &controlClient{remote: true, device: "viewer-dev"}
	for _, name := range structuredCommands {
		if commandAccess[name] != accessFull {
			t.Errorf("%s is not accessFull", name)
		}
		if e.srv.allowCommand(viewer, name) {
			t.Errorf("a viewer that may watch panes was let send %s", name)
		}
	}
	viewer.accessOf = func() store.DeviceAccess { return e.srv.deviceAccess("viewer-dev") }
	for _, ty := range []string{"structured"} {
		if viewer.mayReceive([]byte(`{"type":"` + ty + `"}`)) {
			t.Errorf("a viewer was sent %s", ty)
		}
	}
}

// A tool's output is not in a push: the conversation's entry says it has
// detail, and conversationDetail has the text, cut.
func TestStructuredOutputIsFetchedAndNotPushed(t *testing.T) {
	e := newStructuredEnv(t, "big")
	e.status(t, session.StatusIdle)
	var id string
	for _, en := range e.entries(t) {
		if en.Kind == "tool" && en.HasDetail {
			id = en.ID
		}
	}
	if id == "" {
		t.Fatalf("no tool entry with detail: %+v", e.entries(t))
	}
	raw := structuredReply(t, e.srv, e.c, command{Cmd: "structuredSnapshot", ID: e.child, Req: "r"}, "structured")
	if strings.Contains(string(raw), strings.Repeat("x", 1000)) || len(raw) > 16<<10 {
		t.Errorf("the push is %d bytes and carries the output", len(raw))
	}

	c := &controlClient{out: make(chan []byte, 16)}
	structuredPage(t, e, c, "")
	e.srv.conversationDetailReq(c, e.child, id)
	var d conversationDetailMsg
	if err := json.Unmarshal(nextRaw(t, c), &d); err != nil || d.Entry != id || d.Gone || len(d.Detail.Text) != 64<<10 || !strings.HasPrefix(d.Detail.Text, "xxx") {
		t.Errorf("detail: %+v (%v), %d bytes", d.Entry, err, len(d.Detail.Text))
	}

	// structuredDetail is gone: nothing sends it and a window that asks is refused as for any unknown command.
	if _, there := commandAccess["structuredDetail"]; there {
		t.Error("structuredDetail is still in the table of commands")
	}
	e.srv.handleCommand(e.c, command{Cmd: "structuredDetail", ID: e.child, Entry: id, Req: "d"})
	for _, raw := range drain(e.c) {
		if typeOf(raw) == "structuredDetail" {
			t.Errorf("the server still answers structuredDetail: %.200s", raw)
		}
	}
}

// A push has what one pane showed and nothing else: not another pane's text,
// not mail, and not the environment of the process.
func TestStructuredPushHasNoOtherPanesTextMailOrEnvironment(t *testing.T) {
	const envSecret, mailSecret, otherSecret = "ENV-SECRET-4f9a", "MAIL-SECRET-77c1", "OTHER-PANE-SECRET-b3d2"
	t.Setenv("FLOCKDECK_TEST_SECRET", envSecret)
	e := newStructuredEnv(t, "plain")
	e.status(t, session.StatusIdle)
	other, ok := ask(e.srv, func() string {
		id, err := e.ws.Spawn(e.boss, spawnStructuredOpts("plain "+otherSecret))
		if err != nil {
			t.Error(err)
		}
		return id
	})
	if !ok || other == "" {
		t.Fatal("no second pane")
	}
	if _, err := e.ws.SendMessage(e.dev, e.child, mailSecret); err != nil {
		t.Fatal(err)
	}
	e.waitFor(t, other, otherSecret)

	m := e.snapshotOf(t, e.child)
	raw, _ := json.Marshal(m)
	for _, secret := range []string{envSecret, mailSecret, otherSecret} {
		if strings.Contains(string(raw), secret) {
			t.Errorf("the push has %q", secret)
		}
	}
	if os.Getenv("FLOCKDECK_TEST_SECRET") != envSecret {
		t.Fatal("the test lost its variable")
	}
}

// The error is cleaned: no control or escape characters, one line, cut.
func TestStructuredErrIsCleaned(t *testing.T) {
	dirty := "boom\x1b[31m red\x1b[0m\nsecond line\x00\r" + strings.Repeat("é", 500)
	m := buildStructured("p", structured.Snapshot{Phase: structured.PhaseFailed, Err: dirty})
	if strings.ContainsAny(m.Err, "\x1b\n\r\x00") || utf8.RuneCountInString(m.Err) > structuredErrCut || !utf8.ValidString(m.Err) {
		t.Errorf("err = %q", m.Err)
	}
	if !strings.HasPrefix(m.Err, "boom[31m red[0msecond line") {
		t.Errorf("err = %q", m.Err)
	}
}

// Text is cut only between characters, whatever the width of the characters, and
// what is left is the start of the text.
func TestClipStructuredNeverCutsACharacter(t *testing.T) {
	for _, unit := range []string{"a", "é", "日", "😀", "a日é😀"} {
		text := strings.Repeat(unit, 40)
		for max := 0; max <= len(text)+1; max++ {
			got, cut := clipStructured(text, max)
			if !utf8.ValidString(got) || strings.ContainsRune(got, utf8.RuneError) || !strings.HasPrefix(text, got) || len(got) > max {
				t.Fatalf("%q max %d: %q", unit, max, got)
			}
			if cut != (len(text) > max) {
				t.Fatalf("%q max %d: cut %v", unit, max, cut)
			}
			// No more than one character is lost to the boundary.
			if cut && max-len(got) >= utf8.UTFMax {
				t.Fatalf("%q max %d kept only %d bytes", unit, max, len(got))
			}
		}
	}
	got, _ := clipStructured("ok\xff\xfe\x07 text\ttab\nline", 100)
	if got != "ok� text\ttab\nline" {
		t.Errorf("got %q", got)
	}
}

// The plan, the card and the other fields are cut the same way.
func TestStructuredFieldsAreBounded(t *testing.T) {
	long := strings.Repeat("日本語", 400)
	sn := structured.Snapshot{
		Phase: structured.PhaseReady, Status: bridge.StatusWaiting,
		Plan: make([]acp.PlanEntry, 150),
		Pending: &bridge.Card{Seq: 3, RequestID: "r", Tool: long, Command: long, Summary: long, File: long,
			Options: []acp.PermissionOption{{OptionID: long, Name: long, Kind: long}}},
		Setup: &structured.Setup{Kind: structured.SetupLogin, Message: long + "\nmore", Login: "Sign in\nthen restart " + strings.Repeat("x", 1000)},
	}
	for i := range sn.Plan {
		sn.Plan[i] = acp.PlanEntry{Content: long, Status: "pending"}
	}
	m := buildStructured("p", sn)
	if len(m.Plan) != structuredPlanMax {
		t.Errorf("%d plan rows", len(m.Plan))
	}
	raw, _ := json.Marshal(m)
	if !utf8.Valid(raw) || strings.ContainsRune(string(raw), utf8.RuneError) {
		t.Fatal("the push is not clean UTF-8")
	}
	if s := m.Setup; s == nil || s.Fix == "" || len(s.Fix) > structuredCardCut || len(s.Message) > structuredCardCut || strings.ContainsAny(s.Fix+s.Message, "\n") || !strings.HasPrefix(s.Fix, "Sign in then restart ") {
		t.Errorf("setup = %+v", s)
	}
	if !m.Card.Clipped || len(m.Card.Command) > structuredCommandCut || len(m.Card.Tool) > structuredCardCut || len(m.Card.Options[0].Name) > 100 {
		t.Errorf("card = %+v", m.Card)
	}
}

// A watch holds no goroutine: the push comes from the state push the server
// already makes.
func TestStructuredWatchesLeaveNoGoroutines(t *testing.T) {
	e := newStructuredEnv(t, "plain")
	e.status(t, session.StatusIdle)
	before := runtime.NumGoroutine()
	for i := 0; i < 5; i++ {
		c := e.watcher(t, 8)
		structuredReply(t, e.srv, c, command{Cmd: "structuredWatch", ID: e.child}, "structured")
		e.srv.handleCommand(c, command{Cmd: "structuredUnwatch"})
	}
	e.push()
	// Goroutines of the pane and the server come and go; what counts is that the
	// number settles back, since a watch holds none.
	deadline := time.Now().Add(10 * time.Second)
	for runtime.NumGoroutine() > before+2 {
		if time.Now().After(deadline) {
			t.Fatalf("%d goroutines, %d before", runtime.NumGoroutine(), before)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func spawnStructuredOpts(task string) workspace.SpawnOptions {
	return workspace.SpawnOptions{Task: task, Role: "worker", Agent: "opencode-acp", Kind: session.KindClaude, SpawnedByAgent: true}
}

// waitFor waits until a pane's entries show text.
func (e *structuredEnv) waitFor(t *testing.T, id, text string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		entries, _ := ask(e.srv, func() []transcript.Entry { en, _ := e.ws.StructuredEntries(id); return en })
		for _, en := range entries {
			if strings.Contains(en.Text, text) || strings.Contains(en.Markdown, text) {
				return
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("pane %s never showed %q", id, text)
}

// A long reply is read whole through the conversation commands: the push that
// used to cut it carries no text.
func TestStructuredLongReplyIsReadThroughTheConversation(t *testing.T) {
	e := newStructuredEnv(t, "plain")
	e.status(t, session.StatusIdle)
	if err, _ := ask(e.srv, func() error { _, err := e.ws.SendToPane(e.child, "long", false); return err }); err != nil {
		t.Fatal(err)
	}
	e.status(t, session.StatusIdle)
	c := &controlClient{out: make(chan []byte, 16)}
	var got string
	deadline := time.Now().Add(20 * time.Second)
	for got == "" && time.Now().Before(deadline) {
		for _, x := range structuredPage(t, e, c, "").Entries {
			if x.Kind == "reply" && len(x.Markdown) >= 12000 {
				got = x.Markdown
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(got) != 12000 {
		t.Fatalf("the reply is %d bytes through the conversation, want 12000", len(got))
	}
}
