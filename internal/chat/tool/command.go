package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/jmwri/flockdeck/internal/sysproc"
)

const (
	// commandTimeout is how long a command is given when the model does not
	// say. A build or a test run is the point of the tool, so it is generous;
	// something that outlives it has hung, and a hung command in a pane the
	// user is not looking at is worse than a failed one.
	commandTimeout = 2 * time.Minute
	// commandMaxTimeout bounds what the model may ask for.
	commandMaxTimeout = 30 * time.Minute
	// commandMaxOutput bounds what comes back. A test run that prints a
	// hundred megabytes has said everything useful in the first and last of
	// it, which is what is kept.
	commandMaxOutput = 64 << 10
	// commandWaitDelay is how long output is still read once the command has
	// exited or been killed, for whatever it started that still holds the pipe.
	commandWaitDelay = 3 * time.Second
)

// runCommand runs one program in the pane's working directory.
//
// There is no shell. Flockdeck runs on Windows as well as on Unix, and a tool that
// went through `sh` would work on two of the three platforms and quietly mean
// something different on the third; a tool that went through `cmd` would do
// the same in reverse. So the command line is split here, the program is found
// on PATH, and it is run directly. Anything that needs a pipeline needs a
// shell, and the model is told to run the pieces itself instead.
type runCommand struct {
	root *Root
	// hide names variables kept out of a command's environment.
	hide []string
}

func (t *runCommand) Name() string { return "run_command" }

func (t *runCommand) Describe() Schema {
	description := "Run one program in the working directory and return its output and exit " +
		"status. There is no shell, so pipes, redirection, globbing and command chaining " +
		"(| > < ; && ||) are not available: run one program at a time and use the other " +
		"tools to read files or find them. Arguments may be quoted with single or double " +
		"quotes. The user is asked before anything runs."
	if runtime.GOOS == "windows" {
		// Said up front, because the only other way a model learns it is a
		// failed call for every cmd.exe command it tries.
		description += " On Windows, a command cmd.exe carries out itself -- dir, type, copy, " +
			"del, mkdir -- has no program behind it and is run as `cmd /c dir`."
	}
	return Schema{
		Name:        t.Name(),
		Description: description,
		Params: object(map[string]Property{
			"command":         {Type: "string", Description: "The command line, for example: go test ./..."},
			"timeout_seconds": {Type: "integer", Description: "How long to allow before the command is killed. Defaults to 120."},
		}, "command"),
	}
}

