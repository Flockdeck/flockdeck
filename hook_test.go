package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/hooks"
)

// A pane's settings no longer put the secret on the hook's command line, where
// anybody on the machine could read it for as long as the hook ran: the hook
// takes it from the pane's environment, which Claude Code passes on. A pane an
// earlier build started still gives it as --token, and that still works.
func TestTheHookTakesTheSecretFromThePane(t *testing.T) {
	got := make(chan hooks.Event, 4)
	srv, err := hooks.Serve(func(e hooks.Event) { got <- e })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	t.Setenv("PERCH_TOKEN", "")

	for _, c := range []struct {
		name, env string
		args      []string
	}{
		{"from the environment", srv.Token(), nil},
		{"given as --token, by an earlier build's settings", "", []string{"--token", srv.Token()}},
	} {
		t.Setenv("FLOCKDECK_TOKEN", c.env)
		var errs bytes.Buffer
		args := append([]string{"--endpoint", srv.Endpoint(), "--session", "pane-1", "--event", "Stop"}, c.args...)
		hook(args, strings.NewReader("{}"), &bytes.Buffer{}, &errs)
		select {
		case e := <-got:
			if e.SessionID != "pane-1" || e.Event != "Stop" {
				t.Errorf("%s: got %+v, want the pane's Stop", c.name, e)
			}
		case <-time.After(5 * time.Second):
			t.Errorf("%s: the event did not arrive: %s", c.name, errs.String())
		}
	}
}

// The settings file writes the hook as a program and its arguments or as one
// command line, depending on the Claude Code reading it, and both start this
// subcommand in the environment Claude Code passes on. That is where the pane
// says which start of it is running, and the hook sends it, so an event from
// the process before a restart can be told from the new one's.
func TestTheHookSaysWhichStartOfThePaneItIsFrom(t *testing.T) {
	got := make(chan hooks.Event, 1)
	srv, err := hooks.Serve(func(e hooks.Event) { got <- e })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	t.Setenv("FLOCKDECK_TOKEN", srv.Token())
	t.Setenv(hooks.LaunchEnv, "this-start")

	var errs bytes.Buffer
	hook([]string{"--endpoint", srv.Endpoint(), "--session", "pane-1", "--event", "Stop"}, strings.NewReader("{}"), &bytes.Buffer{}, &errs)
	select {
	case e := <-got:
		if e.Launch != "this-start" {
			t.Errorf("launch = %q, want the start the pane's environment names", e.Launch)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("the event did not arrive: %s", errs.String())
	}
}

// A notice of mail reaches Claude Code the way SessionStart's description does:
// as additionalContext in the hook's output, under the event's own name.
func TestTheHookPrintsAMessageNoticeAsContextForThePromptAndToolEvents(t *testing.T) {
	srv, err := hooks.Serve(func(hooks.Event) {})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	srv.SetNoticeHandler(func(string) string { return "[flockdeck] 1 new message from work (reviewer) (#3)." })
	t.Setenv("FLOCKDECK_TOKEN", srv.Token())

	for _, event := range []string{"UserPromptSubmit", "PostToolUse"} {
		var out, errs bytes.Buffer
		hook([]string{"--endpoint", srv.Endpoint(), "--session", "pane-1", "--event", event}, strings.NewReader("{}"), &out, &errs)
		var got struct {
			HookSpecificOutput struct {
				HookEventName     string `json:"hookEventName"`
				AdditionalContext string `json:"additionalContext"`
			} `json:"hookSpecificOutput"`
		}
		if err := json.Unmarshal(out.Bytes(), &got); err != nil {
			t.Fatalf("%s: output %q: %v (stderr %q)", event, out.String(), err, errs.String())
		}
		if got.HookSpecificOutput.HookEventName != event || !strings.Contains(got.HookSpecificOutput.AdditionalContext, "1 new message") {
			t.Errorf("%s: output = %q", event, out.String())
		}
	}
}
