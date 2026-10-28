package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/acp"
	"github.com/jmwri/flockdeck/internal/acp/bridge"
	"github.com/jmwri/flockdeck/internal/server/structuredtest"
)

const secretCommand = "curl -H 'Authorization: Bearer hunter2' https://example.com"

var cardOptions = []acp.PermissionOption{
	{OptionID: "opt-a", Name: "Allow", Kind: "allow_once"},
	{OptionID: "opt-b", Name: "Always allow", Kind: "allow_always"},
	{OptionID: "opt-c", Name: "Reject", Kind: "reject_once"},
}

// lenient is a pane that takes any answer it is given and says so. The server's
// own checks are what stand between a command and it, so a test with this pane
// fails when one of them is removed. (The strict pane, structuredtest.Memory,
// would refuse a stale answer itself and hide that.)
type lenient struct {
	mu       sync.Mutex
	card     *bridge.Card
	answered []structuredtest.Answer
}

func (l *lenient) PendingPermission() *bridge.Card {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.card == nil {
		return nil
	}
	c := *l.card
	return &c
}

func (l *lenient) OwnAdapter() bool     { return false }
func (l *lenient) RuntimeToken() string { return "lenient" }

func (l *lenient) AnswerPermission(seq uint64, requestID, optionID string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.answered = append(l.answered, structuredtest.Answer{Seq: seq, RequestID: requestID, OptionID: optionID})
	return nil
}

func (l *lenient) got() []structuredtest.Answer {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]structuredtest.Answer(nil), l.answered...)
}

// testPane is the pane "p1" and what to ask it afterwards.
type testPane struct {
	pane     StructuredPane
	card     bridge.Card
	answered func() []structuredtest.Answer
	end      func()
}

// withPane makes a server whose pane "p1" is the strict reference pane, or the
// lenient one, with a card pending, and captures what is logged.
func withPane(t *testing.T, strict bool) (*Server, *testPane, *[]string) {
	t.Helper()
	srv, _ := newTestServer(t)
	tp := &testPane{}
	if strict {
		m := &structuredtest.Memory{ErrStale: ErrPermissionStale, Tool: "Run curl", Command: secretCommand}
		tp.card = m.Open("req-1", cardOptions)
		tp.pane, tp.answered, tp.end = m, m.Answered, func() { m.End(tp.card) }
	} else {
		l := &lenient{}
		tp.card = bridge.Card{Seq: 1, RequestID: "req-1", SessionID: "s1", Tool: "Run curl", Kind: "execute", Command: secretCommand, Options: cardOptions}
		card := tp.card
		l.card = &card
		tp.pane, tp.answered, tp.end = l, l.got, func() { l.mu.Lock(); l.card = nil; l.mu.Unlock() }
	}
	srv.SetStructuredPanes(func(id string) (StructuredPane, bool) {
		if id == "p1" {
			return tp.pane, true
		}
		return nil, false
	})
	var mu sync.Mutex
	var lines []string
	old := permissionLog
	permissionLog = func(format string, args ...any) {
		mu.Lock()
		lines = append(lines, fmt.Sprintf(format, args...))
		mu.Unlock()
	}
	t.Cleanup(func() { permissionLog = old })
	return srv, tp, &lines
}

func answerCmd(pane string, seq uint64, req, opt string) command {
	return command{Cmd: "permissionAnswer", ID: pane, CardSeq: seq, RequestID: req, OptionID: opt, Req: "r1"}
}

// send runs the command as the window c and returns the reply and, for a
// refusal, the notice.
func send(t *testing.T, srv *Server, c *controlClient, cmd command) (reply permissionAnswerMsg, notice string) {
	t.Helper()
	srv.handleCommand(c, cmd)
	deadline := time.After(10 * time.Second)
	for reply.Type == "" {
		select {
		case raw := <-c.out:
			switch typeOf(raw) {
			case "permissionAnswer":
				if err := json.Unmarshal(raw, &reply); err != nil {
					t.Fatal(err)
				}
			case "notice":
				var n noticeMsg
				_ = json.Unmarshal(raw, &n)
				if n.Error {
					notice = n.Text
				}
			}
		case <-deadline:
			t.Fatalf("%s was not answered", cmd.Cmd)
		}
	}
	// The notice is sent just after the reply.
	select {
	case raw := <-c.out:
		if typeOf(raw) == "notice" {
			var n noticeMsg
			_ = json.Unmarshal(raw, &n)
			if n.Error {
				notice = n.Text
			}
		}
	case <-time.After(200 * time.Millisecond):
	}
	return reply, notice
}

