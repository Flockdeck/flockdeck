//go:build windows

package session

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"

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
// `a" & echo x & "b` ran `echo x` on its own. A batch file is therefore run
// through cmd.exe with a command line written for cmd.exe, as Go's own os/exec
// tells a caller to do.
func command(p pty.Pty, exe string, args []string) *pty.Cmd {
	switch strings.ToLower(filepath.Ext(exe)) {
	case ".cmd", ".bat":
	default:
		return p.Command(exe, args...)
	}
	shell := os.Getenv("COMSPEC")
	if shell == "" {
		shell = filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")
	}
	cmd := p.Command(shell)
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: batchCommandLine(exe, args)}
	return cmd
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
