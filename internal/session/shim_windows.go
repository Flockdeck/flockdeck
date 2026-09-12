//go:build windows

package session

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"unicode/utf8"

	"github.com/aymanbagabas/go-pty"
)

// command builds the process a pane runs.
//
// An agent installed with npm is not a program on Windows but a batch file --
// codex.cmd, gemini.cmd -- and a batch file is run by cmd.exe, which reads the
// command line by rules of its own. The quoting go-pty writes is for a
// program's argument parser, and cmd.exe does not know it: it ends the
// command at the first line break, so an agent without hooks, whose briefing
// is put in front of its task, was started with "<flockdeck-context>" and
// nothing else; and a quote in the task was taken as closing the argument, so
// `a" & echo x & "b` ran `echo x` on its own. It also refuses a command line
// over 8191 characters, which a briefing and a task together pass.
//
// So an npm shim is not run at all: all it does is start node on a script
// beside it, and node is started on that script directly, with nothing
// reading the task but the agent. Any other batch file is run through
// cmd.exe with a command line written for cmd.exe, as Go's own os/exec tells a
// caller to do. Either way a command line too long to start with is shortened
// by fit rather than keeping the agent from starting.
func command(p pty.Pty, exe string, args []string) *pty.Cmd {
	switch strings.ToLower(filepath.Ext(exe)) {
	case ".cmd", ".bat":
	default:
		args = fit(args, maxCommandLine, func(a []string) int { return directLength(exe, a) })
		return p.Command(exe, args...)
	}
	if node, prefix, ok := npmScript(exe); ok {
		args = fit(args, maxCommandLine, func(a []string) int { return directLength(node, append(slices.Clone(prefix), a...)) })
		return p.Command(node, append(prefix, args...)...)
	}
	args = fit(args, maxBatchLine, func(a []string) int { return len(batchCommandLine(exe, a)) })
	shell := os.Getenv("COMSPEC")
	if shell == "" {
		shell = filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")
	}
	cmd := p.Command(shell)
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: batchCommandLine(exe, args)}
	return cmd
}

const (
	// maxBatchLine is the longest command line cmd.exe accepts.
	maxBatchLine = 8191
	// maxCommandLine is the longest CreateProcess accepts, 32767 characters,
	// less room for the environment's own quoting going wrong in its favour.
	maxCommandLine = 32000
)

// directLength is how long a command line is that starts exe with args, as a
// program's arguments are quoted.
func directLength(exe string, args []string) int {
	n := len(syscall.EscapeArg(exe))
	for _, a := range args {
		n += 1 + len(syscall.EscapeArg(a))
	}
	return n
}

// fit shortens a command line longer than limit, as length measures it, so
// that the agent starts with less rather than not at all.
//
// What gives way is the briefing an agent without hooks is handed in front of
// its task: its sections go from the last back -- the key list and the
// environment before the other agents and this pane -- with a note that it was
// shortened, and then the briefing goes altogether. The task is kept whole for
// as long as anything else is left to give, because it is the reason the pane
// exists. Only a task too long on its own is cut, and says so.
func fit(args []string, limit int, length func([]string) int) []string {
	if length(args) <= limit {
		return args
	}
	out := slices.Clone(args)
	for i := range out {
		for length(out) > limit {
			shorter, ok := shortenBriefing(out[i])
			if !ok {
				break
			}
			out[i] = shorter
		}
		if length(out) <= limit {
			return out
		}
	}
	i := 0
	for j := range out {
		if len(out[j]) > len(out[i]) {
			i = j
		}
	}
	for length(out) > limit && out[i] != "" {
		out[i] = cutTask(out[i], length(out)-limit)
	}
	return out
}

const (
	contextOpen  = "<flockdeck-context>\n"
	contextClose = "\n</flockdeck-context>"
	// shortenedNote and cutNote tell the agent that what it was handed is not
	// all there was.
	shortenedNote = "\n\n(This briefing was shortened to fit the command line this agent is started with.)"
	cutNote       = "\n\n[The rest of this task was cut to fit the command line this agent is started with.]"
)

// shortenBriefing takes one step off the briefing at the front of a prompt:
// its last section, or once only its opening is left, the briefing itself. It
// reports false when there is no briefing to shorten.
//
// The briefing is the workspace's, fenced in <flockdeck-context> with a "## "
// heading per section. Should that ever change, nothing here breaks: the
// prompt is simply read as having no briefing, and the task is cut instead.
func shortenBriefing(prompt string) (string, bool) {
	if !strings.HasPrefix(prompt, contextOpen) {
		return prompt, false
	}
	end := strings.Index(prompt, contextClose)
	if end < 0 {
		return prompt, false
	}
	body, rest := prompt[len(contextOpen):end], prompt[end+len(contextClose):]
	body = strings.TrimSuffix(body, shortenedNote)
	if cut := strings.LastIndex(body, "\n## "); cut > 0 {
		return contextOpen + strings.TrimRight(body[:cut], "\n") + shortenedNote + contextClose + rest, true
	}
	return strings.TrimLeft(rest, "\n"), true
}

