package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/jmwri/flockdeck/internal/helpers"
	"github.com/jmwri/flockdeck/internal/server"
)

// `flockdeck helpers` installs and runs helper apps: small local programs that
// Flockdeck downloads, checks, starts and opens in the browser. list, install
// and uninstall work from the state directory and need no running instance.
// start, stop and open ask the running instance, which is the one that
// supervises the process (see internal/helpers and docs/plans/helper-apps.md).

// helperCLI is what the helpers subcommand talks to, so a test can give it
// fakes.
type helperCLI struct {
	out, errOut io.Writer
	in          io.Reader
	interactive bool
	store       *helpers.Store
	installer   *helpers.Installer
	// request asks the running instance; nil instance means none is running.
	instance func() (url, token string, err error)
	request  func(base, token, action, id string) (helpers.Status, error)
}

func helpersUsage(out io.Writer) {
	fmt.Fprint(out, `Usage:
  flockdeck helpers list [-check]
  flockdeck helpers install [-version=<version>] [-allow-unsigned] [-yes] <helper>
  flockdeck helpers start|stop|open <helper>
  flockdeck helpers uninstall [-purge-data] [-yes] <helper>

A helper app is a small program Flockdeck downloads, checks against its release
key and the SHA-256 in the signed manifest, installs under its own folder, and
runs for you. It is not part of Flockdeck and runs with your access.

list, install and uninstall work without Flockdeck running. start, stop and open
are done by the running Flockdeck, which supervises the helper, so it has to be
open. open shows the helper's page in your browser.

install shows what it is going to download and asks first; -yes skips the
question. A release with no signature is refused unless -allow-unsigned is
given, and then the SHA-256 of the archive is shown, which proves the download
matches the manifest and not who built it. A signature that is there and
wrong is always refused.

uninstall keeps the helper's data folder and says where it is; -purge-data
deletes it too.
`)
}

type helpersInstallFlags struct {
	version       string
	allowUnsigned bool
	yes           bool
}

func helpersInstallFlagSet(f *helpersInstallFlags) *flag.FlagSet {
	fs := flag.NewFlagSet("flockdeck helpers install", flag.ContinueOnError)
	fs.StringVar(&f.version, "version", "", "install this `version` (like 0.4.0) instead of the latest, forward or back")
	fs.BoolVar(&f.allowUnsigned, "allow-unsigned", false, "go on with a release that has no signature, once its hash has been shown")
	fs.BoolVar(&f.yes, "yes", false, "do not ask before installing")
	fs.Usage = func() { helpersUsage(fs.Output()) }
	return fs
}

type helpersUninstallFlags struct {
	purge bool
	yes   bool
}

func helpersUninstallFlagSet(f *helpersUninstallFlags) *flag.FlagSet {
	fs := flag.NewFlagSet("flockdeck helpers uninstall", flag.ContinueOnError)
	fs.BoolVar(&f.purge, "purge-data", false, "also delete the helper's data folder")
	fs.BoolVar(&f.yes, "yes", false, "do not ask before removing")
	fs.Usage = func() { helpersUsage(fs.Output()) }
	return fs
}

type helpersListFlags struct{ check bool }

func helpersListFlagSet(f *helpersListFlags) *flag.FlagSet {
	fs := flag.NewFlagSet("flockdeck helpers list", flag.ContinueOnError)
	fs.BoolVar(&f.check, "check", false, "also ask each helper's source for a newer version")
	fs.Usage = func() { helpersUsage(fs.Output()) }
	return fs
}

// runHelpers is `flockdeck helpers`, wired to the real state directory.
func runHelpers(args []string, out io.Writer) error {
	// A hidden subcommand: the supervisor runs it to send CTRL_BREAK to a
	// helper on Windows, from a process that is attached to the helper's
	// console and then exits. See helpers.CtrlBreak.
	if len(args) == 2 && args[0] == "ctrl-break" {
		pid, err := strconv.Atoi(args[1])
		if err != nil {
			return fmt.Errorf("ctrl-break: %q is not a process id", args[1])
		}
		st, err := helpers.DefaultStore()
		if err != nil {
			return err
		}
		return helpers.CtrlBreakHelper(st, pid)
	}
	st, err := helpers.DefaultStore()
	if err != nil {
		return err
	}
	c := &helperCLI{
		out: out, errOut: os.Stderr, in: os.Stdin, interactive: stdinIsTerminal(),
		store: st, installer: helpers.NewInstaller(helpers.Options{Store: st}),
		instance: func() (string, string, error) {
			inst, base, err := runningInstance()
			if err != nil || inst == nil {
				return "", "", err
			}
			return base, inst.Token, nil
		},
		request: server.HelperRequest,
	}
	return c.run(args)
}

