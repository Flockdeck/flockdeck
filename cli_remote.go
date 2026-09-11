package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/jmwri/flockdeck/internal/remote"
	"github.com/jmwri/flockdeck/internal/server"
)

// `flockdeck remote` enrols this machine with a relay, and manages what is
// paired with it.
//
// Enrolling is done here rather than from the window because it is the one
// step that decides where the traffic goes: which relay, under what name, into
// whose account. That is worth a deliberate command, and a terminal is where a
// relay address and an invitation code are pasted anyway. Everything after it
// — pairing a device, seeing what is paired, unpairing one — can be done from
// either.

// remoteIO is where the subcommand writes, and how it reaches a running
// instance, gathered so a test can drive it without one.
type remoteIO struct {
	out io.Writer
	// reload tells a running instance that the enrolment has changed, and
	// reports whether there was one to tell.
	reload func() (bool, error)
}

// runRemote implements the `remote` subcommand.
func runRemote(args []string) error {
	return remoteCmd(args, remoteIO{out: os.Stdout, reload: reloadRunningRemote})
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
		if len(args) != 2 {
			remoteUsage(rio.out)
			return errors.New("usage: flockdeck remote revoke <device id>")
		}
		err = remoteRevokeCmd(args[1], rio)
	case "disable":
		err = remoteDisable(args[1:], rio)
	case "-h", "--help", "help":
		remoteUsage(rio.out)
		return nil
	default:
		remoteUsage(rio.out)
		return fmt.Errorf("unknown command %q", args[0])
	}
	if errors.Is(err, errHelpAsked) {
		return nil
	}
	return err
}

func remoteUsage(out io.Writer) {
	fmt.Fprintf(out, "Usage: flockdeck remote <command>\n\n")
	fmt.Fprintf(out, "Reach this machine's agents from another device, through a relay.\n\n")
	fmt.Fprintf(out, "Commands:\n")
	fmt.Fprintf(out, "  enable [-relay URL] [-name N] [-join CODE] [-invite CODE]\n")
	fmt.Fprintf(out, "                 enrol this machine with a relay\n")
	fmt.Fprintf(out, "  pair [-desktop]  print a one-time link (and QR code) that pairs a device;\n")
	fmt.Fprintf(out, "                 -desktop prints a code for enrolling another machine instead\n")
	fmt.Fprintf(out, "  status         say whether remote access is on, and connected\n")
	fmt.Fprintf(out, "  devices        list the paired devices and enrolled machines\n")
	fmt.Fprintf(out, "  revoke <id>    unpair a device\n")
	fmt.Fprintf(out, "  disable [-force]  remove this machine from the relay\n\n")
	fmt.Fprintf(out, "The relay is %s unless -relay or %s says otherwise.\n", remote.DefaultRelay, remote.RelayEnv)
	fmt.Fprintf(out, "Traffic is encrypted on its way to and from the relay, which forwards it.\n")
}

// remoteFlags is a flag set for one of the subcommands, reporting to stderr
// the way `spawn` does.
func remoteFlags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet("remote "+name, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
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
		return fmt.Errorf("unexpected %q", fs.Arg(0))
	}
	return nil
}

// enrolled loads the enrolment, and says what to do when there is none.
func enrolled() (*remote.Config, error) {
	cfg, err := remote.Load()
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		return nil, remote.ErrNotEnabled
	}
	return cfg, nil
}

// The subcommands' flag sets are built by functions of their own, like the
// top-level one, so that the test holding the help page to the command line
// walks the same definitions the program parses with.

type remoteEnableFlags struct{ relay, name, join, invite string }

func remoteEnableFlagSet(f *remoteEnableFlags) *flag.FlagSet {
	fs := remoteFlags("enable")
	fs.StringVar(&f.relay, "relay", "", "the relay's `URL` (default "+remote.DefaultRelay+", or $"+remote.RelayEnv+")")
	fs.StringVar(&f.name, "name", "", "what this machine is called on your devices (default: its host name)")
	fs.StringVar(&f.join, "join", "", "a `code` from `flockdeck remote pair -desktop` on another machine, to join its account")
	fs.StringVar(&f.invite, "invite", "", "an invitation `code`, for a relay that asks for one")
	return fs
}

