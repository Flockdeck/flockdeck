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

import "os/exec"

// lookPath is a variable so a test can describe a machine with gh installed,
// or not, without touching PATH.
var lookPath = exec.LookPath

// Installed reports whether the gh binary can be found on PATH.
func Installed() bool {
	_, err := lookPath("gh")
	return err == nil
}
