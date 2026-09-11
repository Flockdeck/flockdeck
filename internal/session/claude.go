package session

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
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

	hooks := make(map[string][]hookMatcher, len(hookEvents))
	for _, ev := range hookEvents {
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
		return StatusWorking, tool, true
	case "PostToolUse":
		return StatusWorking, "", true
	case "Notification":
		// Fired when Claude needs permission or has been idle waiting on input.
		return StatusWaiting, "", true
	case "Stop":
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
	path := TranscriptPath(sessionID)
	if path == "" {
		return false
	}
	// The file existing is not enough: a session that was interrupted before
	// it recorded anything leaves an empty one behind, and Claude Code refuses
	// that the same way it refuses a missing one.
	fi, err := os.Stat(path)
	return err == nil && fi.Size() > 0
}
