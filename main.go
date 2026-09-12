// Command flockdeck is a desktop application for running several Claude
// Code agents at once.
//
// It drives the `claude` CLI in real pseudo-terminals, so every agent behaves
// exactly as it does in a normal terminal, and presents them in a window with
// tabs and split panes, per-pane status, layout persistence, git worktrees and
// broadcast input.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/appwindow"
	"github.com/jmwri/flockdeck/internal/hooks"
	"github.com/jmwri/flockdeck/internal/remote"
	"github.com/jmwri/flockdeck/internal/server"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/store"
	"github.com/jmwri/flockdeck/internal/workspace"
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
				fmt.Fprintln(os.Stderr, "flockdeck spawn:", err)
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
	// `chat` is Flockdeck's own chat client, which is what an API agent's pane
	// runs. It is started by the pane rather than by a person, and it is a
	// subcommand for the same reason `hook` is: one artifact to ship, and a
	// pane that can run Flockdeck can run everything Flockdeck does.
	if len(os.Args) > 1 && os.Args[1] == "chat" {
		if err := runChat(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "flockdeck chat:", err)
			os.Exit(1)
		}
		return
	}
	// `keys` is where an API agent's key is set, and it reads the key from
	// stdin so that it never lands in shell history.
	if len(os.Args) > 1 && os.Args[1] == "keys" {
		if err := runKeys(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "flockdeck keys:", err)
			os.Exit(1)
		}
		return
	}
	// `remote` enrols this machine with a relay, so the window can be opened
	// from another device, and manages what is paired with it.
	if len(os.Args) > 1 && os.Args[1] == "remote" {
		if err := runRemote(os.Args[2:]); err != nil {
			if !errors.Is(err, errReported) {
				fmt.Fprintln(os.Stderr, "flockdeck remote:", err)
			}
			os.Exit(1)
		}
		return
	}
	// `update` fetches the latest release and puts it in place. It is a
	// subcommand rather than something only the interface can do, so that a
	// detached instance, or one being run from a terminal, can be updated
	// without opening a window to click in.
	if len(os.Args) > 1 && os.Args[1] == "update" {
		if err := runUpdate(os.Args[2:]); err != nil {
			if !errors.Is(err, errReported) {
				fmt.Fprintln(os.Stderr, "flockdeck update:", err)
			}
			os.Exit(1)
		}
		return
	}

	// `help` is what somebody who has never run the program types first, and
	// they were told it was an unrecognised argument, with exit status 2,
	// before being shown the usage -h would have printed.
	if len(os.Args) > 1 && os.Args[1] == "help" {
		flockdeckFlagSet(&cliFlags{}).Usage()
		return
	}

	var c cliFlags
	fs := flockdeckFlagSet(&c)
	_ = fs.Parse(os.Args[1:]) // ExitOnError: a bad flag has already ended us
	fs.Visit(func(f *flag.Flag) { c.dirGiven = c.dirGiven || f.Name == "C" })

	if c.version {
		// The platform is part of the answer: it is what a bug report needs
		// alongside the version, and what says which archive to download.
		fmt.Printf("flockdeck %s (%s/%s)\n", version, runtime.GOOS, runtime.GOARCH)
		return
	}

	// Anything left over is a mistyped flag or a subcommand that does not
	// exist. Ignoring it would open a window on the current directory and
	// leave the user believing `flockdeck quit` had done something.
	if fs.NArg() > 0 {
		arg := fs.Arg(0)
		// Between plain quotes rather than %q, which would double every
		// backslash of a Windows path and show a different one from the
		// one typed.
		fmt.Fprintf(os.Stderr, "flockdeck: unrecognised argument \"%s\"\n", arg)
		switch fi, statErr := os.Stat(arg); {
		case statErr == nil && fi.IsDir():
			// Quoted when it would otherwise split, so the line can be copied
			// as it stands. strconv.Quote is no use here: it doubles every
			// backslash in a Windows path.
			if strings.ContainsAny(arg, " \t") {
				arg = `"` + arg + `"`
			}
			fmt.Fprintf(os.Stderr, "To open that directory: flockdeck -C %s\n", arg)
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
		fmt.Fprintln(os.Stderr, "flockdeck:", err)
		os.Exit(2)
	}
	if w := notInstalledWarning(c.agent); w != "" {
		fmt.Fprintln(os.Stderr, "flockdeck:", w)
	}
	server.Version = version

	if c.quit {
		// Nothing running is nothing to do rather than a failure: it used to
		// exit 1, append to error.log and, on Windows, put up an error box,
		// all for a request that had already got what it asked for.
		switch err := quitRunning(); {
		case errors.Is(err, errNoneRunning):
			fmt.Println("flockdeck: nothing is running, so there is nothing to stop")
		case err != nil:
			fail("Flockdeck could not stop the running instance.", err)
		}
		return
	}

	if err := run(c.options); err != nil {
		fail("Flockdeck could not start.", err)
	}
}

