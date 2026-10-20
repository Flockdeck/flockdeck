package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/jmwri/flockdeck/internal/store"
	"github.com/jmwri/flockdeck/internal/team"
	"github.com/jmwri/flockdeck/internal/workspace"
)

// `flockdeck wake` shows and sets the off switches for typed wake, the line
// Flockdeck types into an idle agent's input when it has unread mail. It reads
// and writes the preferences file, which the wake service reads again every
// second, so it needs no running instance.
//
// on and off are refused inside a pane. That refusal is advisory: it reads the
// environment a pane is started with, which an agent can unset in the command
// it runs. The real barrier is the agent's own permission prompt for running a
// command. The refusal stops an agent doing it by accident, and says why.
//
// on is checked harder than off, because turning typing back on is the change
// that matters. It is also refused when stdin is not a terminal (an agent runs
// commands with none) and when the pane's API address or token is in the
// environment (a variable the agent has to unset, as well as the pane's own,
// to get past it). None of this is a barrier against an agent that edits the
// preferences file, which it can do with any command that writes a file.

func wakeUsage(w io.Writer) {
	fmt.Fprint(w, "Usage: flockdeck wake [status] [-n count]\n"+
		"       flockdeck wake on|off [-agent id | -pane id]\n\n"+
		"Typed wake types one fixed line into an idle agent's input when it has unread\n"+
		"mail. status shows the switches and the last wake events (no message text).\n"+
		"on and off set the global switch, or with -agent or -pane the switch for an\n"+
		"agent type or one pane. A wake needs every switch on. A switch takes effect\n"+
		"within a second, and a wake already on its way stops before the line or its\n"+
		"Enter if the switch is off by then. on and off must be run outside a Flockdeck\n"+
		"pane, and on from a terminal you are typing in.\n")
}

// wakeEnv is what runWake depends on besides the preferences, so a test can
// stand in for it.
type wakeEnv struct {
	inPane func() bool
	events func() ([]team.Event, error)
	// terminal says stdin is a terminal, and paneAPI that the address or token
	// a pane is given is in the environment.
	terminal func() bool
	paneAPI  func() bool
	// read is store.ReadWake.
	read func() (store.Prefs, error)
}

func runWake(args []string, out io.Writer) error {
	return runWakeWith(wakeEnv{
		inPane:   func() bool { return quietPaneEnv("PANE") != "" },
		events:   readWakeEvents,
		terminal: stdinIsTerminal,
		paneAPI:  func() bool { return quietPaneEnv("API") != "" || quietPaneEnv("TOKEN") != "" },
		read:     store.ReadWake,
	}, args, out)
}

