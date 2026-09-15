package main

import (
	"bufio"
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
	// confirm asks the person at the terminal a yes-or-no question, and is
	// nil when nobody is there to answer one.
	confirm func(question string) bool
}

// runRemote implements the `remote` subcommand.
func runRemote(args []string) error {
	rio := remoteIO{out: os.Stdout, reload: reloadRunningRemote, running: func() bool {
		inst, _, err := runningInstance()
		return err == nil && inst != nil
	}}
	// A question is only asked of somebody who can answer it. Piped into, the
	// command is a script, which says -yes instead.
	if stdinIsTerminal() {
		in := bufio.NewReader(os.Stdin)
		rio.confirm = func(question string) bool {
			fmt.Fprint(os.Stderr, question+" [y/N] ")
			line, _ := in.ReadString('\n')
			answer := strings.ToLower(strings.TrimSpace(line))
			return answer == "y" || answer == "yes"
		}
	}
	return remoteCmd(args, rio)
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
	case "rename":
		err = remoteRenameCmd(args[1:], rio)
	case "disable":
		err = remoteDisable(args[1:], rio)
	case "move":
		err = remoteMoveCmd(args[1:], rio)
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
	case "rename":
		fs = remoteRenameFlagSet(&remoteRenameFlags{})
	case "move":
		fs = remoteMoveFlagSet(&remoteMoveFlags{})
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
var remoteCommands = []string{"enable", "pair", "status", "devices", "revoke", "rename", "disable", "move"}

// remoteGuesses are words somebody is likely to try for one of them. They are
// answered with the command's name rather than taken for it: revoke and
// disable cost something, and are not run on a guess.
var remoteGuesses = map[string]string{
	"on": "enable", "start": "enable", "setup": "enable", "register": "enable",
	"off": "disable", "stop": "disable", "unregister": "disable",
	"unpair": "revoke", "remove": "revoke", "rm": "revoke", "delete": "revoke",
	"add": "pair", "link": "pair", "qr": "pair",
	"info": "status", "state": "status",
	"name": "rename", "mv": "rename",
	"migrate": "move", "switch": "move",
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
  rename <name>  rename this machine; -device <id or name> renames a device
  disable [-force]  remove this machine from the relay
  move <relay>   enrol with another relay, then leave this one once it answers

Run flockdeck remote help <command> for more about one of them.
The relay is %s unless -relay or %s
says otherwise.
To move to another relay, run move; every device then has to pair again.
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
	"rename": {" [-device <id or name>] <new name>",
		"Give this machine a new name, which is what every paired device and the\naccount's other machines call it. With -device, rename a paired device\ninstead; `flockdeck remote devices` lists them by id and name."},
	"disable": {" [-force]",
		"Take this machine off its relay. If it is the account's only machine, its\npaired devices are unpaired too."},
	"move": {" [flags] <relay>",
		"Move this machine to another relay: enrol it there first, and take it off\nthe relay it is on only once the new one answers. Every device paired with\nit has to pair again afterwards, with the new relay, because a device's\npairing belongs to the relay it was made on."},
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

// parseFlags parses args with fs, turning -h/--help into errHelpAsked so a
// subcommand need not compare against flag.ErrHelp itself.
func parseFlags(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return errHelpAsked
		}
		return errReported
	}
	return nil
}