// cliFlags are the top-level flags and where their values land. The two that
// are acted on here rather than passed to run sit alongside the rest.
type cliFlags struct {
	options
	quit    bool
	version bool
}

// flockdeckFlagSet defines the top-level command line. It is built here rather
// than inline in main so that a test can walk the same set the program uses
// and check the help documents it.
func flockdeckFlagSet(c *cliFlags) *flag.FlagSet {
	fs := flag.NewFlagSet("flockdeck", flag.ExitOnError)
	fs.StringVar(&c.dir, "C", ".", "directory to open the workspace on")
	fs.StringVar(&c.agent, "agent", "", "`id` of the agent new panes start as for this run; flockdeck agents lists them")
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
	fmt.Fprintf(out, "flockdeck — run several coding agents at once, in tabs and split panes.\n\n")
	fmt.Fprintf(out, "Usage:\n  flockdeck [flags]\n\nFlags:\n")
	fs.PrintDefaults()
	fmt.Fprintf(out, "\nSubcommands:\n")
	fmt.Fprintf(out, "  spawn [--worktree <branch>] [--split] [--shell] [--agent <id>] [--model <model>] <task>\n")
	fmt.Fprintf(out, "        start another agent; run from inside a pane\n")
	fmt.Fprintf(out, "        run flockdeck spawn -h for what the flags do\n")
	fmt.Fprintf(out, "  agents\n")
	fmt.Fprintf(out, "        list the agents flockdeck can run, with their models\n")
	fmt.Fprintf(out, "  chat [flags]\n")
	fmt.Fprintf(out, "        flockdeck's own chat client, which an API agent's pane runs; chat -h for its flags\n")
	fmt.Fprintf(out, "  keys [list|set <agent>|clear <agent>]\n")
	fmt.Fprintf(out, "        the API keys agents talk to a model API with\n")
	fmt.Fprintf(out, "  remote enable [-relay <url>] [-name <name>] [-join <code>] [-invite <code>]\n")
	fmt.Fprintf(out, "        enrol this machine with a relay, so another device can reach its agents\n")
	fmt.Fprintf(out, "  remote pair [-desktop] | status | devices | revoke <id> | disable [-force]\n")
	fmt.Fprintf(out, "        pair a device, and see or change what is paired\n")
	fmt.Fprintf(out, "  update [-check]\n")
	fmt.Fprintf(out, "        fetch the latest release and put it in place\n")
	// These are settings with no flag, so this is the only place a person
	// reading the usage would learn that they exist.
	fmt.Fprintf(out, "\nEnvironment:\n")
	fmt.Fprintf(out, "  %s=<program>\n", appwindow.BrowserEnv)
	fmt.Fprintf(out, "        the browser that provides the window, instead of the first one found\n")
	fmt.Fprintf(out, "  %s=off\n", updateEnv)
	fmt.Fprintf(out, "        do not look for new releases in the background; update still works\n")
	fmt.Fprintf(out, "  %s=<url>\n", remote.RelayEnv)
	fmt.Fprintf(out, "        the relay remote access goes through, instead of the default one\n")
	fmt.Fprintf(out, "\nRunning it again attaches to an instance that is already going.\n")
	fmt.Fprintf(out, "Press F1 in the window for the help: the shortcuts, and how the rest of it works.\n")
}

