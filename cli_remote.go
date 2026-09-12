package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jmwri/flockdeck/internal/remote"
	"github.com/jmwri/flockdeck/internal/server"
)

// `flockdeck remote` enrols this machine with a relay, and manages what is
// paired with it.
//
// The window's Remote access dialog does the same jobs. The work of enrolling
// and of taking the machine off again is remote.Enable and remote.Disable,
// which both use; what is here is the command line's wording of it, which can
// point at flags the window does not have.

// remoteIO is where the subcommand writes, and how it reaches a running
// instance, gathered so a test can drive it without one.
type remoteIO struct {
	out io.Writer
	// reload tells a running instance that the enrolment has changed, and
	// reports whether there was one to tell.
	reload func() (bool, error)
	// running reports whether flockdeck is running here, which is what
	// decides whether the tunnel ought to be up.
	running func() bool
}

// runRemote implements the `remote` subcommand.
func runRemote(args []string) error {
	return remoteCmd(args, remoteIO{out: os.Stdout, reload: reloadRunningRemote, running: func() bool {
		inst, _, err := runningInstance()
		return err == nil && inst != nil
	}})
}

func remoteCmd(args []string, rio remoteIO) error {
	if len(args) == 0 {
		remoteUsage(rio.out)
		return nil
	}
	var err error
	switch args[0] {
	case "enable":
		err = remoteEnable(args[1:], rio)
	case "pair":
		err = remotePairCmd(args[1:], rio)
	case "status":
		err = remoteStatusCmd(args[1:], rio)
	case "devices", "list", "ls":
		err = remoteDevicesCmd(args[1:], rio)
	case "revoke":
		err = remoteRevokeCmd(args[1:], rio)
	case "disable":
		err = remoteDisable(args[1:], rio)
	case "-h", "--help", "help":
		// `remote help pair` is how most commands are asked about one of
		// their own, so it gives that subcommand's help.
		if len(args) > 1 {
			return remoteHelp(args[1], rio)
		}
		remoteUsage(rio.out)
		return nil
	default:
		remoteUsage(rio.out)
		return unknownRemote(args[0])
	}
	if errors.Is(err, errHelpAsked) {
		return nil
	}
	return err
}

// remoteHelp gives one subcommand's help, as its -h does, but on stdout: it
// was asked for, so it is the output and not a complaint.
func remoteHelp(name string, rio remoteIO) error {
	var fs *flag.FlagSet
	switch name {
	case "enable":
		fs = remoteEnableFlagSet(&remoteEnableFlags{})
	case "pair":
		fs = remotePairFlagSet(&remotePairFlags{})
	case "disable":
		fs = remoteDisableFlagSet(&remoteDisableFlags{})
	case "devices", "list", "ls":
		fs = remoteFlags("devices")
	case "status", "revoke":
		fs = remoteFlags(name)
	default:
		remoteUsage(rio.out)
		return unknownRemote(name)
	}
	fs.SetOutput(rio.out)
	fs.Usage()
	return nil
}

// remoteCommands are the subcommands a mistyped one is compared with.
var remoteCommands = []string{"enable", "pair", "status", "devices", "revoke", "disable"}

// remoteGuesses are words somebody is likely to try for one of them. They are
// answered with the command's name rather than taken for it: revoke and
// disable cost something, and are not run on a guess.
var remoteGuesses = map[string]string{
	"on": "enable", "start": "enable", "setup": "enable", "register": "enable",
	"off": "disable", "stop": "disable", "unregister": "disable",
	"unpair": "revoke", "remove": "revoke", "rm": "revoke", "delete": "revoke",
	"add": "pair", "link": "pair", "qr": "pair",
	"info": "status", "state": "status",
}

