package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/jmwri/flockdeck/internal/baton"
)

// A window reached through the relay can make, save and start batons, but its
// "confirmed" is only a field it fills in: a start that goes to another company
// does not run on it. With no window on this machine to ask, it is refused.
func TestARelayWindowsTickDoesNotSendABatonToAnotherCompany(t *testing.T) {
	srv, ws := newTestServer(t)
	batonAgent(t, ws)
	ts := remoteServer(t, srv)
	phone, err := dialRemoteControl(ts, ts.URL)
	if err != nil {
		t.Fatalf("dial through the tunnel: %v", err)
	}
	defer phone.CloseNow()
	pane := spawnGoPane(t, srv, ws)
	sendCmd(t, phone, command{Cmd: "makeBaton", ID: pane})
	var draft batonDraftMsg
	readUntil(t, phone, "batonDraft", &draft)

	sendCmd(t, phone, command{Cmd: "startFromBaton", ID: pane, Text: draft.Text, Agent: "gitcli", Confirmed: true})
	var e batonErrorMsg
	readUntil(t, phone, "batonError", &e)
	if !strings.Contains(e.Error, "no Flockdeck window") {
		t.Errorf("batonError = %+v, want the refusal that says no window on this machine can be asked", e)
	}
	if list := storedBatons(t); len(list) != 0 {
		t.Errorf("%d batons were kept for a refused start", len(list))
	}
}

// With a window on this machine open, the same start waits for its approval
// notice, the one a spawn with -baton-send-elsewhere waits for, and goes ahead
// only when that window allows it.
func TestARelayWindowsStartToAnotherCompanyWaitsForTheDesktopNotice(t *testing.T) {
	srv, ws := newTestServer(t)
	batonAgent(t, ws)
	window := dialWindow(t, srv)
	ts := remoteServer(t, srv)
	phone, err := dialRemoteControl(ts, ts.URL)
	if err != nil {
		t.Fatalf("dial through the tunnel: %v", err)
	}
	defer phone.CloseNow()
	pane := spawnGoPane(t, srv, ws)
	shortApprovalWait(t, 20*time.Second)
	sendCmd(t, phone, command{Cmd: "makeBaton", ID: pane})
	var draft batonDraftMsg
	readUntil(t, phone, "batonDraft", &draft)

	sendCmd(t, phone, command{Cmd: "startFromBaton", ID: pane, Text: draft.Text, Agent: "gitcli", Confirmed: true})
	n := nextApproval(t, window)
	if n.Action.Send["cmd"] != "approveBaton" || !strings.Contains(n.Text, "Git as an agent") || !strings.Contains(n.Text, "relay") {
		t.Errorf("the notice does not say who asked and where it goes: %+v", n)
	}
	time.Sleep(300 * time.Millisecond)
	if list := storedBatons(t); len(list) != 0 {
		t.Fatalf("%d batons were kept before the window allowed it", len(list))
	}
	// The phone cannot allow it itself, with the real token.
	sendCmd(t, phone, command{Cmd: "approveBaton", Text: n.Action.Send["text"]})
	time.Sleep(300 * time.Millisecond)
	if list := storedBatons(t); len(list) != 0 {
		t.Fatalf("%d batons were kept after the phone tried to allow it", len(list))
	}

	sendCmd(t, window, command{Cmd: "approveBaton", Text: n.Action.Send["text"]})
	// Starting the second agent may fail in a test for reasons of its own; the answer
	// is not a refusal for the company.
	var msg struct {
		Type  string `json:"type"`
		Error string `json:"error"`
	}
	readOneOf(t, phone, &msg, "batonSaved", "batonError")
	if msg.Type == "batonError" && strings.Contains(msg.Error, "different company") {
		t.Fatalf("still refused for the company after it was allowed: %s", msg.Error)
	}
}

func storedBatons(t *testing.T) []baton.Baton {
	t.Helper()
	st, err := baton.Open()
	if err != nil {
		t.Fatal(err)
	}
	list, _ := st.List()
	return list
}

// readOneOf reads control messages until one of the given types arrives, and
// decodes it into out.
func readOneOf(t *testing.T, conn *websocket.Conn, out any, types ...string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		_, data, err := conn.Read(ctx)
		cancel()
		if err != nil {
			t.Fatalf("read control: %v", err)
		}
		var probe struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(data, &probe) != nil {
			continue
		}
		for _, want := range types {
			if probe.Type == want {
				if err := json.Unmarshal(data, out); err != nil {
					t.Fatalf("decode %s: %v", want, err)
				}
				return
			}
		}
	}
	t.Fatalf("timed out waiting for one of %v", types)
}
