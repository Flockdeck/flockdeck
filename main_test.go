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
			[]string{"-new", "-shell", "-agent", "-detach"}},
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
// request: until it has, its address still answers, and the next `flockdeck`
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

// useCatalog swaps the catalog the command line checks names against, and the
// answer to whether each of them is installed, for the length of one test.
// Neither can be left to the machine the test runs on: whether `claude` is on
// this PATH is not something a check of the messages should depend on.
func useCatalog(t *testing.T, specs []agent.Spec, defaultID string, installed map[string]bool) {
	t.Helper()
	catalog, available := agentCatalog, agentAvailable
	t.Cleanup(func() { agentCatalog, agentAvailable = catalog, available })
	agentCatalog = func() ([]agent.Spec, string) { return specs, defaultID }
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