type commandArgs struct {
	Command        string `json:"command"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

func (t *runCommand) Approval(args json.RawMessage) string {
	var a commandArgs
	if err := decode(args, &a); err != nil {
		return ""
	}
	argv, err := splitCommand(a.Command)
	if err != nil || len(argv) == 0 {
		return ""
	}
	if batchUnsafe(t.program(argv[0]), argv[1:]) != nil {
		// Run refuses it, and says why: it finds the program the same way.
		return ""
	}
	return fmt.Sprintf("Run `%s` in %s?", strings.TrimSpace(a.Command), t.root.Dir())
}

// Prefix is the standing permission on offer for this call, which the chat
// loop offers as "always" and remembers for the rest of the session.
//
// Approval is per call everywhere else, deliberately: an approved write is one
// file and the next one is a different decision. A command prefix is the one
// place where standing permission is worth offering, because a session spends
// its time running the same handful of commands and asking every time trains
// the user to stop reading the question. It is the first two words of the
// command, so that allowing `git status` does not also allow `git push` while
// allowing `go test` covers every package the session goes on to test.
func (t *runCommand) Prefix(args json.RawMessage) string {
	var a commandArgs
	if err := decode(args, &a); err != nil {
		return ""
	}
	argv, err := splitCommand(a.Command)
	if err != nil || runsAnything(argv) {
		return ""
	}
	// An option in the second place names no subcommand, so the two words
	// cover every one there is: "always" for `git -c` is standing permission
	// for `git -c core.pager=<anything> log`, which runs <anything>.
	if len(argv) > 1 && strings.HasPrefix(argv[1], "-") {
		return ""
	}
	return commandPrefix(argv)
}

// runsAnything reports whether a command's first two words leave what will run
// to the words after them. "Always" for one of those would be standing
// permission for every command there is, offered as though it were for one, so
// it is not offered: each is asked about on its own.
//
// Some programs run the rest of their arguments as a command of their own, and
// their first two words never say what: `env FOO=1 ...`, `sudo ...`, and on
// Windows `powershell` and `wsl`, which hand the rest to a shell -- so that
// `powershell Get-ChildItem "; Remove-Item x"` runs both. A shell or an
// interpreter is agreed to for one script, `bash build.sh` or `python
// manage.py`; with an option in that place -- `-c`, `-lc`, `-e`, `-m`,
// `-NoProfile` -- the code or the module it runs is named later, or anything
// at all.
func runsAnything(argv []string) bool {
	if len(argv) < 2 {
		return false
	}
	prog := strings.TrimSuffix(strings.ToLower(filepath.Base(argv[0])), ".exe")
	next := strings.ToLower(argv[1])
	switch prog {
	case "cmd", "powershell", "wsl", "env", "sudo", "doas", "runas", "xargs", "nohup", "nice",
		"time", "timeout", "busybox", "start", "watch":
		return true
	case "sh", "bash", "zsh", "dash", "ksh", "fish", "pwsh", "python", "python3", "py",
		"node", "ruby", "perl", "php", "deno", "bun":
		return strings.HasPrefix(next, "-") || strings.HasPrefix(next, "/") || next == "eval"
	}
	return false
}

func (t *runCommand) Run(ctx context.Context, args json.RawMessage) (string, error) {
	var a commandArgs
	if err := decode(args, &a); err != nil {
		return "", err
	}
	argv, err := splitCommand(a.Command)
	if err != nil {
		return "", err
	}
	if len(argv) == 0 {
		return "", fmt.Errorf("command is empty")
	}
	timeout := commandTimeout
	if a.TimeoutSeconds > 0 {
		timeout = time.Duration(a.TimeoutSeconds) * time.Second
		if timeout > commandMaxTimeout {
			timeout = commandMaxTimeout
		}
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	program := t.program(argv[0])
	if err := batchUnsafe(program, argv[1:]); err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, program, argv[1:]...)
	cmd.Dir = t.root.Dir()
	// Something the command started and left running -- a dev server, a
	// watcher -- keeps the output pipe open for as long as it lives, and
	// without a limit here the tool would wait on it for that long, timeout
	// or none: killing the command does not kill what it started.
	cmd.WaitDelay = commandWaitDelay
	// The output is captured for the model, so a window would show the user
	// nothing they could use. On Windows it would otherwise open one for every
	// call, and another for each console program the command starts in turn.
	sysproc.NoWindow(cmd)
	// Nobody can answer a prompt the command puts up: its input is empty and
	// its output goes to the model. git asking for a username on a push waits
	// for the whole timeout and then reports nothing useful; told there is no
	// terminal, it fails at once and says why.
	cmd.Env = append(t.environ(), "GIT_TERMINAL_PROMPT=0")
	// Standard output and standard error are interleaved because that is the
	// order they happened in, and a compiler's diagnostics are only useful
	// beside the line of progress they interrupted. One writer for both is
	// written to by one goroutine at a time.
	var out capture
	cmd.Stdout, cmd.Stderr = &out, &out
	runErr := cmd.Run()

	var b strings.Builder
	b.WriteString(settleLines(stripANSI(out.String())))
	if b.Len() > 0 && !strings.HasSuffix(b.String(), "\n") {
		b.WriteString("\n")
	}
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		// Said with how to get longer: told only that it was killed, a model
		// runs the same command again and is killed at the same point.
		fmt.Fprintf(&b, "[killed after %s; a longer run can be asked for with timeout_seconds, up to %d]\n",
			timeout, int(commandMaxTimeout/time.Second))
	case runErr == nil:
		b.WriteString("[exit status 0]\n")
	case errors.Is(runErr, exec.ErrWaitDelay):
		b.WriteString("[exit status 0; something it started is still running, and its output was not waited for]\n")
	default:
		var exit *exec.ExitError
		if errors.As(runErr, &exit) {
			fmt.Fprintf(&b, "[exit status %d]\n", exit.ExitCode())
		} else {
			// A command that could not be started at all -- usually a program
			// that is not installed -- is an error rather than a result, so
			// the model is told plainly instead of reading an empty output.
			return "", notFoundHint(argv[0], runErr)
		}
	}
	return b.String(), nil
}

// program is the file a command's first word runs, found one way for both the
// question and the run, so that what is checked is what runs.
//
// A bare name is looked up on PATH, as exec.Command would. A relative path is
// taken from the working directory the command runs in, not from wherever the
// chat itself was started, and looked up as an absolute path so that Windows'
// extensions are added to it here: left relative, Go adds them only as the
// program starts, so .\gradlew was checked as a name with no extension and ran
// as gradlew.bat.
func (t *runCommand) program(name string) string {
	path := name
	// An empty word -- the command `"" x` -- is no path, and there is no first
	// character of it to look at.
	if name != "" && filepath.Base(name) != name && !filepath.IsAbs(name) && filepath.VolumeName(name) == "" &&
		!os.IsPathSeparator(name[0]) {
		path = filepath.Join(t.root.Dir(), name)
	}
	if p, err := exec.LookPath(path); err == nil {
		return p
	}
	return path
}

