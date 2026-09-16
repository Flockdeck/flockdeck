package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"runtime/debug"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jmwri/flockdeck/internal/selfupdate"
	"github.com/jmwri/flockdeck/internal/server"
	"github.com/jmwri/flockdeck/internal/store"
)

// updatesDir is where a downloaded update waits. It sits under the state
// directory so it travels with everything else when the application is renamed
// or its state is adopted by a newer build.
func updatesDir() (string, error) {
	base, err := store.Dir()
	if err != nil {
		return "", err
	}
	return selfupdate.Dir(base)
}

// runUpdate implements `flockdeck update`.
//
// Unlike the interface's update, which stages and waits for a restart, this
// puts the new version in place there and then. Replacing the file does not
// disturb an instance that is already running — it is running from an image the
// operating system already has — so the only difference the user sees is that
// the next start is the new version.
func runUpdate(args []string) error {
	var f updateFlags
	if err := parseUpdate(updateFlagSet(&f), args); errors.Is(err, errHelpAsked) {
		return nil
	} else if err != nil {
		return err
	}

	// A local build is settled without asking GitHub anything. Checking
	// first would turn "you built this yourself" into whatever the network had
	// to say, which for a repository that has published nothing yet is a 404.
	if !selfupdate.Parseable(version) {
		fmt.Printf("This build (%s) was not made from a release, so there is no released version to compare it with.\n", shownVersion())
		fmt.Printf("Releases are published from %s; only a copy installed from one of them is updated.\n", selfupdate.Repo)
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	warnIfRecalled(ctx, version)

	if f.version != "" {
		return runRollback(ctx, f)
	}

	rel, err := selfupdate.Latest(ctx)
	if err != nil {
		return explainUnreachable(err, "look for a newer release")
	}
	if rel.Draft || !selfupdate.Newer(rel.Version, version) {
		fmt.Println(upToDate(version, rel.Version))
		return nil
	}

	fmt.Printf("A newer release is available: %s (you have %s)\n", rel.Version, version)
	if f.check {
		if rel.URL != "" {
			fmt.Println(rel.URL)
		}
		// -check stops here on purpose, and the one thing somebody reading
		// this wants next is how to go on.
		fmt.Println("Run `flockdeck update` to download it and put it in place.")
		return nil
	}

	dir, err := updatesDir()
	if err != nil {
		return err
	}

	// A running window may already have downloaded and checked this very
	// release in the background, and fetching it again would only keep the
	// user waiting for a file that is already here.
	staged, ok := stagedUpdate(dir, version)
	if !ok || staged.Version != rel.Version {
		// Nothing else is printed until the archive has arrived, so how much
		// is coming is worth saying first.
		if size := rel.DownloadSize(); size > 0 {
			fmt.Printf("Downloading %.1f MB…\n", float64(size)/(1<<20))
		} else {
			fmt.Println("Downloading…")
		}
		staged, err = selfupdate.Stage(ctx, rel, dir)
		if err != nil {
			if errors.Is(err, selfupdate.ErrNoAsset) {
				return fmt.Errorf("%w — nothing was built for this platform", err)
			}
			return explainUnreachable(err, "download the release")
		}
	}

	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if err := selfupdate.Apply(dir, exe); err != nil {
		// The download is sound and still staged; only putting it in place
		// failed, which is usually a program installed somewhere the user
		// cannot write. Saying which is far more use than the raw error, and
		// saying it in the words of the platform it happened on.
		how := "with sudo"
		if runtime.GOOS == "windows" {
			how = "from an administrator shell"
		}
		return fmt.Errorf("%w\n\nThe download is fine and is still staged. This usually means\n%s cannot be written to — try again %s,\nor move the program somewhere you own", err, exe, how)
	}

	fmt.Printf("Updated to %s. It will be in use from the next start.\n", staged.Version)
	// A Flockdeck already running goes on as the old version until it is
	// started again, and that is the one step left to the user, so it is
	// worth saying when there is one.
	if inst, _, err := runningInstance(); err == nil && inst != nil {
		fmt.Println("Flockdeck is running now: quit it and start it again to switch to the new version.")
	}
	return nil
}

// warnIfRecalled tells a person running `flockdeck update` when the version
// they are already on has been pulled, whether or not there is anything newer
// to move to yet.
//
// Latest and Newer only ever answer "is something newer available," never
// "is what I'm running known-bad" -- so without this, somebody who updated
// just before a recall keeps running the bad build indefinitely, with no
// signal, until a newer fix happens to ship and they update again through the
// ordinary path. selfupdate.Recall is best-effort, the same way every other
// read from the site here is: nothing is printed when it cannot be checked.
func warnIfRecalled(ctx context.Context, running string) {
	rv := selfupdate.Recall(ctx, running)
	if rv == nil {
		return
	}
	fmt.Printf("WARNING: %s has been recalled: %s\n", running, rv.Reason)
	if rv.Upgrade != "" {
		fmt.Printf("A fix is available: run `flockdeck update -version=%s` to install it.\n\n", rv.Upgrade)
	} else {
		fmt.Println("Run `flockdeck update` once a fixed release is published.")
		fmt.Println()
	}
}

// runRollback implements `flockdeck update -version=X`: installs a named
// release, forward or back, rather than whatever Latest currently returns.
//
// It is the one path here that deliberately skips the Newer guard that keeps
// every other path -- runUpdate's ordinary case, and the background watcher
// -- moving only forward: someone who hits a bug wants to pick a previous
// version and reinstall it themselves, right now, not wait for a fix to be
// published and offered through the ordinary path. Because that is an
// exception on purpose, it is never done without confirmation first, unless
// told not to ask.
func runRollback(ctx context.Context, f updateFlags) error {
	rel, err := selfupdate.Fetch(ctx, f.version)
	if err != nil {
		return explainUnreachable(err, "look for "+f.version)
	}
	if rel.Draft {
		return fmt.Errorf("%s is a draft release, not a published one", f.version)
	}

	fmt.Printf("%s %s (you have %s)\n", rollbackVerb(rel.Version, version), rel.Version, version)
	if rel.URL != "" {
		fmt.Println(rel.URL)
	}
	if f.check {
		fmt.Println("Run `flockdeck update -version=" + f.version + "` to download it and put it in place.")
		return nil
	}
	if !f.yes {
		ok, err := confirmRollback(os.Stdout, os.Stdin, stdinIsTerminal(), rel.Version, version)
		if err != nil {
			return err
		}
		if !ok {
			fmt.Println("Not installed.")
			return nil
		}
	}

	dir, err := updatesDir()
	if err != nil {
		return err
	}
	if size := rel.DownloadSize(); size > 0 {
		fmt.Printf("Downloading %.1f MB…\n", float64(size)/(1<<20))
	} else {
		fmt.Println("Downloading…")
	}
	staged, err := selfupdate.Stage(ctx, rel, dir)
	if err != nil {
		if errors.Is(err, selfupdate.ErrNoAsset) {
			return fmt.Errorf("%w — nothing was built for this platform", err)
		}
		return explainUnreachable(err, "download the release")
	}

	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if err := selfupdate.Apply(dir, exe); err != nil {
		// Same explanation runUpdate gives for the ordinary path: the download
		// is sound, only putting it in place failed.
		how := "with sudo"
		if runtime.GOOS == "windows" {
			how = "from an administrator shell"
		}
		return fmt.Errorf("%w\n\nThe download is fine and is still staged. This usually means\n%s cannot be written to — try again %s,\nor move the program somewhere you own", err, exe, how)
	}

	fmt.Printf("Installed %s. It will be in use from the next start.\n", staged.Version)
	if inst, _, err := runningInstance(); err == nil && inst != nil {
		fmt.Println("Flockdeck is running now: quit it and start it again to switch to it.")
	}
	return nil
}

// rollbackVerb says what installing candidate over running would do, so the
// message reads honestly whether it moves forward, back, or reinstalls the
// very version already running.
func rollbackVerb(candidate, running string) string {
	switch {
	case selfupdate.Newer(candidate, running):
		return "Installing"
	case selfupdate.Newer(running, candidate):
		return "Rolling back to"
	default:
		return "Reinstalling"
	}
}

// confirmRollback asks before installing a version other than the latest,
// since skipping the Newer guard on purpose is the one thing here that must
// never happen without somebody meaning it.
//
// Piped input never counts as "yes": interactive says whether stdin is a
// terminal, and a script has to pass -yes instead, the same way `flockdeck
// keys set` must be piped a key rather than typed one -- otherwise an
// unattended run reading EOF from a closed pipe could install an old version
// nobody confirmed.
func confirmRollback(out io.Writer, in io.Reader, interactive bool, candidate, running string) (bool, error) {
	if !interactive {
		return false, fmt.Errorf("this is not an interactive terminal; pass -yes to install %s without asking", candidate)
	}
	fmt.Fprintf(out, "Install %s over %s now? [y/N] ", candidate, running)
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	line = strings.TrimSpace(strings.ToLower(line))
	return line == "y" || line == "yes", nil
}

// shownVersion is the version to show a person, as opposed to the one updates
// are decided on.
func shownVersion() string {
	built := ""
	if bi, ok := debug.ReadBuildInfo(); ok {
		built = bi.Main.Version
	}
	return displayVersion(version, built)
}

// displayVersion is shownVersion for a given stamp and the module version Go
// recorded in the build.
//
// A release is stamped by its build, but `go install
// github.com/jmwri/flockdeck@latest`, the README's first way to install, stamps
// nothing, and so does a plain go build: the program called itself `dev`,
// which is no use in a bug report, although Go records the version it was
// built from — the module's, or since Go 1.24 a pseudo-version taken from the
// checkout's tags. That is shown instead, marked as not a release. Updates are
// still decided on the stamp, so such a build is never replaced by a release.
func displayVersion(stamp, built string) string {
	if stamp != "dev" || !selfupdate.Parseable(built) {
		return stamp
	}
	return built + " (built from source, not a release)"
}

// upToDate is what `flockdeck update` says when there is nothing to install.
//
// It used to say the running version was the latest release whatever the
// latest was, which is untrue of a build newer than anything published: a
// release candidate, or a release that has since been withdrawn.
func upToDate(running, latest string) string {
	if selfupdate.Newer(running, latest) {
		return fmt.Sprintf("flockdeck %s is newer than the latest release, %s, so there is nothing to update to.", running, latest)
	}
	return fmt.Sprintf("flockdeck %s is the latest release.", running)
}

// explainUnreachable says in words what a failure to reach anywhere releases
// are published means, while doing what the words in doing describe. Left
// alone it was Go's own error — `Get "https://api.github.com/…": dial tcp:
// lookup api.github.com: no such host` — naming a URL and a system call rather
// than the problem and what to do about it. The updater tries dl.flockdeck.ai
// first and GitHub after it, so by the time this is reached neither answered.
// An answer GitHub did give, such as a rate limit, already says what it is and
// is passed on.
func explainUnreachable(err error, doing string) error {
	var unreachable *url.Error
	if !errors.As(err, &unreachable) {
		return err
	}
	return fmt.Errorf("could not reach dl.flockdeck.ai or GitHub to %s; check the connection and try again (%v)", doing, unreachable.Err)
}

// parseUpdate reads the command line of `flockdeck update` into the flag set's
// values.
//
// A word where a flag was meant used to be ignored, and for `flockdeck update
// check` ignoring it did the one thing the word asked not to: downloaded the
// release and put it in place.
func parseUpdate(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		// -h is somebody asking for the usage they have just been given,
		// which is not a failure to exit with.
		if errors.Is(err, flag.ErrHelp) {
			return errHelpAsked
		}
		return errReported
	}
	if fs.NArg() == 0 {
		return nil
	}
	out := fs.Output()
	fmt.Fprintf(out, "flockdeck update: unexpected %q\n", fs.Arg(0))
	if fs.Lookup(fs.Arg(0)) != nil {
		fmt.Fprintf(out, "Did you mean -%s?\n", fs.Arg(0))
	}
	fmt.Fprintln(out)
	fs.Usage()
	return errReported
}