// unknownRemote refuses a subcommand that is not one, naming the one most
// likely meant: a word for it, or one typed a letter or two wrong.
func unknownRemote(name string) error {
	err := fmt.Errorf("unknown command %q", name)
	word := strings.ToLower(strings.TrimSpace(name))
	if c, ok := remoteGuesses[word]; ok {
		return fmt.Errorf("%w; did you mean %s?", err, c)
	}
	best, bestAt := "", 3
	for _, c := range remoteCommands {
		if d := editDistance(word, c); d < bestAt {
			best, bestAt = c, d
		}
	}
	if best != "" {
		return fmt.Errorf("%w; did you mean %s?", err, best)
	}
	return err
}

// editDistance is how many letters have to be added, removed or changed to
// turn a into b.
func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			change := 1
			if a[i-1] == b[j-1] {
				change = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+change)
		}
		prev = cur
	}
	return prev[len(b)]
}

func remoteUsage(out io.Writer) {
	fmt.Fprintf(out, `Usage: flockdeck remote <command>

Reach this machine's agents from another device, through a relay.

Commands:
  enable [-relay URL] [-name N] [-join CODE] [-invite CODE]
                 enrol this machine with a relay
  pair [-desktop]  print a one-time link (and QR code) that pairs a device;
                 -desktop prints a code for enrolling another machine instead
  status         say whether remote access is on, and connected
  devices        list the paired devices and enrolled machines
  revoke <id>    unpair a device, named by its id or its name
  disable [-force]  remove this machine from the relay

Run flockdeck remote help <command> for more about one of them.
The relay is %s unless -relay or %s
says otherwise.
To move to another relay, disable remote access, then enable it with -relay.
In the window, Remote access… in the command palette does the same things.
Traffic is encrypted on its way to and from the relay, which decrypts it to
forward it: the relay is trusted, and it is not end-to-end encrypted.
`, remote.DefaultRelay, remote.RelayEnv)
}

// remoteSynopses is how each subcommand is written and what it is for, which
// its -h says before its flags.
var remoteSynopses = map[string][2]string{
	"enable": {" [flags]",
		"Enrol this machine with a relay, so its window can be opened from another\ndevice."},
	"pair": {" [-desktop]",
		"Print a one-time link, and a QR code of it, that pairs a device with this\nmachine."},
	"status": {"",
		"Say whether remote access is on, whether this machine is connected to its\nrelay, the address a paired device opens it at, and which devices are paired."},
	"devices": {"",
		"List the paired devices, with the ids and names revoke takes, and the\naccount's machines."},
	"revoke": {" <device id or name>",
		"Unpair a device, closing any window it has open. `flockdeck remote devices`\nlists the paired devices with their ids and names."},
	"disable": {" [-force]",
		"Take this machine off its relay. If it is the account's only machine, its\npaired devices are unpaired too."},
}

// remoteFlags is a flag set for one of the subcommands, reporting to stderr
// the way `spawn` does. Its -h says how the command is written and what it
// is for, then its flags, where flag's own would say only "Usage of remote
// status:".
func remoteFlags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet("remote "+name, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		syn := remoteSynopses[name]
		fmt.Fprintf(fs.Output(), "Usage: flockdeck remote %s%s\n\n%s\n", name, syn[0], syn[1])
		flags := false
		fs.VisitAll(func(*flag.Flag) { flags = true })
		if flags {
			fmt.Fprintln(fs.Output())
			fs.PrintDefaults()
		}
	}
	return fs
}

// parseRemote parses a subcommand's flags and refuses anything left over.
func parseRemote(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return errHelpAsked
		}
		return errReported
	}
	if fs.NArg() > 0 {
		// Which command it was, and where to find what it does take, are what
		// somebody who typed a word too many needs to hear.
		return fmt.Errorf("%s takes no arguments, but was given %q; `flockdeck %s -h` says what it does take", fs.Name(), fs.Arg(0), fs.Name())
	}
	return nil
}

// enrolled loads the enrolment, and says what to do when there is none.
func enrolled() (*remote.Config, error) {
	cfg, err := remote.Load()
	if err != nil {
		return nil, loadAdvice(err)
	}
	if cfg == nil {
		return nil, remote.ErrNotEnabled
	}
	return cfg, nil
}

