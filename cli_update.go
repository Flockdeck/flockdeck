package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
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
	fs := updateFlagSet(&f)
	if err := fs.Parse(args); err != nil {
		return errReported
	}

	// An untagged build is settled without asking GitHub anything. Checking
	// first would turn "you built this yourself" into whatever the network had
	// to say, which for a repository that has published nothing yet is a 404.
	if !selfupdate.Parseable(version) {
		fmt.Printf("This is an untagged build (%s), so there is no released version to compare it with.\n", version)
		fmt.Printf("Releases are published from %s; a build made here is stamped by git describe.\n", selfupdate.Repo)
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	rel, err := selfupdate.Check(ctx, version)
	if err != nil {
		return err
	}
	if rel == nil {
		fmt.Printf("flockdeck %s is the latest release.\n", version)
		return nil
	}

	fmt.Printf("A newer release is available: %s (you have %s)\n", rel.Version, version)
	if f.check {
		if rel.URL != "" {
			fmt.Println(rel.URL)
		}
		return nil
	}

	dir, err := updatesDir()
	if err != nil {
		return err
	}

	fmt.Println("Downloading…")
	staged, err := selfupdate.Stage(ctx, rel, dir)
	if err != nil {
		if errors.Is(err, selfupdate.ErrNoAsset) {
			return fmt.Errorf("%w — nothing was built for this platform", err)
		}
		return err
	}

	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if err := selfupdate.Apply(dir, exe); err != nil {
		// The download is sound and still staged; only putting it in place
		// failed, which is usually a program installed somewhere the user
		// cannot write. Saying which is far more use than the raw error.
		return fmt.Errorf("%w\n\nThe download is fine and is still staged. This usually means\n%s cannot be written to — try again from an administrator shell,\nor move the program somewhere you own", err, exe)
	}

	fmt.Printf("Updated to %s. It will be in use from the next start.\n", staged.Version)
	return nil
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
	exe, err := os.Executable()
	if err != nil {
		return
	}
	applyStaged(out, dir, exe, version)
}

// applyStaged is applyStagedUpdate for a given staging directory, program and
// running version, so that a test can hand it all three.
func applyStaged(out io.Writer, dir, exe, current string) {
	p, ok := stagedUpdate(dir, current)
	if !ok {
		return
	}
	if err := selfupdate.Apply(dir, exe); err != nil {
		fmt.Fprintf(out, "flockdeck: %s is staged but could not be put in place: %v\n", p.Version, err)
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

// relaunch starts the program again and returns once it is on its own feet.
//
// It is started with no arguments on purpose. The flags that a run began with
// are about how that run started — which project to open, whether to detach —
// and a restart asked for from the interface should come back the ordinary
// way, reopening the layout that was just saved.
func relaunch() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	// The child is left to run on its own; waiting for it would keep this
	// process alive for the whole of the next session.
	return cmd.Process.Release()
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
	if exe, err := os.Executable(); err == nil {
		selfupdate.Sweep(exe)
	}
}

// updateEnv turns the check off. It is read rather than kept as a setting
// because the people most likely to want an unchanging binary — anyone
// packaging Flockdeck for somewhere with its own updater — are configuring a
// machine rather than clicking in a window.
const updateEnv = "FLOCKDECK_UPDATE"

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
	if !selfupdate.Parseable(version) || strings.EqualFold(os.Getenv(updateEnv), "off") {
		return
	}
	dir, err := updatesDir()
	if err != nil {
		return
	}

	// An update staged by an earlier run is still waiting, and the badge
	// should be there in the first frame rather than after the first check.
	if p, ok := stagedUpdate(dir, version); ok {
		srv.SetUpdate(&server.UpdateView{Version: p.Version, Notes: p.Notes, URL: p.URL})
	}

	for {
		if p, ok := stagedUpdate(dir, version); !ok || selfupdate.Newer(latestSeen(ctx), p.Version) {
			stageUpdate(ctx, srv, dir)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(updateInterval):
		}
	}
}

// latestSeen reports the newest published version, or an empty string when it
// could not be asked. An empty string is never newer than anything, so a
// failed check leaves an already-staged update alone.
func latestSeen(ctx context.Context) string {
	rel, err := selfupdate.Latest(ctx)
	if err != nil || rel == nil {
		return ""
	}
	return rel.Version
}

func stageUpdate(ctx context.Context, srv *server.Server, dir string) {
	rel, err := selfupdate.Check(ctx, version)
	if err != nil || rel == nil {
		return
	}
	p, err := selfupdate.Stage(ctx, rel, dir)
	if err != nil {
		return
	}
	srv.SetUpdate(&server.UpdateView{Version: p.Version, Notes: p.Notes, URL: p.URL})
}

// updateFlags are the flags of `flockdeck update` and where their values land.
type updateFlags struct {
	check bool
}

// updateFlagSet defines the command line of `flockdeck update`. It is built
// here rather than inline in runUpdate so that a test can walk the same set the
// program parses with -- which is what keeps the help page and the usage honest
// about a subcommand's own flags, not only the top-level ones.
func updateFlagSet(f *updateFlags) *flag.FlagSet {
	fs := flag.NewFlagSet("flockdeck update", flag.ContinueOnError)
	fs.BoolVar(&f.check, "check", false, "report whether a newer release exists and stop")
	fs.Usage = func() {
		out := fs.Output()
		fmt.Fprintf(out, "Usage: flockdeck update [-check]\n\n")
		fmt.Fprintf(out, "Downloads the latest release from GitHub, checks it against the\n")
		fmt.Fprintf(out, "published SHA-256 and puts it in place. A running instance keeps\n")
		fmt.Fprintf(out, "going; the new version is used from its next start.\n\nFlags:\n")
		fs.PrintDefaults()
	}
	return fs
}