func (c *helperCLI) run(args []string) error {
	if len(args) == 0 {
		helpersUsage(c.errOut)
		return errReported
	}
	switch args[0] {
	case "-h", "-help", "--help", "help":
		helpersUsage(c.out)
		return nil
	case "list":
		var f helpersListFlags
		fs := helpersListFlagSet(&f)
		if err := c.parse(fs, args[1:], 0, "list"); err != nil {
			return err
		}
		return c.list(f)
	case "install":
		var f helpersInstallFlags
		fs := helpersInstallFlagSet(&f)
		if err := c.parse(fs, args[1:], 1, "install"); err != nil {
			return err
		}
		return c.install(fs.Arg(0), f)
	case "uninstall":
		var f helpersUninstallFlags
		fs := helpersUninstallFlagSet(&f)
		if err := c.parse(fs, args[1:], 1, "uninstall"); err != nil {
			return err
		}
		return c.uninstall(fs.Arg(0), f)
	case "start", "stop", "open":
		fs := flag.NewFlagSet("flockdeck helpers "+args[0], flag.ContinueOnError)
		fs.Usage = func() { helpersUsage(fs.Output()) }
		if err := c.parse(fs, args[1:], 1, args[0]); err != nil {
			return err
		}
		return c.control(args[0], fs.Arg(0))
	}
	fmt.Fprintf(c.errOut, "flockdeck helpers: unknown command %q\n\n", args[0])
	helpersUsage(c.errOut)
	return errReported
}

// parse reads a command's flags, and wants exactly want arguments after them.
func (c *helperCLI) parse(fs *flag.FlagSet, args []string, want int, name string) error {
	fs.SetOutput(c.errOut)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return errHelpAsked
		}
		return errReported
	}
	if fs.NArg() == want {
		return nil
	}
	if fs.NArg() > want {
		fmt.Fprintf(c.errOut, "flockdeck helpers %s: unexpected %q\n", name, fs.Arg(want))
		if fs.Lookup(fs.Arg(want)) != nil {
			fmt.Fprintf(c.errOut, "Did you mean -%s?\n", fs.Arg(want))
		}
	} else {
		fmt.Fprintf(c.errOut, "flockdeck helpers %s: which helper? One of: %s\n", name, helperIDs())
	}
	fmt.Fprintln(c.errOut)
	helpersUsage(c.errOut)
	return errReported
}

func helperIDs() string {
	var ids []string
	for _, e := range helpers.Catalogue() {
		ids = append(ids, e.ID)
	}
	return strings.Join(ids, ", ")
}

func (c *helperCLI) entry(id string) (helpers.Entry, error) {
	e, ok := helpers.Lookup(id)
	if !ok {
		return helpers.Entry{}, fmt.Errorf("%q is not a helper Flockdeck knows; the helpers are: %s", id, helperIDs())
	}
	return e, nil
}