// loadAdvice adds, to an enrolment that cannot be read or used, the command
// here that clears it, which is less to do than finding the file that
// remote.Load's words say to delete.
func loadAdvice(err error) error {
	return fmt.Errorf("%w (`flockdeck remote disable -force` removes it)", err)
}

// The subcommands' flag sets are built by functions of their own, like the
// top-level one, so that the test holding the help page to the command line
// walks the same definitions the program parses with.

type remoteEnableFlags struct{ relay, name, join, invite string }

func remoteEnableFlagSet(f *remoteEnableFlags) *flag.FlagSet {
	fs := remoteFlags("enable")
	// PrintDefaults starts a description at column 8 and indents a line break
	// in one the same way, so the long ones are broken to fit 80 columns.
	fs.StringVar(&f.relay, "relay", "", "the relay's `URL`\n(default: $"+remote.RelayEnv+" if set, else "+remote.DefaultRelay+")")
	fs.StringVar(&f.name, "name", "", "the `name` this machine goes by on your devices (default: its host name)")
	fs.StringVar(&f.join, "join", "", "a `code` from `flockdeck remote pair -desktop` on another machine,\nto join its account")
	fs.StringVar(&f.invite, "invite", "", "an invitation `code`, for a relay that asks for one")
	return fs
}

type remotePairFlags struct{ desktop bool }

func remotePairFlagSet(f *remotePairFlags) *flag.FlagSet {
	fs := remoteFlags("pair")
	fs.BoolVar(&f.desktop, "desktop", false, "print a code that enrols another machine into this account,\nrather than pairing a device")
	return fs
}

type remoteDisableFlags struct{ force bool }

func remoteDisableFlagSet(f *remoteDisableFlags) *flag.FlagSet {
	fs := remoteFlags("disable")
	fs.BoolVar(&f.force, "force", false, "forget the enrolment here even if the relay cannot be told")
	return fs
}

func remoteEnable(args []string, rio remoteIO) error {
	var f remoteEnableFlags
	if err := parseRemote(remoteEnableFlagSet(&f), args); err != nil {
		return err
	}
	cfg, replaced, err := remote.Enable(context.Background(), version, remote.EnableRequest{
		Relay: f.relay, Name: f.name, Join: f.join, Invite: f.invite,
	})
	if err != nil {
		return enableAdvice(f, err)
	}
	if replaced {
		fmt.Fprintln(rio.out, "the relay no longer knew this machine, so it has been enrolled again")
	}
	fmt.Fprintf(rio.out, "remote access enabled: this machine is %q on %s\n", cfg.Name, cfg.Relay)
	if f.join != "" {
		// Joining puts this machine in another's account rather than a new
		// one, which is the whole point of the code, and worth confirming.
		fmt.Fprintln(rio.out, "Joined the other machine's account: a device paired with either reaches both.")
	}
	reportReload(rio, "flockdeck will connect to the relay when it next starts")
	fmt.Fprintln(rio.out, "Pair a device with `flockdeck remote pair`, or Remote access… in the window.")
	return nil
}