type remotePairFlags struct{ desktop bool }

func remotePairFlagSet(f *remotePairFlags) *flag.FlagSet {
	fs := remoteFlags("pair")
	fs.BoolVar(&f.desktop, "desktop", false, "print a code that enrols another machine into this account, rather than pairing a device")
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
	relay, name, join, invite := &f.relay, &f.name, &f.join, &f.invite
	relayURL, err := remote.RelayURL(*relay)
	if err != nil {
		return err
	}

	ctx := context.Background()
	existing, err := remote.Load()
	if err != nil {
		return err
	}
	if existing != nil {
		// Enrolling a second time would leave the first host on the relay
		// with nobody holding its token, listed on every device as a machine
		// that is never online. The one time it is right is when the relay
		// has already forgotten the first.
		_, err := remote.NewClient(existing, version).Devices(ctx)
		switch {
		case err == nil:
			return fmt.Errorf("remote access is already enabled, with %s; run `flockdeck remote disable` first to enrol this machine again", existing.Relay)
		case !remote.IsRevoked(err):
			return fmt.Errorf("remote access is already enabled with %s, which could not be asked whether it still is (%v); run `flockdeck remote disable -force` to start again", existing.Relay, err)
		}
		fmt.Fprintln(rio.out, "the relay no longer knows this machine, so it is being enrolled again")
	}

	hostName := strings.TrimSpace(*name)
	if hostName == "" {
		hostName, _ = os.Hostname()
	}
	reg, err := remote.Register(ctx, relayURL, version, remote.RegisterRequest{
		Name: hostName, Join: strings.TrimSpace(*join), Invite: strings.TrimSpace(*invite),
	})
	if err != nil {
		return err
	}
	cfg := &remote.Config{
		Relay: relayURL, HostID: reg.HostID, AccountID: reg.AccountID, Token: reg.Token, Name: hostName,
	}
	if err := cfg.Save(); err != nil {
		// The relay now has a host that nothing here can speak for. Take it
		// back off rather than leave it listed on every device for good.
		_ = remote.NewClient(cfg, version).Unregister(ctx)
		return err
	}
	fmt.Fprintf(rio.out, "remote access enabled: this machine is %q on %s\n", hostName, relayURL)
	reportReload(rio, "flockdeck will connect to the relay when it next starts")
	fmt.Fprintln(rio.out, "Pair a device with: flockdeck remote pair")
	return nil
}

func remotePairCmd(args []string, rio remoteIO) error {
	var f remotePairFlags
	if err := parseRemote(remotePairFlagSet(&f), args); err != nil {
		return err
	}
	desktop := &f.desktop
	cfg, err := enrolled()
	if err != nil {
		return err
	}
	kind := remote.KindDevice
	if *desktop {
		kind = remote.KindHost
	}
	p, err := remote.NewClient(cfg, version).Pair(context.Background(), kind)
	if err != nil {
		return err
	}
	until := describeExpiry(p.ExpiresAt, time.Now())
	if *desktop {
		fmt.Fprintf(rio.out, "On the other machine, run:\n\n  flockdeck remote enable -relay %s -join %s\n\n", cfg.Relay, p.Code)
		fmt.Fprintf(rio.out, "The code works once, %s.\n", until)
		return nil
	}
	if p.URL == "" {
		return errors.New("the relay sent a pairing code but no link to open it with")
	}
	if code, err := remote.QRTerminal(p.URL); err == nil {
		fmt.Fprintln(rio.out, code)
	}
	fmt.Fprintf(rio.out, "Scan the code, or open this link on the device you want to pair:\n\n  %s\n\n", p.URL)
	fmt.Fprintf(rio.out, "It works once, %s. Whoever opens it can drive every agent here,\n", until)
	fmt.Fprintf(rio.out, "so treat it like a password until then.\n")
	return nil
}

