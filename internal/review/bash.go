package review

import "strings"

// metacharacters are the bytes that let one command run more than what its
// own name and arguments say: a pipe or chain into something else, a
// redirection that writes a file, backgrounding, a subshell or substitution
// that runs a nested command, or a variable expansion that can hide any of
// those inside what looks like a plain argument. A command holding any of
// them is never read as safe, whatever its first word is.
const metacharacters = "|&;<>`$(){}\n"

// safeCommands are program names whose every use reads something without
// changing anything -- no flag turns cat, ls or grep into a write. Kept
// deliberately short: the value of the list is that everything on it can be
// verified by inspection, not that it covers every safe program that exists.
//
// Left off on purpose, because a flag or an argument makes each of them do
// more than read: env (runs any command it is given), sort -o, uniq's second
// argument and tree -o (write a file), rg --pre (runs a program on every
// file), file -C (writes a compiled magic file), date -s (sets the clock),
// and printenv (prints every secret in the environment into the transcript).
var safeCommands = map[string]bool{
	"ls": true, "pwd": true, "cat": true, "head": true, "tail": true,
	"wc": true, "grep": true, "which": true, "whoami": true,
	"diff": true, "stat": true, "cut": true,
	"basename": true, "dirname": true, "true": true, "type": true,
}

// safeSubcommands are the git and go subcommands that read without writing --
// unlike, say, "git branch", which lists with no arguments but deletes with
// "-d". Only subcommands with no such destructive form are on this list, and
// the few flags that would give one a way to write or run something are
// refused by unsafeFlag.
var safeSubcommands = map[[2]string]bool{
	{"git", "status"}:    true,
	{"git", "diff"}:      true,
	{"git", "log"}:       true,
	{"git", "show"}:      true,
	{"git", "blame"}:     true,
	{"git", "rev-parse"}: true,
	{"git", "describe"}:  true,
	{"go", "version"}:    true,
	{"go", "env"}:        true,
	{"go", "doc"}:        true,
}

// unsafeFlag reports whether arg is one of the flags that turn an otherwise
// read-only git or go subcommand above into a write or a run: git's
// --output=<file> and --ext-diff and --textconv (the last two run a
// configured program), and go env's -w and -u (which rewrite go's own
// settings file).
func unsafeFlag(arg string) bool {
	name, _, _ := strings.Cut(arg, "=")
	switch name {
	case "--output", "--ext-diff", "--textconv", "-w", "-u":
		return true
	}
	return false
}

// outsideProject reports whether arg names a path outside the directory the
// command runs in: an absolute path (a leading slash or backslash, or a
// Windows drive), one under the home directory, or one that climbs out with
// "..". Claude Code asks before reading outside the project, and auto-review
// must not quietly read what it would have asked about -- a key, a token, a
// credentials file. A flag's value (--file=/etc/x) is checked the same way.
func outsideProject(arg string) bool {
	if _, v, ok := strings.Cut(arg, "="); ok {
		arg = v
	}
	if arg == "" {
		return false
	}
	if arg[0] == '/' || arg[0] == '\\' || arg[0] == '~' {
		return true
	}
	if len(arg) >= 2 && arg[1] == ':' && (arg[0]|0x20 >= 'a' && arg[0]|0x20 <= 'z') {
		return true
	}
	return strings.Contains(arg, "..")
}

// readOnlyCommand reports whether cmd is a single call to one of the commands
// above, reading only inside the project, with nothing in it that could make
// it do more than that. It is deliberately conservative: a command it cannot
// be sure of is not read-only, even where a person would recognise it as
// harmless at a glance.
func readOnlyCommand(cmd string) bool {
	if strings.ContainsAny(cmd, metacharacters) {
		return false
	}
	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return false
	}
	for _, f := range fields[1:] {
		if outsideProject(f) {
			return false
		}
	}
	if safeCommands[fields[0]] {
		return true
	}
	if len(fields) < 2 || !safeSubcommands[[2]string{fields[0], fields[1]}] {
		return false
	}
	for _, f := range fields[2:] {
		if unsafeFlag(f) {
			return false
		}
	}
	return true
}
