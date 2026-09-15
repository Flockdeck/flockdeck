package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/help"
	"github.com/jmwri/flockdeck/internal/hooks"
	"github.com/jmwri/flockdeck/internal/store"
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
		{"the agent for the run", options{agent: "codex"}, []string{"-agent"}},
		{"all of them", options{fresh: true, shell: true, agent: "codex", detach: true},
			[]string{"-new", "-shell", "-agent"}},
		// -C, -no-window and -detach still mean something when attaching —
		// the last two that no window is opened — so they are not in the list.
		{"flags that still apply", options{dir: "/elsewhere", noWindow: true, detach: true}, nil},
	}
	for _, c := range cases {
		got := startupOnlyFlags(c.opts)
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

// Started from its own folder, as a double-click starts it, the program goes
// back to the project the user was last in rather than opening the folder it
// was downloaded to as a project of its own. From anywhere else the directory
// it was started in is the one meant.
func TestLandingRoot(t *testing.T) {
	install, project, elsewhere := t.TempDir(), t.TempDir(), t.TempDir()
	exe := filepath.Join(install, "flockdeck.exe")
	saved := &store.Session{Open: []string{project}, Active: project}
	gone := &store.Session{Active: filepath.Join(project, "deleted")}

	cases := []struct {
		name  string
		cwd   string
		saved *store.Session
		want  string
	}{
		{"double-clicked, with a project to go back to", install, saved, project},
		{"started from a terminal somewhere else", elsewhere, saved, elsewhere},
		{"double-clicked, nothing saved yet", install, nil, install},
		{"double-clicked, the last project has gone", install, gone, install},
	}
	for _, c := range cases {
		if got := landingRoot(c.cwd, exe, c.saved); got != c.want {
			t.Errorf("%s: landingRoot = %s, want %s", c.name, got, c.want)
		}
	}
}

// A scheduled or login start is run from a system directory — System32 under
// Task Scheduler with no "Start in", / under launchd — which is nowhere anybody
// works: it goes back to the last project, or failing that home.
func TestLandingRootLeavesSystemDirectories(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	sys := "/"
	if runtime.GOOS == "windows" {
		root := t.TempDir()
		t.Setenv("SystemRoot", root)
		sys = filepath.Join(root, "System32")
	}
	exe := filepath.Join(t.TempDir(), "flockdeck.exe")
	saved := &store.Session{Open: []string{project}, Active: project}

	if got := landingRoot(sys, exe, saved); got != project {
		t.Errorf("started in %s with a project to go back to: landingRoot = %s, want %s", sys, got, project)
	}
	if got := landingRoot(sys, exe, nil); got != home {
		t.Errorf("started in %s with nothing saved: landingRoot = %s, want the home directory %s", sys, got, home)
	}
}

// PowerShell and cmd.exe pass a leading ~ through as typed, so -C has to read
// it as the home directory itself, or the README's own example fails there.
func TestExpandHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cases := []struct{ in, want string }{
		{"~/code/api", filepath.Join(home, "code", "api")},
		{"~", home},
		{"~someone/code", "~someone/code"},
		{"code/~/api", "code/~/api"},
		{"", ""},
	}
	for _, c := range cases {
		if got := expandHome(c.in); got != c.want {
			t.Errorf("expandHome(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// `flockdeck help update` is asking for update's usage, the way `git help
// commit` is; `help` alone, or followed by something that is not a
// subcommand, gets the top-level usage.
func TestHelpArgs(t *testing.T) {
	cases := []struct {
		rest    []string
		want    []string
		wantTop bool
	}{
		{nil, nil, true},
		{[]string{"update"}, []string{"update", "-h"}, false},
		{[]string{"remote", "pair"}, []string{"remote", "-h"}, false},
		{[]string{"spawn"}, []string{"spawn", "-h"}, false},
		{[]string{"hook"}, nil, true}, // hidden, so not something the usage offers
		{[]string{"statusline"}, nil, true},
		{[]string{"nonsense"}, nil, true},
	}
	for _, c := range cases {
		got, top := helpArgs(c.rest)
		if top != c.wantTop || strings.Join(got, " ") != strings.Join(c.want, " ") {
			t.Errorf("helpArgs(%q) = %q, %v; want %q, %v", c.rest, got, top, c.want, c.wantTop)
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
	t.Setenv("FLOCKDECK_API", "")
	t.Setenv("FLOCKDECK_TOKEN", "")
	// paneEnv still falls back to the names an earlier build used, so both
	// spellings have to go. Running this test from inside a pane started by
	// such a build would otherwise hand it a working address, and the test
	// would spawn a real agent instead of failing to find one.
	t.Setenv("PERCH_API", "")
	t.Setenv("PERCH_TOKEN", "")

	err := runSpawn([]string{"tidy", "the", "imports"})
	if err == nil || !strings.Contains(err.Error(), "pane") {
		t.Errorf("err = %v, want it to mention panes", err)
	}
}

// A spawn the instance did not answer in time is explained as the instance
// being busy; anything else is passed on as it was.
func TestExplainSpawn(t *testing.T) {
	late := &url.Error{Op: "Post", URL: "http://127.0.0.1:1/spawn", Err: context.DeadlineExceeded}
	got := explainSpawn(late).Error()
	if !strings.Contains(got, "did not answer within a minute") || strings.Contains(got, "127.0.0.1") {
		t.Errorf("a spawn that ran out of time: %q, want it explained without the URL", got)
	}
	refused := errors.New("spawn: worktree fix-auth already exists")
	if got := explainSpawn(refused); got != refused {
		t.Errorf("a refusal became %q", got)
	}
}

// An agent pane is where spawn is meant to run, so from there the missing
// piece is the task, and nothing is sent without one.
func TestSpawnRequiresATask(t *testing.T) {
	t.Setenv("FLOCKDECK_API", "http://127.0.0.1:1")
	t.Setenv("FLOCKDECK_TOKEN", "secret")

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

// Outside a pane the answer says what is missing and how to start an agent
// from where the user is: it said only where the command works.
func TestSpawnOutsideAPaneSaysWhatToDoInstead(t *testing.T) {
	for _, name := range []string{"FLOCKDECK_API", "FLOCKDECK_TOKEN", "PERCH_API", "PERCH_TOKEN"} {
		t.Setenv(name, "")
	}
	err := runSpawn([]string{"watch the build"})
	if err == nil || !strings.Contains(err.Error(), "FLOCKDECK_API") || !strings.Contains(err.Error(), "run `flockdeck` to open the window") {
		t.Errorf("spawn outside a pane = %v, want it to name what is missing and what to do", err)
	}
}

// A value flag given last with no value has to be reported as missing one.
// The separator the reordering adds used to follow it, and the flag set took
// "--" for its value: a worktree on a branch called --, and no complaint.
func TestSpawnValueFlagWithNothingAfterItIsAnError(t *testing.T) {
	for _, args := range [][]string{
		{"watch the build", "--worktree"},
		{"watch the build", "--agent"},
		{"--split", "watch the build", "-model"},
	} {
		req, err := parseSpawn(args)
		if err == nil {
			t.Errorf("parseSpawn(%q) = %+v, want an error for the missing value", args, req)
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
	for _, fs := range []*flag.FlagSet{
		flockdeckFlagSet(&cliFlags{}),
		spawnFlagSet(&spawnFlags{}),
		updateFlagSet(&updateFlags{}),
		remoteEnableFlagSet(&remoteEnableFlags{}),
		remotePairFlagSet(&remotePairFlags{}),
		remoteDisableFlagSet(&remoteDisableFlags{}),
		remoteRenameFlagSet(&remoteRenameFlags{}),
		remoteMoveFlagSet(&remoteMoveFlags{}),
	} {
		fs.VisitAll(func(f *flag.Flag) { names[f.Name] = true })
	}
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
		if pendingHelpFlags[name] {
			continue
		}
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

// pendingHelpFlags are the flags whose entry on the command-line help page has
// not been written yet. It held -agent and -model for as long as the page and
// the flags were being written by different agents; both are documented now, so
// there is nothing here and the check is strict about every flag.
var pendingHelpFlags = map[string]bool{}

// The usage printed for a wrong command line is the other hand-written copy.
func TestUsageNamesEveryFlag(t *testing.T) {
	var buf bytes.Buffer
	fs := flockdeckFlagSet(&cliFlags{})
	fs.SetOutput(&buf)
	usage(fs)
	for name := range cliFlagNames() {
		if !strings.Contains(buf.String(), "-"+name) {
			t.Errorf("-%s is missing from the usage message", name)
		}
	}
}

// The usage's line for keys is a hand-written copy of keys' own list, and it
// had fallen behind it: check and endpoint were in `flockdeck keys` and in the
// help, and somebody reading `flockdeck -h` never learned either existed.
func TestUsageNamesEveryKeysCommand(t *testing.T) {
	var buf bytes.Buffer
	fs := flockdeckFlagSet(&cliFlags{})
	fs.SetOutput(&buf)
	usage(fs)
	var line string
	for _, l := range strings.Split(buf.String(), "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "keys ") {
			line = l
		}
	}
	var own bytes.Buffer
	keysUsage(&own)
	for _, l := range strings.Split(own.String(), "\n") {
		// keysUsage lists each command as "  <name> ..." under "Commands:".
		if !strings.HasPrefix(l, "  ") || strings.HasPrefix(l, "   ") {
			continue
		}
		name := strings.Fields(l)[0]
		if !strings.Contains(line, name) {
			t.Errorf("the usage's keys line %q does not name %q, which keys offers", line, name)
		}
	}
}

// The settings that have no flag are only discoverable if the usage names them.
func TestUsageNamesTheEnvironment(t *testing.T) {
	var buf bytes.Buffer
	fs := flockdeckFlagSet(&cliFlags{})
	fs.SetOutput(&buf)
	usage(fs)
	for _, name := range []string{
		"FLOCKDECK_UPDATE", "FLOCKDECK_RELAY", "FLOCKDECK_API_KEY",
		dirEnv, startAgentEnv, freshEnv, shellFirstEnv, noWindowEnv, detachEnv, soloEnv,
	} {
		if !strings.Contains(buf.String(), name) {
			t.Errorf("%s is missing from the usage message", name)
		}
	}
}

// Each top-level flag that has an environment equivalent takes its default
// from the environment, but a flag actually given on the command line still
// wins -- the same "seed the default, then parse" pattern, and the same
// guarantee, internal/chat/flags.go already has and tests.
func TestTopLevelFlagsTakeTheirDefaultFromTheEnvironment(t *testing.T) {
	t.Setenv(dirEnv, "/srv/project")
	t.Setenv(startAgentEnv, "codex")
	t.Setenv(freshEnv, "1")
	t.Setenv(shellFirstEnv, "true")
	t.Setenv(noWindowEnv, "1")
	t.Setenv(detachEnv, "TRUE")
	t.Setenv(soloEnv, "1")

	var c cliFlags
	fs := flockdeckFlagSet(&c)
	if err := fs.Parse(nil); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	switch {
	case c.dir != "/srv/project":
		t.Errorf("dir = %q, want the value of %s", c.dir, dirEnv)
	case c.agent != "codex":
		t.Errorf("agent = %q, want the value of %s", c.agent, startAgentEnv)
	case !c.fresh || !c.shell || !c.noWindow || !c.detach || !c.solo:
		t.Errorf("options = %+v, want every boolean set from its env var", c.options)
	}

	// A flag actually given beats the environment behind it.
	c = cliFlags{}
	fs = flockdeckFlagSet(&c)
	if err := fs.Parse([]string{"-C", "/elsewhere", "-agent", "claude"}); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if c.dir != "/elsewhere" || c.agent != "claude" {
		t.Errorf("options = %+v, want the flags to beat %s and %s", c.options, dirEnv, startAgentEnv)
	}
}

// envBool is the boolean env vars' own parsing: "1" or "true", either case,
// mean yes; anything else, including unset, means no.
func TestEnvBool(t *testing.T) {
	const name = "FLOCKDECK_TEST_ENV_BOOL"
	cases := map[string]bool{"": false, "0": false, "false": false, "yes": false, "1": true, "true": true, "TRUE": true, " 1 ": true}
	for v, want := range cases {
		t.Setenv(name, v)
		if got := envBool(name); got != want {
			t.Errorf("envBool with %s=%q = %v, want %v", name, v, got, want)
		}
	}
}

// dirWasGiven is the trap the plan flagged by name: fs.Visit alone only sees
// -C actually typed on the command line, not FLOCKDECK_DIR merely seeding its
// default, so FLOCKDECK_DIR on its own -- no -C -- still has to count as
// given, or launchRoot would silently ignore it.
func TestDirWasGiven(t *testing.T) {
	cases := []struct {
		name   string
		args   []string
		envDir string
		want   bool
	}{
		{"neither", nil, "", false},
		{"env var alone, no -C on the command line", nil, "/srv/project", true},
		{"-C alone", []string{"-C", "/elsewhere"}, "", true},
		{"both", []string{"-C", "/elsewhere"}, "/srv/project", true},
	}
	for _, c := range cases {
		var cf cliFlags
		fs := flockdeckFlagSet(&cf)
		if err := fs.Parse(c.args); err != nil {
			t.Fatalf("%s: Parse: %v", c.name, err)
		}
		if got := dirWasGiven(fs, c.envDir); got != c.want {
			t.Errorf("%s: dirWasGiven = %v, want %v", c.name, got, c.want)
		}
	}
}

// FLOCKDECK_AGENT is already taken -- it is what a pane's own `flockdeck chat`
// reads to know which agent it is (internal/chat's paneEnv("AGENT")). The
// default-agent-for-new-panes setting must not reuse it, or a service started
// with FLOCKDECK_AGENT set would have every pane's own chat process read it
// too, corrupting per-pane agent identity.
func TestStartAgentEnvDoesNotCollideWithThePaneHandshakeVar(t *testing.T) {
	if startAgentEnv == "FLOCKDECK_AGENT" {
		t.Fatal("startAgentEnv must not be FLOCKDECK_AGENT: that name is already the pane-side agent-identity handshake variable")
	}
}

// -join, -invite and -name on `remote enable` fall back to the environment
// too, so a first-boot container entrypoint can enrol with no interactive
// input; a flag given still wins, as with the top-level flags.
func TestRemoteEnableFlagsTakeTheirDefaultFromTheEnvironment(t *testing.T) {
	t.Setenv(remoteJoinEnv, "join-code")
	t.Setenv(remoteInviteEnv, "invite-code")
	t.Setenv(remoteNameEnv, "my-server")

	var f remoteEnableFlags
	fs := remoteEnableFlagSet(&f)
	if err := fs.Parse(nil); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if f.join != "join-code" || f.invite != "invite-code" || f.name != "my-server" {
		t.Errorf("flags = %+v", f)
	}

	f = remoteEnableFlags{}
	fs = remoteEnableFlagSet(&f)
	if err := fs.Parse([]string{"-join", "other-code"}); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if f.join != "other-code" {
		t.Errorf("join = %q, want the flag to beat %s", f.join, remoteJoinEnv)
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

// A record of the running instance that cannot be read is not the same as
// there being nothing running. Carrying on regardless starts a second set of
// agents while the first keeps going with no window and no record to find it
// by, so the guess has to be said out loud.
func TestJoinRunningSaysWhenItCannotTell(t *testing.T) {
	var warnings []string
	inst, base, err := joinRunning(
		func() (*store.Instance, string, error) { return nil, "", errors.New("unreadable") },
		func(text string) { warnings = append(warnings, text) },
	)
	if inst != nil || base != "" || err != nil {
		t.Errorf("joined %v at %q (%v) on an unreadable record, want a new one started", inst, base, err)
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
	inst, base, err := joinRunning(
		func() (*store.Instance, string, error) { return running, running.URL, nil },
		func(text string) { t.Errorf("unexpected warning: %s", text) },
	)
	if inst != running || base != running.URL || err != nil {
		t.Errorf("joined %v at %q (%v), want the running instance", inst, base, err)
	}

	inst, base, err = joinRunning(
		func() (*store.Instance, string, error) { return nil, "", nil },
		func(text string) { t.Errorf("unexpected warning: %s", text) },
	)
	if inst != nil || base != "" || err != nil {
		t.Errorf("joined %v at %q (%v) when nothing was running", inst, base, err)
	}
}

// useCatalog swaps the catalog the command line checks names against, and the
// answer to whether each of them is installed, for the length of one test.
// Neither can be left to the machine the test runs on: whether `claude` is on
// this PATH is not something a check of the messages should depend on.
func useCatalog(t *testing.T, specs []agent.Spec, defaultID string, installed map[string]bool) {
	t.Helper()
	catalog, available := agentCatalog, agentAvailable
	t.Cleanup(func() { agentCatalog, agentAvailable = catalog, available })
	agentCatalog = func() ([]agent.Spec, string, string) { return specs, defaultID, "" }
	agentAvailable = func(s agent.Spec) bool { return installed[s.ID] }
}

// testCatalog is a stand-in for the real one: an agent with models, an agent
// with none, and one somebody has hidden.
func testCatalog() []agent.Spec {
	return []agent.Spec{
		{ID: "claude", Name: "Claude Code", Runner: agent.RunnerCLI, Exe: "claude",
			Models: []agent.Model{
				{ID: "", Name: "Default"},
				{ID: "opus"}, {ID: "sonnet"}, {ID: "haiku"},
			},
			Install: "https://claude.com/claude-code"},
		{ID: "codex", Name: "Codex", Runner: agent.RunnerCLI, Exe: "codex",
			Models:  []agent.Model{{ID: "gpt-5"}},
			Install: "npm i -g @openai/codex"},
		{ID: "local", Name: "Local llama", Runner: agent.RunnerAPI},
		{ID: "retired", Name: "Retired", Hidden: true},
	}
}

// A name that is not in the catalog has to say what the catalog does have.
// Being told "no agent called codx" and nothing else leaves the user with no
// way to find the spelling without reading the source.
func TestCheckAgentNamesWhatIsOnOffer(t *testing.T) {
	useCatalog(t, testCatalog(), "claude", map[string]bool{"claude": true})
	cases := []struct {
		name    string
		agent   string
		model   string
		wantErr []string // every one of these has to appear in the message
	}{
		{"nothing chosen", "", "", nil},
		{"an agent that exists", "claude", "", nil},
		{"an agent and one of its models", "claude", "sonnet", nil},
		{"an agent whose models are not listed takes anything", "local", "qwen3-coder", nil},
		// The empty model is the catalog's own entry for "leave it alone", so
		// naming an agent without a model is never wrong.
		{"an agent with no model", "codex", "", nil},
		{"a misspelt agent", "codx", "",
			[]string{"codx", "claude", "codex", "local"}},
		{"a model the agent does not have", "claude", "gpt-5",
			[]string{"claude", "gpt-5", "opus", "sonnet", "haiku"}},
		// Which agent a bare model belongs to is the project's default and is
		// not known here, so it is checked against the whole catalog. "local"
		// lists no models, so nothing can be ruled out while it is there.
		{"a model with no agent, offered by another", "", "gpt-5", nil},
		// A hidden agent is out of the picker, so it is not somewhere a name
		// can be found either.
		{"a hidden agent", "retired", "", []string{"retired", "claude"}},
	}
	for _, c := range cases {
		err := checkAgent(c.agent, c.model)
		if len(c.wantErr) == 0 {
			if err != nil {
				t.Errorf("%s: checkAgent(%q, %q) = %v, want nil", c.name, c.agent, c.model, err)
			}
			continue
		}
		if err == nil {
			t.Errorf("%s: checkAgent(%q, %q) = nil, want an error", c.name, c.agent, c.model)
			continue
		}
		for _, want := range c.wantErr {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: message %q does not mention %q", c.name, err, want)
			}
		}
	}
}

// With every agent in the catalog listing its models, a bare model that none
// of them offers is a typo and is worth saying so.
func TestCheckAgentCatchesAModelNobodyOffers(t *testing.T) {
	specs := testCatalog()[:2] // the two that list their models
	useCatalog(t, specs, "claude", map[string]bool{"claude": true})
	if err := checkAgent("", "sonnet"); err != nil {
		t.Errorf("checkAgent(\"\", sonnet) = %v, want nil", err)
	}
	err := checkAgent("", "gpt-6")
	if err == nil || !strings.Contains(err.Error(), "gpt-6") {
		t.Errorf("err = %v, want it to name the model", err)
	}
}

// Somebody who has not installed Codex should still learn from here that Flockdeck
// would run it, and be told how — so an agent that is missing is listed with
// its install line rather than left out.
func TestPrintAgentsShowsWhatIsNotInstalled(t *testing.T) {
	useCatalog(t, testCatalog(), "claude", map[string]bool{"claude": true})
	var buf bytes.Buffer
	printAgents(&buf)
	out := buf.String()

	for _, want := range []string{
		"claude", "Claude Code", "installed", "default",
		"opus, sonnet, haiku",
		"codex", "not installed", "npm i -g @openai/codex", "gpt-5",
		"local", "whatever you ask it for",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the listing does not mention %q:\n%s", want, out)
		}
	}
	// An agent that is there does not need telling how to install it.
	if strings.Contains(out, "https://claude.com/claude-code") {
		t.Errorf("the listing tells you how to install an agent you have:\n%s", out)
	}
	// A hidden agent is out of the picker, and this is the picker in another
	// form.
	if strings.Contains(out, "Retired") {
		t.Errorf("the listing shows a hidden agent:\n%s", out)
	}
}

// When agents.json could not all be used, an agent defined there is missing
// from the catalog, and "no agent called mine" alone sends the user to check
// the spelling rather than the file. Both the error and the listing say so.
func TestAnUnreadAgentsFileIsSaid(t *testing.T) {
	useCatalog(t, testCatalog(), "claude", map[string]bool{"claude": true})
	agentCatalog = func() ([]agent.Spec, string, string) {
		return testCatalog(), "claude", "agents.json: invalid character '}' after object key"
	}
	err := checkAgent("mine", "")
	if err == nil || !strings.Contains(err.Error(), "agents.json was not fully read") || !strings.Contains(err.Error(), "invalid character") {
		t.Errorf("checkAgent(mine) = %v, want it to say agents.json was not fully read, and why", err)
	}
	var buf bytes.Buffer
	printAgents(&buf)
	if !strings.Contains(buf.String(), "agents.json was not fully read") {
		t.Errorf("the listing does not say agents.json was not fully read:\n%s", buf.String())
	}
}

// An agent that exists but is not installed is still worth a word at the
// command line, and the word says how to get it.
func TestNotInstalledWarning(t *testing.T) {
	useCatalog(t, testCatalog(), "claude", map[string]bool{"claude": true})
	if w := notInstalledWarning("codex"); !strings.Contains(w, "Codex") || !strings.Contains(w, "npm i -g @openai/codex") {
		t.Errorf("codex, not installed: %q, want its name and install line", w)
	}
	if w := notInstalledWarning("claude"); w != "" {
		t.Errorf("claude, installed: %q, want nothing", w)
	}
	if w := notInstalledWarning(""); w != "" {
		t.Errorf("no agent chosen: %q, want nothing", w)
	}
}

// A flag that takes a value says in -h what the value is. Go's default for a
// string is the word "string", which is how `-C string` and `-worktree string`
// sat beside `-agent id` and `-model model` in the usage.
func TestEveryFlagNamesItsValue(t *testing.T) {
	for name, fs := range map[string]*flag.FlagSet{
		"flockdeck":       flockdeckFlagSet(&cliFlags{}),
		"flockdeck spawn": spawnFlagSet(&spawnFlags{}),
	} {
		fs.VisitAll(func(f *flag.Flag) {
			if b, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && b.IsBoolFlag() {
				return
			}
			switch value, _ := flag.UnquoteUsage(f); value {
			case "string", "value", "int", "uint", "float", "duration":
				t.Errorf("%s -%s is shown as taking a %q; say what goes there", name, f.Name, value)
			}
		})
	}
}

// An API agent is not a program to install: what it lacks is a key. It was
// listed as "not installed" above a line saying to set one, and warned about
// as "not installed here".
func TestAPIAgentsAreSetUpRatherThanInstalled(t *testing.T) {
	api := agent.Spec{ID: "anthropic", Name: "Claude API", Runner: agent.RunnerAPI,
		Install: "set a key with `flockdeck keys set anthropic`"}
	for _, ready := range []bool{false, true} {
		useCatalog(t, append(testCatalog(), api), "claude", map[string]bool{"claude": true, "anthropic": ready})
		var buf bytes.Buffer
		printAgents(&buf)
		out := buf.String()
		want, wantNot := "Claude API  [not set up]", "Claude API  [not installed]"
		if ready {
			want = "Claude API  [ready]"
		}
		if !strings.Contains(out, want) || strings.Contains(out, wantNot) {
			t.Errorf("key set %v: the listing does not say %q:\n%s", ready, want, out)
		}
		if got := strings.Contains(out, "set up: set a key"); got == ready {
			t.Errorf("key set %v: the line saying how to set it up is shown %v:\n%s", ready, got, out)
		}
	}
	useCatalog(t, append(testCatalog(), api), "claude", map[string]bool{"claude": true})
	if w := notInstalledWarning("anthropic"); !strings.Contains(w, "not set up") || !strings.Contains(w, "keys set anthropic") || strings.Contains(w, "install") {
		t.Errorf("anthropic, no key: %q, want it called not set up, with how to set it up", w)
	}
}

// The models line has to say something for each of the three shapes an entry
// can take, because "models:" followed by nothing reads as a bug.
func TestModelSummary(t *testing.T) {
	cases := []struct {
		name string
		spec agent.Spec
		want string
	}{
		{"named models", agent.Spec{Models: []agent.Model{{ID: "gpt-5"}, {ID: "o3"}}},
			"gpt-5, o3"},
		{"none listed", agent.Spec{}, "whatever you ask it for"},
		{"with the empty one", agent.Spec{Models: []agent.Model{{ID: ""}, {ID: "opus"}}},
			"opus, or none for whatever it is already set to"},
	}
	for _, c := range cases {
		if got := modelSummary(c.spec); got != c.want {
			t.Errorf("%s: modelSummary = %q, want %q", c.name, got, c.want)
		}
	}
}

// The choice has to reach the request, or `spawn --agent codex` starts the
// helper as whatever the default is and says nothing about it.
func TestParseSpawnCarriesTheAgentAndModel(t *testing.T) {
	useCatalog(t, testCatalog(), "claude", map[string]bool{"claude": true})

	req, err := parseSpawn([]string{"repair the token refresh", "--agent", "codex", "--model", "gpt-5"})
	if err != nil {
		t.Fatalf("parseSpawn: %v", err)
	}
	if req.Task != "repair the token refresh" {
		t.Errorf("task = %q, want the task without the flags in it", req.Task)
	}
	if req.Agent != "codex" || req.Model != "gpt-5" {
		t.Errorf("request carried %q/%q, want codex/gpt-5", req.Agent, req.Model)
	}

	// Asking for nothing has to keep looking exactly like every earlier build,
	// since that is what an agent spawning a helper without an opinion sends.
	plain, err := parseSpawn([]string{"watch the build"})
	if err != nil {
		t.Fatalf("parseSpawn: %v", err)
	}
	if plain.Agent != "" || plain.Model != "" {
		t.Errorf("a spawn with no opinion carried %q/%q, want both empty", plain.Agent, plain.Model)
	}
}

// The catalog is checked before anything is sent, so a misspelling is answered
// here rather than becoming a pane in a window that never starts.
func TestParseSpawnChecksTheAgentAgainstTheCatalog(t *testing.T) {
	useCatalog(t, testCatalog(), "claude", map[string]bool{"claude": true})
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"a misspelt agent", []string{"do a thing", "--agent", "codx"}, "codx"},
		{"a model the agent does not have", []string{"do a thing", "--agent", "codex", "--model", "opus"}, "opus"},
		// -shell starts a shell, which runs no agent at all.
		{"an agent alongside -shell", []string{"--shell", "--agent", "codex"}, "-shell"},
	}
	for _, c := range cases {
		_, err := parseSpawn(c.args)
		if err == nil {
			t.Errorf("%s: parseSpawn(%q) = nil, want an error", c.name, c.args)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: message %q does not mention %q", c.name, err, c.want)
		}
	}
}

// The reordering reads which flags take a value from the flag set, so the two
// new ones need nothing added to it — but that is only true while they are
// defined there, and this is what would notice if they were not.
func TestSpawnAgentFlagsSurviveTheReordering(t *testing.T) {
	fs := spawnFlagSet(&spawnFlags{})
	got := orderSpawnArgs(fs, []string{"repair the token refresh", "--agent", "codex", "--model", "gpt-5"})
	want := []string{"--agent", "codex", "--model", "gpt-5", "--", "repair the token refresh"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestHookPrintsAnAllowPermissionDecision covers auto-review's other end: a
// PreToolUse hook the application's review handler allows must print the
// hookSpecificOutput shape Claude Code itself reads a permission decision
// from, since printing nothing at all is what every ordinary PreToolUse call
// still does.
func TestHookPrintsAnAllowPermissionDecision(t *testing.T) {
	srv, err := hooks.Serve(func(hooks.Event) {})
	if err != nil {
		t.Fatalf("serve: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	srv.SetReviewHandler(func(string, hooks.Event) (bool, string) {
		return true, "auto-review: a read-only command"
	})

	stdin := strings.NewReader(`{"session_id":"s","tool_name":"Bash","tool_input":{"command":"git status"}}`)
	var stdout, stderr bytes.Buffer
	hook([]string{"-endpoint", srv.Endpoint(), "-token", srv.Token(), "-session", "pane-1", "-event", "PreToolUse"}, stdin, &stdout, &stderr)
	if stderr.Len() != 0 {
		t.Fatalf("hook wrote to stderr: %s", stderr.String())
	}

	var out hookOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("hook did not print valid JSON: %v (%s)", err, stdout.String())
	}
	if out.HookSpecificOutput.PermissionDecision != "allow" {
		t.Errorf("permissionDecision = %q, want allow", out.HookSpecificOutput.PermissionDecision)
	}
	if out.HookSpecificOutput.PermissionDecisionReason == "" {
		t.Error("permissionDecisionReason is empty, want the handler's reason")
	}
}

// TestHookPrintsNothingWhenNotAllowed covers every PreToolUse call today,
// before this ever existed: without a review handler installed, the hook must
// print nothing at all, so Claude Code's own permission prompt is untouched.
func TestHookPrintsNothingWhenNotAllowed(t *testing.T) {
	srv, err := hooks.Serve(func(hooks.Event) {})
	if err != nil {
		t.Fatalf("serve: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	stdin := strings.NewReader(`{"session_id":"s","tool_name":"Bash","tool_input":{"command":"rm -rf /"}}`)
	var stdout, stderr bytes.Buffer
	hook([]string{"-endpoint", srv.Endpoint(), "-token", srv.Token(), "-session", "pane-1", "-event", "PreToolUse"}, stdin, &stdout, &stderr)
	if stdout.Len() != 0 {
		t.Errorf("hook printed %q, want nothing without a review handler", stdout.String())
	}
}
