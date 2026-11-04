// Package ghcli talks to GitHub through the gh command-line tool, the same
// way internal/gitx talks to git: by running the binary and reading what it
// says, rather than through a client of GitHub's API. gh already knows how to
// authenticate, how to find the repository a working tree belongs to and how
// to talk to GitHub Enterprise as well as github.com, and duplicating that
// with an HTTP client of Flockdeck's own would mean keeping a second copy of
// all of it in step.
//
// Everything here needs gh on PATH; Installed reports whether it is, and
// Login walks a person through getting it authenticated when it is not (or
// its login has expired). Nothing in this package stores a token itself --
// gh keeps its own, in its own configuration -- so there is nothing here for
// Flockdeck to leak.
package ghcli

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// lookPath is a variable so a test can describe a machine with gh installed,
// or not, without touching PATH.
var lookPath = exec.LookPath

// goos is a variable so a test can exercise the Windows fallback anywhere.
var goos = runtime.GOOS

// Installed reports whether the gh binary can be found on PATH.
//
// On Windows a winget install adds gh to the machine PATH, which a process
// that is already running never sees. When PATH has no gh, the usual install
// folders are checked and, if gh is there, its folder is added to this
// process's PATH so the gh commands that follow can find it too.
func Installed() bool {
	if _, err := lookPath("gh"); err == nil {
		return true
	}
	if goos != "windows" {
		return false
	}
	for _, env := range []string{"ProgramFiles", "ProgramW6432", "LocalAppData"} {
		base := os.Getenv(env)
		if base == "" {
			continue
		}
		dir := filepath.Join(base, "GitHub CLI")
		if env == "LocalAppData" {
			dir = filepath.Join(base, "Programs", "GitHub CLI")
		}
		if fi, err := os.Stat(filepath.Join(dir, "gh.exe")); err == nil && !fi.IsDir() {
			os.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			return true
		}
	}
	return false
}
