// Command perch is a desktop application for running several Claude
// Code agents at once.
//
// It drives the `claude` CLI in real pseudo-terminals, so every agent behaves
// exactly as it does in a normal terminal, and presents them in a window with
// tabs and split panes, per-pane status, layout persistence, git worktrees and
// broadcast input.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/jmwri/perch/internal/agent"
	"github.com/jmwri/perch/internal/appwindow"
	"github.com/jmwri/perch/internal/hooks"
	"github.com/jmwri/perch/internal/server"
	"github.com/jmwri/perch/internal/session"
	"github.com/jmwri/perch/internal/store"
	"github.com/jmwri/perch/internal/workspace"
)

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "dev"

// windowGrace is how long the application waits after the last window closes
// before shutting down. A page reload briefly drops the connection, and that
// must not be mistaken for the user quitting.
const windowGrace = 3 * time.Second

func main() {
	// `hook` is how panes report their lifecycle back to a running instance.
	// It is a hidden subcommand rather than a separate binary so there is only
	// ever one artifact to ship.
	if len(os.Args) > 1 && os.Args[1] == "hook" {
		runHook(os.Args[2:])
		return
	}
	// `spawn` is how an agent hands work to a helper of its own. It is run
	// from inside a pane, which is where the address and token come from.
	if len(os.Args) > 1 && os.Args[1] == "spawn" {
		if err := runSpawn(os.Args[2:]); err != nil {
			// A flag set has already explained a parse failure itself; saying
			// it a second time only makes the real message harder to find.
			if !errors.Is(err, errReported) {
				fmt.Fprintln(os.Stderr, "perch spawn:", err)
			}
			os.Exit(1)
		}
		return
	}
	// `agents` prints the catalog. It is a subcommand rather than a flag
	// because it answers a question instead of changing how a run starts, and
	// because it is the only place to find out what an -agent name may be.
	if len(os.Args) > 1 && os.Args[1] == "agents" {
		printAgents(os.Stdout)
		return
	}

	var c cliFlags
	fs := perchFlagSet(&c)
	_ = fs.Parse(os.Args[1:]) // ExitOnError: a bad flag has already ended us

	if c.version {
		fmt.Println("perch", version)
		return
	}

	// Anything left over is a mistyped flag or a subcommand that does not
	// exist. Ignoring it would open a window on the current directory and
	// leave the user believing `perch quit` had done something.
	if fs.NArg() > 0 {
		arg := fs.Arg(0)
		fmt.Fprintf(os.Stderr, "perch: unrecognised argument %q\n", arg)
		switch fi, statErr := os.Stat(arg); {
		case statErr == nil && fi.IsDir():
			fmt.Fprintf(os.Stderr, "To open that directory: perch -C %s\n", arg)
		case fs.Lookup(arg) != nil:
			fmt.Fprintf(os.Stderr, "Did you mean -%s?\n", arg)
		}
		fmt.Fprintln(os.Stderr)
		fs.Usage()
		os.Exit(2)
	}
	// An agent the catalog does not have is worth two lines here rather than a
	// window full of panes that will not start.
	if err := checkAgent(c.agent, ""); err != nil {
		fmt.Fprintln(os.Stderr, "perch:", err)
		os.Exit(2)
	}
	server.Version = version

	if c.quit {
		if err := quitRunning(); err != nil {
			fail(err)
		}
		return
	}

	if err := run(c.options); err != nil {
		fail(err)
	}
}

// cliFlags are the top-level flags and where their values land. The two that
// are acted on here rather than passed to run sit alongside the rest.
type cliFlags struct {
	options
	quit    bool
	version bool
}

// perchFlagSet defines the top-level command line. It is built here rather
// than inline in main so that a test can walk the same set the program uses
// and check the help documents it.
func perchFlagSet(c *cliFlags) *flag.FlagSet {
	fs := flag.NewFlagSet("perch", flag.ExitOnError)
	fs.StringVar(&c.dir, "C", ".", "directory to open the workspace on")
	fs.StringVar(&c.agent, "agent", "", "`id` of the agent new panes start as for this run; perch agents lists them")
	fs.BoolVar(&c.fresh, "new", false, "ignore any saved layout and start with a single pane")
	fs.BoolVar(&c.shell, "shell", false, "open the first pane as a shell instead of an agent")
	fs.BoolVar(&c.noWindow, "no-window", false, "do not open a window; print the URL and keep serving")
	fs.BoolVar(&c.detach, "detach", false, "keep running without a window; reattach later by running it again")
	fs.BoolVar(&c.quit, "quit", false, "stop a running instance and its agents")
	fs.BoolVar(&c.solo, "solo", false, "always start a new instance instead of attaching to a running one")
	fs.BoolVar(&c.version, "version", false, "print the version and exit")
	fs.Usage = func() { usage(fs) }
	return fs
}

