package ghcli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/jmwri/flockdeck/internal/sysproc"
)

// commandTimeout bounds an ordinary gh call: listing or viewing a PR or
// issue, posting a comment, asking for CI status. Every one of these is a
// single round trip to the GitHub API, so this is generous next to git's own
// commandTimeout in internal/gitx, which only has to cover reading the local
// repository.
//
// It is a variable so a test can shorten it; nothing else assigns to it.
var commandTimeout = 30 * time.Second

// binName is the executable run, a variable so a test can point it at a
// fake gh without touching PATH.
var binName = "gh"

// run executes gh in dir and returns stdout. Args go on gh's command line
// exactly as given; a caller with a value that might start with "-" is
// responsible for putting "--" ahead of it, the same as with gitx.
func run(dir string, args ...string) (string, error) {
	out, _, err := runCapture(context.Background(), commandTimeout, dir, nil, args...)
	return out, err
}

// runCtx is run with a context the caller can cancel -- used only by Login,
// whose wait is a person's, not the network's.
func runCtx(ctx context.Context, timeout time.Duration, dir string, args ...string) (string, error) {
	out, _, err := runCapture(ctx, timeout, dir, nil, args...)
	return out, err
}

// runWithStdin is run with input handed to gh on stdin, for a comment or
// description body: gh reads "--body-file -" from stdin, which sidesteps
// both a shell's own quoting and the length a command line can hold, the same
// problem runWithInput in gitx exists for.
func runWithStdin(dir, input string, args ...string) (string, error) {
	out, _, err := runCapture(context.Background(), commandTimeout, dir, strings.NewReader(input), args...)
	return out, err
}

// runJSON runs gh with --json already among args and decodes its stdout into
// out. gh's own --json support is what makes this package practical: asking
// for exactly the fields wanted, as JSON, means there is no screen-scraping
// of prose meant for a terminal to get out of step with.
func runJSON(dir string, out any, args ...string) error {
	text, err := run(dir, args...)
	if err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(text), out); err != nil {
		return fmt.Errorf("gh %s: could not read its answer: %w", ghLabel(args), err)
	}
	return nil
}

// runCapture runs gh and returns stdout and stderr separately, under timeout.
// stdin is nil for a call that has none.
func runCapture(parent context.Context, timeout time.Duration, dir string, stdin *strings.Reader, args ...string) (string, string, error) {
	if strings.TrimSpace(dir) == "" {
		return "", "", fmt.Errorf("gh %s: no working tree was named", ghLabel(args))
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, binName, args...)
	cmd.Dir = dir
	// Flockdeck on Windows is a GUI program with no console of its own; see
	// sysproc.NoWindow. Without this every gh call would flash open a
	// terminal window.
	sysproc.NoWindow(cmd)
	// gh's own prompts -- "would you like to authenticate", "choose an
	// editor" -- have nowhere to be answered from here, so it is told not to
	// ask and to fail instead, the same reasoning as GIT_TERMINAL_PROMPT=0 in
	// gitx.
	cmd.Env = append(os.Environ(), "GH_PROMPT_DISABLED=1", "GH_NO_UPDATE_NOTIFIER=1", "GH_PAGER=", "PAGER=")
	if stdin != nil {
		cmd.Stdin = stdin
	}
	var outb, errb bytes.Buffer
	cmd.Stdout = &outb
	cmd.Stderr = &errb
	err := cmd.Run()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", "", fmt.Errorf("gh %s: gave up after %s", ghLabel(args), timeout)
		}
		if parent.Err() != nil {
			return "", "", parent.Err()
		}
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = strings.TrimSpace(outb.String())
		}
		if msg == "" {
			msg = err.Error()
		}
		return "", "", fmt.Errorf("gh %s: %s", ghLabel(args), firstLines(msg, 6))
	}
	return outb.String(), errb.String(), nil
}

// ghLabel names a command for an error message by its subcommand -- "gh pr
// view", "gh issue comment" -- rather than by its whole argument list, which
// can carry a comment's entire body. Mirrors gitLabel in internal/gitx.
func ghLabel(args []string) string {
	words := []string{"gh"}
	for _, a := range args {
		if strings.HasPrefix(a, "-") || len(words) == 3 {
			break
		}
		words = append(words, a)
	}
	return strings.Join(words, " ")
}

// firstLines keeps an error short enough for a toast. gh can answer with
// several lines -- a GraphQL error, then which query it came from -- of which
// the first line or two carry the point.
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
