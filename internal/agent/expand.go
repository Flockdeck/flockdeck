package agent

import (
	"os"
	"regexp"
	"strings"
)

// expandExe reads a program path the way the shell somebody copied it from
// would have: a leading "~" is their home, and "$NAME", "${NAME}" or "%NAME%"
// is that variable. It was looked up on PATH as written, so an entry saying
// "~/bin/mycli" or "%LOCALAPPDATA%\Tools\mycli.exe" was never found. Only a
// variable that is set is replaced, so a path that merely contains a "$" or
// a "%" keeps it.
func expandExe(exe string) string {
	if exe == "~" || strings.HasPrefix(exe, "~/") || strings.HasPrefix(exe, `~\`) {
		if home, err := os.UserHomeDir(); err == nil {
			exe = home + exe[1:]
		}
	}
	return expandVars(exe)
}

// expandVars replaces "$NAME", "${NAME}" and "%NAME%" with the variable, where
// it is set, in Flockdeck's own environment -- which is the one a pane
// inherits, so "$PATH" means what the pane would have had.
//
// Anything else is left exactly as written. os.Expand, which this used, reads
// "$5", "$$" and "$@" as the shell's own variables and eats an unclosed "${",
// so a password of "pa$5word" in an entry's environment reached the pane as
// "pa${5}word", and an unset "$NAME" came back as "${NAME}". It is one pass,
// too, so a variable's value is never read for variables of its own.
func expandVars(s string) string {
	return varRef.ReplaceAllStringFunc(s, func(m string) string {
		if v, ok := os.LookupEnv(strings.Trim(m, "%${}")); ok {
			return v
		}
		return m
	})
}

// varRef matches one variable reference: Windows' %NAME%, or ${NAME} or $NAME.
var varRef = regexp.MustCompile(`%[A-Za-z_][A-Za-z0-9_()]*%|\$\{[A-Za-z_][A-Za-z0-9_]*\}|\$[A-Za-z_][A-Za-z0-9_]*`)