func usage(fs *flag.FlagSet) {
	out := fs.Output()
	fmt.Fprintf(out, "perch — run several Claude Code agents in tabs and split panes.\n\n")
	fmt.Fprintf(out, "Usage:\n  perch [flags]\n\nFlags:\n")
	fs.PrintDefaults()
	fmt.Fprintf(out, "\nSubcommands:\n")
	fmt.Fprintf(out, "  spawn [--worktree <branch>] [--split] [--shell] [--agent <id>] [--model <model>] <task>\n")
	fmt.Fprintf(out, "        start another agent; run from inside a pane\n")
	fmt.Fprintf(out, "        run perch spawn -h for what the flags do\n")
	fmt.Fprintf(out, "  agents\n")
	fmt.Fprintf(out, "        list the agents perch can run, with their models\n")
	fmt.Fprintf(out, "\nRunning it again attaches to an instance that is already going.\n")
	fmt.Fprintf(out, "Press F1 in the window for the help: the shortcuts, and how the rest of it works.\n")
}

// fail reports a startup error. When the process was started from a desktop
// shortcut there is no console to print to, so the message is also written to
// a log file the user can be pointed at.
func fail(err error) {
	fmt.Fprintln(os.Stderr, "perch:", err)
	if dir, dirErr := store.Dir(); dirErr == nil {
		path := filepath.Join(dir, "error.log")
		stamp := time.Now().Format(time.RFC3339)
		if f, openErr := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); openErr == nil {
			fmt.Fprintf(f, "%s %v\n", stamp, err)
			f.Close()
		}
	}
	os.Exit(1)
}

// options are the settings run needs.
type options struct {
	dir      string
	agent    string
	fresh    bool
	shell    bool
	noWindow bool
	detach   bool
	solo     bool
}

// quitRunning stops an instance that is already going.
func quitRunning() error {
	inst, base, err := runningInstance()
	// A record that cannot be read is not the same as there being nothing to
	// stop: reporting it as "none found" sends the user looking for a process
	// that is very likely still running.
	if err != nil {
		return fmt.Errorf("read the record of the running instance: %w", err)
	}
	if inst == nil {
		return fmt.Errorf("no running perch found")
	}
	if err := server.RequestQuit(base, inst.Token); err != nil {
		return fmt.Errorf("ask the instance at %s to stop: %w", base, err)
	}
	// The request only asks. What follows it is saving every open project's
	// layout and stopping a screenful of agent processes, and the instance is
	// still listening the whole time — so `perch -quit && perch` used to find
	// the old instance still answering and attach to one on its way out,
	// opening a window onto agents that were in the middle of being killed.
	// Saying "stopped" before it has is the same claim in words.
	if !waitGone(func() bool { _, err := server.Probe(base, inst.Token); return err != nil }, quitWait) {
		return fmt.Errorf("the instance at %s took the request but is still running", base)
	}
	fmt.Println("perch: stopped")
	return nil
}

// quitWait is how long `-quit` waits for the instance to actually go. It is
// longer than the deadline the instance puts on its own shutdown, so an
// instance that gives up on a wedged pane is still gone before this gives up
// on the instance.
const quitWait = 20 * time.Second

// quitPoll is how often the address is tried while waiting. A refused
// connection on loopback comes back at once, so this costs nothing.
const quitPoll = 100 * time.Millisecond

// waitGone polls until gone reports true, or until within has passed.
func waitGone(gone func() bool, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for {
		if gone() {
			return true
		}
		if !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(quitPoll)
	}
}

// runningInstance returns the recorded instance if it is alive and answering.
func runningInstance() (*store.Instance, string, error) {
	inst, err := store.LoadInstance()
	if err != nil || inst == nil {
		return nil, "", err
	}
	base := inst.URL
	if _, err := server.Probe(base, inst.Token); err != nil {
		// The record is stale: the process died without clearing it.
		_ = store.ClearInstance()
		return nil, "", nil
	}
	return inst, base, nil
}

