package server

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/acp/bridge"
	"github.com/jmwri/flockdeck/internal/remote"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/session/transcript"
)

// Strings that stand for the agent's words. A push never holds any of them:
// each test puts them in the card and then looks for them in everything that
// would be sent.
var cardMarkers = []string{"CMDMARKER", "TOOLMARKER", "PATHMARKER", "SUMMARYMARKER", "PLANMARKER", "BODYMARKER", "CWDMARKER", "QUESTIONMARKER", "DIFFMARKER"}

// markedCard is a card with a marker in every field an agent writes.
func markedCard(f func(*bridge.Card)) *bridge.Card {
	c := &bridge.Card{Seq: 5, RequestID: "req-CMDMARKER", Kind: "execute", Tool: "TOOLMARKER", Summary: "SUMMARYMARKER",
		Command: "echo CMDMARKER", CommandText: "echo CMDMARKER", Options: plainOptions}
	if f != nil {
		f(c)
	}
	return c
}

// cardPane is a structured pane with a card fixed in it.
type cardPane struct {
	card *bridge.Card
	own  bool
	root string
}

func (p *cardPane) PendingPermission() *bridge.Card {
	if p.card == nil {
		return nil
	}
	c := *p.card
	return &c
}
func (p *cardPane) AnswerPermission(uint64, string, string) error { return nil }
func (p *cardPane) OwnAdapter() bool                              { return p.own }
func (p *cardPane) RuntimeToken() string                          { return "1.0" }
func (p *cardPane) ProjectRoot() string                           { return p.root }

// pushEnv is a structured pane that waits on a request, with the push for it
// switched on, and its card chosen by the test. The pane is the fake agent's, so
// its status and the time its wait began are the runtime's own.
type pushEnv struct {
	*structuredEnv
	pane  *cardPane
	since time.Time
}

func newPushEnv(t *testing.T, card *bridge.Card, own bool) *pushEnv {
	t.Helper()
	e := &pushEnv{structuredEnv: newStructuredEnv(t, "hold-perm")}
	e.status(t, session.StatusWaiting)
	e.pane = &cardPane{card: card, own: own, root: testRoot}
	enrolled(e.srv)
	real := e.srv.structuredPane
	e.srv.SetStructuredPanes(func(id string) (StructuredPane, bool) {
		if id == e.child {
			return e.pane, true
		}
		return real(id)
	})
	ask(e.srv, func() bool {
		e.srv.push.structuredOn = true
		// The desk is not in use.
		e.srv.push.idleSince = func() (time.Duration, bool, bool) { return time.Hour, true, true }
		return true
	})
	e.since, _ = ask(e.srv, func() time.Time { return e.ws.Runtime(e.ws.Pane(e.child)).StatusSince() })
	return e
}

func (e *pushEnv) due(at time.Duration) *pushOut { return due(e.srv, e.since.Add(at)) }

func (e *pushEnv) name() string { return paneName(e.srv, e.ws, e.child) }

