package gitx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/jmwri/flockdeck/internal/sysproc"
)

// commandTimeout bounds a git invocation that only reads the local repository,
// so a hung command cannot freeze the UI thread.
const commandTimeout = 20 * time.Second

// networkTimeout bounds push, pull and fetch instead.
//
// Twenty seconds is nothing to a command that talks to a remote: a first push
// of a branch with any history behind it, a fetch of a repository nobody has
// cloned recently, or any of it over a link that is having a bad day. Killing
// those part way through and reporting a hang is worse than waiting, and
// nothing is waiting on them -- they run off the UI thread and tell the panel
// when they are done. Asking for a password is already refused outright, so
// the hang these guard against cannot happen here in the first place.
//
// A commit gets it too, for its hooks, which are just as free to take their time.
//
// It is a variable so a test can shorten it; nothing else assigns to it.
var networkTimeout = 10 * time.Minute

// pipeGrace is how long git's output is waited for once git has exited.
const pipeGrace = 2 * time.Second

// run executes git in dir and returns stdout.
func run(dir string, args ...string) (string, error) {
	out, _, err := runCapture(context.Background(), commandTimeout, dir, args...)
	return out, err
}

// runUntil is run for a command whose answer stops being wanted part way
// through, so that cancelling ctx kills the process rather than leaving it to
// finish work nobody will read.
func runUntil(ctx context.Context, dir string, args ...string) (string, error) {
	out, _, err := runCapture(ctx, commandTimeout, dir, args...)
	return out, err
}

// runVerbose returns what git said on both streams, under the network deadline.
//
// push, pull and fetch write their progress and their summary to stderr, so a
// caller that shows the user only stdout shows them nothing at all: stderr
// comes first because that is the order the two were written in. They are also
// the only commands here that talk to anything outside the machine, which is
// why they are the ones given the longer deadline.
func runVerbose(dir string, args ...string) (string, error) {
	var outb bytes.Buffer
	errText, err := runToEnv(context.Background(), networkTimeout, dir, batchSSH(dir), nil, &outb, args...)
	out := outb.String()
	if err != nil {
		// A remote that wants a login, on a machine with no credential
		// helper to supply one, is refused a prompt here -- there is no
		// terminal to type into -- and git says only that it "could not read
		// Username ... terminal prompts disabled". Saying how to get past it
		// is the useful part: sign in once where git can ask.
		if strings.Contains(err.Error(), "terminal prompts disabled") {
			return "", &gitError{"the remote " + quotedURL(err.Error()) + "wants a login, and git cannot ask for one from inside Flockdeck. " +
				"Sign in once from a terminal in " + dir + " (git fetch will do), or set up a credential helper; the panel then uses the saved login."}
		}
		return "", err
	}
	return cleanProgress(errText + out), nil
}

// batchSSH is the environment push, pull and fetch are given so that ssh
// cannot stop to ask a question.
//
// GIT_TERMINAL_PROMPT stops only git's own prompts. ssh opens the terminal
// itself to ask whether to trust a host it has not seen, or for a key's
// passphrase, so a run started from a terminal was asked there -- where nobody
// was looking -- and waited all of networkTimeout for an answer. BatchMode
// makes ssh fail at once instead, saying why.
//
// A user who has chosen an ssh command of their own keeps it: GIT_SSH_COMMAND
// or GIT_SSH in the environment, or core.sshCommand in git's config, which is
// often what names the key a repository needs and which GIT_SSH_COMMAND would
// override. The config is asked here, one short local git process ahead of a
// command that goes out to the network, rather than ahead of every status.
func batchSSH(dir string) []string {
	for _, name := range []string{"GIT_SSH_COMMAND", "GIT_SSH"} {
		if _, ok := os.LookupEnv(name); ok {
			return nil
		}
	}
	if cmd, err := run(dir, "config", "--get", "core.sshCommand"); err == nil && strings.TrimSpace(cmd) != "" {
		return nil
	}
	return []string{"GIT_SSH_COMMAND=ssh -o BatchMode=yes"}
}

// quotedURL picks the address out of git's "could not read Username for
// 'https://…'", with a space after it, or "" when there is none to pick.
func quotedURL(msg string) string {
	_, rest, ok := strings.Cut(msg, " for '")
	if !ok {
		return ""
	}
	url, _, ok := strings.Cut(rest, "'")
	if !ok || url == "" {
		return ""
	}
	return url + " "
}

