package appwindow

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	pathpkg "path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/jmwri/flockdeck/internal/sysproc"
)

// revealStep is one program to run to show a file in the desktop's file
// manager. Nothing here is ever run through a shell: a program and the
// arguments it is given, as they are.
type revealStep struct {
	name string
	args []string
	// rawLine, on Windows, is the whole command line to give the program as it
	// stands, because explorer reads /select, with the path quoted by itself
	// and not the way Go quotes one argument.
	rawLine string
	// ignoreExit is set for explorer, which exits non-zero when it has done
	// what it was asked.
	ignoreExit bool
}

// revealRunner runs a step and reports whether it did what it was asked.
type revealRunner func(revealStep) error

// RevealFile shows path in the desktop's file manager with the file selected: a
// local, absolute path to a file, which the caller has checked is one it means
// to show. It waits for the program that does it, for a few seconds at most.
func RevealFile(path string) error {
	return reveal(runtime.GOOS, path, runStep)
}

// reveal works out what to run for goos and runs it. Where there are two ways
// (Linux), the second is tried when the first fails.
func reveal(goos, path string, run revealRunner) error {
	steps, err := revealPlan(goos, path)
	if err != nil {
		return err
	}
	var last error
	for _, s := range steps {
		if last = run(s); last == nil {
			return nil
		}
	}
	return last
}

// revealPlan is the programs, in the order to try them, that show path in the
// file manager on goos.
//
// The path is checked first: it has to be absolute, which also means it cannot
// begin with a dash for a program to read as a flag, and it cannot hold a
// character that would end an argument early where a command line is built by
// hand.
func revealPlan(goos, path string) ([]revealStep, error) {
	if !filepath.IsAbs(path) && !strings.HasPrefix(path, "/") {
		return nil, fmt.Errorf("%q is not an absolute path", path)
	}
	if strings.HasPrefix(path, "-") || strings.ContainsAny(path, "\x00\r\n") {
		return nil, fmt.Errorf("%q is not a path that can be shown", path)
	}
	switch goos {
	case "windows":
		if strings.Contains(path, `"`) {
			return nil, fmt.Errorf("%q is not a path that can be shown", path)
		}
		return []revealStep{{name: "explorer.exe", rawLine: `explorer.exe /select,"` + path + `"`, ignoreExit: true}}, nil
	case "darwin":
		return []revealStep{{name: "/usr/bin/open", args: []string{"-R", path}}}, nil
	default:
		// The file manager's own D-Bus interface selects the file in whichever
		// manager is running; where there is none, or no dbus-send, the folder
		// the file is in is opened.
		uri := (&url.URL{Scheme: "file", Path: path}).String()
		// dbus-send splits an array's items at commas.
		uri = strings.ReplaceAll(uri, ",", "%2C")
		return []revealStep{
			{name: "dbus-send", args: []string{"--session", "--print-reply", "--dest=org.freedesktop.FileManager1", "--type=method_call",
				"/org/freedesktop/FileManager1", "org.freedesktop.FileManager1.ShowItems", "array:string:" + uri, "string:"}},
			{name: "xdg-open", args: []string{pathpkg.Dir(path)}},
		}, nil
	}
}

// revealTimeout is how long a program is given to say it has shown the file.
const revealTimeout = 5 * time.Second

func runStep(s revealStep) error {
	ctx, cancel := context.WithTimeout(context.Background(), revealTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, s.name, s.args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	sysproc.NoWindow(cmd)
	setRawCommandLine(cmd, s.rawLine)
	err := cmd.Run()
	var exit *exec.ExitError
	if s.ignoreExit && errors.As(err, &exit) {
		return nil
	}
	return err
}