// enableAdvice words a refusal to enable for the command line: what
// remote.Enable said, then the command or flag here that does what it asks.
// The window shares Enable's words and offers its own ways instead.
func enableAdvice(f remoteEnableFlags, err error) error {
	var already *remote.AlreadyEnabledError
	var refused *remote.APIError
	if errors.As(err, &already) && f.relay != "" {
		// Asking for another relay is moving this machine, which is two steps,
		// and the refusal names both rather than only the first.
		if want, _ := remote.RelayURL(f.relay); want != "" && !remote.SameRelay(want, already.Relay) {
			// Leaving a relay that could not be asked, which is most often why
			// somebody moves, takes -force: plain disable would stop at the same.
			disable := "flockdeck remote disable"
			if already.Err != nil {
				disable += " -force"
			}
			return fmt.Errorf("%v; to move this machine to %s, run `%s`, then `flockdeck remote enable -relay %s`", err, want, disable, want)
		}
	}
	switch {
	case errors.As(err, &already) && already.Err == nil && f.join != "":
		// A join code is asking for another account, which this machine can
		// be in only after leaving the one it is in.
		return fmt.Errorf("%v; to join that account instead, run `flockdeck remote disable` first, which unpairs this machine's devices if it is its account's only machine, then run this again", err)
	case errors.As(err, &already) && already.Err == nil:
		// Whoever runs enable twice most likely forgot the first; the way to
		// enrol again costs the account's devices if this is its only
		// machine, so that is said before anybody takes it.
		return fmt.Errorf("%v, so there is nothing to do; to enrol this machine again, run `flockdeck remote disable` first, which unpairs its devices if it is the account's only machine", err)
	case errors.As(err, &already):
		// A relay out of reach is most often a network that is down for now,
		// when trying again is the answer. -force is for a relay that is gone
		// for good, and it costs a listing there that nothing can take off.
		return fmt.Errorf("%v; try again once the relay can be reached, or, if it is gone for good, run `flockdeck remote disable -force` to start again, which leaves this machine listed on it", err)
	case f.invite == "" && strings.Contains(err.Error(), "needs an invite code"):
		// The relay's words name an invite code but not the flag that takes
		// one. They are matched rather than its 403, which a relay closed to
		// new accounts also answers, and for which an invite does not help.
		return fmt.Errorf("%v; pass it with -invite CODE", err)
	case f.invite != "" && strings.Contains(err.Error(), "invite code is not valid"):
		// An invitation that has been used, or was never one. Whoever runs
		// the relay makes them, and can make another; the relay's words are
		// matched because its 403 is also the closed relay's and the missing
		// invitation's.
		return fmt.Errorf("%v; whoever runs the relay makes invitations, and can make another", err)
	case strings.Contains(err.Error(), "not accepting new accounts"):
		// A relay closed to new accounts still takes machines into the ones
		// it has, which is the way in that is left. Its words are matched for
		// the reason the invite's are.
		return fmt.Errorf("%v; a machine already on it can take this one into its account: `flockdeck remote pair -desktop` there prints the command to run here", err)
	case f.join != "" && errors.As(err, &refused) && refused.Status == http.StatusBadRequest:
		// A join code that has run out, been used, or was never one: the
		// relay says which, but not where a good one comes from. With a
		// join code given, its 400 is about the code; its other one is for
		// a body this client never sends.
		return fmt.Errorf("%v; `flockdeck remote pair -desktop` on the other machine makes a new one", err)
	case f.join != "" && errors.As(err, &refused) && refused.Status == http.StatusConflict:
		// An account with all the machines it may have. The relay says to
		// unregister one, which is not a word this command line uses.
		return fmt.Errorf("%v; running `flockdeck remote disable` on one of that account's machines does that", err)
	}
	return err
}

// relayRefusal adds what to do to the relay refusing this machine, which on
// its own says what is wrong but not that `enable` puts it right.
func relayRefusal(err error) error {
	if remote.IsRevoked(err) {
		return fmt.Errorf("%w; `flockdeck remote enable` enrols this machine again", err)
	}
	return err
}

// relayToJoin is the relay a machine joining this one is told to enrol with:
// the hosted relay by its current name even from a machine enrolled under the
// old one, which the relay keeps answering only for the machines that already
// were, so that no more are added to them.
func relayToJoin(relay string) string {
	if remote.SameRelay(relay, remote.DefaultRelay) {
		return remote.DefaultRelay
	}
	return relay
}

