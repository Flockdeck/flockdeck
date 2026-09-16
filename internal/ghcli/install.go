package ghcli

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/jmwri/flockdeck/internal/sysproc"
)

// installTimeout bounds an install attempt. A package manager that has never
// updated its index can take a while the first time; one that needs a
// password it cannot ask for fails fast instead of sitting here for it.
var installTimeout = 5 * time.Minute

// Installer is one way to install gh on this machine: a package manager
// command, or nothing more than a place to go and do it by hand.
type Installer struct {
	// Manager names what would run it -- "winget", "brew", "apt", "dnf",
	// "pacman", "apk" -- or "" when none of those was found, and only the
	// manual route is offered.
	Manager string
	// Command is the command line TryInstall runs for this installer, or nil
	// when Manager is "".
	Command []string
	// URL is where to install gh by hand, offered whether or not a package
	// manager was found: a manager can still fail (no network, a repository
	// not yet enabled), and the button should not be a dead end.
	URL string
}

const ghDownloadsURL = "https://cli.github.com"

// managers are the package managers this looks for, most specific platform
// tool first. Each is checked for on PATH; the first one found is offered.
// winget ships with Windows 11 and current Windows 10, so it is worth trying
// before falling back to nothing at all on the platform Flockdeck is most
// tested on.
var managers = []struct {
	name string
	cmd  []string
}{
	{"winget", []string{"winget", "install", "--id", "GitHub.cli", "--silent",
		"--accept-package-agreements", "--accept-source-agreements"}},
	{"brew", []string{"brew", "install", "gh"}},
	{"apt-get", []string{"apt-get", "install", "-y", "gh"}},
	{"dnf", []string{"dnf", "install", "-y", "gh"}},
	{"pacman", []string{"pacman", "-S", "--noconfirm", "gh"}},
	{"apk", []string{"apk", "add", "gh"}},
}

// DetectInstaller looks for a package manager this machine has, to offer as
// a one-click install; the manual URL is always filled in, since a manager
// found here can still turn out to need a password, a repository that is not
// enabled, or a network this machine does not have right now.
func DetectInstaller() Installer {
	for _, m := range managers {
		if _, err := lookPath(m.name); err == nil {
			return Installer{Manager: m.name, Command: m.cmd, URL: ghDownloadsURL}
		}
	}
	return Installer{URL: ghDownloadsURL}
}

// TryInstall runs the installer's command, if it has one, streaming its
// combined output a line at a time to onLine as it runs -- apt and dnf, run
// as a regular user, print why they refused rather than fail silently, and
// that is worth showing rather than swallowing.
//
// It returns an error when there is no command to run (Manager == ""), when
// the command could not be started, when it exits non-zero, or when
// installTimeout passes.
func TryInstall(ctx context.Context, in Installer, onLine func(string)) error {
	if len(in.Command) == 0 {
		return fmt.Errorf("no package manager was found to install gh with; get it from %s", in.URL)
	}
	ctx, cancel := context.WithTimeout(ctx, installTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, in.Command[0], in.Command[1:]...)
	sysproc.NoWindow(cmd)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("could not start %s: %w", in.Command[0], err)
	}
	// The output is read back once the process has finished rather than
	// streamed line by line as it is written: winget and apt both rewrite a
	// progress line in place with carriage returns, which would otherwise
	// read back as a wall of near-duplicate lines. What is lost is seeing
	// output before the command finishes; what is gained is output a person
	// can read.
	err := cmd.Wait()
	for _, line := range strings.Split(cleanProgress(buf.String()), "\n") {
		if onLine != nil && strings.TrimSpace(line) != "" {
			onLine(line)
		}
	}
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("%s: gave up after %s", in.Manager, installTimeout)
		}
		return fmt.Errorf("%s: %w", in.Manager, err)
	}
	return nil
}

// cleanProgress collapses a line rewritten in place with carriage returns --
// "Downloading 33%\rDownloading 100%" -- down to the state it finished in.
// Mirrors the function of the same name in internal/gitx.
func cleanProgress(s string) string {
	var kept []string
	for _, line := range strings.Split(s, "\n") {
		if i := strings.LastIndex(line, "\r"); i >= 0 {
			line = line[i+1:]
		}
		if strings.TrimSpace(line) != "" {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n")
}

// Platform names the OS an install hint is being shown for, for the caller
// that has no package manager to offer and wants to say something more
// specific than "visit a website".
func Platform() string { return runtime.GOOS }