// readWakeEvents is the event log as it was last saved.
func readWakeEvents() ([]team.Event, error) {
	path, err := workspace.EventsPath()
	if err != nil {
		return nil, err
	}
	data, err := store.ReadState(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var log team.EventLog
	log.Load(data)
	return log.Events(), nil
}

// wakeFlags are the flags of `flockdeck wake`.
type wakeFlags struct {
	agent, pane string
	n           int
}

func wakeFlagSet(f *wakeFlags) *flag.FlagSet {
	fs := flag.NewFlagSet("wake", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&f.agent, "agent", "", "agent type")
	fs.StringVar(&f.pane, "pane", "", "pane id")
	fs.IntVar(&f.n, "n", 10, "events to show")
	return fs
}

func runWakeWith(env wakeEnv, args []string, out io.Writer) error {
	action := "status"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		action, args = args[0], args[1:]
	}
	var f wakeFlags
	fs := wakeFlagSet(&f)
	agentID, paneID, count := &f.agent, &f.pane, &f.n
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			wakeUsage(out)
			return nil
		}
		wakeUsage(os.Stderr)
		return errReported
	}
	if fs.NArg() > 0 {
		wakeUsage(os.Stderr)
		return errReported
	}
	switch action {
	case "help":
		wakeUsage(out)
		return nil
	case "status":
		if *agentID != "" || *paneID != "" {
			wakeUsage(os.Stderr)
			return errors.New("status takes no -agent or -pane")
		}
		return wakeStatus(env, *count, out)
	case "on", "off":
	default:
		wakeUsage(os.Stderr)
		return fmt.Errorf("unknown action %q", action)
	}
	if *agentID != "" && *paneID != "" {
		return errors.New("give -agent or -pane, not both")
	}
	if env.inPane() {
		return errors.New("this changes what is typed into panes, so it is not run from inside a pane; run it in a terminal outside Flockdeck")
	}
	if action == "on" {
		if !env.terminal() {
			return errors.New("on is run from a terminal you are typing in, and stdin here is not one")
		}
		if env.paneAPI() {
			return errors.New("this environment has a pane's API address or token, so on is refused; run it in a terminal outside Flockdeck")
		}
	}
	scope, id := store.WakeGlobal, ""
	switch {
	case *agentID != "":
		scope, id = store.WakeAgent, *agentID
	case *paneID != "":
		scope, id = store.WakePane, *paneID
	}
	on := action == "on"
	// Read and written as one step, and refused if the file cannot be read:
	// saving defaults with one change over a file that exists would lose every
	// other setting.
	if err := store.UpdatePrefs(func(p *store.Prefs) bool { return p.SetWake(scope, id, on) }); err != nil {
		return fmt.Errorf("could not save the setting: %w", err)
	}
	// A switch set by hand is the user saying what they want, so a damaged file
	// no longer holds wake off.
	store.ClearWakeDamaged()
	what := "everything"
	if id != "" {
		what = scope + " " + id
	}
	fmt.Fprintf(out, "Typed wake for %s: %s\n", what, action)
	p, err := store.ReadPrefs()
	if err == nil && on && !p.WakeEnabled(*agentID, *paneID) {
		fmt.Fprintln(out, "Still held off by another switch; wake status shows which.")
	}
	return nil
}

func wakeStatus(env wakeEnv, count int, out io.Writer) error {
	p, err := env.read()
	damaged := errors.Is(err, store.ErrPrefsDamaged)
	if err != nil && !damaged {
		return fmt.Errorf("the preferences could not be read, so wake is off: %w", err)
	}
	w := p.Wake
	if damaged {
		fmt.Fprintln(out, "preferences damaged, wake is off for every pane until you set a switch again")
		fmt.Fprintln(out, "(the file may have been moved aside; switches below are what could be read from it)")
	}
	fmt.Fprintf(out, "global: %s\n", onOff(!w.Off))
	fmt.Fprintf(out, "agent types off: %s\n", listOr(w.Agents))
	fmt.Fprintf(out, "panes off: %s\n", listOr(w.Panes))
	fmt.Fprintln(out, "A wake is typed only when every switch is on and the agent's wake rule is tested.")
	evs, err := env.events()
	if err != nil {
		return fmt.Errorf("the event log could not be read: %w", err)
	}
	var wakes []team.Event
	for _, e := range evs {
		if e.Kind == team.EventWake {
			wakes = append(wakes, e)
		}
	}
	sort.SliceStable(wakes, func(i, j int) bool { return wakes[i].Time.Before(wakes[j].Time) })
	if count < 0 {
		count = 0
	}
	if len(wakes) > count {
		wakes = wakes[len(wakes)-count:]
	}
	if len(wakes) == 0 {
		fmt.Fprintln(out, "no wake events")
		return nil
	}
	fmt.Fprintln(out, "last wake events:")
	for _, e := range wakes {
		note := ""
		if e.Unsubmitted {
			note = " (typed, Enter not sent)"
		}
		fmt.Fprintf(out, "  %s  pane %s  %s  message %d  rule %s %s%s\n",
			e.Time.Local().Format("2006-01-02 15:04:05"), e.Pane, e.Agent, e.ID, e.RuleSource, e.Rule, note)
	}
	return nil
}

func onOff(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

func listOr(l []string) string {
	if len(l) == 0 {
		return "none"
	}
	return strings.Join(l, ", ")
}