func desk() *controlClient { return &controlClient{out: make(chan []byte, 32)} }

func TestPermissionAnswerIsFullAccessOnly(t *testing.T) {
	if commandAccess["permissionAnswer"] != accessFull {
		t.Fatal("permissionAnswer is open to viewers")
	}
	srv, _ := newTestServer(t)
	setAccess(t, srv, "viewer-dev", "viewer", true)
	viewer := &controlClient{remote: true, device: "viewer-dev"}
	if srv.allowCommand(viewer, "permissionAnswer") {
		t.Error("a viewer, even one that may watch panes, was let send permissionAnswer")
	}
	if srv.allowCommand(&controlClient{remote: true}, "permissionAnswer") {
		t.Error("an unnamed relay device was let send permissionAnswer")
	}
}

func TestALocalWindowAnswersWithAnOptionOnTheCard(t *testing.T) {
	for _, strict := range []bool{true, false} {
		srv, tp, logged := withPane(t, strict)
		reply, notice := send(t, srv, desk(), answerCmd("p1", tp.card.Seq, "req-1", "opt-a"))
		if !reply.OK || reply.Error != "" || notice != "" || reply.RequestID != "req-1" || reply.CardSeq != tp.card.Seq {
			t.Fatalf("strict=%v: reply = %+v, notice %q", strict, reply, notice)
		}
		if got := tp.answered(); len(got) != 1 || got[0].OptionID != "opt-a" || got[0].Seq != tp.card.Seq {
			t.Errorf("the pane was answered %v", got)
		}
		// What is logged says which card and what kind of option, and nothing of
		// what the request was about.
		if len(*logged) != 1 || !strings.Contains((*logged)[0], "req-1") || !strings.Contains((*logged)[0], "allow_once") {
			t.Errorf("log = %q", *logged)
		}
		for _, l := range *logged {
			for _, secret := range []string{"hunter2", "curl", "example.com", "Run curl"} {
				if strings.Contains(l, secret) {
					t.Errorf("the log has %q: %s", secret, l)
				}
			}
		}
	}
}

// The server compares an option id with the ids on the card as strings. A kind
// name is refused because it is not an id on the card, not because kinds are
// understood; an agent that names an option "allow_always" has an id that is
// accepted.
func TestOptionsAreComparedByIdAndKindNamesAreOnlyIds(t *testing.T) {
	srv, tp, _ := withPane(t, false)
	for _, byName := range []string{"allow_always", "allow_once", "Always allow", "always", "reject_once", "allow"} {
		reply, _ := send(t, srv, desk(), answerCmd("p1", tp.card.Seq, "req-1", byName))
		if reply.OK || reply.Error != permissionUnknownOption {
			t.Errorf("an answer of %q: %+v", byName, reply)
		}
	}
	if len(tp.answered()) != 0 {
		t.Fatalf("the pane was answered %v", tp.answered())
	}
	reply, _ := send(t, srv, desk(), answerCmd("p1", tp.card.Seq, "req-1", "opt-b"))
	if !reply.OK {
		t.Fatalf("reply = %+v", reply)
	}
	// An agent that calls its option "allow_always" has an id that is on the card.
	srv2, tp2, _ := withPane(t, false)
	l := tp2.pane.(*lenient)
	l.mu.Lock()
	l.card.Options = []acp.PermissionOption{{OptionID: "allow_always", Name: "Allow", Kind: "allow_once"}}
	l.mu.Unlock()
	reply, _ = send(t, srv2, desk(), answerCmd("p1", tp2.card.Seq, "req-1", "allow_always"))
	if !reply.OK {
		t.Errorf("an option whose id is a kind name, and is on the card, was refused: %+v", reply)
	}
}

