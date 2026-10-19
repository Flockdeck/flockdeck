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
)

// fakeStructured is a structured pane with one pending card. The first answer
// to the request wins.
type fakeStructured struct {
	mu       sync.Mutex
	card     *bridge.Card
	answered [][2]string
	failWith error
}

func (f *fakeStructured) PendingPermission() *bridge.Card {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.card == nil {
		return nil
	}
	c := *f.card
	return &c
}

func (f *fakeStructured) AnswerPermission(requestID, optionID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != nil {
		return f.failWith
	}
	if f.card == nil || f.card.RequestID != requestID {
		return ErrPermissionStale
	}
	f.answered = append(f.answered, [2]string{requestID, optionID})
	f.card = nil
	return nil
}

func (f *fakeStructured) got() [][2]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][2]string(nil), f.answered...)
}

const secretCommand = "curl -H 'Authorization: Bearer hunter2' https://example.com"

func newCard(id string) *bridge.Card {
	return &bridge.Card{
		RequestID: id, SessionID: "s1", Tool: "Run curl", Kind: "execute", Command: secretCommand,
		Options: []acp.PermissionOption{
			{OptionID: "opt-a", Name: "Allow", Kind: "allow_once"},
			{OptionID: "opt-b", Name: "Always allow", Kind: "allow_always"},
			{OptionID: "opt-c", Name: "Reject", Kind: "reject_once"},
		},
	}
}