func remotePairCmd(args []string, rio remoteIO) error {
	var f remotePairFlags
	if err := parseRemote(remotePairFlagSet(&f), args); err != nil {
		return err
	}
	cfg, err := enrolled()
	if err != nil {
		return err
	}
	kind := remote.KindDevice
	if f.desktop {
		kind = remote.KindHost
	}
	p, err := remote.NewClient(cfg, version).Pair(context.Background(), kind)
	if err != nil {
		return relayRefusal(err)
	}
	until := describeExpiry(p.ExpiresAt, time.Now())
	if f.desktop {
		fmt.Fprintf(rio.out, "On the other machine, run:\n\n  flockdeck remote enable -relay %s -join %s\n\n", relayToJoin(cfg.Relay), p.Code)
		// What joining does is the reason to do it, and worth knowing before
		// a code goes to a machine somebody else uses.
		fmt.Fprintf(rio.out, "The machines then share an account: a device paired with either reaches both.\n")
		fmt.Fprintf(rio.out, "The code works once, %s.\n", until)
		return nil
	}
	if p.URL == "" {
		return errors.New("the relay sent a pairing code but no link to open it with")
	}
	fmt.Fprintf(rio.out, "Open this link on the device you want to pair, or scan the code below:\n\n  %s\n\n", p.URL)
	fmt.Fprintf(rio.out, "It works once, %s.\n", until)
	fmt.Fprintf(rio.out, "Whoever opens it can drive every agent here, so treat it like a password\nuntil then.\n")
	// The device pairs whether or not anything is running here, and would
	// then find the machine offline with nothing to say why.
	if rio.running != nil && !rio.running() {
		fmt.Fprintln(rio.out, "flockdeck is not running here, so this machine is offline until it starts.")
	}
	// The code is drawn in the terminal's own colours, which on a light
	// background comes out inverted, and a phone's camera is not reliably
	// able to read that. The window draws it dark on white whatever the theme.
	fmt.Fprintf(rio.out, "If the code will not scan, Remote access… in the window shows it dark on white.\n")
	// The code comes last: all of this is taller than a 24-row terminal, and
	// what is printed last is what stays in view, at the bottom, to be scanned.
	if code, err := remote.QRTerminal(p.URL); err == nil {
		fmt.Fprintln(rio.out)
		fmt.Fprint(rio.out, code)
	}
	return nil
}

func remoteStatusCmd(args []string, rio remoteIO) error {
	if err := parseRemote(remoteFlags("status"), args); err != nil {
		return err
	}
	cfg, err := remote.Load()
	if err != nil {
		return loadAdvice(err)
	}
	if cfg == nil {
		fmt.Fprintln(rio.out, "remote access is not enabled; `flockdeck remote enable` turns it on")
		// Which relay is the one choice enabling makes, so it is said before
		// it is made, along with where it would come from.
		relay, err := remote.RelayURL("")
		switch {
		case err != nil:
			fmt.Fprintf(rio.out, "relay:   %v\n", err)
		case strings.TrimSpace(os.Getenv(remote.RelayEnv)) != "":
			fmt.Fprintf(rio.out, "relay:   %s, from %s\n", relay, remote.RelayEnv)
		default:
			// Held to 80 columns, as everything here is, by carrying on under
			// the value rather than under the label.
			fmt.Fprintf(rio.out, "relay:   %s, unless enable is given -relay\n         or %s is set\n", relay, remote.RelayEnv)
		}
		return nil
	}
	fmt.Fprintf(rio.out, "relay:   %s\n", cfg.Relay)
	fmt.Fprintf(rio.out, "machine: %s (%s)\n", cfg.Name, cfg.HostID)
	roster, err := remote.NewClient(cfg, version).Devices(context.Background())
	switch {
	case remote.IsRevoked(err):
		fmt.Fprintln(rio.out, "state:   the relay no longer accepts this machine;\n         `flockdeck remote enable` enrols it again")
		return nil
	case err != nil:
		// A relay that answered with an error was reached, so this says only
		// that it could not be asked, which is true either way.
		fmt.Fprintf(rio.out, "state:   could not ask the relay: %v\n", err)
		return nil
	}
	online, at := false, ""
	for _, h := range roster.Hosts {
		if h.Self {
			online, at = h.Online, h.URL
		}
	}
	switch {
	case online:
		fmt.Fprintln(rio.out, "state:   connected")
	case rio.running != nil && rio.running():
		fmt.Fprintln(rio.out, "state:   not connected, though flockdeck is running;\n         the Remote chip in its window says why")
	default:
		fmt.Fprintln(rio.out, "state:   not connected — flockdeck connects while it is running")
	}
	// Where a paired device opens this machine, for one that has lost its
	// bookmark, or somebody asked where to point it.
	if at != "" {
		fmt.Fprintf(rio.out, "open at: %s\n", at)
	}
	if len(roster.Devices) == 0 {
		fmt.Fprintln(rio.out, "devices: none paired — `flockdeck remote pair` pairs one")
	} else {
		fmt.Fprintf(rio.out, "devices: %s\n", pairedSummary(roster.Devices, 80-len("devices: ")))
	}
	return nil
}

