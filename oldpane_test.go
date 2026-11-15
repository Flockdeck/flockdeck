package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/hooks"
)

// oldPane makes the environment of a pane an earlier build started: the PERCH_
// names and none of the FLOCKDECK_ ones.
func oldPane(t *testing.T, token string) {
	t.Helper()
	for _, n := range []string{"API", "TOKEN", "PANE"} {
		t.Setenv("FLOCKDECK_"+n, "")
	}
	t.Setenv("PERCH_API", "http://127.0.0.1:1")
	t.Setenv("PERCH_TOKEN", token)
	t.Setenv("PERCH_PANE", "pane-1")
}

func TestAnOldPaneIsToldToRestart(t *testing.T) {
	oldPane(t, "tok")
	if _, _, _, err := paneCaller(); !errors.Is(err, errOldPane) {
		t.Errorf("paneCaller err = %v, want the restart message", err)
	}
	if err := runSpawn([]string{"tidy", "the", "imports"}); !errors.Is(err, errOldPane) {
		t.Errorf("runSpawn err = %v, want the restart message", err)
	}
	if err := runPeerName([]string{"flockdeck-8d"}); !errors.Is(err, errOldPane) {
		t.Errorf("runPeerName err = %v, want the restart message", err)
	}
	if err := runClose([]string{"pane-2"}); !errors.Is(err, errOldPane) {
		t.Errorf("runClose err = %v, want the restart message", err)
	}
	if !strings.Contains(errOldPane.Error(), "restart the pane") {
		t.Errorf("message = %q", errOldPane)
	}
}

// The old names say a pane is old and nothing else: a pane with the new names
// set is not told to restart.
func TestANewPaneIsNotToldToRestart(t *testing.T) {
	oldPane(t, "tok")
	t.Setenv("FLOCKDECK_PANE", "pane-1")
	if _, _, _, err := paneCaller(); !errors.Is(err, errNotInPane) || errors.Is(err, errOldPane) {
		t.Errorf("paneCaller err = %v, want the ordinary not-in-a-pane error", err)
	}
}

func TestAnOldPaneStillRefusesWakeAndKnowledgeChanges(t *testing.T) {
	oldPane(t, "tok")
	for _, args := range [][]string{{"on"}, {"off"}, {"text", "on"}} {
		err := runWake(args, &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "inside a pane") {
			t.Errorf("wake %v: err = %v, want a refusal from inside a pane", args, err)
		}
	}
	for _, action := range []string{"on", "off"} {
		err := runKnowledge([]string{action}, &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "inside a pane") {
			t.Errorf("knowledge %s: err = %v, want a refusal from inside a pane", action, err)
		}
	}
}

// The old token is never used as the identity: the hook and the status line
// send nothing with it.
func TestTheHookAndStatuslineIgnoreTheOldToken(t *testing.T) {
	srv, got := usageServer(t)
	oldPane(t, srv.Token())

	events := make(chan hooks.Event, 2)
	hsrv, err := hooks.Serve(func(e hooks.Event) { events <- e })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = hsrv.Close() })
	t.Setenv("PERCH_TOKEN", hsrv.Token())
	hook([]string{"--endpoint", hsrv.Endpoint(), "--session", "pane-1", "--event", "Stop"}, strings.NewReader("{}"), &bytes.Buffer{}, &bytes.Buffer{})
	select {
	case e := <-events:
		t.Errorf("the hook delivered %+v with only the old token", e)
	case <-time.After(500 * time.Millisecond):
	}

	t.Setenv("PERCH_TOKEN", srv.Token())
	statusline([]string{"--endpoint", srv.UsageEndpoint(), "--session", "pane-1"}, strings.NewReader(statusJSON), &bytes.Buffer{}, &bytes.Buffer{})
	select {
	case <-got:
		t.Error("the status line reported with only the old token")
	case <-time.After(500 * time.Millisecond):
	}
}