// applyStagedUpdate puts a staged update in place as the application exits.
//
// Doing it here rather than at startup is what makes "applies on the next
// start" true without a second launch: by the time this runs the interface is
// closed and the panes are gone, and the next start reads the new file.
//
// A failure is reported and otherwise ignored. The update stays staged, the
// old version keeps working, and the user is told why rather than finding out
// when a restart silently changed nothing.
func applyStagedUpdate(out io.Writer) {
	dir, err := updatesDir()
	if err != nil {
		return
	}
	if startedAs == "" {
		return
	}
	applyStaged(out, dir, startedAs, version)
}

// startedAs is the program this process was started from, asked once, as it
// starts.
//
// Asking later is not the same question. On Linux the answer follows the file
// to wherever it has since been renamed, and `flockdeck update` run from a
// terminal renames a running instance's program to <name>.old to put the new
// one in its place. That instance then applied its own staged update at exit
// to the .old file — the next start swept it away — and restarted into it,
// while reporting that it had updated.
var startedAs, _ = os.Executable()

// applyStaged is applyStagedUpdate for a given staging directory, program and
// running version, so that a test can hand it all three.
func applyStaged(out io.Writer, dir, exe, current string) {
	// Turning updates off has to cover this too. The watcher never starts with
	// it set, but something staged by a run before it was set is still here,
	// and would replace the program of somebody who had asked for it to stay
	// as it is — without the top bar ever having offered it.
	if updatesOff() {
		return
	}
	p, ok := stagedUpdate(dir, current)
	if !ok {
		return
	}
	if err := selfupdate.Apply(dir, exe); err != nil {
		fmt.Fprintf(out, "flockdeck: %s is staged but could not be put in place: %v\n", p.Version, err)
		// This runs as the program exits, with the window gone and any
		// terminal let go of, so error.log is where it can be found.
		logError(fmt.Errorf("%s is staged but could not be put in place: %w", p.Version, err))
		return
	}
	fmt.Fprintf(out, "flockdeck: updated to %s\n", p.Version)
}