// fail reports an error that ends the run, under heading, which says what was
// being attempted. When the process was started from a desktop shortcut there
// is no console to print to, so the message is also written to a log file the
// user can be pointed at.
func fail(heading string, err error) {
	fmt.Fprintln(os.Stderr, "flockdeck:", err)
	text := heading + "\n\n" + err.Error()
	if dir, dirErr := store.Dir(); dirErr == nil {
		path := filepath.Join(dir, "error.log")
		stamp := time.Now().Format(time.RFC3339)
		if f, openErr := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); openErr == nil {
			fmt.Fprintf(f, "%s %v\n", stamp, err)
			f.Close()
			text += "\n\nThis is also written to " + path
		}
	}
	showStartupError(text)
	os.Exit(1)
}

// options are the settings run needs.
type options struct {
	dir      string
	dirGiven bool // -C was on the command line, rather than dir being its default
	agent    string
	fresh    bool
	shell    bool
	noWindow bool
	detach   bool
	solo     bool
}

// quitRunning stops an instance that is already going.
func quitRunning() error {
	inst, err := store.LoadInstance()
	// A record that cannot be read is not the same as there being nothing to
	// stop: reporting it as "none found" sends the user looking for a process
	// that is very likely still running.
	if err != nil {
		return fmt.Errorf("read the record of the running instance: %w", err)
	}
	if inst == nil {
		return errNoneRunning
	}
	// The instance is asked outright rather than probed first. One whose
	// workspace has wedged answers the probe as too busy, which read as
	// nothing running at all — and a wedged instance is exactly the one
	// somebody reaches for -quit to stop, while the request to quit needs
	// nothing from the workspace. Only a request that cannot even connect
	// means the record was left by an instance that has gone.
	//
	// RequestQuit returns only once the instance has stopped answering, not
	// when it has taken the request: saving every project and stopping the
	// agents comes after, with the instance still listening, and a
	// `flockdeck -quit && flockdeck` would otherwise attach to one on its way
	// out. So "stopped" below is true when it is printed.
	if err := server.RequestQuit(inst.URL, inst.Token); err != nil {
		var dial *net.OpError
		if errors.As(err, &dial) && dial.Op == "dial" {
			_ = store.ClearInstance()
			return errNoneRunning
		}
		return fmt.Errorf("ask the instance at %s to stop: %w", inst.URL, err)
	}
	fmt.Println("flockdeck: stopped")
	return nil
}

// errNoneRunning is what -quit reports when there is nothing to stop.
var errNoneRunning = errors.New("no running flockdeck found")

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

// answering reports whether the instance on record is running and answering.
// A record left by one that has gone is cleared on the way, as for any launch.
func answering() bool {
	inst, _, err := runningInstance()
	return err == nil && inst != nil
}

