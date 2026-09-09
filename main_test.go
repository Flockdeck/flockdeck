package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/jmwri/perch/internal/hooks"
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
	t.Setenv("PERCH_API", "")
	t.Setenv("PERCH_TOKEN", "")
	// paneEnv still falls back to the names an earlier build used, so both
	// spellings have to go. Running this test from inside a pane started by
	// such a build would otherwise hand it a working address, and the test
	// would spawn a real agent instead of failing to find one.
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
	t.Setenv("PERCH_API", "http://127.0.0.1:1")
	t.Setenv("PERCH_TOKEN", "secret")

	err := runSpawn([]string{"   "})
	if err == nil || !strings.Contains(err.Error(), "task") {
		t.Errorf("err = %v, want it to name the missing task", err)
	}
}

// An agent writing a shell command puts the flags where it likes, and the task
// is the one argument that is never a flag — so a flag after it has to still be
// a flag. Before this, `spawn "watch the build" --split` parsed no flags at all
// and folded "--split" into the task.
func TestSpawnFlagsAfterTheTask(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want []string
	}{
		{"flags first", []string{"-split", "do a thing"}, []string{"-split", "--", "do a thing"}},
		{"flag after the task", []string{"do a thing", "--split"}, []string{"--split", "--", "do a thing"}},
		{"value flag after the task", []string{"do a thing", "--worktree", "fix-auth"},
			[]string{"--worktree", "fix-auth", "--", "do a thing"}},
		{"value flag written with =", []string{"do a thing", "--worktree=fix-auth"},
			[]string{"--worktree=fix-auth", "--", "do a thing"}},
		{"flags on both sides", []string{"-shell", "tail the log", "--split"},
			[]string{"-shell", "--split", "--", "tail the log"}},
		// An unknown flag still reaches the flag set, so -h works and a typo is
		// reported rather than silently appended to the task.
		{"an unknown flag is left for the flag set", []string{"-h"}, []string{"-h", "--"}},
		// Everything after a bare -- is task text, dashes and all.
		{"-- ends the flags", []string{"--", "-not-a-flag", "--split"},
			[]string{"--", "-not-a-flag", "--split"}},
	}
	for _, c := range cases {
		got := orderSpawnArgs(c.args)
		if strings.Join(got, "\x00") != strings.Join(c.want, "\x00") {
			t.Errorf("%s: orderSpawnArgs(%q) = %q, want %q", c.name, c.args, got, c.want)
		}
	}
}

// The whole point of the reordering is what the parsed request ends up saying:
// the flag takes effect and the task is only what the agent actually wrote.
func TestParseSpawnReadsFlagsAfterTheTask(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want hooks.SpawnRequest
	}{
		{"split after the task", []string{"watch the build", "--split"},
			hooks.SpawnRequest{Task: "watch the build", Split: true}},
		{"worktree after the task", []string{"repair the token refresh", "--worktree", "fix-auth"},
			hooks.SpawnRequest{Task: "repair the token refresh", Branch: "fix-auth"}},
		{"an unquoted task with a flag on the end",
			[]string{"tail", "the", "build", "log", "-shell", "-split"},
			hooks.SpawnRequest{Task: "tail the build log", Shell: true, Split: true}},
		{"flags first still work", []string{"--split", "watch the build"},
			hooks.SpawnRequest{Task: "watch the build", Split: true}},
		// After a bare "--" a dash is part of the task, which is how a task
		// that genuinely starts with one is written.
		{"-- protects the task", []string{"--", "-shell is the flag I mean to describe"},
			hooks.SpawnRequest{Task: "-shell is the flag I mean to describe"}},
	}
	for _, c := range cases {
		got, err := parseSpawn(c.args)
		if err != nil {
			t.Errorf("%s: parseSpawn(%q) = %v", c.name, c.args, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: parseSpawn(%q) = %+v, want %+v", c.name, c.args, got, c.want)
		}
	}
}
