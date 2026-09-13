package main

import (
	"bytes"
	"encoding/base64"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/hooks"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/spend"
)

// statusJSON is what Claude Code hands a status line, trimmed to the parts the
// bridge reads and a little around them.
const statusJSON = `{"session_id":"conv-1","cwd":"/repo","model":{"id":"claude-opus-5","display_name":"Opus"},` +
	`"cost":{"total_cost_usd":1.31,"total_duration_ms":5000},` +
	`"context_window":{"total_input_tokens":84000,"total_output_tokens":1200,"context_window_size":200000,"used_percentage":42},` +
	`"rate_limits":{"five_hour":{"used_percentage":72,"resets_at":1789300800},"seven_day":{"used_percentage":31.5}}}`

// shellLines are the user's status line commands the tests run, in the shell
// Claude Code would run them in here.
type shellLines struct{ echo, slow, fails string }

func userLines(t *testing.T) shellLines {
	t.Helper()
	if runtime.GOOS == "windows" && session.GitBash() == "" {
		return shellLines{
			echo:  `[Console]::In.ReadToEnd()`,
			slow:  `Start-Sleep -Seconds 3; Write-Output late`,
			fails: `Write-Output partial; exit 3`,
		}
	}
	return shellLines{echo: `cat`, slow: `sleep 3; echo late`, fails: `echo partial; exit 3`}
}