// pairedSummary is status's line about the devices: how many, and as many of
// their names as fit in width, so that the one to hand can be seen to be
// there without asking for the whole list.
func pairedSummary(ds []remote.Device, width int) string {
	s := fmt.Sprintf("%d paired", len(ds))
	for i, d := range ds {
		sep := ": "
		if i > 0 {
			sep = ", "
		}
		// Room is kept for saying how many are left, should this be the last
		// name that fits.
		more := ""
		if rest := len(ds) - i - 1; rest > 0 {
			more = fmt.Sprintf(" and %d more", rest)
		}
		next := s + sep + orUnnamed(strings.TrimSpace(d.Name))
		if len([]rune(next+more)) > width {
			if i == 0 {
				return s
			}
			return fmt.Sprintf("%s and %d more", s, len(ds)-i)
		}
		s = next
	}
	return s
}

func remoteDevicesCmd(args []string, rio remoteIO) error {
	if err := parseRemote(remoteFlags("devices"), args); err != nil {
		return err
	}
	cfg, err := enrolled()
	if err != nil {
		return err
	}
	roster, err := remote.NewClient(cfg, version).Devices(context.Background())
	if err != nil {
		return relayRefusal(err)
	}
	printRoster(rio.out, roster, time.Now(), rio.running != nil && !rio.running())
	return nil
}

// printRoster lists what the account has: the devices first, since they are
// what `revoke` takes, with the id it takes them by. idle is flockdeck not
// running here, which is why this machine is offline when it is.
func printRoster(out io.Writer, r *remote.Roster, now time.Time, idle bool) {
	if len(r.Devices) == 0 {
		fmt.Fprintln(out, "No devices are paired. `flockdeck remote pair` pairs one.")
	} else {
		fmt.Fprintln(out, "Devices (`flockdeck remote revoke <id or name>` unpairs one):")
		width := 0
		for _, d := range r.Devices {
			width = max(width, len(d.ID))
		}
		for _, d := range r.Devices {
			fmt.Fprintf(out, "  %-*s  %s — last seen %s\n", width, d.ID, orUnnamed(d.Name), ago(d.LastSeen, now))
		}
	}
	if len(r.Hosts) > 0 {
		fmt.Fprintln(out, "\nMachines:")
		for _, h := range r.Hosts {
			state := "offline, last seen " + ago(h.LastSeen, now)
			switch {
			case h.Online:
				state = "online"
			case h.Self && idle:
				// The one reason this side knows for itself, and the one
				// thing to do about it.
				state = "offline — flockdeck is not running here"
			}
			self := ""
			if h.Self {
				self = " (this one)"
			}
			fmt.Fprintf(out, "  %s%s — %s\n", orUnnamed(h.Name), self, state)
		}
	}
}