// everything is all that would be put in front of any device.
func everything(t *testing.T, n *pushOut) string {
	t.Helper()
	raw, err := json.Marshal(n)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func noMarkers(t *testing.T, what, s string) {
	t.Helper()
	for _, m := range cardMarkers {
		if strings.Contains(s, m) {
			t.Errorf("%s holds %s: %s", what, m, s)
		}
	}
}

// The two audiences and the fixed wordings. The body is the whole of what the
// agent's card changes, and only as a yes or no.
func TestAStructuredWaitIsPushedInTwoWordings(t *testing.T) {
	cases := []struct {
		name     string
		card     *bridge.Card
		own      bool
		approver string // the body for a device with approvals on; "" is the same as the rest
	}{
		{"a command the phone may allow", markedCard(nil), true, "Allow or deny. On desk"},
		{"an edit the phone may allow", markedCard(func(c *bridge.Card) {
			path := inRoot("PATHMARKER.txt")
			*c = *editCard(path, transcript.EditDiff(path, "DIFFMARKER one", "DIFFMARKER two"))
			c.Tool, c.Summary = "TOOLMARKER", "SUMMARYMARKER"
		}), true, "Allow or deny. On desk"},
		{"an edit with no diff", markedCard(func(c *bridge.Card) {
			*c = *editCard(inRoot("PATHMARKER.txt"), "")
			c.Tool = "TOOLMARKER"
		}), true, ""},
		{"a command on an agent that is not ours", markedCard(nil), false, ""},
		{"a tool kind the phone may only deny", markedCard(func(c *bridge.Card) { c.Kind = "fetch" }), true, ""},
		{"a command with settings that cannot be checked", markedCard(func(c *bridge.Card) { c.Cwd = "CWDMARKER" }), true, ""},
		{"a plan", markedCard(func(c *bridge.Card) { c.Kind = "switch_mode"; c.Body = "PLANMARKER" }), true, ""},
		{"a question", markedCard(func(c *bridge.Card) {
			c.Kind = bridge.QuestionKind
			c.Questions = []bridge.Question{{Question: "QUESTIONMARKER"}}
		}), true, ""},
		{"a request with a reason of the agent's", markedCard(func(c *bridge.Card) { c.Body = "BODYMARKER" }), true, ""},
		{"no card at all", nil, true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newPushEnv(t, tc.card, tc.own)
			n := e.due(30 * time.Second)
			if n == nil {
				t.Fatal("a wait of 30 seconds was not pushed")
			}
			title := e.name() + " · "
			if !strings.HasPrefix(n.Title, title) || !strings.HasSuffix(n.Title, " needs you") {
				t.Errorf("title %q", n.Title)
			}
			if n.Body != "Answer on the desktop. On desk" || n.URL != "/d/h1/"+e.child || n.Tag != "flockdeck-h1" {
				t.Errorf("the push for every other device: %+v", n.Notification)
			}
			switch {
			case tc.approver == "" && n.Approver != nil:
				t.Errorf("a device with approvals on is told %+v of a card it cannot answer", n.Approver)
			case tc.approver != "":
				if n.Approver == nil {
					t.Fatal("a device with approvals on is told nothing different")
				}
				if n.Approver.Body != tc.approver || n.Approver.Title != n.Title || n.Approver.URL != n.URL || n.Approver.Tag != n.Tag {
					t.Errorf("the push for a device with approvals on: %+v", *n.Approver)
				}
			}
			noMarkers(t, "the push", everything(t, n))
		})
	}
}

// Allow or deny is said only for a card the phone may allow. A card with a
// command that is "Allow or deny" in one test is "Answer on the desktop" when
// the same command holds a secret.
func TestAStructuredWaitWithASecretIsNotOfferedAnAllow(t *testing.T) {
	e := newPushEnv(t, markedCard(func(c *bridge.Card) {
		c.Command, c.CommandText = "echo CMDMARKER "+fakeToken, "echo CMDMARKER "+fakeToken
	}), true)
	n := e.due(30 * time.Second)
	if n == nil || n.Approver != nil {
		t.Fatalf("a command with a token in it: %+v", n)
	}
	if s := everything(t, n); strings.Contains(s, fakeToken) || strings.Contains(s, "ghp_") {
		t.Errorf("the push holds the token: %s", s)
	}
}

// Anonymous says only how many, with no wording about the card.
func TestAnAnonymousStructuredWaitSaysOnlyThatAnAgentWaits(t *testing.T) {
	e := newPushEnv(t, markedCard(nil), true)
	ask(e.srv, func() bool { e.srv.prefs.Push.Anonymous = true; return true })
	n := e.due(30 * time.Second)
	if n == nil {
		t.Fatal("not pushed")
	}
	if n.Title != "An agent on desk needs you" || n.Body != "" || n.Approver != nil || n.URL != "/d/h1/"+e.child {
		t.Errorf("push = %+v approver %v", n.Notification, n.Approver)
	}
	s := everything(t, n)
	noMarkers(t, "the push", s)
	if strings.Contains(s, e.name()) {
		t.Errorf("an anonymous push names the pane: %s", s)
	}
}

// Several waiting agents are one push that counts them, the same for every
// device, whether or not one of them is structured.
func TestSeveralWaitsAreOnePushWithNoApproverBody(t *testing.T) {
	e := newPushEnv(t, markedCard(nil), true)
	waitIn(t, e.srv, e.ws, e.boss)
	n := e.due(time.Hour)
	if n == nil {
		t.Fatal("not pushed")
	}
	if n.Title != "2 agents need you" || n.Approver != nil || !strings.HasPrefix(n.Body, "On desk: ") {
		t.Errorf("push = %+v approver %v", n.Notification, n.Approver)
	}
	noMarkers(t, "the push", everything(t, n))
}

// Until the approvals socket is built, a structured pane's wait is not pushed:
// what ships is structuredPushLive, and nothing in the test server sets it.
func TestAStructuredWaitIsNotPushedWhileTheSwitchIsOff(t *testing.T) {
	e := newPushEnv(t, markedCard(nil), true)
	ask(e.srv, func() bool { e.srv.push.structuredOn = false; return true })
	if structuredPushLive {
		t.Skip("the push is live; TestStructuredPushNeedsTheApprovalsSocket is the test that matters")
	}
	if n := e.due(time.Hour); n != nil {
		t.Errorf("a structured wait was pushed with the push off: %+v", n)
	}
}