// attach hands the requested project to an instance that is already running
// and shows a window onto it, so a second launch joins the agents already
// going instead of starting a rival set.
func attach(inst *store.Instance, base, root string, noWindow bool) error {
	if err := server.RequestOpen(base, inst.Token, root); err != nil {
		return err
	}
	url := base + "/?t=" + inst.Token
	if noWindow {
		fmt.Println("perch is already running at:")
		fmt.Println(" ", url)
		return nil
	}
	profile, err := store.BrowserProfileDir()
	if err != nil {
		return err
	}
	if _, err := appwindow.Open(url, profile); err != nil {
		// The instance carries on without us, so its address stays good.
		if errors.Is(err, appwindow.ErrNoBrowser) {
			return fmt.Errorf("%w — set %s to one, or open this URL manually:\n  %s",
				err, appwindow.BrowserEnv, url)
		}
		return fmt.Errorf("%w — open this URL manually:\n  %s", err, url)
	}
	return nil
}

// joinRunning reports the instance a launch should join, if there is one.
//
// A record that cannot be read is not the same as there being nothing to join,
// and the difference matters here more than anywhere: carrying on starts a
// second set of agents, which is the outcome the whole attach path exists to
// prevent. The first set keeps running with no window showing it and a record
// that has just been overwritten, so `perch -quit` will not find it either.
//
// Starting is still the right default — refusing would leave the application
// unusable over a file the user has never heard of — but it is a guess, and
// the guess is said out loud rather than made silently.
func joinRunning(lookup func() (*store.Instance, string, error), warn func(string)) (*store.Instance, string) {
	inst, base, err := lookup()
	if err != nil {
		warn("could not tell whether one is already running (" + err.Error() +
			"), so starting a new one; any agents already running still are")
		return nil, ""
	}
	return inst, base
}

