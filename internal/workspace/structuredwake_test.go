package workspace

import (
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/structured"
	"github.com/jmwri/flockdeck/internal/team"
)

// These tests send to structured panes. The fake agent answers every prompt; a
// prompt starting "slow" keeps a turn running until it is cancelled. Mail to a
// structured pane is covered in promptdelivery_test.go.

// prompts counts the prompts the agent has been sent, and the last one's text.
// Mail it was prompted with is shown as a message entry and counts too.
func prompts(ws *Workspace, id string) (int, string) {
	entries, _ := ws.StructuredEntries(id)
	n, last := 0, ""
	for _, e := range entries {
		if e.Kind == "prompt" || e.Kind == "message" && e.Dir == "in" {
			n++
			last = e.Markdown
			if last == "" {
				last = e.Text
			}
		}
	}
	return n, last
}

func waitForPrompts(t *testing.T, ws *Workspace, id string, want int) string {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		n, last := prompts(ws, id)
		if n >= want {
			return last
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d prompts reached the agent, want %d", n, want)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func mailTo(t *testing.T, ws *Workspace, id, text string) team.Message {
	t.Helper()
	m, err := ws.team.inbox.Send(team.Sender{Name: "peer"}, id, text)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// A prompt sent to an idle pane goes to the agent at once, and to a working one
// is queued behind the turn and not sent into it.
func TestSendToAStructuredPaneIsQueuedBehindARunningTurn(t *testing.T) {
	ws, lead, _ := structuredCrew(t)
	id := spawnStructured(t, ws, lead, "plain")
	statusWithin(t, ws, id, session.StatusIdle)
	res, err := ws.SendToPane(id, "plain again", false)
	if err != nil || res.Queued {
		t.Fatalf("to an idle pane: %+v, %v", res, err)
	}
	waitForPrompts(t, ws, id, 2)

	busy := spawnStructured(t, ws, lead, "slow")
	statusWithin(t, ws, busy, session.StatusWorking)
	res, err = ws.SendToPane(busy, "plain queued", false)
	if err != nil || !res.Queued {
		t.Fatalf("to a working pane: %+v, %v", res, err)
	}
	if n, _ := prompts(ws, busy); n != 1 {
		t.Fatalf("the queued prompt reached the agent during the turn: %d prompts", n)
	}
	if sn := ws.StructuredRuntime(busy).Snapshot(); sn.Status != "working" {
		t.Errorf("status after the queued send: %q", sn.Status)
	}
}

// A pane waiting on a permission request is refused text, a message from the
// graph included.
func TestAStructuredPaneWaitingOnAPermissionTakesNoText(t *testing.T) {
	ws, lead, _ := structuredCrew(t)
	id := spawnStructured(t, ws, lead, "hold-perm")
	statusWithin(t, ws, id, session.StatusWaiting)
	if _, err := ws.SendToPane(id, "plain", false); err != ErrSendPermission {
		t.Errorf("text to a waiting pane: %v", err)
	}
	if n, _ := prompts(ws, id); n != 1 {
		t.Errorf("%d prompts reached the agent", n)
	}
}

// stubStructured is a structured runtime that says what the test tells it to, to
// reach states the fake agent cannot be made to show.
type stubStructured struct {
	PaneRuntime
	status session.Status
	detail string
	err    error
	sent   []string
}

func (s *stubStructured) Transport() Transport             { return TransportStructured }
func (s *stubStructured) Status() (session.Status, string) { return s.status, s.detail }
func (s *stubStructured) Alive() bool                      { return true }
func (s *stubStructured) Activity() Activity               { return ActivityIdle }
func (s *stubStructured) Close() error                     { return nil }
func (s *stubStructured) Wake(m string) error              { s.sent = append(s.sent, m); return s.err }

// stubbedPane puts a stub runtime in the manager's pane. The terminal one comes
// back at the end of the test so that the workspace can close it.
func stubbedPane(t *testing.T, ws *Workspace, id string, s *stubStructured) {
	t.Helper()
	ws.SetRuntime(id, s)
	t.Cleanup(func() { ws.SetRuntime(id, nil) })
}

// A structured pane waiting on a request takes no text, whatever the tool is
// called: a terminal's AskUserQuestion takes typed text, and this does not.
func TestAStructuredPaneWaitingOnAnyToolTakesNoText(t *testing.T) {
	ws, lead, _ := structuredCrew(t)
	stub := &stubStructured{status: session.StatusWaiting, detail: "AskUserQuestion"}
	stubbedPane(t, ws, lead, stub)
	if _, err := ws.SendToPane(lead, "an answer", false); err != ErrSendPermission {
		t.Errorf("text to a waiting structured pane: %v", err)
	}
	if len(stub.sent) != 0 {
		t.Errorf("%q reached the agent", stub.sent)
	}
}

// The runtime's refusals are told in the send errors, and a refused send is not
// counted as queued.
func TestSendToAStructuredPaneTellsWhyItWasRefused(t *testing.T) {
	ws, lead, _ := structuredCrew(t)
	stub := &stubStructured{status: session.StatusIdle}
	stubbedPane(t, ws, lead, stub)
	for _, c := range []struct {
		from error
		want error
	}{{structured.ErrQueueFull, ErrSendQueueFull}, {structured.ErrNotRunning, ErrSendExited}} {
		stub.err = c.from
		res, err := ws.SendToPane(lead, "text", false)
		if err != c.want || res.Queued {
			t.Errorf("%v: got %+v, %v; want %v", c.from, res, err, c.want)
		}
	}
	stub.err = nil
	if _, err := ws.SendToPane(lead, "text", false); err != nil {
		t.Errorf("an accepted send: %v", err)
	}
}

