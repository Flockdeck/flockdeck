package main

import (
	"errors"
	"strings"
	"testing"
)

// The flags that describe a fresh start are dropped when a launch turns into
// attaching to a running instance, so the user has to be told which ones.
func TestStartupOnlyFlags(t *testing.T) {
	cases := []struct {
		name string
		opts options
		want []string
	}{
		{"nothing to report", options{}, nil},
		{"one", options{fresh: true}, []string{"-new"}},
		{"all three", options{fresh: true, shell: true, detach: true},
			[]string{"-new", "-shell", "-detach"}},
		// -C and -no-window still mean something when attaching, so they are
		// not in the list.
		{"flags that still apply", options{dir: "/elsewhere", noWindow: true}, nil},
	}
	for _, c := range cases {
		got := startupOnlyFlags(c.opts)
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

// `spawn -h` is a request for the usage it has just been given, not a failure.
func TestSpawnHelpSucceeds(t *testing.T) {
	if err := runSpawn([]string{"-h"}); err != nil {
		t.Errorf("runSpawn(-h) = %v, want nil", err)
	}
}

// A flag the set has already complained about must not be reported twice.
func TestSpawnBadFlagIsReportedOnce(t *testing.T) {
	err := runSpawn([]string{"-nosuchflag"})
	if !errors.Is(err, errReported) {
		t.Errorf("runSpawn(-nosuchflag) = %v, want errReported", err)
	}
}

// Outside a pane there is no address to spawn through, and the message has to
// say that rather than blaming the task.
func TestSpawnOutsideAPaneExplainsItself(t *testing.T) {
	t.Setenv("AGENT_WRAPPER_API", "")
	t.Setenv("AGENT_WRAPPER_TOKEN", "")

	err := runSpawn([]string{"tidy", "the", "imports"})
	if err == nil || !strings.Contains(err.Error(), "pane") {
		t.Errorf("err = %v, want it to mention panes", err)
	}
}

// An agent pane is where spawn is meant to run, so from there the missing
// piece is the task, and nothing is sent without one.
func TestSpawnRequiresATask(t *testing.T) {
	t.Setenv("AGENT_WRAPPER_API", "http://127.0.0.1:1")
	t.Setenv("AGENT_WRAPPER_TOKEN", "secret")

	err := runSpawn([]string{"   "})
	if err == nil || !strings.Contains(err.Error(), "task") {
		t.Errorf("err = %v, want it to name the missing task", err)
	}
}