// startupOnlyFlags lists the flags that describe a fresh start, and so mean
// nothing when the launch turns into attaching to a running instance.
func startupOnlyFlags(opts options) []string {
	var out []string
	if opts.fresh {
		out = append(out, "-new")
	}
	if opts.shell {
		out = append(out, "-shell")
	}
	if opts.agent != "" {
		out = append(out, "-agent")
	}
	if opts.detach {
		out = append(out, "-detach")
	}
	return out
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// useAgent tells the workspace which agent a new pane starts as for the rest
// of this run, which is what -agent asks for.
//
// The workspace is another task's file in this work, so until that lands this
// does nothing beyond the name having been checked; the merge is this one
// function body.
var useAgent = func(ws *workspace.Workspace, agentID string) {}

// run starts the workspace, serves it and shows the window.
func run(opts options) error {
	root, err := filepath.Abs(opts.dir)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", opts.dir, err)
	}
	// Say which of the three ways this can go wrong actually happened: a
	// path that is missing and one that cannot be read are both common, and
	// "is not a directory" sends the user looking for the wrong thing.
	switch fi, err := os.Stat(root); {
	case errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("%s does not exist", root)
	case err != nil:
		return fmt.Errorf("open %s: %w", root, err)
	case !fi.IsDir():
		return fmt.Errorf("%s is not a directory", root)
	}

	// Attach to an instance that is already running rather than starting a
	// second one: its agents are the ones the user means.
	if !opts.solo {
		if inst, base := joinRunning(runningInstance, func(text string) {
			fmt.Fprintln(os.Stderr, "perch:", text)
		}); inst != nil {
			// The flags that describe how to start up have nobody to apply
			// to once we are joining agents that are already running. Say so:
			// silently ignoring -new looks like the layout was kept on purpose.
			if ignored := startupOnlyFlags(opts); len(ignored) > 0 {
				fmt.Fprintf(os.Stderr,
					"perch: joining the instance already running, so %s %s no effect here (use -solo to start a separate one)\n",
					strings.Join(ignored, " and "), plural(len(ignored), "has", "have"))
			}
			return attach(inst, base, root, opts.noWindow)
		}
	}

	ws, err := workspace.New(workspace.Options{Root: root})
	if err != nil {
		return err
	}
	defer ws.Close()

	if opts.agent != "" {
		useAgent(ws, opts.agent)
	}

	// Build the initial workspace before the server exists. Once it is
	// running, every access to the workspace has to go through its owner
	// goroutine, so setting up here keeps startup free of that constraint.
	restored := false
	if !opts.fresh {
		// A layout that fails to restore should never stop the app starting.
		restored, _ = ws.Restore()
		// Bring back the other projects that were open last time, too.
		ws.RestoreSession()
	}
	if !restored {
		kind := session.KindClaude
		if opts.shell || !ws.ClaudeAvailable() {
			kind = session.KindShell
		}
		ws.NewTab(kind, root, "")
	}

	srv, err := server.New(ws)
	if err != nil {
		return err
	}
	defer srv.Close()
	ws.SetWake(srv.Wake)

	// Record where this instance is listening so a later launch can attach.
	if err := store.SaveInstance(&store.Instance{
		PID: os.Getpid(), URL: srv.BaseURL(), Token: srv.Token(), Started: time.Now(),
	}); err != nil {
		fmt.Fprintln(os.Stderr, "perch: could not record the instance:", err)
	}
	defer store.ClearInstance()

	// Shutdown can be requested by the window closing, by a signal, or by the
	// user quitting from the UI.
	quit := make(chan struct{})
	var closeOnce = make(chan struct{}, 1)
	stop := func() {
		select {
		case closeOnce <- struct{}{}:
			close(quit)
			// Everything past this point closes panes and kills their
			// processes. A pane whose process will not die must not be able to
			// keep the whole application alive with its window already gone,
			// so the orderly shutdown is given a deadline of its own. The
			// process exits normally long before this fires; it only ever runs
			// when the shutdown has wedged.
			go func() {
				time.Sleep(shutdownGrace)
				forceQuit()
			}()
		default:
		}
	}

	// Two deep, so a second interrupt arriving while the first is still being
	// acted on is not dropped on the floor.
	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, os.Interrupt)
	go interrupts(sigs, stop, forceQuit)

	srv.OnQuit = stop
	if opts.detach {
		srv.Detach()
	}

	var win *appwindow.Window
	if opts.noWindow || opts.detach {
		fmt.Println("perch serving at:")
		fmt.Println(" ", srv.URL())
		if opts.detach {
			fmt.Println("Running detached. Attach with `perch`, stop with `perch -quit`.")
		} else {
			fmt.Println("Press Ctrl+C to stop.")
		}
	} else {
		profile, err := store.BrowserProfileDir()
		if err != nil {
			return err
		}
		win, err = appwindow.Open(srv.URL(), profile)
		if err != nil {
			// Unlike attaching, this server is ours and stops with us, so the
			// address it was serving will not answer by the time anyone reads
			// this. Name the ways to get a window instead.
			if errors.Is(err, appwindow.ErrNoBrowser) {
				return fmt.Errorf("%w — set %s to one, or run `perch -no-window` and open the URL it prints",
					err, appwindow.BrowserEnv)
			}
			return fmt.Errorf("open the window: %w — or run `perch -no-window` and open the URL it prints", err)
		}
		defer win.Close()

		if win.AppMode {
			// The window process ending is the user closing the application,
			// unless they asked to leave the agents running.
			go func() {
				_ = win.Wait()
				if !srv.Detached() {
					stop()
				}
			}()
		} else {
			// A tab in the user's own browser cannot be watched, so fall back
			// to shutting down when the page disconnects.
			fmt.Println("Opened in your browser:", srv.URL())
		}

		// Whichever way the UI is shown, losing every connected window for
		// more than a moment means nobody is looking any more.
		srv.OnLastClientGone = func() {
			go func() {
				time.Sleep(windowGrace)
				if srv.ClientCount() == 0 && !srv.Detached() {
					stop()
				}
			}()
		}
	}

	<-quit

	if err := shutdown(srv.Close, ws.SaveAll); err != nil {
		fmt.Fprintln(os.Stderr, "perch: could not save layout:", err)
	}
	return nil
}

// shutdown stops serving, and only then saves.
//
// The order is the whole of it. The workspace is not safe for concurrent use,
// which is why the server funnels every read and write of it through a single
// goroutine of its own — and that goroutine is still running commands from any
// window that is still connected right up until the server is closed. Saving
// first walks the tabs and the pane tree while that goroutine may be adding to
// them, which is a race for the one piece of state the user would actually
// notice losing.
//
// Closing first also loses nothing: what is being closed is the way in, and
// everything it was serving is about to go.
func shutdown(stopServing func() error, save func() error) error {
	_ = stopServing()
	return save()
}

// shutdownGrace bounds the orderly shutdown. It is generous: closing panes is
// killing a handful of processes, which takes no time at all when it works.
const shutdownGrace = 10 * time.Second

