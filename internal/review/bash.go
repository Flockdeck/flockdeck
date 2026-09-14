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
var safeCommands = map[string]bool{
	"ls": true, "pwd": true, "cat": true, "head": true, "tail": true,
	"wc": true, "grep": true, "rg": true, "which": true, "whoami": true,
	"date": true, "env": true, "printenv": true, "diff": true, "file": true,
	"stat": true, "tree": true, "sort": true, "uniq": true, "cut": true,
	"basename": true, "dirname": true, "true": true, "type": true,
}

// safeSubcommands are the git and go subcommands that read without writing
// under any flag they take -- unlike, say, "git branch", which lists with no
// arguments but deletes with "-d". Only subcommands with no such destructive
// form are on this list.
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
	{"go", "list"}:       true,
}

// readOnlyCommand reports whether cmd is a single call to one of the commands
// above, with nothing in it that could make it do more than that. It is
// deliberately conservative: a command it cannot be sure of is not read-only,
// even where a person would recognise it as harmless at a glance.
func readOnlyCommand(cmd string) bool {
	if strings.ContainsAny(cmd, metacharacters) {
		return false
	}
	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return false
	}
	if safeCommands[fields[0]] {
		return true
	}
	return len(fields) >= 2 && safeSubcommands[[2]string{fields[0], fields[1]}]
}