// confirm asks a yes or no question. Without a terminal there is nobody to
// ask, and a pipe never counts as an answer, so it fails and says to pass -yes.
func (c *helperCLI) confirm(question string, yes bool, flagName string) (bool, error) {
	if yes {
		return true, nil
	}
	if !c.interactive {
		return false, fmt.Errorf("this is not an interactive terminal; pass %s to go on without asking", flagName)
	}
	fmt.Fprintf(c.out, "%s [y/N] ", question)
	line, err := bufio.NewReader(c.in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	line = strings.TrimSpace(strings.ToLower(line))
	return line == "y" || line == "yes", nil
}

func (c *helperCLI) list(f helpersListFlags) error {
	w := tabwriter.NewWriter(c.out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "HELPER\tINSTALLED\tSTATUS\tNOTES")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	for _, e := range helpers.Catalogue() {
		info, installed := c.store.Info(e.ID)
		version, status, notes := "-", "not installed", ""
		if installed {
			version, status = info.Version, "stopped"
			if pid, running := c.store.RunningPID(e.ID); running {
				status = fmt.Sprintf("running (process %d)", pid)
			}
			if !info.Signed {
				notes = "Unsigned"
			}
			if f.check {
				newer, err := c.installer.CheckUpdate(ctx, e.ID)
				switch {
				case err != nil:
					notes = joinNotes(notes, "could not check: "+shortError(err))
				case newer != "" && helpers.UpdateAvailable(e, newer, info.Version):
					notes = joinNotes(notes, "Update available "+newer)
				}
			}
		} else if f.check {
			if newer, err := c.installer.CheckUpdate(ctx, e.ID); err == nil {
				notes = "Available " + newer
			} else {
				notes = "could not check: " + shortError(err)
			}
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", e.ID, version, status, notes)
	}
	return w.Flush()
}

func joinNotes(a, b string) string {
	if a == "" {
		return b
	}
	return a + "; " + b
}

func shortError(err error) string {
	s := strings.ReplaceAll(err.Error(), "\n", " ")
	if len(s) > 100 {
		s = s[:100] + "..."
	}
	return s
}

func (c *helperCLI) install(id string, f helpersInstallFlags) error {
	e, err := c.entry(id)
	if err != nil {
		return err
	}
	if f.version != "" {
		f.version = strings.TrimPrefix(f.version, "v")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	fmt.Fprintf(c.out, "Looking up %s...\n", e.Name)
	plan, err := c.installer.Plan(ctx, id, f.version)
	if err != nil {
		return c.explainInstall(e, err)
	}

	if plan.Repair {
		fmt.Fprintf(c.out, "\nRepair %s %s\n", e.Name, plan.Version)
		fmt.Fprintf(c.out, "  The installed copy no longer matches what was installed. It is replaced by a\n  checked copy of the same version.\n")
	} else {
		fmt.Fprintf(c.out, "\nInstall %s %s\n", e.Name, plan.Version)
	}
	fmt.Fprintf(c.out, "  %s\n", e.Summary)
	fmt.Fprintf(c.out, "  From:       %s\n", plan.URL)
	if plan.Signed {
		fmt.Fprintf(c.out, "  Signature:  manifest.json is signed with Flockdeck's release key; the archive\n              is checked against it when it is downloaded\n")
	} else {
		fmt.Fprintf(c.out, "  Signature:  NONE\n")
	}
	fmt.Fprintf(c.out, "  SHA-256:    %s\n", plan.SHA256)
	fmt.Fprintf(c.out, "  It may:\n")
	for _, a := range e.Allows {
		fmt.Fprintf(c.out, "    - %s\n", a)
	}
	fmt.Fprintf(c.out, "  This describes what it does by design. Any program running as you can read and\n  write a helper's files and reach it on this computer, and any account on this\n  computer can reach it with no sign-in. It is not sandboxed.\n")
	if plan.Installed != "" {
		fmt.Fprintf(c.out, "  Replaces:   %s\n", plan.Installed)
	}

	if !plan.Signed {
		fmt.Fprintf(c.out, "\nWARNING: this release is not signed.\n")
		fmt.Fprintf(c.out, "The SHA-256 above is from its manifest. It proves the download matches that\n")
		fmt.Fprintf(c.out, "file. It does not prove who built it: anyone who can change the release can\n")
		fmt.Fprintf(c.out, "change both. This applies to this version only and is asked again for the next.\n")
		if !f.allowUnsigned {
			return fmt.Errorf("%s %s has no signature, so it was not installed; run again with -allow-unsigned to install it anyway", e.Name, plan.Version)
		}
	}
	fmt.Fprintln(c.out)
	ok, err := c.confirm(fmt.Sprintf("Install %s %s?", e.Name, plan.Version), f.yes, "-yes")
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintln(c.out, "Not installed.")
		return nil
	}
	fmt.Fprintf(c.out, "Downloading %s...\n", plan.Archive)
	info, err := c.installer.InstallPlan(ctx, plan)
	if err != nil {
		return c.explainInstall(e, err)
	}
	note := ""
	if !info.Signed {
		note = " (unsigned)"
	}
	fmt.Fprintf(c.out, "Installed %s %s%s.\n", e.Name, info.Version, note)
	fmt.Fprintf(c.out, "Start it with: flockdeck helpers start %s\n", e.ID)
	return nil
}

// explainInstall puts an install failure in words for a person.
func (c *helperCLI) explainInstall(e helpers.Entry, err error) error {
	var sig *helpers.SignatureError
	var required *helpers.SignedRequiredError
	var arch *helpers.ArchiveError
	var sum *helpers.ChecksumError
	switch {
	case errors.As(err, &sig):
		return fmt.Errorf("%w\nNothing was installed, and this cannot be overridden: a signature that is there and wrong means the release is not what it claims to be", err)
	case errors.As(err, &required):
		return fmt.Errorf("%w\nNothing was installed, and -allow-unsigned does not apply to this", err)
	case errors.As(err, &sum), errors.As(err, &arch):
		return fmt.Errorf("%w\nNothing was installed.", err)
	case errors.Is(err, helpers.ErrNoAsset):
		return fmt.Errorf("%s has no build for this platform (%s/%s)", e.Name, goosArch()[0], goosArch()[1])
	case errors.Is(err, helpers.ErrBusy):
		return fmt.Errorf("%s is running; stop it first with: flockdeck helpers stop %s", e.Name, e.ID)
	}
	return err
}

func goosArch() [2]string {
	os, arch := helpers.Platform()
	return [2]string{os, arch}
}

func (c *helperCLI) uninstall(id string, f helpersUninstallFlags) error {
	e, err := c.entry(id)
	if err != nil {
		return err
	}
	if pid, running := c.store.RunningPID(id); running {
		return fmt.Errorf("%s is running (process %d); stop it first with: flockdeck helpers stop %s", e.Name, pid, id)
	}
	_, installed := c.store.Current(id)
	if !installed && !(f.purge && c.store.HasData(id)) {
		fmt.Fprintf(c.out, "%s is not installed.\n", e.Name)
		return nil
	}
	question := fmt.Sprintf("Remove %s?", e.Name)
	if f.purge {
		question = fmt.Sprintf("Remove %s and DELETE its data folder %s?", e.Name, c.store.DataDir(id))
	}
	ok, err := c.confirm(question, f.yes, "-yes")
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintln(c.out, "Not removed.")
		return nil
	}
	if err := c.store.Uninstall(id, f.purge); err != nil {
		return err
	}
	fmt.Fprintf(c.out, "Removed %s.\n", e.Name)
	switch {
	case f.purge:
		fmt.Fprintln(c.out, "Its data folder was deleted.")
	case c.store.HasData(id):
		fmt.Fprintf(c.out, "Its data is kept in %s\nDelete it too with: flockdeck helpers uninstall -purge-data %s\n", c.store.DataDir(id), id)
	}
	return nil
}

// control is start, stop and open: done by the running instance.
func (c *helperCLI) control(action, id string) error {
	e, err := c.entry(id)
	if err != nil {
		return err
	}
	base, token, err := c.instance()
	if err != nil {
		return err
	}
	if base == "" {
		return fmt.Errorf("Flockdeck is not running. The running Flockdeck is what starts and watches %s, so open it first, then run this again", e.Name)
	}
	if action == "start" {
		fmt.Fprintf(c.out, "Starting %s...\n", e.Name)
	}
	st, err := c.request(base, token, action, id)
	if err != nil {
		return err
	}
	switch action {
	case "start":
		if st.State != helpers.StateRunning {
			fmt.Fprintf(c.out, "%s is %s.\n", e.Name, st.State)
			if st.Err != "" {
				fmt.Fprintf(c.out, "  %s\n", helpers.Printable(st.Err))
			}
			// What a helper wrote is shown as plain text: an escape sequence in it
			// could move the cursor, change the title or hide the line above.
			for _, l := range st.Log {
				fmt.Fprintf(c.out, "  | %s\n", helpers.Printable(l))
			}
			return errReported
		}
		fmt.Fprintf(c.out, "%s is running at %s\nOpen it with: flockdeck helpers open %s\n", e.Name, st.URL, id)
		if st.Owner == helpers.OwnerUnverified {
			fmt.Fprintf(c.out, "Port owner not verified: this platform cannot confirm which process holds the port.\n")
		}
	case "stop":
		fmt.Fprintf(c.out, "%s is %s.\n", e.Name, st.State)
	case "open":
		fmt.Fprintf(c.out, "Opened %s in your browser.\n", st.URL)
	}
	return nil
}