// interrupts turns the interrupt signal into the two things it means.
//
// The first asks for an orderly stop, which saves the layout and shuts the
// agents down. A second, arriving while that is still going, is the user
// saying it has taken long enough — and it has to be acted on, because
// signal.Notify has already taken Ctrl+C away from the runtime's own handler.
// Without this the only way out of a wedged shutdown is to kill the process,
// which is exactly the way to leave agent processes behind.
func interrupts(sigs <-chan os.Signal, stop, force func()) {
	<-sigs
	stop()
	<-sigs
	force()
}

// forceQuit ends the process without waiting for the orderly shutdown to
// finish. It is the last resort: a window that has gone and an application
// that will not stop is worse than an abrupt exit.
func forceQuit() {
	fmt.Fprintln(os.Stderr, "perch: shutting down is taking too long — stopping now")
	os.Exit(1)
}

// errReported marks an error the failing code has already printed, so the
// caller exits without repeating it.
var errReported = errors.New("already reported")

// errHelpAsked marks `spawn -h`: the usage has been printed and there is
// nothing left to do, so the command succeeds rather than failing.
var errHelpAsked = errors.New("usage shown")

// runSpawn implements the `spawn` subcommand, which starts another agent from
// inside a pane.
//
// It exists so a lead agent can split its own work up: given a plan, it can run
// this once per task and watch the helpers appear beside it. The address and
// token come from the environment its pane was started with, so only processes
// running inside a pane can use it.
// paneEnv reads one of the variables a pane carries, accepting the name an
// earlier build used alongside the one in use now. A pane started by an
// instance of that build is still running with the old names in its
// environment, and an agent inside it should not lose the ability to spawn
// just because the binary on its PATH has been upgraded. The fallback can go a
// release after the rename.
func paneEnv(name string) string {
	if v := os.Getenv("PERCH_" + name); v != "" {
		return v
	}
	return os.Getenv("AGENT_WRAPPER_" + name)
}

func runSpawn(args []string) error {
	req, err := parseSpawn(args)
	if errors.Is(err, errHelpAsked) {
		return nil
	}
	if err != nil {
		return err
	}
	api := paneEnv("API")
	token := paneEnv("TOKEN")
	pane := paneEnv("PANE")
	if api == "" || token == "" {
		return fmt.Errorf("this only works inside a perch pane")
	}

	res, err := hooks.Spawn(api, token, pane, req)
	if err != nil {
		return err
	}
	// Where it landed is the part the caller could not have worked out:
	// --worktree names a branch, and which directory that becomes is the
	// application's decision. Without it an agent that has just handed work to
	// a helper has no way to go and look at what the helper did.
	if res.Cwd != "" {
		fmt.Println("started agent", res.PaneID, "in", res.Cwd)
		return nil
	}
	fmt.Println("started agent", res.PaneID)
	return nil
}

// parseSpawn turns the arguments of `perch spawn` into the request to send.
// It is separate from sending it so the parsing can be tested without an
// instance to spawn into.
//
// An error of errReported means the flag set has already said what was wrong;
// a nil request with a nil error means the caller asked for the usage.
func parseSpawn(args []string) (hooks.SpawnRequest, error) {
	var f spawnFlags
	fs := spawnFlagSet(&f)
	if err := fs.Parse(orderSpawnArgs(fs, args)); err != nil {
		// `spawn -h` is the user asking for the usage they have just been
		// given, not a failure to report on top of it.
		if errors.Is(err, flag.ErrHelp) {
			return hooks.SpawnRequest{}, errHelpAsked
		}
		return hooks.SpawnRequest{}, errReported
	}

	task := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if task == "" && !f.shell {
		fs.Usage()
		return hooks.SpawnRequest{}, fmt.Errorf("a task is required")
	}
	// A shell pane runs no agent, so an agent named alongside -shell is a
	// contradiction rather than a choice, and dropping it quietly would leave
	// the caller believing it had asked for something.
	if f.shell && (f.agent != "" || f.model != "") {
		return hooks.SpawnRequest{}, fmt.Errorf("-shell starts a shell, so it cannot also take -agent or -model")
	}
	if err := checkAgent(f.agent, f.model); err != nil {
		return hooks.SpawnRequest{}, err
	}
	return withAgent(hooks.SpawnRequest{
		Task:   task,
		Branch: f.worktree,
		Split:  f.split,
		Shell:  f.shell,
	}, f.agent, f.model), nil
}

