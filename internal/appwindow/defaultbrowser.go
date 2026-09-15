package appwindow

import (
	"os/exec"
	"runtime"

	"github.com/jmwri/flockdeck/internal/sysproc"
)

// OpenDefault opens target in the user's default browser: the fallback used
// where Flockdeck's own window could not be opened at all -- no working
// webview on this machine, say -- so that something still shows the
// interface rather than nothing.
func OpenDefault(target string) error {
	name, args := defaultBrowserCommand(runtime.GOOS, target)
	cmd := exec.Command(name, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	// rundll32 opens no console of its own, but a console program started from
	// a windowless Flockdeck would flash a terminal up just to pass the address
	// on, and saying so costs nothing.
	sysproc.NoWindow(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	// xdg-open, open and rundll32 hand the address on and exit, and nothing
	// waits for them: on Linux and macOS leaving a fall-back-to-default-browser
	// process unwaited left a zombie behind for as long as Flockdeck ran.
	// Waiting is what reaps one; letting the process go, on Unix, does not.
	go func() { _ = cmd.Wait() }()
	return nil
}

// defaultBrowserCommand is the program, and its arguments, that hands url to
// the desktop's own handler on goos.
//
// On Windows that used to be `cmd /c start "" url`, and cmd.exe reads & and %
// in what it is given for itself: an address with a second query parameter
// would have been cut off at the &, and the rest run as a command of its own.
// Nothing quoted it -- Go quotes an argument for the program's own parsing,
// not for cmd.exe. rundll32 hands the URL to its handler as it stands, with no
// shell in between.
func defaultBrowserCommand(goos, url string) (string, []string) {
	switch goos {
	case "windows":
		return "rundll32", []string{"url.dll,FileProtocolHandler", url}
	case "darwin":
		return "/usr/bin/open", []string{url}
	default:
		return "xdg-open", []string{url}
	}
}
