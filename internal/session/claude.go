package session

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jmwri/flockdeck/internal/session/transcript"
	"github.com/jmwri/flockdeck/internal/sysproc"
)

// LookClaude returns the path to the claude CLI, or an error explaining that
// it is not installed. Look does the same for any agent, reading the command
// and where it comes from off its Spec; this is the answer for the one agent
// Flockdeck could run before it had a Spec to read it from.
func LookClaude() (string, error) {
	exe, err := exec.LookPath("claude")
	if err != nil {
		return "", fmt.Errorf("the `claude` CLI was not found on PATH; install Claude Code first: %w", err)
	}
	return exe, nil
}

// ClaudeArgs builds the argv for a Claude pane.
//
// A pane is built from a Spec by Launch, and what this is for is to say, in one
// place and independently of the catalog, exactly what a Claude pane is run
// with. TestArgvFromSpecMatchesClaude holds the Spec-built argv against it,
// because a Claude pane that gains or loses an argument either refuses to start
// or starts a conversation that cannot be resumed.
//
// New panes pin their conversation to our own session id with --session-id so
// that a later run can reattach to exactly that conversation with --resume,
// which is what makes layout restore meaningful rather than cosmetic.
func ClaudeArgs(sessionID, settingsPath string, resume bool, extra []string) []string {
	argv := []string{"claude"}
	if resume {
		argv = append(argv, "--resume", sessionID)
	} else {
		argv = append(argv, "--session-id", sessionID)
	}
	if settingsPath != "" {
		argv = append(argv, "--settings", settingsPath)
	}
	// The extra arguments are the task the pane opens with, and a task is
	// free to begin with a dash -- "-p is not what I meant, use ...". Left as
	// it is, the CLI reads it as an option it does not have and the pane dies
	// on the spot; "--" is how the rest is declared not to be options.
	if len(extra) > 0 && strings.HasPrefix(extra[0], "-") {
		argv = append(argv, "--")
	}
	return append(argv, extra...)
}

// hookEvents are the Claude Code lifecycle events Flockdeck subscribes to.
// Each maps to the status the pane should take on when the event fires.
var hookEvents = []string{
	// SessionStart is subscribed to for a second reason: its reply is how the
	// pane tells its agent which pane it is. It fires again after a compaction,
	// so that description survives the context being summarised away.
	"SessionStart",
	"UserPromptSubmit",
	"PreToolUse",
	"PostToolUse",
	"Notification",
	"Stop",
	"SessionEnd",
}

// laterHookEvents are the events that say what the ones above leave unsaid:
// PermissionRequest as a permission dialog opens -- the Notification about it
// comes only six seconds later, and never if it is answered first -- and the
// turn or tool that ended on an error or a refusal, which fires none of the
// events above, so the pane went on saying "working" until the next prompt.
//
// They are only subscribed to where the installed Claude Code is known to have
// them. Claude Code 2.1.269, read from its executable, has all four, and skips
// a hook event it does not know with a warning ("Unknown hook event ... was
// ignored") instead of refusing the file. How an older one treats a name it
// does not know was not established, and a settings file it refused would take
// every hook of the pane with it.
var laterHookEvents = []string{"PermissionRequest", "PostToolUseFailure", "StopFailure", "PermissionDenied"}

// laterHooksSince is the first Claude Code known to have laterHookEvents.
var laterHooksSince = [3]int{2, 1, 269}

// claudeVersion is what `claude --version` says, asked once per run. It is a
// variable so a test can say instead.
var claudeVersion = sync.OnceValue(installedClaudeVersion)

// installedClaudeVersion asks the installed Claude Code what version it is,
// which takes it some tens of milliseconds, and gives up after three seconds.
// Anything that goes wrong is an unknown version, which subscribes to what
// every Claude Code has.
func installedClaudeVersion() string {
	exe, err := LookClaude()
	if err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "--version")
	sysproc.NoWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// hookEventsFor is the events to subscribe to for a Claude Code reporting a
// version, as `claude --version` prints it: "2.1.269 (Claude Code)".
func hookEventsFor(version string) []string {
	if versionAtLeast(version, laterHooksSince) {
		return append(slices.Clip(hookEvents), laterHookEvents...)
	}
	return hookEvents
}

// versionAtLeast reports whether a version, as `claude --version` prints it, is
// at least min. One it cannot read is not.
func versionAtLeast(version string, min [3]int) bool {
	field, _, _ := strings.Cut(strings.TrimSpace(version), " ")
	parts := strings.SplitN(field, ".", 3)
	if len(parts) != 3 {
		return false
	}
	var got [3]int
	for i, part := range parts {
		// A pre-release is numbered like the release: "2.1.269-beta.1".
		digits := strings.IndexFunc(part, func(r rune) bool { return r < '0' || r > '9' })
		if digits < 0 {
			digits = len(part)
		}
		n, err := strconv.Atoi(part[:digits])
		if err != nil {
			return false
		}
		got[i] = n
	}
	for i := range got {
		if got[i] != min[i] {
			return got[i] > min[i]
		}
	}
	return true
}

