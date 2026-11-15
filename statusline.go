package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jmwri/flockdeck/internal/hooks"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/session/transcript"
	"github.com/jmwri/flockdeck/internal/spend"
)

// runStatusline implements the hidden `statusline` subcommand, which a Claude
// pane's status line runs when it is routed through Flockdeck: see
// internal/session/statusline.go for when that is and why.
func runStatusline(args []string) {
	os.Exit(statusline(args, os.Stdin, os.Stdout, os.Stderr))
}

// statusReportTimeout bounds the post to the application. Claude Code runs a
// status line after every answer, debounced to 300 ms, so the bridge has to be
// well inside that when things are well -- a loopback post is -- and must not
// hold the user's line back for long when they are not.
const statusReportTimeout = time.Second

// maxStatusInput bounds what is read from Claude Code. What it writes is a few
// kilobytes; this is a ceiling on something read once and thrown away.
const maxStatusInput = 16 << 20

// statusline is the bridge. It reads what Claude Code hands a status line, posts
// the figures in it to the application, and runs the user's own status line
// command with the same input, printing what that prints.
//
// The two run side by side, so a slow or failing command of the user's does
// not hold the figures up, and an application that is not there costs the
// user's line nothing but the wait for the post. Whatever goes wrong, what the
// user sees is what their own command printed and nothing else: a bridge that
// broke somebody's status line would be a poor trade for a figure in a header.
// Its exit code is theirs, too.
func statusline(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	stderr = &lockedWriter{w: stderr}
	fs := flag.NewFlagSet("statusline", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		endpoint = fs.String("endpoint", "", "where to post the figures")
		token    = fs.String("token", "", "shared secret; the pane's FLOCKDECK_TOKEN when not given")
		sessID   = fs.String("session", "", "pane session id")
		then     = fs.String("then", "", "the user's own status line command, base64url")
	)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	// The secret comes from the pane's environment, which Claude Code passes on
	// to its status line, rather than from a command line anybody on the
	// machine can read. --token is still taken, for a pane an earlier build
	// started.
	if *token == "" {
		*token = paneEnv("TOKEN")
	}
	input, _ := io.ReadAll(io.LimitReader(stdin, maxStatusInput))

	var user string
	if *then != "" {
		// DecodeString hands back what it decoded before the fault as well as
		// the fault, and that part was run: a value cut short, or edited by
		// hand, ran the start of somebody's command -- `rm -rf ~/tmp/x` as far
		// as `rm -rf ~/`. A command that does not decode is not run at all.
		if b, err := base64.RawURLEncoding.DecodeString(*then); err != nil {
			fmt.Fprintln(stderr, "flockdeck statusline: the status line command it was given does not decode, so it is not run:", err)
		} else {
			user = string(b)
		}
	}

	posted := make(chan struct{})
	go func() {
		defer close(posted)
		if *endpoint == "" || *sessID == "" {
			return
		}
		rep, ok := statusReport(input, *sessID, claudeAccount())
		if !ok {
			return
		}
		// Stderr, where Claude Code keeps what a status line complains about:
		// stdout is the line itself.
		if err := hooks.Report(*endpoint, *token, rep, statusReportTimeout); err != nil {
			fmt.Fprintln(stderr, "flockdeck statusline:", err)
		}
	}()

	code := 0
	if strings.TrimSpace(user) != "" {
		code = runUserStatusLine(user, input, stdout, stderr)
	}
	<-posted
	return code
}

// runUserStatusLine runs the user's own status line command as Claude Code
// would have: through the same shell, fed the same input, in the environment
// Claude Code gave the bridge -- which is where the terminal's COLUMNS and
// LINES are.
func runUserStatusLine(line string, input []byte, stdout, stderr io.Writer) int {
	cmd := session.ClaudeShellCommand(line)
	cmd.Stdin = bytes.NewReader(input)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &exit):
		return exit.ExitCode()
	}
	fmt.Fprintln(stderr, "flockdeck statusline: could not run your status line command:", err)
	return 1
}

// claudeAccount names the login a Claude pane draws on, which every Claude
// pane using the same configuration folder shares, limits and all.
func claudeAccount() string {
	home := transcript.ClaudeHome()
	if home == "" {
		return ""
	}
	return "claude:" + filepath.Clean(home)
}

// statusInput is the part of what Claude Code hands its status line that the
// pane header shows. Every part of it may be missing: rate_limits "appears
// only for Claude.ai Pro and Max subscribers ... and only after the first API
// response in the session", and each window may be absent on its own.
type statusInput struct {
	SessionID string `json:"session_id"`
	Model     struct {
		ID string `json:"id"`
	} `json:"model"`
	Cost struct {
		// Claude Code's own estimate, at list prices, of what the session
		// has cost. It starts again at nothing when /clear does.
		TotalCostUSD *float64 `json:"total_cost_usd"`
	} `json:"cost"`
	ContextWindow struct {
		TotalInputTokens  int64 `json:"total_input_tokens"`
		TotalOutputTokens int64 `json:"total_output_tokens"`
	} `json:"context_window"`
	RateLimits map[string]json.RawMessage `json:"rate_limits"`
}

type statusWindow struct {
	UsedPercentage *float64 `json:"used_percentage"`
	// ResetsAt is Unix seconds.
	ResetsAt *float64 `json:"resets_at"`
}

// statusReport turns what Claude Code handed the status line into a report for
// the pane, or reports false when it could not be read at all.
func statusReport(input []byte, pane, account string) (spend.Report, bool) {
	var in statusInput
	if json.Unmarshal(input, &in) != nil {
		return spend.Report{}, false
	}
	rep := spend.Report{
		Pane:         pane,
		Conversation: in.SessionID,
		Model:        in.Model.ID,
		Account:      account,
		Cumulative:   true,
		Tokens:       spend.Tokens{In: in.ContextWindow.TotalInputTokens, Out: in.ContextWindow.TotalOutputTokens},
	}
	if in.Cost.TotalCostUSD != nil {
		rep.Cost = spend.Cost{USD: *in.Cost.TotalCostUSD, Known: true, Source: "agent"}
	}
	for name, raw := range in.RateLimits {
		var w statusWindow
		if json.Unmarshal(raw, &w) != nil || w.UsedPercentage == nil || account == "" {
			continue
		}
		win := spend.Window{Account: account, Name: name, Used: *w.UsedPercentage, Percent: true,
			Source: "Claude Code status line"}
		if w.ResetsAt != nil && *w.ResetsAt > 0 {
			win.ResetsAt = time.Unix(int64(*w.ResetsAt), 0)
		}
		rep.Windows = append(rep.Windows, win)
	}
	return rep, true
}

// lockedWriter lets the bridge and the user's command both write to stderr.
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}