// withAgent puts the agent and model `perch spawn` was given on the request it
// sends.
//
// They are two more fields on hooks.SpawnRequest, which is another task's file
// in this work. Until that lands this drops them, so the flags are parsed and
// checked against the catalog but the helper still starts as the default
// agent; the merge is this one function body.
var withAgent = func(req hooks.SpawnRequest, agentID, model string) hooks.SpawnRequest { return req }

// spawnFlags are the flags of `perch spawn` and where their values land.
type spawnFlags struct {
	worktree string
	agent    string
	model    string
	split    bool
	shell    bool
}

// spawnFlagSet defines the command line of `perch spawn`. It is built here
// rather than inline so that the reordering, the parsing and the tests all
// work from the one definition.
func spawnFlagSet(f *spawnFlags) *flag.FlagSet {
	fs := flag.NewFlagSet("spawn", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&f.worktree, "worktree", "", "branch name; the helper gets its own git worktree")
	fs.BoolVar(&f.split, "split", false, "place the helper beside this pane instead of in a new tab")
	fs.BoolVar(&f.shell, "shell", false, "start a shell instead of an agent")
	fs.StringVar(&f.agent, "agent", "", "`id` of the agent to start; perch agents lists them")
	fs.StringVar(&f.model, "model", "", "which of that agent's `model`s to ask for")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: perch spawn [flags] <task>\n\n")
		fmt.Fprintf(os.Stderr, "Starts another agent, working on <task>.\n\nFlags:\n")
		fs.PrintDefaults()
	}
	return fs
}

// orderSpawnArgs moves the flags in front of the task.
//
// Go's flag package stops at the first argument that is not a flag, which for
// this command is the task — so `perch spawn "watch the build" --split` parses
// no flags at all and quietly folds "--split" into the task text. The agent
// then gets a pane in a new tab, with a task ending in a word it did not
// write, and nothing anywhere says why. Since the task is the one argument
// that is never a flag, the two can be told apart wherever they appear.
//
// Anything after a bare "--" is task text, which is how a task whose own first
// word begins with a dash is written.
// Which flags take a value is read from fs rather than listed here, so a flag
// added to the command later cannot have its value swallowed into the task by
// a list nobody remembered to extend.
func orderSpawnArgs(fs *flag.FlagSet, args []string) []string {
	var flags, task []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			task = append(task, args[i+1:]...)
			break
		}
		// An unknown flag is still passed to the flag set rather than swallowed
		// into the task: `-h` has to reach it, and a typo has to be reported
		// rather than silently prepended to what the agent is asked to do.
		if len(arg) > 1 && arg[0] == '-' {
			flags = append(flags, arg)
			name, _, hasValue := strings.Cut(strings.TrimLeft(arg, "-"), "=")
			if !hasValue && takesValue(fs, name) && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
			continue
		}
		task = append(task, arg)
	}
	// The separator keeps a task word that begins with a dash — which only
	// reaches here after an explicit "--" — from being read as a flag again.
	return append(flags, append([]string{"--"}, task...)...)
}

// takesValue reports whether the named flag is followed by a value of its own.
// A boolean is not: `-split true` is -split with the word "true" left over,
// which for this command is the first word of the task.
//
// A flag the set does not define is answered no, so it is handed over alone
// and reported as the unknown flag it is rather than quietly eating the word
// after it.
func takesValue(fs *flag.FlagSet, name string) bool {
	f := fs.Lookup(name)
	if f == nil {
		return false
	}
	b, ok := f.Value.(interface{ IsBoolFlag() bool })
	return !ok || !b.IsBoolFlag()
}

// printAgents writes the catalog: the agents Perch can run, the models each
// one offers, and -- for one this machine does not have -- the line that says
// how to get it.
//
// An agent that is not installed is listed rather than left out, because
// somebody who has never installed Codex should still be able to learn from
// here that Perch would run it.
func printAgents(w io.Writer) {
	specs, defaultID := agentCatalog()
	fmt.Fprintf(w, "Agents perch can run.\n\n")
	for _, s := range specs {
		// A hidden entry is one somebody has taken out of the picker in their
		// own agents.json, and this is the picker in another form.
		if s.Hidden {
			continue
		}
		notes := []string{"not installed"}
		if agentAvailable(s) {
			notes = []string{"installed"}
		}
		if s.ID == defaultID {
			notes = append(notes, "default")
		}
		fmt.Fprintf(w, "%s - %s  [%s]\n", s.ID, agentName(s), strings.Join(notes, ", "))
		fmt.Fprintf(w, "  models: %s\n", modelSummary(s))
		// The install line answers the state just printed, so it is only worth
		// the room when that state is "not installed".
		if s.Install != "" && !agentAvailable(s) {
			fmt.Fprintf(w, "  install: %s\n", s.Install)
		}
		fmt.Fprintln(w)
	}
	fmt.Fprintf(w, "Choose one for a whole run with `perch -agent <id>`, or for a single\n")
	fmt.Fprintf(w, "helper with `perch spawn --agent <id> --model <model> <task>`.\n")
}

