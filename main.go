// Command agent-wrapper is a desktop application for running several Claude
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
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/jmwri/agent-wrapper/internal/appwindow"
	"github.com/jmwri/agent-wrapper/internal/hooks"
	"github.com/jmwri/agent-wrapper/internal/server"
	"github.com/jmwri/agent-wrapper/internal/session"
	"github.com/jmwri/agent-wrapper/internal/store"
	"github.com/jmwri/agent-wrapper/internal/workspace"
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
			fmt.Fprintln(os.Stderr, "agent-wrapper spawn:", err)
			os.Exit(1)
		}
		return
	}

	var (
		dir      = flag.String("C", ".", "directory to open the workspace on")
		fresh    = flag.Bool("new", false, "ignore any saved layout and start with a single pane")
		shell    = flag.Bool("shell", false, "open the first pane as a shell instead of an agent")
		noWindow = flag.Bool("no-window", false, "do not open a window; print the URL and keep serving")
		detach   = flag.Bool("detach", false, "keep running without a window; reattach later by running it again")
		quit     = flag.Bool("quit", false, "stop a running instance and its agents")
		solo     = flag.Bool("solo", false, "always start a new instance instead of attaching to a running one")
		showVer  = flag.Bool("version", false, "print the version and exit")
	)
	flag.Usage = usage
	flag.Parse()

	if *showVer {
		fmt.Println("agent-wrapper", version)
		return
	}

	// Anything left over is a mistyped flag or a subcommand that does not
	// exist. Ignoring it would open a window on the current directory and
	// leave the user believing `agent-wrapper quit` had done something.
	if flag.NArg() > 0 {
		arg := flag.Arg(0)
		fmt.Fprintf(os.Stderr, "agent-wrapper: unrecognised argument %q\n", arg)
		switch fi, statErr := os.Stat(arg); {
		case statErr == nil && fi.IsDir():
			fmt.Fprintf(os.Stderr, "To open that directory: agent-wrapper -C %s\n", arg)
		case flag.Lookup(arg) != nil:
			fmt.Fprintf(os.Stderr, "Did you mean -%s?\n", arg)
		}
		fmt.Fprintln(os.Stderr)
		usage()
		os.Exit(2)
	}
	server.Version = version

	if *quit {
		if err := quitRunning(); err != nil {
			fail(err)
		}
		return
	}

	if err := run(options{
		dir: *dir, fresh: *fresh, shell: *shell,
		noWindow: *noWindow, detach: *detach, solo: *solo,
	}); err != nil {
		fail(err)
	}
}

func usage() {
	out := flag.CommandLine.Output()
	fmt.Fprintf(out, "agent-wrapper — run several Claude Code agents in tabs and split panes.\n\n")
	fmt.Fprintf(out, "Usage:\n  agent-wrapper [flags]\n\nFlags:\n")
	flag.PrintDefaults()
	fmt.Fprintf(out, "\nSubcommands:\n")
	fmt.Fprintf(out, "  spawn [--worktree <branch>] [--split] <task>\n")
	fmt.Fprintf(out, "        start another agent; run from inside a pane\n")
	fmt.Fprintf(out, "\nRunning it again attaches to an instance that is already going.\n")
	fmt.Fprintf(out, "Press F1 in the window for the keyboard shortcuts.\n")
}

// fail reports a startup error. When the process was started from a desktop
// shortcut there is no console to print to, so the message is also written to
// a log file the user can be pointed at.
func fail(err error) {
	fmt.Fprintln(os.Stderr, "agent-wrapper:", err)
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
	fresh    bool
	shell    bool
	noWindow bool
	detach   bool
	solo     bool
}

// quitRunning stops an instance that is already going.
func quitRunning() error {
	inst, base, err := runningInstance()
	if err != nil || inst == nil {
		return fmt.Errorf("no running agent-wrapper found")
	}
	if err := server.RequestQuit(base, inst.Token); err != nil {
		return err
	}
	fmt.Println("agent-wrapper: stopped")
	return nil
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
		fmt.Println("agent-wrapper is already running at:")
		fmt.Println(" ", url)
		return nil
	}
	profile, err := store.BrowserProfileDir()
	if err != nil {
		return err
	}
	if _, err := appwindow.Open(url, profile); err != nil {
		return fmt.Errorf("%w — open this URL manually:\n  %s", err, url)
	}
	return nil
}