// batchUnsafe refuses an argument cmd.exe would read as syntax, where the
// program is, or might be, a Windows batch file.
//
// There is no shell here, except that Windows runs a .bat or .cmd -- npm,
// yarn and many other tools installed from a package manager are one -- by
// handing its whole command line to cmd.exe, which reads the arguments as
// cmd.exe syntax. A quote in an argument ends the quoting Go puts round it,
// so `npm test 'x"&del /q *'` runs del. "Always for `npm test`" made that
// a del nobody was asked about. A % expands a variable. The characters are
// refused in any argument to a batch file: none can be passed through safely.
//
// Only a program found as an .exe or a .com is let through with them, rather
// than only a .bat or a .cmd kept out: Windows has more than one way of
// spelling a batch file's name that does not end in .bat -- gradlew.bat. and
// "gradlew.bat " are gradlew.bat once it drops the trailing dot or space --
// and a name that could be spelled past a list of what to refuse is safer
// checked against a list of what to allow.
func batchUnsafe(program string, args []string) error {
	if runtime.GOOS != "windows" {
		return nil
	}
	ext := strings.ToLower(filepath.Ext(strings.TrimRight(program, ". ")))
	if ext == ".exe" || ext == ".com" {
		return nil
	}
	what := "is a batch file"
	if ext != ".bat" && ext != ".cmd" {
		what = "was not found as a program (.exe) and may be a batch file"
	}
	for _, a := range args {
		if i := strings.IndexAny(a, "\"&|<>^%\n\r"); i >= 0 {
			return fmt.Errorf("%s %s, whose arguments cmd.exe reads as its own syntax, "+
				"so %q cannot be passed in one; run it without that character", filepath.Base(program), what, string(a[i]))
		}
	}
	return nil
}

// environ is the environment a command runs with: this process's, less the
// variables hidden from it. Names are compared the way the platform does,
// which on Windows is without regard to case.
func (t *runCommand) environ() []string {
	env := os.Environ()
	if len(t.hide) == 0 {
		return env
	}
	out := env[:0:0]
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		hidden := false
		for _, h := range t.hide {
			if name == h || (runtime.GOOS == "windows" && strings.EqualFold(name, h)) {
				hidden = true
				break
			}
		}
		if !hidden {
			out = append(out, kv)
		}
	}
	return out
}

// notFoundHint says what to do about a program that could not be found, where
// there is something better to say than that it was not on PATH.
//
// On Windows the commonest reasons are two: the command is one cmd.exe provides
// itself, with no program behind it, or it is a Unix habit with no Windows
// program of that name. A model told only "executable file not found" tries
// the same thing again.
func notFoundHint(program string, err error) error {
	if runtime.GOOS != "windows" {
		return err
	}
	// A script is not a program to Windows, however it is named: build.ps1 is
	// "not found" and ./deploy.sh "does not exist", and a model told either
	// runs it the same way again.
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
		switch strings.ToLower(filepath.Ext(program)) {
		case ".ps1":
			return fmt.Errorf("Windows does not run a .ps1 file as a program; if it exists, run it as `powershell -File %s`", program)
		case ".sh":
			return fmt.Errorf("Windows does not run a .sh file as a program; if it exists, run it as `bash %s`, where Git Bash or WSL is installed", program)
		}
	}
	if !errors.Is(err, exec.ErrNotFound) {
		return err
	}
	name := strings.ToLower(program)
	switch {
	case cmdBuiltins[name]:
		return fmt.Errorf("%s is built into cmd.exe rather than a program on PATH; run it as `cmd /c %s ...`", program, program)
	case unixOnly[name] != "":
		return fmt.Errorf("there is no %s on this Windows machine; %s", program, unixOnly[name])
	}
	return err
}

// cmdBuiltins are the commands cmd.exe carries out itself.
var cmdBuiltins = map[string]bool{
	"dir": true, "type": true, "echo": true, "copy": true, "del": true, "erase": true,
	"move": true, "ren": true, "rename": true, "mkdir": true, "md": true, "rmdir": true,
	"rd": true, "set": true, "cd": true, "chdir": true, "mklink": true, "ver": true, "vol": true,
}

