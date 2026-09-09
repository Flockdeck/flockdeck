package main

import (
	"bytes"
	"errors"
	"flag"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/perch/internal/help"
	"github.com/jmwri/perch/internal/hooks"
	"github.com/jmwri/perch/internal/store"
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
	fs := spawnFlagSet(&spawnFlags{})
	for _, c := range cases {
		got := orderSpawnArgs(fs, c.args)
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

// One Ctrl+C asks for an orderly stop. A second, arriving while that is still
// going, has to be acted on: signal.Notify has taken the key away from the
// runtime, so nothing else will.
func TestInterruptsForceQuitOnTheSecond(t *testing.T) {
	sigs := make(chan os.Signal, 2)
	stopped := make(chan struct{})
	forced := make(chan struct{})
	go interrupts(sigs, func() { close(stopped) }, func() { close(forced) })

	sigs <- os.Interrupt
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("the first interrupt did not ask for a stop")
	}
	select {
	case <-forced:
		t.Fatal("the first interrupt forced a quit")
	case <-time.After(50 * time.Millisecond):
	}

	sigs <- os.Interrupt
	select {
	case <-forced:
	case <-time.After(time.Second):
		t.Fatal("the second interrupt was ignored")
	}
}

// The reordering has to know which flags are followed by a value, and reading
// that from the flag set is what keeps a flag added later from having its
// value swallowed into the task.
func TestOrderSpawnArgsLearnsArityFromTheFlagSet(t *testing.T) {
	fs := flag.NewFlagSet("spawn", flag.ContinueOnError)
	fs.String("base", "", "branch to start from")
	fs.Bool("split", false, "beside this pane")

	got := orderSpawnArgs(fs, []string{"repair the token refresh", "-base", "main", "-split"})
	want := []string{"-base", "main", "-split", "--", "repair the token refresh"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("got %q, want %q", got, want)
	}
	// A boolean takes no value, so the word after it stays part of the task.
	got = orderSpawnArgs(fs, []string{"-split", "watch", "the", "build"})
	want = []string{"-split", "--", "watch", "the", "build"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("got %q, want %q", got, want)
	}
}

// flagLike finds the words in prose that read as a command-line flag.
var flagLike = regexp.MustCompile(`(?:^|[\s(\[` + "`" + `])(--?[A-Za-z][A-Za-z0-9-]*)`)

// cliFlagNames is every flag the program actually accepts, from both of its
// flag sets, without their dashes.
func cliFlagNames() map[string]bool {
	names := map[string]bool{}
	perchFlagSet(&cliFlags{}).VisitAll(func(f *flag.Flag) { names[f.Name] = true })
	spawnFlagSet(&spawnFlags{}).VisitAll(func(f *flag.Flag) { names[f.Name] = true })
	return names
}

// The command-line help page is written by hand, so it is the copy of the
// command line that goes stale: a flag added and never documented, or one
// removed and still listed. Both directions are checked against the flag sets
// the program parses with.
func TestHelpPageMatchesTheCommandLine(t *testing.T) {
	pages, err := help.Pages()
	if err != nil {
		t.Fatalf("help.Pages: %v", err)
	}
	var text string
	for _, p := range pages {
		if p.Slug == "cli" {
			text = p.Text
		}
	}
	if text == "" {
		t.Fatal("no command-line help page")
	}

	documented := map[string]bool{}
	for _, m := range flagLike.FindAllStringSubmatch(text, -1) {
		documented[strings.TrimLeft(m[1], "-")] = true
	}
	for name := range cliFlagNames() {
		if !documented[name] {
			t.Errorf("-%s is a flag the program accepts but the help page does not mention", name)
		}
	}
	real := cliFlagNames()
	for name := range documented {
		if !real[name] {
			t.Errorf("the help page documents -%s, which is not a flag the program accepts", name)
		}
	}
}

// The usage printed for a wrong command line is the other hand-written copy.
func TestUsageNamesEveryFlag(t *testing.T) {
	var buf bytes.Buffer
	fs := perchFlagSet(&cliFlags{})
	fs.SetOutput(&buf)
	usage(fs)
	for name := range cliFlagNames() {
		if !strings.Contains(buf.String(), "-"+name) {
			t.Errorf("-%s is missing from the usage message", name)
		}
	}
}

// Saving walks the tabs and the pane tree; the server's own goroutine is still
// changing them while any window is connected. Stopping the server has to come
// first, or the one piece of state the user would notice losing is read while
// it is being written.
func TestShutdownStopsServingBeforeSaving(t *testing.T) {
	var order []string
	err := shutdown(
		func() error { order = append(order, "stop"); return nil },
		func() error { order = append(order, "save"); return nil },
	)
	if err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if strings.Join(order, ",") != "stop,save" {
		t.Errorf("order = %v, want the server stopped before the save", order)
	}
}

// The save is what the caller is told about: a window that will not close is
// not worth a message, and a layout that was not written is.
func TestShutdownReportsTheSave(t *testing.T) {
	boom := errors.New("disk full")
	if err := shutdown(func() error { return errors.New("still serving") }, func() error { return boom }); !errors.Is(err, boom) {
		t.Errorf("err = %v, want the save's error", err)
	}
	if err := shutdown(func() error { return errors.New("still serving") }, func() error { return nil }); err != nil {
		t.Errorf("err = %v, want nil when the layout was saved", err)
	}
}

// `-quit` has to wait for the instance to actually go, not just to take the
// request: until it has, its address still answers, and the next `perch`
// attaches to an instance in the middle of shutting down.
func TestWaitGoneWaitsForTheInstance(t *testing.T) {
	calls := 0
	start := time.Now()
	if !waitGone(func() bool { calls++; return calls >= 3 }, time.Second) {
		t.Fatal("gave up on an instance that did stop")
	}
	if calls != 3 {
		t.Errorf("asked %d times, want 3", calls)
	}
	if elapsed := time.Since(start); elapsed < quitPoll {
		t.Errorf("returned after %s without waiting between tries", elapsed)
	}
}

// An instance that never goes must not be reported as stopped.
func TestWaitGoneGivesUp(t *testing.T) {
	start := time.Now()
	if waitGone(func() bool { return false }, 250*time.Millisecond) {
		t.Error("reported an instance gone that never went")
	}
	if elapsed := time.Since(start); elapsed < 250*time.Millisecond {
		t.Errorf("gave up after %s, before the deadline", elapsed)
	}
}

// The common case is an instance that has already gone by the time it is
// asked, and that must not cost a poll interval.
func TestWaitGoneReturnsAtOnce(t *testing.T) {
	start := time.Now()
	if !waitGone(func() bool { return true }, time.Second) {
		t.Fatal("did not see an instance that was already gone")
	}
	if elapsed := time.Since(start); elapsed >= quitPoll {
		t.Errorf("took %s for an instance that had already gone", elapsed)
	}
}

// A record of the running instance that cannot be read is not the same as
// there being nothing running. Carrying on regardless starts a second set of
// agents while the first keeps going with no window and no record to find it
// by, so the guess has to be said out loud.
func TestJoinRunningSaysWhenItCannotTell(t *testing.T) {
	var warnings []string
	inst, base := joinRunning(
		func() (*store.Instance, string, error) { return nil, "", errors.New("unreadable") },
		func(text string) { warnings = append(warnings, text) },
	)
	if inst != nil || base != "" {
		t.Errorf("joined %v at %q on an unreadable record", inst, base)
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %v, want one", warnings)
	}
	if !strings.Contains(warnings[0], "unreadable") {
		t.Errorf("warning = %q, want the reason in it", warnings[0])
	}
}

// The two ordinary answers are silent: one to join, or nothing running.
func TestJoinRunningIsQuietWhenItCanTell(t *testing.T) {
	running := &store.Instance{URL: "http://127.0.0.1:1"}
	inst, base := joinRunning(
		func() (*store.Instance, string, error) { return running, running.URL, nil },
		func(text string) { t.Errorf("unexpected warning: %s", text) },
	)
	if inst != running || base != running.URL {
		t.Errorf("joined %v at %q, want the running instance", inst, base)
	}

	inst, base = joinRunning(
		func() (*store.Instance, string, error) { return nil, "", nil },
		func(text string) { t.Errorf("unexpected warning: %s", text) },
	)
	if inst != nil || base != "" {
		t.Errorf("joined %v at %q when nothing was running", inst, base)
	}
}
