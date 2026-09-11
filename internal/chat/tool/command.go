package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
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
}

func (t *runCommand) Name() string { return "run_command" }

func (t *runCommand) Describe() Schema {
	return Schema{
		Name: t.Name(),
		Description: "Run one program in the working directory and return its output and exit " +
			"status. There is no shell, so pipes, redirection, globbing and command chaining " +
			"(| > < ; && ||) are not available: run one program at a time and use the other " +
			"tools to read files or find them. Arguments may be quoted with single or double " +
			"quotes. The user is asked before anything runs.",
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
	if err != nil {
		return ""
	}
	return commandPrefix(argv)
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

	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
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
	// Standard output and standard error are interleaved because that is the
	// order they happened in, and a compiler's diagnostics are only useful
	// beside the line of progress they interrupted.
	out, runErr := cmd.CombinedOutput()

	var b strings.Builder
	b.WriteString(clipOutput(string(out)))
	if b.Len() > 0 && !strings.HasSuffix(b.String(), "\n") {
		b.WriteString("\n")
	}
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		fmt.Fprintf(&b, "[killed after %s]\n", timeout)
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
			return "", runErr
		}
	}
	return b.String(), nil
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

// clipOutput keeps the beginning and the end of a long output and says how
// much went missing between them. The end holds the failure and the beginning
// holds what was being attempted, and the middle of a hundred thousand lines
// of test output is where nothing happens.
func clipOutput(s string) string {
	// What a command printed goes to the model over a wire that carries JSON,
	// and bytes that are not valid UTF-8 -- a half-written escape sequence, a
	// file name in some other encoding -- would fail to encode at all, losing
	// the whole result rather than the byte.
	s = strings.ToValidUTF8(s, "")
	if len(s) <= commandMaxOutput {
		return s
	}
	half := commandMaxOutput / 2
	// Cutting by byte count can land in the middle of a rune, so each end is
	// swept again.
	head := strings.ToValidUTF8(s[:half], "")
	tail := strings.ToValidUTF8(s[len(s)-half:], "")
	return fmt.Sprintf("%s\n[... %s of output omitted ...]\n%s", head, humanBytes(int64(len(s)-2*half)), tail)
}