// The rules a terminal wait is pushed under apply to a structured one: the
// delay, a minute between pushes, the desk in use, a muted pane, and a pane used
// from a phone just now.
func TestAStructuredWaitKeepsThePushRules(t *testing.T) {
	e := newPushEnv(t, markedCard(nil), true)
	if n := e.due(29 * time.Second); n != nil {
		t.Errorf("a wait of 29 seconds was pushed: %+v", n)
	}

	// The desk in use.
	ask(e.srv, func() bool {
		e.srv.push.idleSince = func() (time.Duration, bool, bool) { return time.Second, false, true }
		return true
	})
	if n := e.due(time.Minute); n != nil {
		t.Errorf("a wait was pushed to a phone with somebody at the desk: %+v", n)
	}
	ask(e.srv, func() bool {
		e.srv.push.idleAsk = time.Time{}
		e.srv.push.idleSince = func() (time.Duration, bool, bool) { return time.Hour, true, true }
		return true
	})

	// Muted.
	ask(e.srv, func() bool { e.ws.Pane(e.child).Muted = true; return true })
	if n := e.due(time.Minute); n != nil {
		t.Errorf("a muted pane was pushed: %+v", n)
	}
	ask(e.srv, func() bool { e.ws.Pane(e.child).Muted = false; return true })

	// Used from a phone a moment ago.
	relayUse.mark(e.child, e.since.Add(time.Minute))
	if n := e.due(time.Minute + time.Second); n != nil {
		t.Errorf("a pane in use from a phone was pushed: %+v", n)
	}

	// Once, and not again; the minute between pushes is kept.
	ask(e.srv, func() bool { e.srv.push.last = e.since.Add(time.Hour - 10*time.Second); return true })
	if n := e.due(time.Hour); n != nil {
		t.Errorf("a push came within a minute of the last: %+v", n)
	}
	if n := e.due(time.Hour + minPushGap); n == nil {
		t.Error("the wait was not pushed once the minute had passed")
	}
	if n := e.due(2*time.Hour + minPushGap); n != nil {
		t.Errorf("one wait was pushed twice: %+v", n)
	}
}

// A structured pane that needs setting up is blocked, not waiting on a request,
// and is not pushed.
func TestAStructuredPaneThatIsNotWaitingOnARequestIsNotPushed(t *testing.T) {
	e := newStructuredEnv(t, "plain")
	e.status(t, session.StatusIdle)
	enrolled(e.srv)
	ask(e.srv, func() bool {
		e.srv.push.structuredOn = true
		e.srv.push.idleSince = func() (time.Duration, bool, bool) { return time.Hour, true, true }
		return true
	})
	if n := due(e.srv, time.Now().Add(time.Hour)); n != nil {
		t.Errorf("an idle structured pane was pushed: %+v", n)
	}
}

// The terminal wording is unchanged by any of it.
func TestATerminalWaitIsPushedAsBefore(t *testing.T) {
	srv, ws := newTestServer(t)
	enrolled(srv)
	ask(srv, func() bool { srv.push.structuredOn = true; return true })
	id, since := waitingPane(t, srv, ws)
	n := due(srv, since.Add(31*time.Second))
	if n == nil || n.Body != "On desk" || n.Approver != nil || n.URL != "/d/h1/"+id {
		t.Errorf("push = %+v", n)
	}
}