// parseRemote parses a subcommand's flags and refuses anything left over.
func parseRemote(fs *flag.FlagSet, args []string) error {
	if err := parseFlags(fs, args); err != nil {
		return err
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

// Environment variables behind -name, -join and -invite, alongside -relay's
// existing remote.RelayEnv, so a first-boot container entrypoint or
// provisioning script can enrol with zero interactive input. A one-time
// bootstrap step, usually run once by hand, so this is a minor addition next
// to the steady-state launch flags above -- not something every restart needs.
const (
	remoteJoinEnv   = "FLOCKDECK_REMOTE_JOIN"
	remoteInviteEnv = "FLOCKDECK_REMOTE_INVITE"
	remoteNameEnv   = "FLOCKDECK_REMOTE_NAME"
)

func remoteEnableFlagSet(f *remoteEnableFlags) *flag.FlagSet {
	fs := remoteFlags("enable")
	// PrintDefaults starts a description at column 8 and indents a line break
	// in one the same way, so the long ones are broken to fit 80 columns.
	fs.StringVar(&f.relay, "relay", "", "the relay's `URL`\n(default: $"+remote.RelayEnv+" if set, else "+remote.DefaultRelay+")")
	fs.StringVar(&f.name, "name", os.Getenv(remoteNameEnv), "the `name` this machine goes by on your devices\n(default: $"+remoteNameEnv+" if set, else its host name)")
	fs.StringVar(&f.join, "join", os.Getenv(remoteJoinEnv), "a `code` from `flockdeck remote pair -desktop` on another machine,\nto join its account (default: $"+remoteJoinEnv+" if set)")
	fs.StringVar(&f.invite, "invite", os.Getenv(remoteInviteEnv), "an invitation `code`, for a relay that asks for one\n(default: $"+remoteInviteEnv+" if set)")
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

type remoteMoveFlags struct {
	name, join, invite string
	yes                bool
}

func remoteMoveFlagSet(f *remoteMoveFlags) *flag.FlagSet {
	fs := remoteFlags("move")
	fs.StringVar(&f.invite, "invite", "", "an invitation `code`, for a new relay that asks for one")
	fs.StringVar(&f.join, "join", "", "a `code` from `flockdeck remote pair -desktop` on a machine already on\nthe new relay, to join its account")
	fs.StringVar(&f.name, "name", "", "the `name` this machine goes by on the new relay (default: the one it has)")
	fs.BoolVar(&f.yes, "yes", false, "move without asking first, as a script has to")
	return fs
}

type remoteRenameFlags struct{ device string }

func remoteRenameFlagSet(f *remoteRenameFlags) *flag.FlagSet {
	fs := remoteFlags("rename")
	fs.StringVar(&f.device, "device", "", "the paired `device` to rename, by its id or its name,\nrather than this machine")
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
	fmt.Fprintln(rio.out, fitted(fmt.Sprintf("remote access enabled: this machine is %q on %s", cfg.Name, cfg.Relay)))
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
		// Asking for another relay is moving this machine, which move does:
		// enrolling there first, and leaving this one, reachable or not, only
		// once the new one answers.
		if want, _ := remote.RelayURL(f.relay); want != "" && !remote.SameRelay(want, already.Relay) {
			// A relay that is not there is found out now, rather than sent to
			// move to be found out there.
			if perr := remote.Probe(context.Background(), want); perr != nil {
				return fmt.Errorf("%v; %s could not be reached (%v), so check that address before moving this machine there", err, want, perr)
			}
			return fmt.Errorf("%v; to move this machine to %s, run `flockdeck remote move %s`, after which every paired device has to pair again", err, want, want)
		}
	}
	switch {
	case errors.As(err, &already) && already.Err == nil && f.join != "":
		// A join code is asking for another account, which this machine can
		// be in only after leaving the one it is in.
		return fmt.Errorf("%v; to join that account instead, run `flockdeck remote disable` first, which unpairs this machine's devices if it is its account's only machine, then run this again", err)
	case errors.As(err, &already) && already.Err == nil && renaming(f.name):
		// A new name is something to do, and rename does it without enrolling
		// again, which would cost the account's devices if this is its only
		// machine; the cost is said, for whoever meant to enrol again anyway.
		// Quoted when it has to be: "Jim's laptop" pasted as it stood left the
		// shell waiting on the closing quote of its apostrophe, and a name of
		// two words renamed the machine to the first.
		return fmt.Errorf("%v; to give this machine a new name, run `flockdeck remote rename %s`, which keeps what is paired, where enrolling it again, after `flockdeck remote disable`, unpairs its devices if it is the account's only machine", err, cliWord(strings.TrimSpace(f.name)))
	case errors.As(err, &already) && already.Err == nil:
		// Whoever runs enable twice most likely forgot the first; the way to
		// enrol again costs the account's devices if this is its only
		// machine, so that is said before anybody takes it.
		return fmt.Errorf("%v, so there is nothing to do; to enrol this machine again, run `flockdeck remote disable` first, which unpairs its devices if it is the account's only machine", err)
	case errors.As(err, &already):
		// A relay out of reach is most often a network that is down for now,
		// when trying again is the answer. -force is for a relay that is gone
		// for good, and it costs a listing there that only a paired device
		// can then take off.
		return fmt.Errorf("%v; try again once the relay can be reached, or, if it is gone for good, run `flockdeck remote disable -force` to start again, which leaves this machine listed on it", err)
	case f.invite == "" && strings.Contains(err.Error(), "needs an invite"):
		// An invite code is wanted. The relay's words are matched rather than
		// its 403, which a relay closed to new accounts also answers, and for
		// which an invite does not help. Today's relay says "needs an invite
		// to create an account" and names the flag itself; this matched only
		// an older relay's "needs an invite code", so it never came into play.
		return withAdvice(err, "-invite", "pass it with -invite CODE")
	case f.invite != "" && strings.Contains(err.Error(), "invite code is not valid"):
		// An invitation that has been used, or was never one. The relay's
		// words are matched because its 403 is also the closed relay's and
		// the missing invitation's.
		return withAdvice(err, "whoever runs the relay", "whoever runs the relay makes invitations, and can make another")
	case strings.Contains(err.Error(), "not accepting new accounts"):
		// A relay closed to new accounts still takes machines into the ones
		// it has, which is the way in that is left. Its words are matched for
		// the reason the invite's are.
		return withAdvice(err, "remote pair -desktop", "a machine already on it can take this one into its account: `flockdeck remote pair -desktop` there prints the command to run here")
	case f.join != "" && errors.As(err, &refused) && refused.Status == http.StatusBadRequest:
		// A join code that has run out, been used, or was never one. With a
		// join code given, the relay's 400 is about the code; its other one
		// is for a body this client never sends.
		return withAdvice(err, "remote pair -desktop", "`flockdeck remote pair -desktop` on the other machine makes a new one")
	case f.join != "" && errors.As(err, &refused) && refused.Status == http.StatusConflict:
		// An account with all the machines it may have.
		return withAdvice(err, "remote disable", "running `flockdeck remote disable` on one of that account's machines does that")
	}
	return err
}

// withAdvice puts advice after the relay's refusal, unless the refusal already
// gives it -- told by whether it names the flag or command, said. The relay's
// words have come to say what to do themselves, and the same thing said a
// second time, in other words, read as two things to do.
func withAdvice(err error, said, advice string) error {
	if strings.Contains(err.Error(), said) {
		return err
	}
	return fmt.Errorf("%v; %s", err, advice)
}

// cliWord writes one argument of a command the user is told to run so that it
// can be pasted as it stands: bare when it is made only of what a URL or a
// code is, and quoted otherwise, since a name like "Jim's laptop" split in
// two, or left a shell waiting on the closing quote of its apostrophe.
func cliWord(s string) string {
	// Single quotes keep every character, where double quotes still let sh
	// read a $, a backtick, a double quote or a backslash inside them -- a
	// name ending in a backslash escaped the closing quote and left the shell
	// waiting on another -- and let bash and zsh read a ! as a history event.
	if strings.ContainsAny(s, "$`\"\\!") {
		return `'` + strings.ReplaceAll(s, `'`, `'\''`) + `'`
	}
	if s != "" && !strings.ContainsFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._-:/@+=", r))
	}) {
		return s
	}
	return `"` + s + `"`
}