// unixOnly are programs a model reaches for out of habit, and the tool here
// that does their job.
var unixOnly = map[string]string{
	"ls":   "use list_dir, or `cmd /c dir`",
	"cat":  "use read_file",
	"head": "use read_file with a limit",
	"tail": "use read_file with an offset",
	"grep": "use the grep tool",
	"find": "use the glob tool",
	"rm":   "use `cmd /c del` for a file, or `cmd /c rmdir /s /q` for a directory",
	"cp":   "use `cmd /c copy`",
	"mv":   "use `cmd /c move`",
}

// commandPrefix is the first two words of a command, or the first if that is
// all there is.
func commandPrefix(argv []string) string {
	switch {
	case len(argv) == 0:
		return ""
	case len(argv) == 1:
		return argv[0]
	default:
		return argv[0] + " " + argv[1]
	}
}

// splitCommand turns a command line into an argv.
//
// Single and double quotes group words; a backslash is left alone, because on
// Windows it is a path separator and treating it as an escape would break more
// commands than it fixed. Anything a shell would treat as a pipeline, a
// redirection or a chain is refused outright rather than passed through as a
// literal argument, which is the failure that would otherwise be silent: the
// model would be told the command ran when what ran was the first half of it
// with the rest as arguments.
func splitCommand(line string) ([]string, error) {
	if strings.TrimSpace(line) == "" {
		return nil, fmt.Errorf("command is empty")
	}
	var (
		argv  []string
		cur   strings.Builder
		quote rune
		has   bool
	)
	flush := func() {
		if has {
			argv = append(argv, cur.String())
			cur.Reset()
			has = false
		}
	}
	for _, r := range line {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
				continue
			}
			cur.WriteRune(r)
			has = true
		case r == '\'' || r == '"':
			quote = r
			has = true
		case r == ' ' || r == '\t':
			flush()
		case r == '\n' || r == '\r':
			return nil, fmt.Errorf("the command spans more than one line; run one program at a time")
		case strings.ContainsRune("|&;<>`", r):
			return nil, fmt.Errorf("%q is shell syntax and there is no shell here; run one program at a time, and use read_file, glob or grep instead of a pipeline", string(r))
		default:
			cur.WriteRune(r)
			has = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("the command has an unclosed %s quote", string(quote))
	}
	flush()
	if len(argv) == 0 {
		return nil, fmt.Errorf("command is empty")
	}
	return argv, nil
}

// capture keeps the beginning and the end of what a command prints and counts
// what went missing between them. The end holds the failure and the beginning
// holds what was being attempted, and the middle of a hundred thousand lines
// of test output is where nothing happens.
//
// It keeps them as the output arrives rather than gathering all of it and
// cutting afterwards, because a command can print gigabytes -- a runaway log,
// a binary sent to the terminal -- and the chat would have to hold every byte
// of it to hand the model sixty-four kilobytes.
type capture struct {
	head  []byte
	tail  []byte // the most recent output, at most twice half before trimming
	total int64
}

func (c *capture) Write(p []byte) (int, error) {
	n := len(p)
	c.total += int64(n)
	half := commandMaxOutput / 2
	if room := half - len(c.head); room > 0 {
		k := min(room, len(p))
		c.head, p = append(c.head, p[:k]...), p[k:]
	}
	c.tail = append(c.tail, p...)
	if len(c.tail) > 2*half {
		copy(c.tail, c.tail[len(c.tail)-half:])
		c.tail = c.tail[:half]
	}
	return n, nil
}

// String is the output as the model is given it.
func (c *capture) String() string {
	half := commandMaxOutput / 2
	tail := c.tail
	if len(tail) > half && c.total > int64(commandMaxOutput) {
		tail = tail[len(tail)-half:]
	}
	omitted := c.total - int64(len(c.head)) - int64(len(tail))
	// What a command printed goes to the model over a wire that carries JSON,
	// and bytes that are not valid UTF-8 -- a half-written escape sequence, a
	// file name in some other encoding, or a rune cut in two at either end of
	// the gap -- would fail to encode at all, losing the whole result rather
	// than the byte.
	if omitted == 0 {
		return asText(append(c.head[:len(c.head):len(c.head)], tail...))
	}
	return fmt.Sprintf("%s\n[... %s of output omitted ...]\n%s",
		asText(c.head), humanBytes(omitted), asText(tail))
}

// clipOutput is what capture makes of s.
func clipOutput(s string) string {
	var c capture
	c.Write([]byte(s))
	return c.String()
}