func usageServer(t *testing.T) (*hooks.Server, chan spend.Report) {
	t.Helper()
	srv, err := hooks.Serve(nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	got := make(chan spend.Report, 4)
	srv.SetUsageHandler(func(r spend.Report) { got <- r })
	return srv, got
}

func then(line string) string { return base64.RawURLEncoding.EncodeToString([]byte(line)) }

// The user's own command is given what Claude Code gave the bridge, and what it
// prints is what Claude Code gets; the figures go to the application alongside.
func TestStatuslineRunsTheUsersCommandAndReports(t *testing.T) {
	srv, got := usageServer(t)
	var out, errs bytes.Buffer
	code := statusline([]string{"--endpoint", srv.UsageEndpoint(), "--token", srv.Token(), "--session", "pane-1",
		"--then", then(userLines(t).echo)}, strings.NewReader(statusJSON), &out, &errs)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs.String())
	}
	if strings.TrimSpace(out.String()) != statusJSON {
		t.Errorf("the user's command printed %q, want what Claude Code gave it", out.String())
	}
	select {
	case r := <-got:
		if r.Pane != "pane-1" || r.Conversation != "conv-1" || !r.Cumulative || r.Cost.USD != 1.31 || r.Cost.Source != "agent" ||
			r.Tokens.In != 84000 || r.Tokens.Out != 1200 || len(r.Windows) != 2 {
			t.Errorf("got %+v, want the session's running figures and two windows", r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("nothing was reported")
	}
}

// A user's command that is slow does not hold the figures back, and one that
// fails is still printed, with its own exit code.
func TestStatuslineIsNotHeldUpByTheUsersCommand(t *testing.T) {
	srv, got := usageServer(t)
	lines := userLines(t)
	done := make(chan string, 1)
	go func() {
		var out bytes.Buffer
		statusline([]string{"--endpoint", srv.UsageEndpoint(), "--token", srv.Token(), "--session", "pane-1",
			"--then", then(lines.slow)}, strings.NewReader(statusJSON), &out, &bytes.Buffer{})
		done <- out.String()
	}()
	select {
	case <-got:
	case <-done:
		t.Fatal("the figures waited for the user's slow command")
	case <-time.After(2500 * time.Millisecond):
		t.Fatal("the figures waited for the user's slow command")
	}
	if out := <-done; strings.TrimSpace(out) != "late" {
		t.Errorf("the slow command printed %q", out)
	}

	var out bytes.Buffer
	code := statusline([]string{"--endpoint", srv.UsageEndpoint(), "--token", srv.Token(), "--session", "pane-1",
		"--then", then(lines.fails)}, strings.NewReader(statusJSON), &out, &bytes.Buffer{})
	if code != 3 || strings.TrimSpace(out.String()) != "partial" {
		t.Errorf("a failing command: exit %d, printed %q; want 3 and its own line", code, out.String())
	}
}

// A command that does not decode is not run, not even the part that did:
// DecodeString hands that part back beside its error, and it used to be run.
func TestStatuslineRunsNothingOfACommandThatDoesNotDecode(t *testing.T) {
	// Nine bytes are twelve characters of base64, every one of them decoded
	// before the one that is not base64 at all.
	var out, errs bytes.Buffer
	statusline([]string{"--then", then("echo ran;") + "!"}, strings.NewReader(statusJSON), &out, &errs)
	if strings.Contains(out.String(), "ran") {
		t.Errorf("printed %q: the part of the command that decoded was run", out.String())
	}
	if !strings.Contains(errs.String(), "does not decode") {
		t.Errorf("said %q, want it to say the command does not decode", errs.String())
	}
}

// An application that has gone costs the user's line nothing.
func TestStatuslineWithNoApplicationStillPrintsTheUsersLine(t *testing.T) {
	srv, _ := usageServer(t)
	endpoint, token := srv.UsageEndpoint(), srv.Token()
	_ = srv.Close()
	var out bytes.Buffer
	statusline([]string{"--endpoint", endpoint, "--token", token, "--session", "pane-1",
		"--then", then(userLines(t).echo)}, strings.NewReader(statusJSON), &out, &bytes.Buffer{})
	if strings.TrimSpace(out.String()) != statusJSON {
		t.Errorf("printed %q, want the user's line", out.String())
	}
}

// The pane's settings no longer put the secret on the bridge's command line,
// where anybody on the machine could read it: the bridge takes it from the
// pane's environment, which Claude Code passes on.
func TestStatuslineTakesTheSecretFromThePane(t *testing.T) {
	srv, got := usageServer(t)
	t.Setenv("FLOCKDECK_TOKEN", srv.Token())
	var errs bytes.Buffer
	statusline([]string{"--endpoint", srv.UsageEndpoint(), "--session", "pane-1"},
		strings.NewReader(statusJSON), &bytes.Buffer{}, &errs)
	select {
	case <-got:
	case <-time.After(5 * time.Second):
		t.Fatalf("nothing was reported with the secret in the environment alone: %s", errs.String())
	}
}

// With no command of the user's, the bridge prints nothing and still reports:
// that is somebody who turned the status line on to see their limits.
func TestStatuslineWithNoCommandPrintsNothing(t *testing.T) {
	srv, got := usageServer(t)
	var out bytes.Buffer
	code := statusline([]string{"--endpoint", srv.UsageEndpoint(), "--token", srv.Token(), "--session", "pane-1"},
		strings.NewReader(statusJSON), &out, &bytes.Buffer{})
	if code != 0 || out.Len() != 0 {
		t.Errorf("exit %d, printed %q; want nothing", code, out.String())
	}
	select {
	case <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("nothing was reported")
	}
}

// Everything in what Claude Code hands the status line may be missing: the
// windows until the first answer, and for anybody not on a subscription; the
// cost until there is one.
func TestStatusReportReadsWhatIsThere(t *testing.T) {
	r, ok := statusReport([]byte(statusJSON), "p", "claude:/c")
	if !ok {
		t.Fatal("a whole status line input could not be read")
	}
	byName := map[string]spend.Window{}
	for _, w := range r.Windows {
		byName[w.Name] = w
	}
	five, seven := byName["five_hour"], byName["seven_day"]
	if five.Used != 72 || !five.Percent || five.Account != "claude:/c" || five.ResetsAt.Unix() != 1789300800 {
		t.Errorf("five_hour = %+v", five)
	}
	if seven.Used != 31.5 || !seven.ResetsAt.IsZero() {
		t.Errorf("seven_day = %+v, want 31.5%% with no reset time", seven)
	}

	r, ok = statusReport([]byte(`{"session_id":"s","model":{"id":"claude-sonnet-5"}}`), "p", "claude:/c")
	if !ok || r.Cost.Known || len(r.Windows) != 0 || r.Account != "claude:/c" {
		t.Errorf("before the first answer: %+v, want the login and nothing else", r)
	}
	if _, ok := statusReport([]byte(`not json`), "p", "a"); ok {
		t.Error("input that is not JSON was reported")
	}
}