// agentName is what to call an agent in a message, falling back to its id for
// an entry in somebody's agents.json that was never given a name.
func agentName(s agent.Spec) string {
	if s.Name != "" {
		return s.Name
	}
	return s.ID
}

// modelSummary describes the models an agent offers, in one line.
//
// A model with an empty id is the entry meaning "leave the choice alone",
// which reads as nothing at all in a list of names, so it is spelled out
// instead. An agent with no models listed takes whatever it is given, which is
// how a local endpoint whose models depend on what is loaded is written.
func modelSummary(s agent.Spec) string {
	ids := modelIDs(s)
	unset := false
	for _, m := range s.Models {
		if m.ID == "" {
			unset = true
		}
	}
	switch {
	case len(ids) == 0:
		return "whatever you ask it for"
	case unset:
		return strings.Join(ids, ", ") + ", or none for whatever it is already set to"
	}
	return strings.Join(ids, ", ")
}

// modelIDs are the models an agent names, without the empty one.
func modelIDs(s agent.Spec) []string {
	var out []string
	for _, m := range s.Models {
		if m.ID != "" {
			out = append(out, m.ID)
		}
	}
	return out
}

// agentIDs are the ids of every agent the picker would show.
func agentIDs(specs []agent.Spec) []string {
	var out []string
	for _, s := range specs {
		if !s.Hidden {
			out = append(out, s.ID)
		}
	}
	return out
}

// findSpec looks an agent up by id.
func findSpec(specs []agent.Spec, id string) (agent.Spec, bool) {
	for _, s := range specs {
		if s.ID == id && !s.Hidden {
			return s, true
		}
	}
	return agent.Spec{}, false
}

// offersModel reports whether an agent can be asked for a model.
//
// An empty model is the choice not having been made, which every agent accepts
// -- a CLI then keeps whatever it was configured with. One that lists no
// models takes whatever it is given, so there is nothing there to be wrong
// either.
func offersModel(s agent.Spec, model string) bool {
	if model == "" || len(s.Models) == 0 {
		return true
	}
	for _, m := range s.Models {
		if m.ID == model {
			return true
		}
	}
	return false
}

// checkAgent checks an agent and a model named on the command line against the
// catalog, and says what is on offer when one of them is not in it.
//
// It is checked here rather than where the pane is started because this is the
// only place with somewhere to print to. A misspelt agent that gets that far
// is a pane that never starts, in a window that is already open, with the
// reason nowhere at all.
//
// A model given without an agent is checked against the whole catalog rather
// than against any one entry, because which agent it will belong to is the
// project's default and is not known until the pane is made. That still
// catches the typo, which is the point of checking at all.
func checkAgent(id, model string) error {
	specs, _ := agentCatalog()
	if id != "" {
		spec, ok := findSpec(specs, id)
		if !ok {
			return fmt.Errorf("no agent called %q; perch can run %s (run `perch agents` for what each of them offers)",
				id, strings.Join(agentIDs(specs), ", "))
		}
		if !offersModel(spec, model) {
			return fmt.Errorf("%s has no model called %q; it offers %s",
				spec.ID, model, strings.Join(modelIDs(spec), ", "))
		}
		return nil
	}
	if model == "" {
		return nil
	}
	for _, s := range specs {
		if !s.Hidden && offersModel(s, model) {
			return nil
		}
	}
	return fmt.Errorf("no agent offers a model called %q; run `perch agents` to see what each of them does", model)
}

// agentCatalog is how the command line reaches the catalog: the agents Perch
// can run -- the built-in ones overlaid with the user's agents.json -- and the
// id of the one a pane takes when nothing has been chosen.
//
// It is a variable so a test can hand it a catalog of its own, and because the
// catalog itself is another task's file in this work. Until that lands it
// answers with the one agent Perch has always run, so that the command line
// can be finished and checked against something real; the merge repoints this
// at internal/agent and deletes what is below it.
var agentCatalog = func() ([]agent.Spec, string) { return []agent.Spec{claudeSpec}, claudeSpec.ID }

