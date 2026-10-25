package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/changes"
)

func writeCheckFile(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "proj-1234abcd.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func shownChecks(t *testing.T, pins *checkPins, path string) changesChecksMsg {
	t.Helper()
	cl := &controlClient{out: make(chan []byte, 8), page: true}
	confirmChecksAt(cl, command{Cmd: "confirmChecks", Req: "s1"}, path, pins)
	select {
	case raw := <-cl.out:
		var m changesChecksMsg
		if err := json.Unmarshal(raw, &m); err != nil || m.Type != "changesChecks" {
			t.Fatalf("answer = %s, %v", raw, err)
		}
		return m
	case <-time.After(5 * time.Second):
		t.Fatal("not answered")
	}
	return changesChecksMsg{}
}

func confirmed(t *testing.T, pins *checkPins, path, hash string) changesResultMsg {
	t.Helper()
	cl := &controlClient{out: make(chan []byte, 8), page: true}
	confirmChecksAt(cl, command{Cmd: "confirmChecks", Req: "c1", Confirm: true, Hash: hash}, path, pins)
	return answerOf(t, cl)
}

func TestConfirmChecksShowsTheCommandsAndPinsTheHashItWasShown(t *testing.T) {
	pins := &checkPins{}
	p := writeCheckFile(t, `{"checks":[{"name":"unit","cmd":"go test ./..."},{"name":"lint","cmd":"go vet ./..."}]}`)
	shown := shownChecks(t, pins, p)
	if len(shown.Commands) != 2 || shown.Commands[0].Cmd != "go test ./..." || shown.Pinned || shown.Req != "s1" || len(shown.Hash) != 64 {
		t.Fatalf("shown = %+v", shown)
	}
	defs, _ := changes.LoadChecks(p)
	if checksConfirmed(pins, p, defs) {
		t.Fatal("commands nobody confirmed count as confirmed")
	}
	if m := confirmed(t, pins, p, shown.Hash); !m.OK {
		t.Fatalf("confirm = %+v", m)
	}
	if !checksConfirmed(pins, p, defs) || !shownChecks(t, pins, p).Pinned {
		t.Error("the confirmed commands are not pinned")
	}
}

func TestAFileEditedAfterItWasShownIsNotConfirmed(t *testing.T) {
	pins := &checkPins{}
	p := writeCheckFile(t, `{"checks":[{"name":"unit","cmd":"go test ./..."}]}`)
	shown := shownChecks(t, pins, p)
	// The file changes between the window reading it and the person confirming.
	if err := os.WriteFile(p, []byte(`{"checks":[{"name":"unit","cmd":"go test ./... && curl evil"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if m := confirmed(t, pins, p, shown.Hash); m.OK || m.Code != "changed" {
		t.Fatalf("confirm of a stale hash = %+v, want changed", m)
	}
	defs, _ := changes.LoadChecks(p)
	if checksConfirmed(pins, p, defs) {
		t.Error("a stale confirm pinned the new commands")
	}
	// And a file edited after a confirm stops being confirmed.
	now := shownChecks(t, pins, p)
	if m := confirmed(t, pins, p, now.Hash); !m.OK {
		t.Fatalf("confirm = %+v", m)
	}
	if err := os.WriteFile(p, []byte(`{"checks":[{"name":"unit","cmd":"rm -rf ~"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	defs, _ = changes.LoadChecks(p)
	if checksConfirmed(pins, p, defs) || shownChecks(t, pins, p).Pinned {
		t.Error("a command changed after the confirm still counts as confirmed")
	}
}

func TestConfirmChecksRefusesWhatItCannotPin(t *testing.T) {
	pins := &checkPins{}
	missing := filepath.Join(t.TempDir(), "none.json")
	cl := &controlClient{out: make(chan []byte, 8), page: true}
	confirmChecksAt(cl, command{Cmd: "confirmChecks", Req: "r"}, missing, pins)
	if m := answerOf(t, cl); m.Code != "notFound" {
		t.Errorf("no file: %+v", m)
	}
	p := writeCheckFile(t, `{"checks":[{"name":"unit","cmd":"go test"}]}`)
	if m := confirmed(t, pins, p, ""); m.Code != "badRequest" {
		t.Errorf("no hash: %+v", m)
	}
	empty := writeCheckFile(t, `{"checks":[]}`)
	if m := confirmed(t, pins, empty, changes.HashChecks(nil)); m.OK || m.Code != "notFound" {
		t.Errorf("an empty file was pinned: %+v", m)
	}
	if pins.get(empty) != "" {
		t.Error("a pin was recorded for an empty file")
	}
	bad := writeCheckFile(t, `{"checks":[{"name":"unit","cmd":"go test
 curl evil"}]}`)
	cl = &controlClient{out: make(chan []byte, 8), page: true}
	confirmChecksAt(cl, command{Cmd: "confirmChecks", Req: "r"}, bad, pins)
	if m := answerOf(t, cl); m.OK || m.Code != "badRequest" || !strings.Contains(m.Error, "U+000A") {
		t.Errorf("a command holding a line break: %+v, want badRequest naming the character", m)
	}
}

func TestAPinIsForThoseCommandsOfThatFileAndIsForgottenOnRestart(t *testing.T) {
	pins := &checkPins{}
	p := writeCheckFile(t, `{"checks":[{"name":"unit","cmd":"go test"}]}`)
	other := writeCheckFile(t, `{"checks":[{"name":"unit","cmd":"go test"}]}`)
	defs, _ := changes.LoadChecks(p)
	pins.set(p, changes.HashChecks([]changes.CheckDef{{Name: "x", Cmd: "y"}}))
	if checksConfirmed(pins, p, defs) {
		t.Error("the hash of other commands confirmed these")
	}
	pins.set(p, changes.HashChecks(defs))
	if !checksConfirmed(pins, p, defs) {
		t.Fatal("the confirmed commands are not confirmed")
	}
	if checksConfirmed(pins, other, defs) {
		t.Error("a confirmation of one project's file confirmed another's")
	}
	if checksConfirmed(&checkPins{}, p, defs) {
		t.Error("a new server remembers a confirmation")
	}
	if entries, _ := os.ReadDir(filepath.Dir(p)); len(entries) != 1 {
		t.Errorf("the confirmation was written beside the check file: %v", entries)
	}
}

func TestTheCommandsOfTheCheckFileAreNeverSentToARelayWindow(t *testing.T) {
	data, err := json.Marshal(changesChecksMsg{Type: "changesChecks", Commands: []changes.CheckDef{{Name: "unit", Cmd: "go test"}}, Hash: "h"})
	if err != nil {
		t.Fatal(err)
	}
	if out := scrubForRemote(data); out != nil {
		t.Errorf("a relay window was sent %s", out)
	}
}
