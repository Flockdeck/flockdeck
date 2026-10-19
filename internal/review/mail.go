package review

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// mailReadReason is the reason an allowed mail read carries.
const mailReadReason = "allow: reading Flockdeck mail"

// plantable are the names a program called flockdeck could have in a
// directory. On Windows the shell looks in the working directory before PATH,
// and tries each of the executable extensions.
var plantable = []string{
	"flockdeck", "flockdeck.exe", "flockdeck.com", "flockdeck.cmd", "flockdeck.bat",
}

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
// and must not hold one. A directory that cannot be read is refused as well.
func MailRead(tool, toolInputJSON, cwd string) Decision {
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

// mailReadForm accepts one id and, before or after it, -json at most once.
func mailReadForm(args []string) bool {
	var asJSON, haveID bool
	for _, a := range args {
		switch {
		case a == "-json" && !asJSON:
			asJSON = true
		case isMailID(a) && !haveID:
			haveID = true
		default:
			return false
		}
	}
	return haveID
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
	for _, name := range plantable {
		_, err := os.Lstat(filepath.Join(cwd, name))
		if err == nil || !errors.Is(err, os.ErrNotExist) {
			return false
		}
	}
	return true
}