// attach hands the requested project to an instance that is already running
// and shows a window onto it, so a second launch joins the agents already
// going instead of starting a rival set.
func attach(inst *store.Instance, base, root string, noWindow bool) error {
	if err := server.RequestOpen(base, inst.Token, root); err != nil {
		// On its own this was "open project: 400 Bad Request", which says
		// neither what was being attempted nor what else there is to do.
		return fmt.Errorf("the flockdeck already running would not open %s (%w); run with -solo to start a separate one", root, err)
	}
	url := base + "/?t=" + inst.Token
	if noWindow {
		fmt.Println("flockdeck is already running at:")
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
// that has just been overwritten, so `flockdeck -quit` will not find it either.
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
// It is a variable so that a test can watch the name arrive without building a
// workspace to hold it.
var useAgent = func(ws *workspace.Workspace, agentID string) {
	if agentID == "" {
		return
	}
	ws.UseAgent(agentID)
}

// run starts the workspace, serves it and shows the window.
func run(opts options) error {
	// Anything a previous update moved aside can go now, before the interface
	// is up and while nothing is looking.
	sweepReplacedBinary()

	// A staged update goes in, and a restart starts the program again, only
	// once everything else has been torn down, which is why this is deferred
	// first and so runs last. Both used to happen ahead of the deferred
	// closes below: the new process came up while the old window was still
	// open, the old instance still recorded and every old agent still running
	// — and set about resuming the very conversations those agents still had
	// open, because closing a pane only signals its process.
	exiting, reopen := false, ""
	defer func() {
		if !exiting {
			return
		}
		applyStagedUpdate(os.Stderr)
		if restarting.Load() {
			if err := relaunch(reopen); err != nil {
				fmt.Fprintln(os.Stderr, "flockdeck: could not start again:", err)
			}
		}
	}()

	root, err := filepath.Abs(expandHome(opts.dir))
	if err != nil {
		return fmt.Errorf("resolve %s: %w", opts.dir, err)
	}
	if !opts.dirGiven {
		saved, _ := store.LoadSession()
		root = landingRoot(root, startedAs, saved)
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
			fmt.Fprintln(os.Stderr, "flockdeck:", text)
		}); inst != nil {
			// The flags that describe how to start up have nobody to apply
			// to once we are joining agents that are already running. Say so:
			// silently ignoring -new looks like the layout was kept on purpose.
			if ignored := startupOnlyFlags(opts); len(ignored) > 0 {
				fmt.Fprintf(os.Stderr,
					"flockdeck: joining the instance already running, so %s %s no effect here (use -solo to start a separate one)\n",
					strings.Join(ignored, " and "), plural(len(ignored), "has", "have"))
			}
			// -detach asks for no window, and that much of it still holds
			// when joining: the project goes to the running instance and
			// its address is printed, rather than a window being opened by
			// the one flag that asked for none.
			return attach(inst, base, root, opts.noWindow || opts.detach)
		}
	}

	// -new starts this project from nothing, but the save on the way out
	// writes the list of open projects from what this run had, which was only
	// this one. Every other project the user had open fell out of the next
	// start without a word, from a flag that said nothing about them, so the
	// list is kept to be put back once this run's own has been written.
	var before *store.Session
	if opts.fresh {
		before, _ = store.LoadSession()
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
	} else if opts.shell {
		// -shell decides what the first pane is, and a restored layout
		// already has its panes. Said, rather than dropped without a word,
		// as the flags that mean nothing when joining a running instance are.
		fmt.Fprintln(os.Stderr, "flockdeck: -shell has no effect, since this project's saved layout was restored (use -new -shell to start from a single shell)")
	}

	srv, err := server.New(ws)
	if err != nil {
		return err
	}
	defer srv.Close()
	ws.SetWake(srv.Wake)

	// Remote access, for a machine enrolled with a relay. It is started from
	// whatever the enrolment says now and told to look again whenever
	// `flockdeck remote` changes it. A machine that is not enrolled runs none of
	// it: nothing is dialled and nothing is shown.
	remoteAccess := remote.NewManager(version, srv.ServeRemote, srv.Wake)
	srv.SetRemote(remoteAccess)
	if err := remoteAccess.Reload(); err != nil {
		fmt.Fprintln(os.Stderr, "flockdeck: remote access:", err)
	}
	defer remoteAccess.Close()

	// Shutdown can be requested by the window closing, by a signal, or by the
	// user quitting from the UI.
	quit := make(chan struct{})
	var stopping sync.Once
	stop := func() {
		stopping.Do(func() {
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
		})
	}

	// Two deep, so a second interrupt arriving while the first is still being
	// acted on is not dropped on the floor.
	//
	// SIGTERM is what a logout, a shutdown, `kill` or a service manager
	// sends, and what Windows makes of the console being closed. Left to the
	// runtime it ended the process on the spot: no layout or open projects
	// saved, agents not stopped, the instance record left behind.
	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	go interrupts(sigs, stop, forceQuit)

	srv.OnQuit = stop
	// A restart is a quit that comes back. The server only asks; the shutdown
	// path decides what that means, which keeps the order — save, replace,
	// start again — in one place.
	srv.OnRestart = func() {
		restarting.Store(true)
		stop()
	}

	// Record where this instance is listening so a later launch can attach.
	// Only now, with the callbacks that answer for it in place: the server has
	// been serving since it was made, and a `flockdeck -quit` that found the
	// record any sooner was told yes and then ignored, since there was nothing
	// yet to hand the request to.
	//
	// A -solo run beside an instance that is still answering leaves that
	// one's record alone. Writing over it made the first instance unreachable
	// — no later launch could attach to it or -quit it — and when this run
	// quit, it took the record with it, leaving nothing on record while the
	// first went on running: the next launch started a rival, and the start-up
	// sweep stopped sparing the first one's pane settings.
	if !opts.solo || !answering() {
		if err := store.SaveInstance(&store.Instance{
			PID: os.Getpid(), URL: srv.BaseURL(), Token: srv.Token(), Started: time.Now(),
		}); err != nil {
			fmt.Fprintln(os.Stderr, "flockdeck: could not record the instance:", err)
		}
		defer store.ClearInstance()
	}

	// Watching for releases runs for the life of the server and stops with it,
	// so a check in flight cannot hold the shutdown open.
	updateCtx, stopUpdates := context.WithCancel(context.Background())
	defer stopUpdates()
	go watchForUpdates(updateCtx, srv)
	if opts.detach {
		srv.Detach()
	}

	win, err := showWindow(opts, srv, stop)
	if err != nil {
		return err
	}
	defer win.Close()

	<-quit

	// The tunnel is a way in like the local port, so it is closed with it,
	// before anything is saved.
	stopServing := func() error {
		remoteAccess.Close()
		return srv.Close()
	}
	if err := shutdown(stopServing, ws.SaveAll); err != nil {
		fmt.Fprintln(os.Stderr, "flockdeck: could not save layout:", err)
	}
	if err := keepOpenProjects(before); err != nil {
		fmt.Fprintln(os.Stderr, "flockdeck: could not keep the list of open projects:", err)
	}

	// Once the deferred closes have run, the interface is closed and the panes
	// are gone, which is the only moment the program's own file can be
	// replaced without pulling it out from under a running session. The first
	// defer puts a staged update in then, so the next start — whether the
	// user's or a restart's — is the new version.
	exiting, reopen = true, ws.ActiveRoot()
	return nil
}