func remoteStatusCmd(args []string, rio remoteIO) error {
	if err := parseRemote(remoteFlags("status"), args); err != nil {
		return err
	}
	cfg, err := remote.Load()
	if err != nil {
		return err
	}
	if cfg == nil {
		fmt.Fprintln(rio.out, "remote access is not enabled; `flockdeck remote enable` turns it on")
		return nil
	}
	fmt.Fprintf(rio.out, "relay:   %s\n", cfg.Relay)
	fmt.Fprintf(rio.out, "machine: %s (%s)\n", cfg.Name, cfg.HostID)
	roster, err := remote.NewClient(cfg, version).Devices(context.Background())
	switch {
	case remote.IsRevoked(err):
		fmt.Fprintln(rio.out, "state:   the relay no longer accepts this machine; `flockdeck remote enable` enrols it again")
		return nil
	case err != nil:
		fmt.Fprintf(rio.out, "state:   could not reach the relay: %v\n", err)
		return nil
	}
	online := false
	for _, h := range roster.Hosts {
		if h.Self {
			online = h.Online
		}
	}
	if online {
		fmt.Fprintln(rio.out, "state:   connected")
	} else {
		fmt.Fprintln(rio.out, "state:   not connected — flockdeck connects while it is running")
	}
	fmt.Fprintf(rio.out, "devices: %d paired\n", len(roster.Devices))
	return nil
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
		return err
	}
	printRoster(rio.out, roster, time.Now())
	return nil
}

// printRoster lists what the account has: the devices first, since they are
// what `revoke` takes, with the id it takes them by.
func printRoster(out io.Writer, r *remote.Roster, now time.Time) {
	if len(r.Devices) == 0 {
		fmt.Fprintln(out, "No devices are paired. `flockdeck remote pair` pairs one.")
	} else {
		fmt.Fprintln(out, "Devices:")
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
			if h.Online {
				state = "online"
			}
			self := ""
			if h.Self {
				self = " (this one)"
			}
			fmt.Fprintf(out, "  %s%s — %s\n", orUnnamed(h.Name), self, state)
		}
	}
}

func remoteRevokeCmd(id string, rio remoteIO) error {
	cfg, err := enrolled()
	if err != nil {
		return err
	}
	if err := remote.NewClient(cfg, version).Revoke(context.Background(), id); err != nil {
		return err
	}
	fmt.Fprintf(rio.out, "unpaired %s; any window it had open has been closed\n", id)
	return nil
}

func remoteDisable(args []string, rio remoteIO) error {
	var f remoteDisableFlags
	if err := parseRemote(remoteDisableFlagSet(&f), args); err != nil {
		return err
	}
	force := &f.force
	cfg, err := remote.Load()
	if err != nil {
		if !*force {
			return fmt.Errorf("%w (-force removes it anyway)", err)
		}
		cfg = nil
	}
	if cfg != nil {
		err := remote.NewClient(cfg, version).Unregister(context.Background())
		switch {
		case err == nil, remote.IsRevoked(err):
			// Gone either way: removed now, or already.
		case !*force:
			return fmt.Errorf("could not tell the relay (%v); run again with -force to forget the enrolment here anyway — the relay will list this machine until it is removed from a paired device", err)
		default:
			fmt.Fprintf(rio.out, "could not tell the relay (%v); forgetting the enrolment here anyway\n", err)
		}
	} else if !*force {
		fmt.Fprintln(rio.out, "remote access is not enabled")
		return nil
	}
	if err := remote.Clear(); err != nil {
		return err
	}
	fmt.Fprintln(rio.out, "remote access disabled")
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
	return true, server.RequestRemoteReload(base, inst.Token)
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
	return fmt.Sprintf("until %s (%s from now)", at.Local().Format("15:04"), plainDuration(left))
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
