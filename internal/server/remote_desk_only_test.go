package server

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/creds"
)

// TestRemoteAccessIsTurnedOffOrMovedOnlyAtTheDesk covers the Remote access
// dialog on a phone. Turning remote access off from there cuts the way in the
// phone came by, with nothing left at the far end to turn it on again; turning
// it on from a window that is already in means against another relay, moving
// everything typed at the desk and everything the agents print to a relay
// chosen from somewhere else. Both are the desk's to do, as Quit is.
func TestRemoteAccessIsTurnedOffOrMovedOnlyAtTheDesk(t *testing.T) {
	srv, _ := newTestServer(t)
	fake := &fakeRemote{}
	srv.SetRemote(fake)
	ts := remoteServer(t, srv)
	phone, err := dialRemoteControl(ts, ts.URL)
	if err != nil {
		t.Fatalf("dial through the tunnel: %v", err)
	}
	defer phone.CloseNow()

	type outcome struct{ Type, Action, Error string }
	for _, cmd := range []command{
		{Cmd: "remoteDisable"},
		{Cmd: "remoteDisable", Force: true},
		{Cmd: "remoteEnable", Relay: "https://elsewhere.example"},
	} {
		sendCmd(t, phone, cmd)
		// The dialog's button waits for an outcome, so a refusal is one.
		var out outcome
		readUntil(t, phone, "remoteOutcome", &out)
		if out.Error == "" {
			t.Errorf("%s (force %v) through the relay was answered %+v, want it refused", cmd.Cmd, cmd.Force, out)
		}
		var note noticeMsg
		readUntil(t, phone, "notice", &note)
		if !note.Error || !strings.Contains(note.Text, "machine") {
			t.Errorf("%s through the relay was told %+v, want to do it on the machine itself", cmd.Cmd, note)
		}
	}
	if len(fake.disabled) != 0 || len(fake.enabled) != 0 {
		t.Errorf("a window through the relay reached remote access: disable %v, enable %+v", fake.disabled, fake.enabled)
	}

	// The desk still can.
	desk := dialControl(t, srv)
	sendCmd(t, desk, command{Cmd: "remoteDisable"})
	var out outcome
	readUntil(t, desk, "remoteOutcome", &out)
	if out.Error != "" || len(fake.disabled) != 1 {
		t.Errorf("the desk turning remote access off was answered %+v, with disable %v", out, fake.disabled)
	}
}

// TestAKeyIsSetOnlyAtTheDesk covers the API keys dialog on a phone. The relay
// decrypts what passes through it, so a key pasted there would be read on its
// way; keys are set, and cleared, on the machine that uses them.
func TestAKeyIsSetOnlyAtTheDesk(t *testing.T) {
	srv, _ := newTestServer(t)
	if err := creds.Set("anthropic", "sk-set-at-the-desk"); err != nil {
		t.Fatal(err)
	}
	ts := remoteServer(t, srv)
	phone, err := dialRemoteControl(ts, ts.URL)
	if err != nil {
		t.Fatalf("dial through the tunnel: %v", err)
	}
	defer phone.CloseNow()

	for _, cmd := range []command{
		{Cmd: "keySet", ID: "openai", Text: "sk-pasted-on-a-phone"},
		{Cmd: "keyClear", ID: "anthropic"},
	} {
		sendCmd(t, phone, cmd)
		var note noticeMsg
		readUntil(t, phone, "notice", &note)
		if !note.Error || !strings.Contains(note.Text, "machine") {
			t.Errorf("%s through the relay was answered %+v, want it refused", cmd.Cmd, note)
		}
	}
	names, err := creds.Names()
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(names, "openai") {
		t.Error("a key sent through the relay was stored")
	}
	if !slices.Contains(names, "anthropic") {
		t.Error("a window through the relay cleared a key stored at the desk")
	}
}

// TestAnAgentsAddressIsChangedOnlyAtTheDesk covers the address row of the
// agent picker on a phone. An API agent's stored key goes wherever its address
// says, so an address changed from there would send the desk's key to an
// endpoint chosen from somewhere else.
func TestAnAgentsAddressIsChangedOnlyAtTheDesk(t *testing.T) {
	srv, _ := newTestServer(t)
	ts := remoteServer(t, srv)
	phone, err := dialRemoteControl(ts, ts.URL)
	if err != nil {
		t.Fatalf("dial through the tunnel: %v", err)
	}
	defer phone.CloseNow()

	sendCmd(t, phone, command{Cmd: "setAgentAddress", ID: agent.OpenAICompatibleID, Text: "http://elsewhere.example/v1"})
	// The picker's Save waits for an answer, so a refusal is one.
	var answer agentAddressMsg
	readUntil(t, phone, "agentAddress", &answer)
	if !strings.Contains(answer.Error, "machine") {
		t.Errorf("an address sent through the relay was answered %+v, want it refused", answer)
	}
	var note noticeMsg
	readUntil(t, phone, "notice", &note)
	if !note.Error || !strings.Contains(note.Text, "machine") {
		t.Errorf("an address sent through the relay was told %+v, want to change it on the machine itself", note)
	}
	time.Sleep(300 * time.Millisecond) // long enough for a save to have landed
	if spec, _ := agent.Load().Find(agent.OpenAICompatibleID); spec.API.BaseURL != "" {
		t.Errorf("an address sent through the relay was written to agents.json: %q", spec.API.BaseURL)
	}
}
