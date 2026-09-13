package session

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode"

	"github.com/jmwri/flockdeck/internal/session/transcript"
)

// Claude Code hands its status line command the subscription's usage windows
// and the session's cost, on every refresh, and nothing else it offers says
// either: no hook carries them, and /usage asks a private endpoint. So a Claude
// pane's status line can be routed through Flockdeck -- a hidden `flockdeck
// statusline` that posts the figures to the application and then runs the
// user's own status line command, if there is one, with what Claude Code gave
// it, and prints what that prints.
//
// The catch is that the pane's settings file outranks the user's own, so a
// status line set there replaces theirs rather than adding to it, and a user
// who has none loses the keyboard hints Claude Code draws in its footer. That
// is why the bridge carries the user's command faithfully -- the rest of their
// statusLine is copied as it stands -- and why, by default, it is only set
// where the user already has a status line, where it changes nothing they can
// see.

// When a Claude pane's status line goes through Flockdeck.
const (
	// StatusLineAuto routes it only for somebody who has a status line of
	// their own. It is the default, and is kept as nothing.
	StatusLineAuto = ""
	// StatusLineOn always does, at the cost of the footer's keyboard hints for
	// somebody who has no status line of their own.
	StatusLineOn = "on"
	// StatusLineOff never does.
	StatusLineOff = "off"
)

// StatusLine is what a Claude pane's settings need to route its status line
// through Flockdeck.
type StatusLine struct {
	// Mode is one of the StatusLine constants.
	Mode string
	// Endpoint is where the bridge posts what the status line was given.
	Endpoint string
	// Cwd is where the pane runs, which is where Claude Code looks for the
	// project's own settings.
	Cwd string
	// Home is Claude Code's own folder as the pane will have it, which is where
	// it looks for the user's settings. A catalog entry that sets
	// CLAUDE_CONFIG_DIR -- the usual way to run a second account -- moves it
	// away from Flockdeck's own, and reading Flockdeck's carried the other
	// account's status line. Empty is Flockdeck's own.
	Home string
}

// utf8BOM is the byte order mark some Windows editors put at the start of a
// UTF-8 file.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// userStatusLine finds the status line the user has set, where Claude Code
// would: the project's local settings, then the project's, then the user's
// own, the first to say anything winning. Managed settings outrank the pane's
// settings file anyway, so an organisation's status line wins without help.
//
// It returns the statusLine object as it stands, so that the padding, the
// refresh interval and whatever Claude Code adds to it later are carried over
// with the command, and nil when there is none Flockdeck could run.
func userStatusLine(cwd, home string) map[string]any {
	var files []string
	if cwd != "" {
		files = append(files, filepath.Join(cwd, ".claude", "settings.local.json"), filepath.Join(cwd, ".claude", "settings.json"))
	}
	if home != "" {
		files = append(files, filepath.Join(home, "settings.json"))
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		// Claude Code takes a byte order mark off before it parses a settings
		// file (2.1.270, read from its executable), and Notepad and Windows
		// PowerShell both write one. Left on, the file does not parse here and
		// was passed over, so the status line the user set in it was not the
		// one carried.
		data = bytes.TrimPrefix(data, utf8BOM)
		var settings map[string]json.RawMessage
		if json.Unmarshal(data, &settings) != nil {
			continue
		}
		raw, ok := settings["statusLine"]
		if !ok {
			continue
		}
		// The first file that sets it decides, as it does for Claude Code: one
		// the user has set to something unusable is not overruled by another.
		var sl map[string]any
		if json.Unmarshal(raw, &sl) != nil {
			return nil
		}
		if t, _ := sl["type"].(string); t != "command" {
			return nil
		}
		if c, _ := sl["command"].(string); strings.TrimSpace(c) == "" {
			return nil
		}
		return sl
	}
	return nil
}

// statusLineSetting is the statusLine a pane's settings file carries, or nil
// for none.
func statusLineSetting(sl StatusLine, sessionID, selfExe string) map[string]any {
	if sl.Mode == StatusLineOff || sl.Endpoint == "" {
		return nil
	}
	home := sl.Home
	if home == "" {
		home = transcript.ClaudeHome()
	}
	user := userStatusLine(sl.Cwd, home)
	if user == nil && sl.Mode != StatusLineOn {
		return nil
	}
	out := map[string]any{}
	maps.Copy(out, user)
	then, _ := user["command"].(string)
	out["type"] = "command"
	out["command"] = bridgeCommand(runtime.GOOS, ChatExe(selfExe), sl.Endpoint, sessionID, then)
	return out
}

// bridgeCommand is the command line that runs the status line bridge, which
// Claude Code puts through a shell.
//
// It carries no secret: the bridge reads the pane's FLOCKDECK_TOKEN from the
// environment it inherits, as the hooks do (see WriteHookSettings).
//
// The user's own command goes along encoded, in characters no shell does
// anything with, rather than quoted: it is itself a command line for a shell,
// and quoting one for another is exactly the trap this file exists to stay
// out of. It is written into the settings file when the pane starts rather
// than looked up on every refresh.
//
// On Windows the program is the console build beside the release, which
// prints to the pipe Claude Code reads like any other console program.
func bridgeCommand(goos, exe, endpoint, sessionID, then string) string {
	args := []string{"statusline", "--endpoint", endpoint, "--session", sessionID}
	if then != "" {
		args = append(args, "--then", base64.RawURLEncoding.EncodeToString([]byte(then)))
	}
	if goos != "windows" {
		parts := []string{quoteArgFor(goos, exe)}
		for _, a := range args {
			parts = append(parts, quoteArgFor(goos, a))
		}
		return strings.Join(parts, " ")
	}
	return windowsCommandLine(exe, args, shortPath, GitBash() != "")
}

// windowsCommandLine writes a command line that Git Bash and PowerShell both
// run, where there is one, and otherwise one for whichever of them Claude Code
// is going to use.
//
// No quoting is read alike by the two: Git Bash takes a program in quotes and
// PowerShell takes it for a string, and PowerShell's `& 'program'` is a syntax
// error to bash. Tried on Windows 11 with Git Bash 5.2 and Windows PowerShell
// 5.1, a program under a folder with a space ran in both only when it was
// written with no quotes at all, which is its 8.3 short name, with forward
// slashes -- bash reads a backslash as an escape.
func windowsCommandLine(exe string, args []string, short func(string) string, gitBash bool) string {
	// Not filepath.ToSlash, which is written for the platform it runs on and
	// leaves a Windows path alone everywhere else, where the tests also run.
	slashes := func(p string) string { return strings.ReplaceAll(p, `\`, "/") }
	prog := slashes(exe)
	if !plainForBoth(prog) {
		if s := slashes(short(exe)); s != "" && plainForBoth(s) {
			prog = s
		} else if gitBash {
			prog = bashQuote(prog)
		} else {
			// PowerShell runs a program named in quotes only when told to.
			prog = "& " + powerShellQuote(exe)
		}
	}
	parts := []string{prog}
	for _, a := range args {
		if !plainForBoth(a) {
			if gitBash {
				a = bashQuote(a)
			} else {
				a = powerShellQuote(a)
			}
		}
		parts = append(parts, a)
	}
	return strings.Join(parts, " ")
}

// plainForBoth reports whether a word means itself to Git Bash and to
// PowerShell alike, with no quoting.
func plainForBoth(s string) bool {
	// A tilde leading a word is somebody's home directory to bash.
	if s == "" || strings.HasPrefix(s, "~") {
		return false
	}
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			continue
		}
		if !strings.ContainsRune(":/._-~+=", r) {
			return false
		}
	}
	return true
}

func bashQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func powerShellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