// A window reached through the relay is refused whatever its role: through
// the real access check for a viewer, and by the command for a full device.
func TestAWindowReachedThroughTheRelayIsRefusedWhateverItsRole(t *testing.T) {
	srv, tp, logged := withPane(t, true)
	setAccess(t, srv, "full-dev", "full", false)
	setAccess(t, srv, "viewer-dev", "viewer", true)

	full := &controlClient{out: make(chan []byte, 32), remote: true, device: "full-dev"}
	reply, notice := send(t, srv, full, answerCmd("p1", tp.card.Seq, "req-1", "opt-a"))
	if reply.OK || reply.Error != permissionAnswerDeskOnly || notice != permissionAnswerDeskOnly {
		t.Errorf("full device: reply %+v, notice %q", reply, notice)
	}

	for _, device := range []string{"viewer-dev", ""} {
		v := &controlClient{out: make(chan []byte, 32), remote: true, device: device}
		srv.handleCommand(v, answerCmd("p1", tp.card.Seq, "req-1", "opt-a"))
		select {
		case raw := <-v.out:
			var n noticeMsg
			if typeOf(raw) != "notice" || json.Unmarshal(raw, &n) != nil || n.Text != viewerRefused {
				t.Errorf("device %q: got %s, want the viewer refusal", device, raw)
			}
		case <-time.After(5 * time.Second):
			t.Errorf("device %q was told nothing", device)
		}
	}
	if len(tp.answered()) != 0 || len(*logged) != 0 {
		t.Errorf("the pane was answered %v, logged %q", tp.answered(), *logged)
	}
}

// The server's own checks, against a pane that takes any answer.
func TestPermissionAnswerRefusals(t *testing.T) {
	cases := []struct {
		name string
		cmd  func(card bridge.Card) command
		want string
	}{
		{"a wrong option id", func(c bridge.Card) command { return answerCmd("p1", c.Seq, "req-1", "opt-z") }, permissionUnknownOption},
		{"a stale request id", func(c bridge.Card) command { return answerCmd("p1", c.Seq, "req-0", "opt-a") }, permissionStale},
		{"a card number that is not the pending card", func(c bridge.Card) command { return answerCmd("p1", c.Seq+1, "req-1", "opt-a") }, permissionStale},
		{"the number of an earlier card with the same request id", func(c bridge.Card) command { return answerCmd("p1", c.Seq-1, "req-1", "opt-a") }, permissionStale},
		{"a pane that is not structured", func(c bridge.Card) command { return answerCmd("terminal-pane", c.Seq, "req-1", "opt-a") }, permissionNotStructured},
		{"no pane", func(c bridge.Card) command { return answerCmd("", c.Seq, "req-1", "opt-a") }, permissionBadRequest},
		{"no card number", func(c bridge.Card) command { return answerCmd("p1", 0, "req-1", "opt-a") }, permissionBadRequest},
		{"no request id", func(c bridge.Card) command { return answerCmd("p1", c.Seq, "", "opt-a") }, permissionBadRequest},
		{"no option id", func(c bridge.Card) command { return answerCmd("p1", c.Seq, "req-1", "") }, permissionBadRequest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv, tp, logged := withPane(t, false)
			// Card 2 is the one pending, so that "card 1" is an earlier card.
			l := tp.pane.(*lenient)
			l.mu.Lock()
			l.card.Seq = 2
			l.mu.Unlock()
			tp.card.Seq = 2
			reply, notice := send(t, srv, desk(), c.cmd(tp.card))
			if reply.OK || reply.Error != c.want || notice != c.want {
				t.Errorf("reply %+v, notice %q, want %q", reply, notice, c.want)
			}
			if len(tp.answered()) != 0 || len(*logged) != 0 {
				t.Errorf("answered %v, logged %q", tp.answered(), *logged)
			}
		})
	}
}

// The case the card number is for: the agent ends a request and asks again with
// the same id. An answer meant for the first card, replayed, queued or clicked
// late, is stale for the second.
func TestAnAnswerForAnOldCardIsStaleForANewCardWithTheSameRequestId(t *testing.T) {
	for _, strict := range []bool{true, false} {
		srv, tp, _ := withPane(t, strict)
		old := tp.card
		tp.end()
		var fresh bridge.Card
		if strict {
			fresh = tp.pane.(*structuredtest.Memory).Open("req-1", cardOptions)
		} else {
			l := tp.pane.(*lenient)
			fresh = bridge.Card{Seq: old.Seq + 1, RequestID: "req-1", Tool: "rm -rf /", Kind: "execute", Options: cardOptions}
			l.mu.Lock()
			l.card = &fresh
			l.mu.Unlock()
		}
		reply, _ := send(t, srv, desk(), answerCmd("p1", old.Seq, "req-1", "opt-a"))
		if reply.OK || reply.Error != permissionStale {
			t.Errorf("strict=%v: an answer for the old card: %+v", strict, reply)
		}
		if len(tp.answered()) != 0 {
			t.Errorf("strict=%v: the new card was answered: %v", strict, tp.answered())
		}
		reply, _ = send(t, srv, desk(), answerCmd("p1", fresh.Seq, "req-1", "opt-a"))
		if !reply.OK {
			t.Errorf("strict=%v: an answer for the new card: %+v", strict, reply)
		}
	}
}