// settingsFile is the shape of the JSON handed to `claude --settings`.
type settingsFile struct {
	Hooks map[string][]hookMatcher `json:"hooks"`
}

type hookMatcher struct {
	Matcher string     `json:"matcher,omitempty"`
	Hooks   []hookSpec `json:"hooks"`
}

type hookSpec struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout,omitempty"`
}

// WriteHookSettings writes a settings file that makes the pane report its
// lifecycle to Flockdeck, and returns its path.
//
// The hook command re-invokes this same binary in `hook` mode, so there is no
// dependency on node, python or a shell script living next to the binary. The
// settings are additive: the user's own settings and hooks still load.
func WriteHookSettings(dir, sessionID, selfExe, endpoint, token string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create settings dir: %w", err)
	}
	path := filepath.Join(dir, sessionID+".settings.json")

	events := hookEventsFor(claudeVersion())
	hooks := make(map[string][]hookMatcher, len(events))
	for _, ev := range events {
		cmd := fmt.Sprintf("%s hook --endpoint %s --token %s --session %s --event %s",
			quoteArg(selfExe), quoteArg(endpoint), quoteArg(token), quoteArg(sessionID), ev)
		hooks[ev] = []hookMatcher{{
			Hooks: []hookSpec{{Type: "command", Command: cmd, Timeout: 5}},
		}}
	}

	data, err := json.MarshalIndent(settingsFile{Hooks: hooks}, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode settings: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", fmt.Errorf("write settings: %w", err)
	}
	return path, nil
}

// quoteArg quotes an argument for the shell that runs the hook command, when
// it contains anything that shell would otherwise act on.
func quoteArg(s string) string { return quoteArgFor(runtime.GOOS, s) }

// quoteArgFor is quoteArg for a given platform.
//
// On Windows the shell may be cmd.exe, where only double quotes group
// anything. Elsewhere it is sh, where double quotes still expand "$" and a
// backtick: a binary installed under a directory with either in its name ran
// something else or nothing at all, and every pane's status stopped changing.
// Single quotes there take everything literally.
func quoteArgFor(goos, s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\n\"'&|<>()^%$`\\;*?[]{}~#!") {
		return s
	}
	if goos == "windows" {
		return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// StatusForEvent maps a Claude lifecycle event to the pane status it implies.
// The second return value is false for events that should not change status.
func StatusForEvent(event, tool string) (Status, string, bool) {
	switch event {
	case "SessionStart":
		// Subscribed to for its reply, not for status: it fires again on a
		// compaction, which happens mid-turn, so acting on it would report an
		// agent as idle in the middle of its work.
		return StatusIdle, "", false
	case "UserPromptSubmit":
		return StatusWorking, "", true
	case "PreToolUse":
		// A question put to the user arrives as a tool call like any other,
		// so without this the pane shows it as work in progress -- green,
		// with the tool's name on it -- for as long as nobody answers.
		if tool == "AskUserQuestion" {
			return StatusWaiting, tool, true
		}
		return StatusWorking, tool, true
	case "PostToolUse":
		return StatusWorking, "", true
	case "Notification":
		// Fired when Claude needs permission or has been idle waiting on input.
		// Claude's names no tool; Flockdeck's own chat client names the one it
		// is asking permission for.
		return StatusWaiting, tool, true
	case "Stop":
		return StatusIdle, "", true
	case "PermissionRequest":
		// A permission dialog is opening for the tool the PreToolUse before it
		// named. Naming nothing here keeps that tool on the pane, and marks the
		// wait as one Enter answers, as the Notification six seconds later does.
		return StatusWaiting, "", true
	case "PostToolUseFailure", "PermissionDenied":
		// A tool failed, or was refused without asking; the turn goes on.
		return StatusWorking, "", true
	case "StopFailure", "Interrupted":
		// The turn ended on an error rather than with a Stop, or the user
		// stopped the tool it was running (hooks.Interrupted) and Claude Code
		// went back to its prompt.
		return StatusIdle, "", true
	case "SessionEnd":
		// The conversation has ended; the process has not, necessarily.
		// Clearing a conversation fires this and carries straight on, and even
		// on a real exit the pane is still drawing its last screen. Only the
		// reader watching the process go away knows that it has, so this says
		// nothing rather than handing every caller a status that has to be
		// recognised and thrown away -- a pane marked exited under a live
		// process stops accepting what is typed into it and hands new viewers
		// a closed stream, with nothing to put it right again.
		return StatusIdle, "", false
	default:
		return StatusIdle, "", false
	}
}

// ConversationExists reports whether Claude Code has a stored transcript for a
// session id.
//
// This matters because `claude --resume <id>` fails outright when there is
// nothing to resume: it prints "No conversation found with session ID" and
// exits. A pane that was created but never prompted has no transcript, so
// resuming one would kill it on restart, and would kill every such pane when a
// saved layout is restored.
func ConversationExists(sessionID string) bool {
	return transcript.Exists(claudeSpec, sessionID)
}