// showWindow puts the interface in front of the user, or for a run without a
// window says where it is, and arranges for stop to be called once nobody is
// looking at it any more. The window is nil when none was opened.
func showWindow(opts options, srv *server.Server, stop func()) (*appwindow.Window, error) {
	if opts.noWindow || opts.detach {
		fmt.Println("flockdeck serving at:")
		fmt.Println(" ", srv.URL())
		if opts.detach {
			fmt.Println("Running detached. Attach with `flockdeck`, stop with `flockdeck -quit`.")
		} else {
			// Ctrl+C alone was the advice, but the Windows build is linked
			// for the GUI subsystem and is not attached to the console it was
			// started from, so the key never reaches it there.
			fmt.Println("Press Ctrl+C, or run `flockdeck -quit`, to stop.")
		}
		return nil, nil
	}

	profile, err := store.BrowserProfileDir()
	if err != nil {
		return nil, err
	}
	win, err := appwindow.Open(srv.URL(), profile)
	if err != nil {
		// Unlike attaching, this server is ours and stops with us, so the
		// address it was serving will not answer by the time anyone reads
		// this. Name the ways to get a window instead.
		if errors.Is(err, appwindow.ErrNoBrowser) {
			return nil, fmt.Errorf("%w — set %s to one, or run `flockdeck -no-window` and open the URL it prints",
				err, appwindow.BrowserEnv)
		}
		return nil, fmt.Errorf("open the window: %w — or run `flockdeck -no-window` and open the URL it prints", err)
	}

	if win.AppMode {
		// The window process ending is the user closing the application,
		// unless they asked to leave the agents running — or unless it
		// handed the window to a browser already running, which leaves
		// only the connection below to tell when it closes.
		go func() {
			if errors.Is(win.Wait(), appwindow.ErrHandedOff) {
				return
			}
			if !srv.Detached() {
				stop()
			}
		}()
	} else {
		// A tab in the user's own browser cannot be watched, so fall back
		// to shutting down when the page disconnects.
		fmt.Println("Opened in your browser:", srv.URL())
	}

	// Whichever way the UI is shown, losing every connected window for more
	// than a moment means nobody is looking any more. Only the windows on
	// this machine count: one open through the relay is somebody elsewhere,
	// and detaching is how the agents are left running for them.
	srv.OnLastClientGone = func() {
		go func() {
			time.Sleep(windowGrace)
			if srv.LocalClientCount() == 0 && !srv.Detached() {
				stop()
			}
		}()
	}
	return win, nil
}