// cutTask cuts at least over bytes, and the note saying so, off the end of a
// task, on a character boundary.
func cutTask(task string, over int) string {
	task = strings.TrimSuffix(task, cutNote)
	keep := len(task) - over - len(cutNote)
	if keep <= 0 {
		return ""
	}
	for keep > 0 && !utf8.RuneStart(task[keep]) {
		keep--
	}
	return task[:keep] + cutNote
}

// npmShimLine is the line an npm shim starts node on: the node it chose --
// "%_prog%" in the shims npm writes now, "%~dp0\node.exe" or a bare node in
// the older ones -- then the options the package's shebang asks node for,
// then the script, named relative to the shim's own folder, then %*.
//
// The options are taken only while each is a plain word, which is how cmd.exe
// would split them too. One that is quoted or holds anything cmd.exe acts on
// does not match, and that shim is left for cmd.exe to read rather than read
// here by guesswork. So is a batch file that runs a script beside it with
// something other than node -- `cscript "%~dp0\tool.js"` is Windows Script
// Host's, and handing it to node would start the wrong program.
var npmShimLine = regexp.MustCompile(`(?:"%_prog%"|"%~?dp0%?\\node\.exe"|\bnode)[ \t]+((?:[^"%\s&|<>^()]+[ \t]+)*)"%(?:~dp0|dp0%)\\([^"%]+\.[cm]?js)"[ \t]+%\*`)

// npmScript reports the node an npm shim would run and the arguments it puts
// in front of the task -- node's own options, then the script -- or false for
// a batch file that is not one, or whose script or node cannot be found. A
// node.exe beside the shim is the one it prefers, as the shim itself does.
func npmScript(shim string) (node string, prefix []string, ok bool) {
	data, err := os.ReadFile(shim)
	if err != nil || len(data) > 64<<10 {
		return "", nil, false
	}
	m := npmShimLine.FindSubmatch(data)
	if m == nil {
		return "", nil, false
	}
	dir := filepath.Dir(shim)
	script := filepath.Join(dir, string(m[2]))
	if fi, err := os.Stat(script); err != nil || fi.IsDir() {
		return "", nil, false
	}
	node = filepath.Join(dir, "node.exe")
	if _, err := os.Stat(node); err != nil {
		if node, err = exec.LookPath("node"); err != nil {
			return "", nil, false
		}
	}
	return node, append(strings.Fields(string(m[1])), script), true
}

// batchCommandLine writes the command line that has cmd.exe run a batch file
// with args, each arriving whole at the program the batch file hands them to.
//
// /d skips any AutoRun command, /v:off keeps "!" literal, and /s has cmd.exe
// strip exactly the outer pair of quotes and nothing else.
func batchCommandLine(script string, args []string) string {
	var w batchWriter
	w.b.WriteString(`cmd.exe /d /s /v:off /c "`)
	w.quote()
	w.b.WriteString(script)
	w.quote()
	for _, a := range args {
		w.b.WriteByte(' ')
		w.arg(a)
	}
	w.b.WriteByte('"')
	return w.b.String()
}

// batchWriter writes a batch file's command line, knowing at every point
// whether cmd.exe takes it to be inside quotes.
//
// Two readers have to agree on it. The program behind the batch file splits
// its arguments the way every Windows program does, where a quote inside an
// argument is \" -- a doubled quote ends the quoted part there, and the rest
// of the task is split at its spaces. cmd.exe reads the same line knowing
// nothing of \", so every quote turns its own quoting on or off, and where it
// believes it is outside quotes & | < > ^ ( ) act. It reads the line twice:
// once here, and once more on the batch file's own line, where %* puts it
// back. So those characters get three carets there, of which the first
// reading leaves one for the second.
type batchWriter struct {
	b      strings.Builder
	quoted bool
}

func (w *batchWriter) quote() {
	w.b.WriteByte('"')
	w.quoted = !w.quoted
}

// arg writes one argument, quoted for the program.
//
// A "%" cannot be escaped on a command line, only broken: "%%cd:~,%" is a "%"
// followed by an expansion of nothing, so "%PATH%" in a task stays that text.
// A line break cannot be passed at all -- cmd.exe ends the command there -- so
// it becomes a space: the task arrives unwrapped rather than cut off.
func (w *batchWriter) arg(a string) {
	a = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ").Replace(a)
	w.quote()
	slashes := 0
	for _, c := range a {
		switch {
		case c == '\\':
			slashes++
			w.b.WriteByte('\\')
			continue
		case c == '"':
			// Backslashes in front of a quote are doubled, and the quote
			// escaped, for the program; to cmd.exe it is a quote like any other.
			w.b.WriteString(strings.Repeat(`\`, slashes+1))
			w.quote()
		case c == '%':
			w.b.WriteString(`%%cd:~,%`)
		case !w.quoted && strings.ContainsRune("&|<>^()", c):
			w.b.WriteString(`^^^`)
			w.b.WriteRune(c)
		default:
			w.b.WriteRune(c)
		}
		slashes = 0
	}
	// Backslashes before the closing quote would escape it.
	w.b.WriteString(strings.Repeat(`\`, slashes))
	w.quote()
}