// withStructured makes a server whose pane "p1" is the fake one, and captures
// what is logged.
func withStructured(t *testing.T) (*Server, *fakeStructured, *[]string) {
	t.Helper()
	srv, _ := newTestServer(t)
	pane := &fakeStructured{card: newCard("req-1")}
	srv.SetStructuredPanes(func(id string) (StructuredPane, bool) {
		if id == "p1" {
			return pane, true
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
	return srv, pane, &lines
}

func answerCmd(pane, req, opt string) command {
	return command{Cmd: "permissionAnswer", ID: pane, RequestID: req, OptionID: opt, Req: "r1"}
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
	srv, pane, logged := withStructured(t)
	reply, notice := send(t, srv, desk(), answerCmd("p1", "req-1", "opt-a"))
	if !reply.OK || reply.Error != "" || notice != "" || reply.RequestID != "req-1" {
		t.Fatalf("reply = %+v, notice %q", reply, notice)
	}
	if got := pane.got(); len(got) != 1 || got[0] != [2]string{"req-1", "opt-a"} {
		t.Errorf("the pane was answered %v", got)
	}
	// What is logged says which request and what kind of option, and nothing of
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

// Allow always is an option like the others: it is answered with its id, and
// only an id.
func TestAllowAlwaysIsAnsweredByItsIdAndNeverByName(t *testing.T) {
	srv, pane, _ := withStructured(t)
	for _, byName := range []string{"allow_always", "allow_once", "Always allow", "always", "reject_once", "allow"} {
		reply, _ := send(t, srv, desk(), answerCmd("p1", "req-1", byName))
		if reply.OK || reply.Error != permissionUnknownOption {
			t.Errorf("an answer of %q: %+v", byName, reply)
		}
	}
	if len(pane.got()) != 0 {
		t.Fatalf("the pane was answered %v", pane.got())
	}
	reply, _ := send(t, srv, desk(), answerCmd("p1", "req-1", "opt-b"))
	if !reply.OK {
		t.Fatalf("reply = %+v", reply)
	}
	if got := pane.got(); len(got) != 1 || got[0][1] != "opt-b" {
		t.Errorf("answered %v", got)
	}
}

func TestAWindowReachedThroughTheRelayIsRefusedEvenWhenFull(t *testing.T) {
	srv, pane, logged := withStructured(t)
	for _, dev := range []string{"full-dev"} {
		c := &controlClient{out: make(chan []byte, 32), remote: true, device: dev}
		reply, notice := send(t, srv, c, answerCmd("p1", "req-1", "opt-a"))
		if reply.OK || reply.Error != permissionAnswerDeskOnly || notice != permissionAnswerDeskOnly {
			t.Errorf("device %q: reply %+v, notice %q", dev, reply, notice)
		}
	}
	if len(pane.got()) != 0 || len(*logged) != 0 {
		t.Errorf("the pane was answered %v, logged %q", pane.got(), *logged)
	}
	if pane.PendingPermission() == nil {
		t.Error("the request is no longer pending")
	}
}

func TestPermissionAnswerRefusals(t *testing.T) {
	cases := []struct {
		name string
		cmd  command
		want string
	}{
		{"a wrong option id", answerCmd("p1", "req-1", "opt-z"), permissionUnknownOption},
		{"a stale request id", answerCmd("p1", "req-0", "opt-a"), permissionStale},
		{"a pane that is not structured", answerCmd("terminal-pane", "req-1", "opt-a"), permissionNotStructured},
		{"no pane", answerCmd("", "req-1", "opt-a"), permissionBadRequest},
		{"no request id", answerCmd("p1", "", "opt-a"), permissionBadRequest},
		{"no option id", answerCmd("p1", "req-1", ""), permissionBadRequest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv, pane, logged := withStructured(t)
			reply, notice := send(t, srv, desk(), c.cmd)
			if reply.OK || reply.Error != c.want || notice != c.want {
				t.Errorf("reply %+v, notice %q, want %q", reply, notice, c.want)
			}
			if len(pane.got()) != 0 || len(*logged) != 0 {
				t.Errorf("answered %v, logged %q", pane.got(), *logged)
			}
		})
	}
}

func TestAPaneWithNothingPendingIsRefused(t *testing.T) {
	srv, pane, _ := withStructured(t)
	pane.mu.Lock()
	pane.card = nil
	pane.mu.Unlock()
	reply, _ := send(t, srv, desk(), answerCmd("p1", "req-1", "opt-a"))
	if reply.OK || reply.Error != permissionNothingAsked {
		t.Errorf("reply = %+v", reply)
	}
}

func TestBeforeTheWorkspaceSetsItNoPaneIsStructured(t *testing.T) {
	srv, _ := newTestServer(t)
	reply, _ := send(t, srv, desk(), answerCmd("p1", "req-1", "opt-a"))
	if reply.OK || reply.Error != permissionNotStructured {
		t.Errorf("reply = %+v", reply)
	}
}

// Two answers to one request: the first is taken, the second is refused.
func TestTheSecondAnswerToARequestIsRefused(t *testing.T) {
	srv, pane, logged := withStructured(t)
	first, _ := send(t, srv, desk(), answerCmd("p1", "req-1", "opt-a"))
	second, notice := send(t, srv, desk(), answerCmd("p1", "req-1", "opt-c"))
	if !first.OK {
		t.Fatalf("first = %+v", first)
	}
	if second.OK || second.Error == "" || notice == "" {
		t.Errorf("second = %+v, notice %q", second, notice)
	}
	if got := pane.got(); len(got) != 1 || got[0][1] != "opt-a" {
		t.Errorf("the pane was answered %v", got)
	}
	if len(*logged) != 1 {
		t.Errorf("logged %q", *logged)
	}
}

// The request can end between the check and the answer; the pane says so and
// the window is told it is stale.
func TestARequestThatEndsBetweenTheCheckAndTheAnswerIsStale(t *testing.T) {
	srv, pane, logged := withStructured(t)
	pane.failWith = fmt.Errorf("answer: %w", ErrPermissionStale)
	reply, _ := send(t, srv, desk(), answerCmd("p1", "req-1", "opt-a"))
	if reply.OK || reply.Error != permissionStale || len(*logged) != 0 {
		t.Errorf("reply = %+v, logged %q", reply, *logged)
	}
	pane.failWith = errors.New(strings.Repeat("x", 500))
	reply, _ = send(t, srv, desk(), answerCmd("p1", "req-1", "opt-a"))
	if reply.OK || len(reply.Error) > 300 || len(*logged) != 0 {
		t.Errorf("reply = %+v", reply)
	}
}