// stagedUpdate returns the update waiting in dir, if it would move the running
// version forward.
//
// A staged release is only an update to the build that staged it, and the
// program may since have been replaced by something else: a newer release
// installed by hand, or a build of the user's own, stamped `dev`. Every one of
// them shares the state directory, so each would otherwise find the leftover,
// offer it in the top bar and put it in place on the way out — an older version
// over a newer one, and a release over a local build, which is exactly what the
// updater promises never to do. The record is left where it is, because the
// build that staged it may yet be run again and still wants it.
func stagedUpdate(dir, current string) (*selfupdate.Pending, bool) {
	p, ok := selfupdate.Load(dir)
	if !ok || !selfupdate.Newer(p.Version, current) {
		return nil, false
	}
	return p, true
}

// relaunch starts the program again on root, the project that was on screen,
// and returns once it is on its own feet.
//
// The project is the only thing carried over, on purpose. The rest of the
// flags a run began with are about how that run started — whether to detach,
// which agent new panes take — and a restart asked for from the interface
// should come back the ordinary way, reopening the layout that was just saved.
// Without the project it opened whatever directory it had been started from,
// which after `flockdeck -C ~/code/api` run from home is the home directory:
// a project nobody asked for, given a fresh agent pane of its own beside the
// ones the saved session brought back.
func relaunch(root string) error {
	if startedAs == "" {
		return errors.New("could not tell where the program is")
	}
	// Started here rather than in a helper: this is the one process the
	// window guard (internal/sysproc) lets start without NoWindow, by name,
	// since it is the next Flockdeck and has to come back as this one did.
	cmd := exec.Command(startedAs, relaunchArgs(root)...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	// The child is left to run on its own; waiting for it would keep this
	// process alive for the whole of the next session.
	return cmd.Process.Release()
}

// relaunchArgs are the arguments relaunch starts the program with, kept apart
// so a test can read them without starting anything.
func relaunchArgs(root string) []string {
	if root == "" {
		return nil
	}
	return []string{"-C", root}
}

// restarting records that the interface asked for a restart rather than a
// plain quit, so the shutdown path knows to start the program again once the
// staged update is in place.
var restarting atomic.Bool

// sweepReplacedBinary clears away the file a previous update moved aside.
//
// It is done at startup because that is the first moment nothing holds the old
// program open: the run that replaced it was still executing from it right up
// until it exited.
func sweepReplacedBinary() {
	selfupdate.Sweep(startedAs)
}

// ensureChatTwin puts the console twin an API agent's pane runs beside the
// program when the installation has none, as one updated by a release from
// before the twin does not (selfupdate.EnsureChatTwin). Where it cannot be
// written, the pane falls back to the program, as it always did.
func ensureChatTwin() {
	_ = selfupdate.EnsureChatTwin(startedAs)
}

// updateEnv turns the check off. It is read rather than kept as a setting
// because the people most likely to want an unchanging binary — anyone
// packaging Flockdeck for somewhere with its own updater — are configuring a
// machine rather than clicking in a window.
const updateEnv = "FLOCKDECK_UPDATE"

// updatesOff reports whether updating in the background is off: by updateEnv,
// for somebody configuring a machine, or by the setting in the window, for
// somebody using one. The setting is read each time rather than once, since it
// can be changed while this runs.
func updatesOff() bool {
	return strings.EqualFold(os.Getenv(updateEnv), "off") || store.LoadPrefs().UpdatesOff
}

// updateInterval is how often a long-running instance looks again. Flockdeck
// is left open for days at a time, so checking only at startup would mean
// never checking at all.
const updateInterval = 6 * time.Hour

// watchForUpdates keeps the interface's update badge honest, staging any
// release newer than this build so that a restart can apply it.
//
// Everything here is best-effort and quiet. A machine with no network, a rate
// limit, a release with nothing built for this platform: none of them are
// worth interrupting the user over, because nothing is broken and the next
// check is a few hours away.
func watchForUpdates(ctx context.Context, srv *server.Server) {
	// Only the environment ends the watcher for good. The setting in the
	// window can be turned back on while this runs, so it is asked each round
	// below instead, and a round with it off checks nothing.
	if !selfupdate.Parseable(version) || strings.EqualFold(os.Getenv(updateEnv), "off") {
		return
	}
	dir, err := updatesDir()
	if err != nil {
		return
	}

	// The badge rides on the state the window is sent, and setting it sends
	// nothing by itself: with the agents idle and the trees unchanged, a
	// release staged a moment after the window opened, or one withdrawn
	// since, would not show until something else happened to change.
	offer := func(u *server.UpdateView) {
		srv.SetUpdate(u)
		srv.Wake()
	}

	// An update staged by an earlier run is still waiting, and the badge
	// should be there in the first frame rather than after the first check.
	if p, ok := stagedUpdate(dir, version); ok && !updatesOff() {
		offer(&server.UpdateView{Version: p.Version, Notes: p.Notes, URL: p.URL})
	}

	for {
		// One question to GitHub a round, and its answer decides the round;
		// a failed check leaves whatever is staged alone. A round with updates
		// turned off in the window asks nothing at all.
		if !updatesOff() {
			checkRound(ctx, dir, srv)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(updateInterval):
		}
	}
}

// checkRound is one round of looking for a newer release: it is the body of
// watchForUpdates' loop, and also what the interface's manual "Check for
// updates" action runs, so that a person who clicks it and the watcher that
// runs on its own cannot come to different conclusions about what is staged.
//
// The watcher's rounds are silent and ignore what this returns; the manual
// action is a person waiting on an answer, and is given the message worded
// for them.
func checkRound(ctx context.Context, dir string, srv *server.Server) (message string, isErr bool) {
	offer := func(u *server.UpdateView) {
		srv.SetUpdate(u)
		srv.Wake()
	}

	// Checked every round, independent of whether the newer-release check
	// below succeeds: a recall is a distinct signal from the update chip,
	// "the version you are running has a known problem," which the rest of
	// this function never answers since it only ever compares against
	// something newer. Compared against srv's own Recall rather than a
	// variable threaded through the caller's loop, so this round and a
	// manual check both leave the same trail regardless of which one runs
	// it, and a round that finds nothing changed does not wake the window
	// over it.
	switch rv, was := selfupdate.Recall(ctx, version), srv.Recall(); {
	case rv != nil && (was == nil || was.Version != rv.Version):
		srv.SetRecall(&server.RecallView{Version: rv.Version, Reason: rv.Reason, Upgrade: rv.Upgrade})
	case rv == nil && was != nil:
		srv.SetRecall(nil)
	}

	latest, err := selfupdate.Latest(ctx)
	if err != nil {
		return explainUnreachable(err, "look for a newer release").Error(), true
	}
	staged := ""
	if p, ok := stagedUpdate(dir, version); ok {
		staged = p.Version
	}
	// A draft has not been published yet, and comparing against it as though
	// it were the latest release would offer something nobody can download.
	latestVersion := latest.Version
	if latest.Draft {
		latestVersion = ""
	}
	discard, fetch := updateSteps(latestVersion, staged, version)
	if discard {
		selfupdate.Discard(dir)
		offer(nil)
		staged = ""
	}
	if !fetch {
		if staged != "" {
			return "Version " + staged + " is already downloaded and ready to install.", false
		}
		if latestVersion == "" {
			return upToDate(version, version), false
		}
		return upToDate(version, latestVersion), false
	}
	p, err := selfupdate.Stage(ctx, latest, dir)
	if err != nil {
		if errors.Is(err, selfupdate.ErrNoAsset) {
			return fmt.Sprintf("%v — nothing was built for this platform", err), true
		}
		return explainUnreachable(err, "download the release").Error(), true
	}
	offer(&server.UpdateView{Version: p.Version, Notes: p.Notes, URL: p.URL})
	return "Downloaded version " + p.Version + ". Restart flockdeck when you are ready to install it.", false
}

// checkForUpdatesNow is the manual "Check for updates" action in the
// interface: OnCheckForUpdates, wired to it in main. Unlike the watcher it
// runs regardless of the switch in Settings, which only ever governed
// checking in the background -- someone who clicked a button marked "Check
// for updates" is asking this once, not turning the background check back on
// -- but not regardless of updateEnv, which is for a machine that is never to
// touch updates at all, clicked button or not.
func checkForUpdatesNow(srv *server.Server) (string, bool) {
	if !selfupdate.Parseable(version) {
		return "This build was not made from a release, so there is no released version to compare it with.", true
	}
	if strings.EqualFold(os.Getenv(updateEnv), "off") {
		return updateEnv + "=off is set in the environment, so this build never checks for updates.", true
	}
	dir, err := updatesDir()
	if err != nil {
		return err.Error(), true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	return checkRound(ctx, dir, srv)
}

// updateSteps decides one round of the watcher from the newest published
// release, the version staged ("" for none) and the one running: whether to
// throw the staged release away, and whether to fetch the newest.
//
// A staged release newer than anything published has been withdrawn, taken
// down because something was wrong with it. It used to go on being offered,
// and be put in place at the next exit, because nothing looked back at a
// release once it had been downloaded.
func updateSteps(latest, staged, running string) (discard, fetch bool) {
	discard = staged != "" && selfupdate.Newer(staged, latest)
	if discard {
		staged = ""
	}
	fetch = selfupdate.Newer(latest, running) && (staged == "" || selfupdate.Newer(latest, staged))
	return discard, fetch
}

// updateFlags are the flags of `flockdeck update` and where their values land.
type updateFlags struct {
	check   bool
	version string
	yes     bool
}

// updateFlagSet defines the command line of `flockdeck update`. It is built
// here rather than inline in runUpdate so that a test can walk the same set the
// program parses with -- which is what keeps the help page and the usage honest
// about a subcommand's own flags, not only the top-level ones.
func updateFlagSet(f *updateFlags) *flag.FlagSet {
	fs := flag.NewFlagSet("flockdeck update", flag.ContinueOnError)
	fs.BoolVar(&f.check, "check", false, "report whether a newer release exists and stop")
	fs.StringVar(&f.version, "version", "", "install this `release` instead of the latest, forward or back (e.g. v1.4.0)")
	fs.BoolVar(&f.yes, "yes", false, "with -version, skip the confirmation before installing it")
	fs.Usage = func() {
		out := fs.Output()
		fmt.Fprintf(out, "Usage: flockdeck update [-check] [-version=<release> [-yes]]\n\n")
		// The signature is claimed wherever the release came from: GitHub
		// carries checksums.txt.sig beside every release's checksums.txt, and
		// a release read from its API is refused without one.
		fmt.Fprintf(out, "Downloads the latest release from dl.flockdeck.ai, or from GitHub when\n")
		fmt.Fprintf(out, "that cannot be used, checks it against its published SHA-256, signed\n")
		fmt.Fprintf(out, "by the release key, and puts it in place. A running instance keeps\n")
		fmt.Fprintf(out, "going; the new version is used from its next start.\n\nFlags:\n")
		fs.PrintDefaults()
		fmt.Fprintf(out, "\n-version installs a specific release instead, checked the same way, and\n")
		fmt.Fprintf(out, "asks first: it is the way to undo a bad update yourself, right now,\n")
		fmt.Fprintf(out, "without waiting for a newer fix to be published.\n")
		fmt.Fprintf(out, "\nA running Flockdeck also downloads new releases in the background and\n")
		fmt.Fprintf(out, "offers them in the top bar. Set %s=off to stop it doing that.\n", updateEnv)
	}
	return fs
}