// renaming reports whether name, given to enable on a machine already
// enrolled, is not the name it is enrolled under.
func renaming(name string) bool {
	if strings.TrimSpace(name) == "" {
		return false
	}
	cfg, err := remote.Load()
	return err == nil && cfg != nil && strings.TrimSpace(name) != cfg.Name
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
			fmt.Fprintln(rio.out, labelled("relay:", err.Error()))
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
	roster, err := remote.NewClient(cfg, version).Devices(context.Background())
	// The relay's name for this machine, when it answers: a paired device may
	// have renamed it since the name here was saved.
	name := cfg.Name
	if err == nil {
		for _, h := range roster.Hosts {
			if h.Self && strings.TrimSpace(h.Name) != "" {
				name = h.Name
			}
		}
	}
	fmt.Fprintln(rio.out, labelled("machine:", fmt.Sprintf("%s (%s)", name, cfg.HostID)))
	switch {
	case remote.IsRevoked(err):
		fmt.Fprintln(rio.out, labelled("state:", remote.RevokedReason(err))+";\n         `flockdeck remote enable` enrols it again")
		return nil
	case err != nil:
		// A relay that answered with an error was reached, so this says only
		// that it could not be asked, which is true either way.
		fmt.Fprintln(rio.out, labelled("state:", "could not ask the relay: "+err.Error()))
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

// labelled is one of status's lines whose value is not known until it is
// printed, a relay's refusal or a machine's name: the label, padded to the
// values' column, and the value broken at spaces to fit 80 columns, carrying
// on under the value, as the lines written out whole do. A word longer than
// a line is left whole on one of its own.
func labelled(label, value string) string {
	const col = len("machine: ")
	return breakAt(fmt.Sprintf("%-*s", col, label), col, value)
}

// fitted is a sentence the command line prints whose words are not all its
// own, a name or an error, broken at spaces to fit 80 columns.
func fitted(text string) string {
	return breakAt("", 0, text)
}

// breakAt writes prefix and then text's words, starting a new line, indented
// by indent, before any word that would take a line past 80 columns. A word
// longer than a line is left whole on one of its own.
func breakAt(prefix string, indent int, text string) string {
	var b strings.Builder
	b.WriteString(prefix)
	at := len([]rune(prefix))
	for i, word := range strings.Fields(text) {
		w := len([]rune(word))
		if i > 0 {
			if at+1+w > 80 {
				b.WriteString("\n" + strings.Repeat(" ", indent))
				at = indent
			} else {
				b.WriteByte(' ')
				at++
			}
		}
		b.WriteString(word)
		at += w
	}
	return b.String()
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
// what `revoke` takes, with the ids and names it takes them by. idle is
// flockdeck not running here, which is why this machine is offline when it
// is.
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
			// A name may be 64 characters of several words; a row too long
			// for the line carries on under the name, past the id.
			id := fmt.Sprintf("  %-*s  ", width, d.ID)
			fmt.Fprintln(out, breakAt(id, len(id), orUnnamed(d.Name)+" — last seen "+ago(d.LastSeen, now)))
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
			// Carried on two columns in from the name, so that the rest of a
			// long row is not taken for the next machine.
			fmt.Fprintln(out, breakAt("  ", 4, orUnnamed(h.Name)+self+" — "+state))
		}
	}
}