// run starts the workspace, serves it and shows the window.
func run(opts options) error {
	root, err := filepath.Abs(opts.dir)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", opts.dir, err)
	}
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		return fmt.Errorf("%s is not a directory", root)
	}

	// Attach to an instance that is already running rather than starting a
	// second one: its agents are the ones the user means.
	if !opts.solo {
		if inst, base, err := runningInstance(); err == nil && inst != nil {
			return attach(inst, base, root, opts.noWindow)
		}
	}

	ws, err := workspace.New(workspace.Options{Root: root})
	if err != nil {
		return err
	}
	defer ws.Close()

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
		fmt.Fprintln(os.Stderr, "agent-wrapper: could not record the instance:", err)
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
		default:
		}
	}

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt)
	go func() {
		<-sigs
		stop()
	}()

	srv.OnQuit = stop
	if opts.detach {
		srv.Detach()
	}

	var win *appwindow.Window
	if opts.noWindow || opts.detach {
		fmt.Println("agent-wrapper serving at:")
		fmt.Println(" ", srv.URL())
		if opts.detach {
			fmt.Println("Running detached. Attach with `agent-wrapper`, stop with `agent-wrapper -quit`.")
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
			if errors.Is(err, appwindow.ErrNoBrowser) {
				return fmt.Errorf("%w — open this URL manually, or set %s:\n  %s",
					err, appwindow.BrowserEnv, srv.URL())
			}
			return err
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

	if err := ws.SaveAll(); err != nil {
		fmt.Fprintln(os.Stderr, "agent-wrapper: could not save layout:", err)
	}
	return nil
}

// runSpawn implements the `spawn` subcommand, which starts another agent from
// inside a pane.
//
// It exists so a lead agent can split its own work up: given a plan, it can run
// this once per task and watch the helpers appear beside it. The address and
// token come from the environment its pane was started with, so only processes
// running inside a pane can use it.
func runSpawn(args []string) error {
	fs := flag.NewFlagSet("spawn", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		worktree = fs.String("worktree", "", "branch name; the helper gets its own git worktree")
		split    = fs.Bool("split", false, "place the helper beside this pane instead of in a new tab")
		shell    = fs.Bool("shell", false, "start a shell instead of an agent")
	)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: agent-wrapper spawn [flags] <task>\n\n")
		fmt.Fprintf(os.Stderr, "Starts another agent, working on <task>.\n\nFlags:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	api := os.Getenv("AGENT_WRAPPER_API")
	token := os.Getenv("AGENT_WRAPPER_TOKEN")
	pane := os.Getenv("AGENT_WRAPPER_PANE")
	if api == "" || token == "" {
		return fmt.Errorf("this only works inside an agent-wrapper pane")
	}

	task := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if task == "" && !*shell {
		fs.Usage()
		return fmt.Errorf("a task is required")
	}

	id, err := hooks.Spawn(api, token, pane, hooks.SpawnRequest{
		Task:   task,
		Branch: *worktree,
		Split:  *split,
		Shell:  *shell,
	})
	if err != nil {
		return err
	}
	fmt.Println("started agent", id)
	return nil
}

// runHook implements the hidden `hook` subcommand invoked by Claude Code.
// It must never fail loudly: a broken hook would disrupt the agent session it
// is only meant to observe.
func runHook(args []string) {
	fs := flag.NewFlagSet("hook", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		endpoint = fs.String("endpoint", "", "wrapper hook endpoint")
		token    = fs.String("token", "", "shared secret")
		sessID   = fs.String("session", "", "pane session id")
		event    = fs.String("event", "", "lifecycle event name")
	)
	if err := fs.Parse(args); err != nil {
		return
	}
	if *endpoint == "" || *sessID == "" || *event == "" {
		return
	}
	ctx, err := hooks.Emit(os.Stdin, *endpoint, *token, *sessID, *event)
	if err != nil || strings.TrimSpace(ctx) == "" {
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