func remoteRevokeCmd(args []string, rio remoteIO) error {
	// The id goes through a flag set like every other argument here, so that
	// -h asks how revoke is used rather than going to the relay as a device.
	fs := remoteFlags("revoke")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return errHelpAsked
		}
		return errReported
	}
	if fs.NArg() == 0 {
		// Whoever typed revoke knows the command and missed its argument, so
		// its own usage is the answer, not a screen of every command's.
		fs.Usage()
		return errReported
	}
	cfg, err := enrolled()
	if err != nil {
		return err
	}
	// The roster turns a name into the id the relay takes, and an id into the
	// name that says the right device went. A name of several words, typed
	// without quotes as "Chrome on Windows" is, arrives as several arguments,
	// and is taken whole if it names a device. Several words that name none
	// are told so when the roster can say it; without the roster there is no
	// telling what they were, and the usage answers.
	roster := rosterBriefly(cfg)
	arg := strings.Join(fs.Args(), " ")
	id, name, err := pickDevice(roster, arg)
	if err != nil {
		return err
	}
	if fs.NArg() > 1 && name == "" {
		if roster != nil {
			return fmt.Errorf("no paired device is called %q; `flockdeck remote devices` lists them by id and name", arg)
		}
		fs.Usage()
		return errReported
	}
	if err := remote.NewClient(cfg, version).Revoke(context.Background(), id); err != nil {
		// An id that is not one, or a name that matched no device, comes back
		// "there is no such device", which is true but not where to look.
		var refused *remote.APIError
		if errors.As(err, &refused) && refused.Status == http.StatusNotFound && refused.Message != "" {
			return fmt.Errorf("%v; `flockdeck remote devices` lists the paired ones, by id and name", err)
		}
		return relayRefusal(err)
	}
	if name != "" {
		id = name + " (" + id + ")"
	}
	fmt.Fprintf(rio.out, "unpaired %s; any window it had open has been closed\n", id)
	return nil
}

// pickDevice is the device revoke means by arg, as its id and its name: the
// device with that id, else the one with that name, which is what the devices
// list leads with and what somebody holding the phone knows it by. Two devices
// with the name are not guessed between. Without a roster, arg is taken for an
// id, and the relay says whether it is one.
func pickDevice(r *remote.Roster, arg string) (id, name string, err error) {
	if r == nil {
		return arg, "", nil
	}
	want := strings.TrimSpace(arg)
	var named []remote.Device
	for _, d := range r.Devices {
		// An id is lower case, and one typed from a screenshot or read out
		// may not be; ids are unique in any case, so any case is taken.
		if d.ID == arg || strings.EqualFold(d.ID, want) {
			return d.ID, strings.TrimSpace(d.Name), nil
		}
		if want != "" && strings.EqualFold(strings.TrimSpace(d.Name), want) {
			named = append(named, d)
		}
	}
	switch len(named) {
	case 0:
		return arg, "", nil
	case 1:
		return named[0].ID, strings.TrimSpace(named[0].Name), nil
	}
	ids := make([]string, len(named))
	for i, d := range named {
		ids[i] = d.ID
	}
	return "", "", fmt.Errorf("%d devices are called %q; say which by its id: %s", len(named), want, strings.Join(ids, ", "))
}

// rosterBriefly is the account's roster, for a detail worth a few seconds
// but not a wait: nil when the relay does not answer in time, or at all.
func rosterBriefly(cfg *remote.Config) *remote.Roster {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r, err := remote.NewClient(cfg, version).Devices(ctx)
	if err != nil {
		return nil
	}
	return r
}

// devicesLost is how many paired devices taking cfg's machine off its relay
// unpairs with it: every one, when it is the only machine in its account,
// because the account goes with its last machine. It is 0 when there is no
// knowing, and it asks for a few seconds at most.
func devicesLost(cfg *remote.Config) int {
	if cfg == nil {
		return 0
	}
	if r := rosterBriefly(cfg); r != nil && len(r.Hosts) == 1 {
		return len(r.Devices)
	}
	return 0
}