// cleanProgress collapses git's in-place progress lines -- "Writing objects:
// 33%\rWriting objects: 100%, done." -- down to the state they finished in.
func cleanProgress(s string) string {
	var kept []string
	for _, line := range strings.Split(s, "\n") {
		if i := strings.LastIndex(line, "\r"); i >= 0 {
			line = line[i+1:]
		}
		if strings.TrimSpace(line) != "" {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n")
}

// runCapture executes git in dir and returns stdout and stderr separately.
func runCapture(parent context.Context, timeout time.Duration, dir string, args ...string) (string, string, error) {
	var out bytes.Buffer
	errText, err := runTo(parent, timeout, dir, nil, &out, args...)
	if err != nil {
		return "", "", err
	}
	return out.String(), errText, nil
}

// runWithInput is runCapture with input given to git on stdin, for what is too
// long to go on its command line: Windows allows one of about 32,000
// characters, and a commit message an agent wrote past that failed to start
// git at all -- "The filename or extension is too long" -- after everything
// had already been staged.
func runWithInput(timeout time.Duration, dir, input string, args ...string) (string, string, error) {
	var out bytes.Buffer
	errText, err := runTo(context.Background(), timeout, dir, strings.NewReader(input), &out, args...)
	if err != nil {
		return "", "", err
	}
	return out.String(), errText, nil
}

// output is where runTo puts what git writes to stdout. It is read back only
// for an error message, when git explained itself there instead of on stderr.
type output interface {
	io.Writer
	String() string
}

// runTo is runCapture with stdout going to out, for a caller that means to
// keep only part of it; stderr is returned.
func runTo(parent context.Context, timeout time.Duration, dir string, in io.Reader, out output, args ...string) (string, error) {
	return runToEnv(parent, timeout, dir, nil, in, out, args...)
}

// runToEnv is runTo with env added to what git is given, for the commands
// that go out to the network (see batchSSH).
func runToEnv(parent context.Context, timeout time.Duration, dir string, env []string, in io.Reader, out output, args ...string) (string, error) {
	// An empty Dir does not mean "no repository" to exec: it means the
	// directory this process happens to be running in. Flockdeck is often started
	// from inside a checkout of something, so a caller that lost track of
	// which working tree it meant -- a review panel opened with no project
	// open, a pane whose directory never got set -- would have been answered
	// with a real status for an entirely unrelated repository, and shown it as
	// though it were the project's.
	if strings.TrimSpace(dir) == "" {
		return "", fmt.Errorf("git %s: no working tree was named", strings.Join(args, " "))
	}

	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	// Flockdeck on Windows is a GUI program with no console of its own, so
	// without this every one of these -- and the branch labels alone run one
	// per checkout every fifteen seconds -- would open a terminal window.
	sysproc.NoWindow(cmd)
	// Nothing here has a terminal to answer on, so a command that would ask
	// for a password has to fail instead of sitting until the timeout. Giving
	// up the optional index lock also keeps the status polling of several
	// panes from colliding with an agent's own commit.
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_OPTIONAL_LOCKS=0",
	)
	cmd.Env = append(cmd.Env, env...)
	var errb bytes.Buffer
	cmd.Stdin = in
	cmd.Stdout = out
	cmd.Stderr = &errb
	// The deadline kills git and nothing git started. A hook, an ssh or a
	// credential helper still holding the output pipes kept Run waiting for
	// them regardless, so a push given up on after ten minutes went on being
	// waited for until whatever it was stuck on let go -- if it ever did. This
	// stops waiting for the pipes that long after git itself has gone.
	cmd.WaitDelay = pipeGrace
	err := cmd.Run()
	if errors.Is(err, exec.ErrWaitDelay) {
		// git finished, and succeeded; only something it left running in the
		// background was still holding its output, which git had long since
		// finished writing.
		err = nil
	}
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", &timeoutError{fmt.Sprintf("%s: gave up after %s", gitLabel(args), timeout)}
		}
		if parent.Err() != nil {
			return "", parent.Err()
		}
		// Some git subcommands explain themselves on stdout rather than
		// stderr -- "nothing to commit" is the one people hit -- so fall back
		// to it before showing a bare "exit status 1".
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = strings.TrimSpace(out.String())
		}
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("%s: %s", gitLabel(args), firstLines(withoutHints(msg), 5))
	}
	return errb.String(), nil
}

// gitLabel names a command for an error message by its subcommand -- "git
// worktree add", "git push" -- rather than by its whole argument list. The
// arguments carry absolute paths and whole commit messages, and in a toast
// they came before, and crowded out, what git had actually said.
func gitLabel(args []string) string {
	words := []string{"git"}
	for _, a := range args {
		if strings.HasPrefix(a, "-") || len(words) == 3 {
			break
		}
		words = append(words, a)
	}
	return strings.Join(words, " ")
}

// withoutHints drops git's advice when there is anything else to say.
//
// The advice comes first and runs long, so the lines kept for a toast were all
// of it: a pull that could not fast-forward was reported as four lines of
// "hint:", with the "fatal: Not possible to fast-forward" that said what had
// happened cut off below them.
//
// git's "fatal: " goes too. The toast is already an error, and the word only
// stands between the reader and what went wrong.
//
// So does who owns what, in git's refusal of a repository that belongs to
// another user: on Windows it names the owner and the current user, a SID on a
// line of its own under each, and those four lines came before -- and pushed
// out of the toast -- the `git config --global --add safe.directory` line that
// is the way past it.
func withoutHints(msg string) string {
	var kept []string
	owner := false // the line before was one of the ownership report's headings
	for _, line := range strings.Split(msg, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "hint:"):
		case strings.HasSuffix(trimmed, "is owned by:") || trimmed == "but the current user is:":
			owner = true
			continue
		case owner && strings.HasPrefix(line, "\t"):
		default:
			kept = append(kept, strings.TrimPrefix(line, "fatal: "))
		}
		owner = false
	}
	if len(kept) == 0 {
		return msg
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

// firstLines keeps an error message short enough to sit in a toast: git can
// answer with a dozen lines of advice, of which the first few carry the point.
//
// Blank lines are left out rather than counted. git spaces its longer answers
// out with them, and a commit with no identity set kept "Author identity
// unknown", "*** Please tell me who you are." and two blank lines -- and not
// the two commands that would fix it.
func firstLines(msg string, n int) string {
	var lines []string
	for _, line := range strings.Split(msg, "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) <= n {
		return strings.Join(lines, "\n")
	}
	return strings.Join(lines[:n], "\n") + "\n…"
}

type gitError struct{ msg string }

func (e *gitError) Error() string { return e.msg }

// timeoutError is a command given up on at its deadline. errors.Is finds
// context.DeadlineExceeded in it, so a caller can tell a checkout that did not
// answer in time -- whose last answer is now of unknown age -- from one that
// answered with an error, such as no longer being a repository.
type timeoutError struct{ msg string }

func (e *timeoutError) Error() string { return e.msg }
func (e *timeoutError) Unwrap() error { return context.DeadlineExceeded }
