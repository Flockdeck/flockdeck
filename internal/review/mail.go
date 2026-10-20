package review

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// mailReadReason is the reason an allowed mail read carries.
const mailReadReason = "allow: reading Flockdeck mail"

// MailRead decides whether a PreToolUse call is the reading of Flockdeck mail
// and nothing else, which an agent may do in any pane whatever its
// auto-approve setting. It is not part of Decide and shares none of its
// lists: it allows two exact commands and refuses everything else.
//
// The allowed commands are
//
//	flockdeck inbox [-all] [-json]
//	flockdeck inbox read [-json] <id>
//
// with each flag at most once, in either order, and an id of one to nine
// digits. Words are separated by single spaces. Nothing else is accepted: not
// -wait or -follow, not another subcommand, not an extra word, not a path to
// the program or a variable set in front of it.
//
// The command is first checked as a string for anything a shell would read as
// more than words, and only then split on single spaces and compared to the
// forms above word by word, so no word is ever interpreted.
//
// The command runs in cwd, and the word flockdeck is looked up from there by
// some shells before PATH. A directory holding a program of that name (an
// agent can write one) would make the command run it, so cwd must be given
// and must not hold one: no entry may be named flockdeck, or start with
// "flockdeck.", in any letter case, whatever the extension. A directory that
// cannot be read is refused as well.
//
// The word flockdeck must also be the running application. It is looked up on
// env.Path from cwd, as a shell would, and the command is allowed only if what
// is found is env.Self or sits in its directory; a program of that name earlier
// on the PATH (a writable directory, a virtual environment's or node_modules'
// bin) is asked about. A built flockdeck binary in cwd already makes the
// command ask, by the rule above.
//
// This is checked when the hook fires, not when the command runs, and it sees
// the PATH the pane was started with, not one the agent has set since. A shell
// function or alias named flockdeck cannot be seen from here at all.
func MailRead(tool, toolInputJSON, cwd string, env MailEnv) Decision {
	if tool != "Bash" || toolInputJSON == "" {
		return ask
	}
	var in struct {
		Command string `json:"command"`
	}
	if json.Unmarshal([]byte(toolInputJSON), &in) != nil {
		return ask
	}
	if !mailReadCommand(in.Command) {
		return ask
	}
	if !cwdHoldsNoFlockdeck(cwd) {
		return ask
	}
	if !env.runsThisApp(cwd) {
		return ask
	}
	return Decision{Allow: true, Reason: mailReadReason}
}

// mailReadCommand reports whether cmd is one of the two allowed commands.
func mailReadCommand(cmd string) bool {
	if cmd == "" || len(cmd) > maxCommandBytes {
		return false
	}
	// Only printable ASCII: no tab, carriage return, line feed or other
	// control byte, and no non-ASCII space that strings.Fields would count as
	// a separator.
	for i := 0; i < len(cmd); i++ {
		if cmd[i] < 0x20 || cmd[i] > 0x7e {
			return false
		}
	}
	if strings.ContainsAny(cmd, metacharacters) {
		return false
	}
	words := strings.Split(cmd, " ")
	if len(words) < 2 || words[0] != "flockdeck" || words[1] != "inbox" {
		return false
	}
	rest := words[2:]
	if len(rest) > 0 && rest[0] == "read" {
		return mailReadForm(rest[1:])
	}
	return mailListForm(rest)
}

// mailListForm accepts -all and -json, each at most once, in either order.
func mailListForm(args []string) bool {
	var all, asJSON bool
	for _, a := range args {
		switch {
		case a == "-all" && !all:
			all = true
		case a == "-json" && !asJSON:
			asJSON = true
		default:
			return false
		}
	}
	return true
}

// mailReadForm accepts one id, with -json before it if wanted. The program's
// flag parsing stops at the first word that is not a flag, so -json after the
// id is a usage error and is not accepted.
func mailReadForm(args []string) bool {
	switch len(args) {
	case 1:
		return isMailID(args[0])
	case 2:
		return args[0] == "-json" && isMailID(args[1])
	}
	return false
}

// isMailID reports whether s is one to nine ASCII digits.
func isMailID(s string) bool {
	if len(s) < 1 || len(s) > 9 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// cwdHoldsNoFlockdeck reports whether cwd is a directory that was named and
// holds nothing a shell could run as flockdeck.
func cwdHoldsNoFlockdeck(cwd string) bool {
	if cwd == "" || !filepath.IsAbs(cwd) {
		return false
	}
	if fi, err := os.Stat(cwd); err != nil || !fi.IsDir() {
		return false
	}
	d, err := os.Open(cwd)
	if err != nil {
		return false
	}
	defer d.Close()
	// Read in batches and stopped at the first hit, and refused past a bound,
	// so a directory of millions of entries is neither read whole nor trusted.
	for seen := 0; ; {
		names, err := d.Readdirnames(mailDirBatch)
		for _, name := range names {
			if namesFlockdeck(name) {
				return false
			}
		}
		seen += len(names)
		if seen > mailDirMax {
			return false
		}
		if err == io.EOF {
			return true
		}
		if err != nil {
			return false
		}
	}
}

// mailDirBatch is how many names are read from the directory at a time.
const mailDirBatch = 256

// mailDirMax is the most entries a directory may hold and still be trusted. It
// is a variable for a test to lower.
var mailDirMax = 100000

// namesFlockdeck reports whether a directory entry is, or could run as, a
// program called flockdeck: the name, or the name with an extension, in any
// letter case. Trailing dots and spaces are dropped first, since Windows does
// not keep them.
func namesFlockdeck(name string) bool {
	n := strings.ToLower(strings.TrimRight(name, ". "))
	return n == "flockdeck" || strings.HasPrefix(n, "flockdeck.")
}
