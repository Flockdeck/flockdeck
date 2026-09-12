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
func expandVars(s string) string {
	s = percentVar.ReplaceAllStringFunc(s, func(m string) string {
		if v, ok := os.LookupEnv(strings.Trim(m, "%")); ok {
			return v
		}
		return m
	})
	return os.Expand(s, func(name string) string {
		if v, ok := os.LookupEnv(name); ok {
			return v
		}
		return "${" + name + "}"
	})
}

// percentVar matches a Windows %NAME% reference.
var percentVar = regexp.MustCompile(`%[A-Za-z_][A-Za-z0-9_()]*%`)