// claudeSpec is the catalog Perch has always had: one entry. Only the fields
// the command line itself reads are here -- the argument lists, the
// capabilities and the environment it strips belong with the real catalog.
var claudeSpec = agent.Spec{
	ID: "claude", Name: "Claude Code", Runner: agent.RunnerCLI, Exe: "claude",
	Models: []agent.Model{
		{ID: "", Name: "Default", Note: "whatever the CLI is set to"},
		{ID: "opus", Name: "Opus", Note: "most capable"},
		{ID: "sonnet", Name: "Sonnet", Note: "the everyday one"},
		{ID: "haiku", Name: "Haiku", Note: "fastest"},
	},
	Install: "https://claude.com/claude-code",
}

// agentAvailable reports whether an agent could actually be started here.
var agentAvailable = installedAgent

// installedAgent is availability as far as the command line can tell on its
// own: a CLI has to be on PATH, and an API needs a key in the environment or
// an endpoint on this machine that wants none.
//
// The catalog's own answer also looks in Perch's key store, which is another
// task's file in this work; until that lands an API agent whose key lives only
// there is listed as not installed, which understates what Perch can do rather
// than promising something that will not start.
func installedAgent(s agent.Spec) bool {
	if s.Runner == agent.RunnerAPI {
		for _, name := range s.API.KeyEnv {
			if os.Getenv(name) != "" {
				return true
			}
		}
		return loopbackEndpoint(s.API.BaseURL)
	}
	if s.Exe == "" {
		return false
	}
	_, err := exec.LookPath(s.Exe)
	return err == nil
}

// loopbackEndpoint reports whether a base URL points at this machine, which is
// how an endpoint that wants no key -- Ollama, LM Studio, a gateway of one's
// own -- is told from a vendor's.
func loopbackEndpoint(base string) bool {
	if base == "" {
		return false
	}
	u, err := url.Parse(base)
	if err != nil {
		return false
	}
	if u.Hostname() == "localhost" {
		return true
	}
	ip := net.ParseIP(u.Hostname())
	return ip != nil && ip.IsLoopback()
}

// runHook implements the hidden `hook` subcommand invoked by Claude Code.
// It must never fail loudly: a broken hook would disrupt the agent session it
// is only meant to observe.
func runHook(args []string) {
	fs := flag.NewFlagSet("hook", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		endpoint = fs.String("endpoint", "", "Perch hook endpoint")
		token    = fs.String("token", "", "shared secret")
		sessID   = fs.String("session", "", "pane session id")
		event    = fs.String("event", "", "lifecycle event name")
	)
	if err := fs.Parse(args); err != nil {
		return
	}
	// Stderr is the one place a hook can complain: Claude Code shows it under
	// --debug, and it neither fails the session nor lands in the stdout that
	// SessionStart parses as JSON. Without it a hook that never arrives leaves
	// no trace at all, only panes whose status stops changing.
	if *endpoint == "" || *sessID == "" || *event == "" {
		fmt.Fprintln(os.Stderr, "perch hook: -endpoint, -session and -event are all required")
		return
	}
	ctx, err := hooks.Emit(os.Stdin, *endpoint, *token, *sessID, *event)
	if err != nil {
		fmt.Fprintln(os.Stderr, "perch hook:", err)
		return
	}
	if strings.TrimSpace(ctx) == "" {
		return
	}
	// SessionStart is the one event that answers back. Claude Code reads a
	// hook's stdout for this event and adds `additionalContext` to the
	// session, which is how a pane's agent learns which pane it is.
	out, err := json.Marshal(hookOutput{
		HookSpecificOutput: hookSpecificOutput{
			HookEventName:     *event,
			AdditionalContext: ctx,
		},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "perch hook: could not encode the session context:", err)
		return
	}
	_, _ = os.Stdout.Write(out)
}

// hookOutput is the JSON a command hook prints to feed context back into the
// session that invoked it.
type hookOutput struct {
	HookSpecificOutput hookSpecificOutput `json:"hookSpecificOutput"`
}

type hookSpecificOutput struct {
	HookEventName     string `json:"hookEventName"`
	AdditionalContext string `json:"additionalContext"`
}
