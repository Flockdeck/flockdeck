package review

import "strings"

// metacharacters are the bytes that let one command run more than what its
// own name and arguments say: a pipe or chain into something else, a
// redirection that writes a file, backgrounding, a subshell or substitution
// that runs a nested command, or a variable expansion that can hide any of
// those inside what looks like a plain argument. A command holding any of
// them is never read as safe, whatever its first word is.
//
// Quotes, backslashes and glob characters are on it for a quieter reason: the
// shell rewrites them before the command sees its arguments, so the text
// checked here is not the path read. .'.'/x and .\./x both become ../x,
// "/etc/passwd" becomes /etc/passwd, and a glob can expand to a name no
// argument spells -- each one past outsideProject, which only ever sees the
// text as written.
const metacharacters = "|&;<>`$(){}\n'\"\\*?["

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

// safeSubcommands are the git subcommands that read without writing --
// unlike, say, "git branch", which lists with no arguments but deletes with
// "-d". Only subcommands with no such destructive form are on this list, and
// the few flags that would give one a way to write or run something are
// refused by unsafeFlag.
//
// No go subcommand is on it. go env rewrites go's own settings file with -w
// or -u, and Go's flag parser takes --w for -w, so a list of refused
// spellings is always one short; and every go command, go version included,
// first fetches whatever newer toolchain the project's go.mod names, which is
// a download nobody was asked about.
//
// None of them is trusted on its name alone, though: git's own configuration
// can have each of them run a program (core.fsmonitor on git status, a
// textconv driver on git log -p, a submodule's own config on git diff), and
// an agent can arrange that with nothing but ordinary file writes. So a git
// command is let through only once gitRunsNothing has asked git itself, in
// the directory the command will run in -- see gitcheck.go.
var safeSubcommands = map[[2]string]bool{
	{"git", "status"}:    true,
	{"git", "diff"}:      true,
	{"git", "log"}:       true,
	{"git", "show"}:      true,
	{"git", "blame"}:     true,
	{"git", "rev-parse"}: true,
	{"git", "describe"}:  true,
}

// unsafeFlag reports whether arg is one of the flags that turn an otherwise
// read-only git subcommand above into a write or a run: --output=<file>,
// --ext-diff and --textconv (which run a configured program), and
// --submodule, which has git diff, log and show open each submodule as a
// repository of its own, under that submodule's own configuration.
func unsafeFlag(arg string) bool {
	name, _, _ := strings.Cut(arg, "=")
	switch name {
	case "--output", "--ext-diff", "--textconv", "--submodule":
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

// secretPath reports whether arg names a file whose contents are a secret in
// their own right -- a dotenv file, a private key, a credentials or token file.
// Reading inside the project is otherwise fine, but these are exactly the
// files Claude Code would stop to ask about, and auto-review must not read one
// into the transcript unasked. The match is on the base name, lower-cased, and
// a flag's value (--file=.env) is checked the same way as outsideProject does.
//
// It is only a name check, not a classifier: a secret under an unconventional
// name is not caught, and a public key (id_rsa.pub) is deliberately left
// alone. Matching errs towards asking -- a file that merely looks like a
// credential falls through to the ordinary prompt, which is the safe side.
func secretPath(arg string) bool {
	if _, v, ok := strings.Cut(arg, "="); ok {
		arg = v
	}
	if arg == "" || arg[0] == '-' {
		return false
	}
	base := arg
	if i := strings.LastIndexAny(base, "/\\"); i >= 0 {
		base = base[i+1:]
	}
	base = strings.ToLower(base)
	switch {
	case base == ".env" || strings.HasPrefix(base, ".env."):
		return true
	case strings.HasPrefix(base, "id_") && !strings.HasSuffix(base, ".pub"):
		return true // private ssh key; the .pub beside it is public
	case base == ".npmrc" || base == ".netrc" || base == "_netrc" || base == ".pgpass":
		return true
	case base == ".git-credentials" || strings.Contains(base, "credential"):
		return true
	}
	for _, ext := range []string{".pem", ".key", ".p12", ".pfx", ".ppk", ".keystore", ".jks"} {
		if strings.HasSuffix(base, ext) {
			return true
		}
	}
	return false
}

// readOnlyCommand reports whether cmd is a single call to one of the commands
// above, reading only inside the project, with nothing in it that could make
// it do more than that. It is deliberately conservative: a command it cannot
// be sure of is not read-only, even where a person would recognise it as
// harmless at a glance.
//
// For a git subcommand, gitSub names it: read-only by what it says, but only
// safe to let through once gitRunsNothing agrees about where it will run.
func readOnlyCommand(cmd string) (ok bool, gitSub string) {
	if strings.ContainsAny(cmd, metacharacters) {
		return false, ""
	}
	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return false, ""
	}
	for _, f := range fields[1:] {
		if outsideProject(f) || secretPath(f) {
			return false, ""
		}
	}
	if safeCommands[fields[0]] {
		return true, ""
	}
	if len(fields) < 2 || !safeSubcommands[[2]string{fields[0], fields[1]}] {
		return false, ""
	}
	for _, f := range fields[2:] {
		if unsafeFlag(f) {
			return false, ""
		}
	}
	return true, fields[1]
}

// SecretPath is secretPath for other packages: whether arg names a file whose
// contents are a secret in their own right. The transcript recorder uses it to
// withhold what an agent read from such a file, the same name check and with
// the same limits -- see secretPath.
func SecretPath(arg string) bool { return secretPath(arg) }