func remoteDisable(args []string, rio remoteIO) error {
	var f remoteDisableFlags
	if err := parseRemote(remoteDisableFlagSet(&f), args); err != nil {
		return err
	}
	// An enrolment that cannot be read stops Disable too, but only here is
	// there a -force to point at.
	cfg, err := remote.Load()
	if err != nil && !f.force {
		return fmt.Errorf("%w (-force removes it anyway)", err)
	}
	lost := devicesLost(cfg)
	had, untold, err := remote.Disable(context.Background(), version, f.force)
	var relayUntold *remote.RelayUntoldError
	switch {
	case errors.As(err, &relayUntold):
		// Nothing but this machine can take it off the relay, so forgetting
		// the enrolment here leaves it listed there, and that is the cost. A
		// relay out of reach is most often a network down for now, so trying
		// again is said first, as enable's refusal says it.
		return fmt.Errorf("%v; try again once the relay can be reached, or, if it is gone for good, run again with -force to forget the enrolment here anyway — the relay will then list this machine, offline, for good, since nothing but this machine can take it off", err)
	case err != nil:
		return err
	case !had:
		// Nothing was there, readable or not, -force or no: there was
		// nothing to turn off, and nothing to tell a running instance.
		fmt.Fprintln(rio.out, "remote access is not enabled")
		return nil
	case untold != nil:
		fmt.Fprintf(rio.out, "could not tell the relay (%v); forgetting the enrolment here anyway\n", untold)
	}
	fmt.Fprintln(rio.out, "remote access disabled")
	// The account goes with its last machine, and its devices with it, which
	// somebody switching this one machine off may not have expected. A relay
	// that was not told has removed nothing.
	switch {
	case untold != nil:
	case lost == 1:
		fmt.Fprintln(rio.out, "It was the account's only machine, so its paired device has been unpaired too.")
	case lost > 1:
		fmt.Fprintf(rio.out, "It was the account's only machine, so its %d paired devices have been unpaired too.\n", lost)
	}
	reportReload(rio, "")
	return nil
}

// reportReload tells a running instance the enrolment has changed, and says
// how that went. idle is what to say when nothing is running, if anything.
func reportReload(rio remoteIO, idle string) {
	if rio.reload == nil {
		return
	}
	ran, err := rio.reload()
	switch {
	case err != nil:
		fmt.Fprintf(rio.out, "the running flockdeck could not be told (%v); restart it to pick this up\n", err)
	case ran:
		fmt.Fprintln(rio.out, "the running flockdeck has picked this up")
	case idle != "":
		fmt.Fprintln(rio.out, idle)
	}
}

// reloadRunningRemote tells the running instance, if there is one, to reread
// the enrolment.
func reloadRunningRemote() (bool, error) {
	inst, base, err := runningInstance()
	if err != nil {
		return false, err
	}
	if inst == nil {
		return false, nil
	}
	if err := server.RequestRemoteReload(base, inst.Token); err != nil {
		// A request that got no answer comes back naming its URL, with the
		// local server's token in it, and this is printed where a remote
		// window can show it.
		return true, errors.New(redactToken(err.Error(), inst.Token))
	}
	return true, nil
}

// redactToken takes a token out of a message that is about to be shown.
func redactToken(msg, token string) string {
	if token == "" {
		return msg
	}
	return strings.ReplaceAll(msg, token, "…")
}

// describeExpiry says when a pairing code stops working, as a clock time and
// as a distance, which between them answer both "is there still time" and
// "until when".
func describeExpiry(at, now time.Time) string {
	if at.IsZero() {
		return "for a short while"
	}
	left := at.Sub(now).Round(time.Minute)
	if left < time.Minute {
		return "for less than a minute"
	}
	// A clock time alone means today. A code that lasts into another day, as
	// a relay with a long pairing time allows, says which.
	when := at.Local().Format("15:04")
	if at.Local().Format("2006-01-02") != now.Local().Format("2006-01-02") {
		when = at.Local().Format("Mon 2 Jan 15:04")
	}
	return fmt.Sprintf("until %s (%s from now)", when, plainDuration(left))
}

// ago says how long since t, roughly.
func ago(t, now time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := now.Sub(t)
	if d < time.Minute {
		return "just now"
	}
	return plainDuration(d) + " ago"
}

// plainDuration is a duration as a person says it, to the largest unit.
func plainDuration(d time.Duration) string {
	unit := func(n int, one string) string {
		if n == 1 {
			return "1 " + one
		}
		return fmt.Sprintf("%d %ss", n, one)
	}
	switch {
	case d < time.Hour:
		return unit(int(d/time.Minute), "minute")
	case d < 48*time.Hour:
		return unit(int(d/time.Hour), "hour")
	default:
		return unit(int(d/(24*time.Hour)), "day")
	}
}

func orUnnamed(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(unnamed)"
	}
	return s
}