// Which device is told what, from the desk's record of it.
func TestEachDeviceIsToldTheWordingMeantForIt(t *testing.T) {
	e := newApproveEnv(t)
	e.allow(t, "d1")
	listDevice(e.srv, "d3")
	p := pushOut{
		Notification: remote.Notification{Title: "api needs you", Body: "Answer on the desktop. On desk", URL: "/d/h1/p1", Tag: "flockdeck-h1"},
		Approver:     &remote.Notification{Title: "api needs you", Body: "Allow or deny. On desk", URL: "/d/h1/p1", Tag: "flockdeck-h1"},
	}
	notify := e.srv.notificationFor(p)
	if n, ok := notify("d1"); !ok || n.Body != "Allow or deny. On desk" {
		t.Errorf("the device with approvals on: %+v %v", n, ok)
	}
	for _, id := range []string{"d2", "d3"} {
		if n, ok := notify(id); !ok || n.Body != "Answer on the desktop. On desk" {
			t.Errorf("%s, approvals off: %+v %v", id, n, ok)
		}
	}
	if _, ok := notify(""); ok && e.srv.mayPush("") {
		t.Error("a push went to no device")
	}

	// With no approver wording, a device with approvals on is told what the rest are.
	p.Approver = nil
	if n, ok := e.srv.notificationFor(p)("d1"); !ok || n.Body != p.Body {
		t.Errorf("with one wording: %+v %v", n, ok)
	}

	// A viewer that may not see panes is sent nothing, with approvals on or not:
	// the pane's name is in the push.
	e.allow(t, "d2")
	setAccess(t, e.srv, "d2", "viewer", false)
	if !e.srv.approveSwitchOn("d2") {
		t.Fatal("the test needs approvals still on for the viewer")
	}
	p.Approver = &remote.Notification{Title: "t", Body: "Allow or deny. On desk"}
	if n, ok := e.srv.notificationFor(p)("d2"); ok {
		t.Errorf("a viewer that may not see panes was sent %+v", n)
	}
	setAccess(t, e.srv, "d2", "viewer", true)
	if n, ok := e.srv.notificationFor(p)("d2"); !ok || n.Body != "Allow or deny. On desk" {
		t.Errorf("a viewer that watches panes, approvals on: %+v %v", n, ok)
	}
}

// The words "Allow or deny" tell a person the desktop will take their answer.
// They go by the switch; the gate, which holds a decision to the key the
// handshake proved, is the socket's. A key that is another device's passes
// neither.
func TestThePushGoesBySwitchAndNotKey(t *testing.T) {
	e := newApproveEnv(t)
	e.allow(t, "d1")
	d1, d2 := e.fake.keys["d1"], e.fake.keys["d2"]
	if !e.srv.approveSwitchOn("d1") || !e.srv.mayApproveWithKey("d1", d1) {
		t.Fatal("the device the desk allowed is not allowed")
	}
	if e.srv.mayApproveWithKey("d1", d2) {
		t.Error("another device's roster key passed the gate for d1")
	}
	if e.srv.approveSwitchOn("d2") {
		t.Error("a device the desk did not allow has the switch on")
	}
}

// T3 builds the sealed socket the push sends the person to. A push that says
// "Allow or deny" while there is no such socket would send them to a card they
// cannot answer, so structuredPushLive may be true only in a tree where a handler
// is registered for it.
func TestStructuredPushNeedsTheApprovalsSocket(t *testing.T) {
	if !structuredPushLive {
		return
	}
	if !routeRegistered(t, ".", "/ws/approvals") {
		t.Fatal(`structuredPushLive is true, but no handler is registered for "/ws/approvals": ` +
			"a structured wait would be pushed with nowhere to answer it from the phone")
	}
}

// The check above looks at the source, so it has to be shown to see a handler
// where there is one and to miss it where there is none. A guard that finds
// nothing in every tree passes vacuously.
func TestTheRouteCheckSeesARegisteredHandler(t *testing.T) {
	if !routeRegistered(t, ".", "/ws/artifacts") {
		t.Error("the check does not see the handler registered for /ws/artifacts")
	}
	if routeRegistered(t, ".", "/ws/no-such-socket") {
		t.Error("the check sees a handler for a path nothing is registered for")
	}
	dir := t.TempDir()
	write := func(name, src string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.go", "package x\n\nfunc f(mux interface{ HandleFunc(string, func()) }) { mux.HandleFunc(\"/ws/approvals\", nil) }\n")
	if !routeRegistered(t, dir, "/ws/approvals") {
		t.Error("the check does not see a handler in a file")
	}
	// A mention in a comment, a string, or a test file is not a registration.
	_ = os.Remove(filepath.Join(dir, "a.go"))
	write("b.go", "package x\n\n// mux.HandleFunc(\"/ws/approvals\", nil)\nvar s = \"/ws/approvals\"\n")
	write("c_test.go", "package x\n\nfunc g(mux interface{ HandleFunc(string, func()) }) { mux.HandleFunc(\"/ws/approvals\", nil) }\n")
	if routeRegistered(t, dir, "/ws/approvals") {
		t.Error("the check takes a comment, a string or a test file for a handler")
	}
}

// routeRegistered says a non-test Go file in dir calls Handle or HandleFunc with
// the path as a string literal.
func routeRegistered(t *testing.T, dir, path string) bool {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		tree, err := parser.ParseFile(token.NewFileSet(), f, nil, 0)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		ast.Inspect(tree, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || (sel.Sel.Name != "Handle" && sel.Sel.Name != "HandleFunc") {
				return true
			}
			if lit, ok := call.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING && lit.Value == `"`+path+`"` {
				found = true
			}
			return true
		})
	}
	return found
}