func TestAPaneWithNothingPendingIsRefused(t *testing.T) {
	srv, tp, _ := withPane(t, false)
	tp.end()
	reply, _ := send(t, srv, desk(), answerCmd("p1", tp.card.Seq, "req-1", "opt-a"))
	if reply.OK || reply.Error != permissionNothingAsked {
		t.Errorf("reply = %+v", reply)
	}
}

func TestBeforeTheWorkspaceSetsItNoPaneIsStructured(t *testing.T) {
	srv, _ := newTestServer(t)
	reply, _ := send(t, srv, desk(), answerCmd("p1", 1, "req-1", "opt-a"))
	if reply.OK || reply.Error != permissionNotStructured {
		t.Errorf("reply = %+v", reply)
	}
}

// Two answers to one card: the first is taken, the second is refused.
func TestTheSecondAnswerToACardIsRefused(t *testing.T) {
	srv, tp, logged := withPane(t, true)
	first, _ := send(t, srv, desk(), answerCmd("p1", tp.card.Seq, "req-1", "opt-a"))
	second, notice := send(t, srv, desk(), answerCmd("p1", tp.card.Seq, "req-1", "opt-c"))
	if !first.OK {
		t.Fatalf("first = %+v", first)
	}
	if second.OK || second.Error == "" || notice == "" {
		t.Errorf("second = %+v, notice %q", second, notice)
	}
	if got := tp.answered(); len(got) != 1 || got[0].OptionID != "opt-a" {
		t.Errorf("the pane was answered %v", got)
	}
	if len(*logged) != 1 {
		t.Errorf("logged %q", *logged)
	}
}

// failing is a pane that fails its answers with a given error.
type failing struct {
	lenient
	err error
}

func (f *failing) AnswerPermission(uint64, string, string) error { return f.err }

// The card can end between the check and the answer; the pane says so and the
// window is told it is stale. Any other error is shown cleaned and short.
func TestACardThatEndsBetweenTheCheckAndTheAnswerIsStale(t *testing.T) {
	srv, tp, logged := withPane(t, false)
	f := &failing{err: fmt.Errorf("answer: %w", ErrPermissionStale)}
	card := tp.card
	f.card = &card
	srv.SetStructuredPanes(func(string) (StructuredPane, bool) { return f, true })
	reply, _ := send(t, srv, desk(), answerCmd("p1", tp.card.Seq, "req-1", "opt-a"))
	if reply.OK || reply.Error != permissionStale || len(*logged) != 0 {
		t.Errorf("reply = %+v, logged %q", reply, *logged)
	}
	f.err = errors.New("bad \x1b[31mred\x1b[0m\nline " + strings.Repeat("x", 500))
	reply, notice := send(t, srv, desk(), answerCmd("p1", tp.card.Seq, "req-1", "opt-a"))
	if reply.OK || len(reply.Error) > 300 || strings.ContainsAny(reply.Error+notice, "\x1b\n") || len(*logged) != 0 {
		t.Errorf("reply = %+v, notice %q", reply, notice)
	}
}

func TestCleanForNotice(t *testing.T) {
	if got := cleanForNotice("a\x1b[2Jb\r\n c\u009bd"); strings.ContainsAny(got, "\x1b\r\n\u009b") {
		t.Errorf("got %q", got)
	}
	if got := cleanForNotice(strings.Repeat("é", 300)); len(got) > 204 || !strings.HasSuffix(got, "...") {
		t.Errorf("len %d", len(got))
	}
}

// The adapter the workspace writes runs this against itself. The reference pane
// does here, so the test is kept honest.
func TestTheReferencePaneConforms(t *testing.T) {
	structuredtest.Conformance(t, ErrPermissionStale, func(t *testing.T) structuredtest.Harness {
		return (&structuredtest.Memory{ErrStale: ErrPermissionStale}).Harness()
	})
}
