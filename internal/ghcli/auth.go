package ghcli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/jmwri/flockdeck/internal/sysproc"
)

// loginTimeout bounds gh auth login --web: it waits on a person to approve in
// their browser, which git's own networkTimeout (ten minutes, see gitx) is
// about right for too, so it is reused as a round number rather than invented
// afresh.
var loginTimeout = 10 * time.Minute

// AuthStatus is what `gh auth status` says about this machine's login.
type AuthStatus struct {
	// LoggedIn is false both when nobody has ever run gh auth login here and
	// when a login has expired or been revoked; gh answers both the same way.
	LoggedIn bool
	Host     string
	Account  string
	// Scopes is the token's scopes as gh prints them, comma-separated -- e.g.
	// "gist, read:org, repo, workflow" -- or "" when LoggedIn is false or gh
	// did not report them (a token from a fine-grained PAT reports none).
	Scopes string
}

// GetAuthStatus asks gh whether it is logged in to github.com. dir only
// matters in that gh reads it to decide which host an enterprise repository
// found there defaults to; github.com is asked for explicitly regardless, so
// an empty dir (no project open) still gets an answer.
func GetAuthStatus(dir string) (AuthStatus, error) {
	if strings.TrimSpace(dir) == "" {
		var err error
		if dir, err = os.Getwd(); err != nil {
			dir = "."
		}
	}
	out, errText, err := runCapture(context.Background(), commandTimeout, dir, nil, "auth", "status", "--hostname", "github.com")
	if err != nil {
		// Not being logged in is not a failure worth surfacing as one: it is
		// the ordinary state before Login has been run, and the caller's own
		// UI already has a place to say so.
		if strings.Contains(err.Error(), "not logged into") || strings.Contains(err.Error(), "You are not logged") {
			return AuthStatus{}, nil
		}
		return AuthStatus{}, err
	}
	return parseAuthStatus(out + errText), nil
}

var (
	loggedInRe = regexp.MustCompile(`Logged in to (\S+) (?:as|account) (\S+)`)
	scopesRe   = regexp.MustCompile(`Token scopes:\s*(.+)`)
)

// parseAuthStatus reads gh's own report, which is prose meant for a
// terminal -- gh has never offered --json for this command -- rather than
// anything structured. It is kept to a plain string in, struct out function
// so a test can check it against gh's wording without running gh.
func parseAuthStatus(text string) AuthStatus {
	var st AuthStatus
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if m := loggedInRe.FindStringSubmatch(line); m != nil {
			st.LoggedIn = true
			st.Host, st.Account = m[1], m[2]
			continue
		}
		if m := scopesRe.FindStringSubmatch(line); m != nil {
			st.Scopes = strings.Trim(m[1], "'\" ")
		}
	}
	return st
}

// LoginEvent is one thing that happened during Login, sent as it happens so
// the dialog on screen can show the one-time code the moment gh has one to
// show, rather than only once the whole flow has finished.
//
// Exactly one of Code, Line, Done or Err is set on any event but Code, which
// also carries the Line it was read from.
type LoginEvent struct {
	// Code is the one-time code to type at github.com/login/device, once gh
	// has printed it.
	Code string
	// Line is one line of gh's own commentary, kept for a log a person can
	// read if something goes wrong partway through.
	Line string
	// Done is true on the last event, once gh has confirmed the login. No
	// further events follow it.
	Done bool
	// Err is set instead of Done when the flow did not finish -- the person
	// closed the browser tab, the code expired, the timeout passed.
	Err error
}

// oneTimeCodeMarker is the text gh's --web flow puts ahead of the code, in
// "! First copy your one-time code: 1234-ABCD".
const oneTimeCodeMarker = "one-time code: "

// parseOneTimeCode picks the code out of one line of gh's output, or reports
// it found none.
func parseOneTimeCode(line string) (string, bool) {
	i := strings.Index(line, oneTimeCodeMarker)
	if i < 0 {
		return "", false
	}
	code := strings.TrimSpace(line[i+len(oneTimeCodeMarker):])
	if code == "" {
		return "", false
	}
	return code, true
}

// Login walks a person through `gh auth login --web`: gh opens
// github.com/login/device in their browser itself, showing a one-time code
// there for them to copy in, and waits for that to be approved. Every event
// gh prints along the way is sent to onEvent as it happens, so a dialog can
// show the code as soon as it exists and let the person open the browser
// themselves if gh's own attempt to failed (a machine with no default
// browser set, over a remote desktop with none configured).
//
// gh's flow pauses once to ask "Press Enter to open github.com in your
// browser...". There is nothing to gain by waiting for a person to answer
// that from here -- the one thing it might otherwise ask, confirming the
// browser should open, is answered the same way every time -- so a newline is
// sent the moment gh starts.
//
// ctx cancelled closes the flow early, as does loginTimeout passing; either
// way onEvent is sent a final event with Err set, unless gh had already
// finished.
func Login(ctx context.Context, onEvent func(LoginEvent)) error {
	if onEvent == nil {
		onEvent = func(LoginEvent) {}
	}
	ctx, cancel := context.WithTimeout(ctx, loginTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, binName, "auth", "login", "--hostname", "github.com", "--git-protocol", "https", "--web")
	sysproc.NoWindow(cmd)
	cmd.Env = append(os.Environ(), "GH_PROMPT_DISABLED=", "GH_NO_UPDATE_NOTIFIER=1")

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("gh auth login: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("gh auth login: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("gh auth login: could not start: %w", err)
	}
	go func() {
		_, _ = io.WriteString(stdin, "\n")
		stdin.Close()
	}()

	scanner := bufio.NewScanner(stderr)
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if line == "" {
			continue
		}
		if code, ok := parseOneTimeCode(line); ok {
			onEvent(LoginEvent{Code: code, Line: line})
			continue
		}
		onEvent(LoginEvent{Line: line})
	}

	err = cmd.Wait()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			err = fmt.Errorf("gh auth login: gave up after %s -- the code may have expired; try again", loginTimeout)
		} else {
			err = fmt.Errorf("gh auth login: %w", err)
		}
		onEvent(LoginEvent{Err: err})
		return err
	}
	onEvent(LoginEvent{Done: true})
	return nil
}

// Logout signs this machine out of github.com, for the settings panel's own
// "sign out" -- see internal/server's ghcli.go for where it is offered.
func Logout(dir string) error {
	if strings.TrimSpace(dir) == "" {
		var err error
		if dir, err = os.Getwd(); err != nil {
			dir = "."
		}
	}
	_, err := run(dir, "auth", "logout", "--hostname", "github.com", "--yes")
	return err
}