func remoteRevokeCmd(args []string, rio remoteIO) error {
	// The id goes through a flag set like every other argument here, so that
	// -h asks how revoke is used rather than going to the relay as a device.
	fs := remoteFlags("revoke")
	if err := parseFlags(fs, args); err != nil {
		return err
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
	fmt.Fprintln(rio.out, fitted(fmt.Sprintf("unpaired %s; any window it had open has been closed", id)))
	return nil
}

func remoteRenameCmd(args []string, rio remoteIO) error {
	var f remoteRenameFlags
	fs := remoteRenameFlagSet(&f)
	// -device may come after the name, as in `rename Sam's phone -device d1`,
	// and is taken out of it as spawn's flags are taken out of a task.
	if err := parseFlags(fs, orderSpawnArgs(fs, args)); err != nil {
		return err
	}
	// A name of several words, typed without quotes, arrives as several
	// arguments, and is the one name.
	name := strings.Join(fs.Args(), " ")
	if strings.TrimSpace(name) == "" {
		// Whoever typed rename knows the command and missed the name, so its
		// own usage is the answer.
		fs.Usage()
		return errReported
	}
	clean, err := remote.CheckName(name)
	if err != nil {
		return err
	}
	cfg, err := enrolled()
	if err != nil {
		return err
	}
	if f.device == "" {
		if _, err := remote.Rename(context.Background(), version, clean); err != nil {
			return renameRefusal(err)
		}
		fmt.Fprintln(rio.out, fitted(fmt.Sprintf("this machine is now %q on every paired device", clean)))
		reportReload(rio, "")
		return nil
	}
	id, old, err := pickDevice(rosterBriefly(cfg), f.device)
	var machine *notADeviceError
	switch {
	case errors.As(err, &machine) && machine.host.Self:
		return errors.New("that is this machine, not a device; `flockdeck remote rename <new name>`, without -device, renames it")
	case errors.As(err, &machine):
		return fmt.Errorf("%q is another of the account's machines, not a device; it is renamed from the Devices page of a paired device, or with `flockdeck remote rename` on it", orUnnamed(strings.TrimSpace(machine.host.Name)))
	case err != nil:
		return err
	}
	if err := remote.NewClient(cfg, version).RenameDevice(context.Background(), id, clean); err != nil {
		return renameRefusal(err)
	}
	if old != "" {
		id = old + " (" + id + ")"
	}
	fmt.Fprintln(rio.out, fitted(fmt.Sprintf("renamed %s to %q", id, clean)))
	return nil
}

// renameRefusal adds what to do to the relay refusing a rename. A relay from
// before renaming has no such request, and answers it as a method its path
// does not allow; an id it does not know is answered "no such device", which
// is true but not where to look.
func renameRefusal(err error) error {
	var refused *remote.APIError
	switch {
	case errors.As(err, &refused) && refused.Status == http.StatusMethodNotAllowed:
		return fmt.Errorf("%v; this relay is older than renaming, so a new name has to wait until it is updated", err)
	case errors.As(err, &refused) && refused.Status == http.StatusNotFound && refused.Message != "":
		return fmt.Errorf("%v; `flockdeck remote devices` lists the paired ones, by id and name", err)
	}
	return relayRefusal(err)
}

// pickDevice is the device revoke means by arg, as its id and its name: the
// device with that id, in any case, else the one with that name, which is
// what the devices list leads with and what somebody holding the phone knows
// it by. Two devices with the name are not guessed between, and one of the
// account's machines is refused as what it is. Without a roster, arg is taken
// for an id, and the relay says whether it is one.
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
		// Not a device, but perhaps one of the account's machines, which is
		// what somebody tidying away a lost desktop from the list would try;
		// sent as a device's id it would come back "no such device".
		for _, h := range r.Hosts {
			if want != "" && (h.ID == arg || strings.EqualFold(h.ID, want) || strings.EqualFold(strings.TrimSpace(h.Name), want)) {
				return "", "", &notADeviceError{host: h}
			}
		}
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

// notADeviceError is pickDevice finding one of the account's machines where a
// device was meant. Its words say what takes a machine off, which is what
// revoke was reaching for; rename says what renames one instead.
type notADeviceError struct{ host remote.Host }

func (e *notADeviceError) Error() string {
	if e.host.Self {
		return "that is this machine, not a device; `flockdeck remote disable` takes it off the relay"
	}
	// The Devices page comes first: a machine that was wiped, which is what
	// most often brings somebody here, has no Flockdeck left to run disable.
	return fmt.Sprintf("%q is another of the account's machines, not a device; to take it off, remove it from the Devices page of a paired device, or run `flockdeck remote disable` on it", orUnnamed(strings.TrimSpace(e.host.Name)))
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
		// Forgetting the enrolment here leaves this machine listed on the
		// relay until a paired device removes it, and that is the cost. A
		// relay out of reach is most often a network down for now, so trying
		// again is said first, as enable's refusal says it.
		return fmt.Errorf("%v; try again once the relay can be reached, or, if it is gone for good, run again with -force to forget the enrolment here anyway — the relay will then list this machine, offline, until it is removed from the Devices page of a paired device", err)
	case err != nil:
		return err
	case !had:
		// Nothing was there, readable or not, -force or no: there was
		// nothing to turn off, and nothing to tell a running instance.
		fmt.Fprintln(rio.out, "remote access is not enabled")
		return nil
	case untold != nil:
		fmt.Fprintln(rio.out, fitted(fmt.Sprintf("could not tell the relay (%v); forgetting the enrolment here anyway", untold)))
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

// remoteMoveCmd moves this machine to another relay. What it costs, every
// device pairing again, is said before anything is done, and asked about
// when somebody is there to answer; a script says -yes instead.
func remoteMoveCmd(args []string, rio remoteIO) error {
	var f remoteMoveFlags
	fs := remoteMoveFlagSet(&f)
	// The flags may come after the relay, as in `move relay.example -invite C`.
	if err := fs.Parse(orderSpawnArgs(fs, args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return errHelpAsked
		}
		return errReported
	}
	if fs.NArg() != 1 {
		// Whoever typed move knows the command and missed the relay, or gave
		// more than one, so its own usage is the answer.
		fs.Usage()
		return errReported
	}
	cfg, err := enrolled()
	if errors.Is(err, remote.ErrNotEnabled) {
		return fmt.Errorf("remote access is not enabled here, so there is nothing to move; `flockdeck remote enable -relay %s` enrols this machine there", fs.Arg(0))
	}
	if err != nil {
		return err
	}
	want, err := remote.CheckRelay(fs.Arg(0))
	if err != nil {
		return err
	}
	if remote.SameRelay(want, cfg.Relay) {
		return fmt.Errorf("this machine is already on %s, so there is nowhere to move it", cfg.Relay)
	}
	// A relay that is not there is found out before anybody is asked to
	// agree to moving to it.
	if err := remote.Probe(context.Background(), want); err != nil {
		return fmt.Errorf("%s could not be reached (%v); nothing has changed, and this machine is still on %s", want, err, cfg.Relay)
	}

	// What moving costs is said before it is done, since moving back does not
	// undo it: the pairings made on the old relay stay there.
	fmt.Fprintln(rio.out, fitted(fmt.Sprintf("This moves %q from %s to %s: it enrols with the new relay first, and leaves the old one only once the new one answers.", cfg.Name, cfg.Relay, want)))
	fmt.Fprintln(rio.out, fitted("Every device paired with this machine will then have to pair again, with the new relay, because a device's pairing belongs to the relay it was made on."))
	switch lost := devicesLost(cfg); {
	case lost == 1:
		fmt.Fprintln(rio.out, fitted("It is the only machine in its account on the old relay, so the account goes too, and its paired device with it."))
	case lost > 1:
		fmt.Fprintln(rio.out, fitted(fmt.Sprintf("It is the only machine in its account on the old relay, so the account goes too, and its %d paired devices with it.", lost)))
	}
	if !f.yes {
		if rio.confirm == nil {
			return errors.New("nothing has changed; run it again with -yes to move without being asked, as a script has to")
		}
		if !rio.confirm("Move it?") {
			fmt.Fprintln(rio.out, "nothing has changed")
			return nil
		}
	}

	moved, untold, err := remote.Move(context.Background(), version,
		remote.EnableRequest{Relay: want, Name: f.name, Join: f.join, Invite: f.invite},
		// The running instance moves its tunnel across before the old relay
		// is told, rather than seeing it cut by the old relay first.
		func() { reportReload(rio, "flockdeck will connect to the new relay when it next starts") })
	if err != nil {
		// The refusals are enable's -- an invitation needed, a join code used
		// up -- and so is their advice, with the flags move takes too.
		advised := enableAdvice(remoteEnableFlags{name: f.name, join: f.join, invite: f.invite}, err)
		return fmt.Errorf("%v (this machine is still on %s)", advised, cfg.Relay)
	}
	fmt.Fprintln(rio.out, fitted(fmt.Sprintf("moved: this machine is %q on %s", moved.Name, moved.Relay)))
	if untold != nil {
		fmt.Fprintln(rio.out, fitted(fmt.Sprintf("%s could not be told (%v), so it will go on listing this machine, offline, until a device paired there removes it", cfg.Relay, untold)))
	}
	fmt.Fprintln(rio.out, "Pair each device again with `flockdeck remote pair`, or Remote access… in the\nwindow.")
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
		fmt.Fprintln(rio.out, fitted(fmt.Sprintf("the running flockdeck could not be told (%v); restart it to pick this up", err)))
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