// landingRoot is the project a launch opens when none was named: cwd, the
// directory it was started in, unless that is only where the program itself
// lives or where the system starts a program it was told nothing about.
//
// From a terminal the working directory is where the user is standing, and
// exactly right. A double-click, or a shortcut left as it was made, starts the
// program in its own folder instead — Downloads, as often as not — and opening
// that made every such start add the folder as a project, with a fresh agent
// in it, beside the session the user actually had. A scheduled or login start
// is worse: Task Scheduler with no "Start in" runs it in System32, launchd in
// /, and that became a project, with an agent working in it, which every
// start after reopened. The project the user was last in is the better answer
// in both cases, when there is one to go back to; failing that a system
// directory gives way to the home directory, while the program's own folder,
// where somebody did choose to put it, stays.
func landingRoot(cwd, exe string, saved *store.Session) string {
	system := systemDir(cwd)
	if !system && (exe == "" || !sameFolder(cwd, filepath.Dir(exe))) {
		return cwd
	}
	if saved != nil && saved.Active != "" {
		if fi, err := os.Stat(saved.Active); err == nil && fi.IsDir() {
			return saved.Active
		}
	}
	if system {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
	}
	return cwd
}

// systemDir reports whether dir is where the system starts a program that was
// given no directory of its own: the Windows directory or anything below it,
// or the root of the file system.
func systemDir(dir string) bool {
	dir = filepath.Clean(dir)
	if runtime.GOOS != "windows" {
		return dir == "/"
	}
	root := os.Getenv("SystemRoot")
	if root == "" {
		return false
	}
	root = filepath.Clean(root)
	return sameFolder(dir, root) || strings.HasPrefix(strings.ToLower(dir), strings.ToLower(root)+`\`)
}

// expandHome reads a leading ~ as the home directory.
//
// A Unix shell does that before flockdeck ever sees the path, but PowerShell
// and cmd.exe hand it over as typed, so `flockdeck -C ~/code/api` — the very
// form the README and the usage give — became a directory called ~ below
// wherever the command was run, and failed as one that did not exist.
// ~user is a shell's business and is left alone.
func expandHome(p string) string {
	if p != "~" && !strings.HasPrefix(p, "~/") && !strings.HasPrefix(p, `~\`) {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return filepath.Join(home, p[1:])
}

// sameFolder compares two directories the way the file systems holding them
// do: without regard to case on Windows and macOS.
func sameFolder(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// keepOpenProjects adds the projects open before a -new run back to the
// session that run has just saved, after its own, which keeps its active
// project the one the next start lands in. SaveSession drops the ones both
// lists name. It does nothing for a run that was not started with -new.
func keepOpenProjects(before *store.Session) error {
	if before == nil {
		return nil
	}
	now, err := store.LoadSession()
	if err != nil || now == nil {
		return err
	}
	now.Open = append(now.Open, before.Open...)
	return store.SaveSession(now)
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
	fmt.Fprintln(os.Stderr, "flockdeck: shutting down is taking too long — stopping now")
	os.Exit(1)
}

// errReported marks an error the failing code has already printed, so the
// caller exits without repeating it.
var errReported = errors.New("already reported")

// errHelpAsked marks `spawn -h`: the usage has been printed and there is
// nothing left to do, so the command succeeds rather than failing.
var errHelpAsked = errors.New("usage shown")

// paneEnv reads one of the variables a pane carries, accepting the name an
// earlier build used alongside the one in use now. A pane started by an
// instance of that build is still running with the old names in its
// environment, and an agent inside it should not lose the ability to spawn
// just because the binary on its PATH has been upgraded. The fallback can go a
// release after the rename.
func paneEnv(name string) string {
	if v := os.Getenv("FLOCKDECK_" + name); v != "" {
		return v
	}
	return os.Getenv("PERCH_" + name)
}

// runSpawn implements the `spawn` subcommand, which starts another agent from
// inside a pane.
//
// It exists so a lead agent can split its own work up: given a plan, it can run
// this once per task and watch the helpers appear beside it. The address and
// token come from the environment its pane was started with, so only processes
// running inside a pane can use it.
func runSpawn(args []string) error {
	req, err := parseSpawn(args)
	if errors.Is(err, errHelpAsked) {
		return nil
	}
	if err != nil {
		return err
	}
	if w := notInstalledWarning(req.Agent); w != "" {
		fmt.Fprintln(os.Stderr, "flockdeck spawn:", w)
	}
	api := paneEnv("API")
	token := paneEnv("TOKEN")
	pane := paneEnv("PANE")
	if api == "" || token == "" {
		return fmt.Errorf("this only works inside a flockdeck pane")
	}

	res, err := hooks.Spawn(api, token, pane, req)
	if err != nil {
		return explainSpawn(err)
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

// explainSpawn says in words what a spawn that ran out of time means. The
// instance is given a minute, and one that takes longer is busy — creating
// several worktrees, say — rather than refusing; left alone the agent was told
// `Post "http://127.0.0.1:…/spawn": context deadline exceeded`, which names a
// URL and a deadline and not what to do. Any other failure is passed on.
func explainSpawn(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return errors.New("the flockdeck this pane belongs to did not answer within a minute; it is probably busy, so try again shortly")
	}
	return err
}

// parseSpawn turns the arguments of `flockdeck spawn` into the request to send.
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
	return hooks.SpawnRequest{
		Task:   task,
		Branch: f.worktree,
		Split:  f.split,
		Shell:  f.shell,
		Agent:  f.agent,
		Model:  f.model,
	}, nil
}

// spawnFlags are the flags of `flockdeck spawn` and where their values land.
type spawnFlags struct {
	worktree string
	agent    string
	model    string
	split    bool
	shell    bool
}

// spawnFlagSet defines the command line of `flockdeck spawn`. It is built here
// rather than inline so that the reordering, the parsing and the tests all
// work from the one definition.
func spawnFlagSet(f *spawnFlags) *flag.FlagSet {
	fs := flag.NewFlagSet("spawn", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&f.worktree, "worktree", "", "branch name; the helper gets its own git worktree")
	fs.BoolVar(&f.split, "split", false, "place the helper beside this pane instead of in a new tab")
	fs.BoolVar(&f.shell, "shell", false, "start a shell instead of an agent")
	fs.StringVar(&f.agent, "agent", "", "`id` of the agent to start; flockdeck agents lists them")
	fs.StringVar(&f.model, "model", "", "which of that agent's `model`s to ask for")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: flockdeck spawn [flags] <task>\n\n")
		fmt.Fprintf(os.Stderr, "Starts another agent, working on <task>.\n\nFlags:\n")
		fs.PrintDefaults()
	}
	return fs
}

// orderSpawnArgs moves the flags in front of the task.
//
// Go's flag package stops at the first argument that is not a flag, which for
// this command is the task — so `flockdeck spawn "watch the build" --split` parses
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

// printAgents writes the catalog: the agents Flockdeck can run, the models each
// one offers, and -- for one this machine does not have -- the line that says
// how to get it.
//
// An agent that is not installed is listed rather than left out, because
// somebody who has never installed Codex should still be able to learn from
// here that Flockdeck would run it.
func printAgents(w io.Writer) {
	specs, defaultID, notice := agentCatalog()
	fmt.Fprintf(w, "Agents flockdeck can run.\n\n")
	if notice != "" {
		fmt.Fprintf(w, "Note: %s\n\n", unreadAgentsFile(notice))
	}
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
	fmt.Fprintf(w, "Choose one for a whole run with `flockdeck -agent <id>`, or for a single\n")
	fmt.Fprintf(w, "helper with `flockdeck spawn --agent <id> --model <model> <task>`.\n")
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
	specs, _, notice := agentCatalog()
	if id != "" {
		spec, ok := findSpec(specs, id)
		if !ok {
			err := fmt.Errorf("no agent called %q; flockdeck can run %s (run `flockdeck agents` for what each of them offers)",
				id, strings.Join(agentIDs(specs), ", "))
			if notice != "" {
				err = fmt.Errorf("%w. Also, %s", err, unreadAgentsFile(notice))
			}
			return err
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
	return fmt.Errorf("no agent offers a model called %q; run `flockdeck agents` to see what each of them does", model)
}

// notInstalledWarning is what to say when the agent named on the command line
// is in the catalog but cannot be started on this machine, or "" when there is
// nothing to say.
//
// checkAgent only asks whether the name exists. Without this, `-agent codex`
// on a machine with no Codex opened a window of panes that would not start,
// with the reason only in the window. It is a warning rather than a refusal
// because whether an agent is there is a probe, not a certainty.
func notInstalledWarning(id string) string {
	if id == "" {
		return ""
	}
	specs, _, _ := agentCatalog()
	s, ok := findSpec(specs, id)
	if !ok || agentAvailable(s) {
		return ""
	}
	msg := agentName(s) + " is not installed here, so its panes will not start"
	if s.Install != "" {
		msg += "; to install it: " + s.Install
	}
	return msg
}

// agentCatalog is how the command line reaches the catalog: the agents Flockdeck
// can run -- the built-in ones overlaid with the user's agents.json -- and the
// id of the one a pane takes when nothing has been chosen.
//
// It is a variable so that a test can hand it a catalog of its own.
//
// The third value is the catalog's notice: empty unless the user's agents.json
// could not be used in full, in which case an agent defined there may be
// missing from the rest, and a name that is not found has to say why.
var agentCatalog = func() ([]agent.Spec, string, string) {
	c := agent.Load()
	// The command line is not standing in any one project, so what it checks a
	// name against is the installation's default rather than a project's. A
	// project that runs something else of its own is answered where the pane is
	// made, which is the only place the directory is known.
	return c.Visible(), c.DefaultsFor("").Agent, c.Notice
}

// unreadAgentsFile says what a catalog notice means for the command line.
func unreadAgentsFile(notice string) string {
	return "your agents.json was not fully read, so an agent defined there may be missing: " + notice
}

// agentAvailable reports whether an agent could actually be started here.
// The catalog's own answer is the whole of it: a CLI on PATH, or an API whose
// key is in the environment or in Flockdeck's own store, or an endpoint on this
// machine that wants no key at all.
var agentAvailable = agent.Available

// runHook implements the hidden `hook` subcommand invoked by Claude Code.
// It must never fail loudly: a broken hook would disrupt the agent session it
// is only meant to observe.
func runHook(args []string) {
	fs := flag.NewFlagSet("hook", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		endpoint = fs.String("endpoint", "", "Flockdeck hook endpoint")
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
		fmt.Fprintln(os.Stderr, "flockdeck hook: -endpoint, -session and -event are all required")
		return
	}
	ctx, err := hooks.Emit(os.Stdin, *endpoint, *token, *sessID, *event)
	if err != nil {
		fmt.Fprintln(os.Stderr, "flockdeck hook:", err)
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
		fmt.Fprintln(os.Stderr, "flockdeck hook: could not encode the session context:", err)
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
