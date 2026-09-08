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

// inheritedClaudeVars are environment variables a running Claude Code session
// injects into its children. If we passed them through, every pane we spawn
// would think it was a nested child session (and, among other things, stop
// saving its transcript). They are stripped so each pane is a clean top-level
// session, regardless of whether the wrapper itself was launched from Claude.
var inheritedClaudeVars = map[string]bool{
	"CLAUDECODE":                   true,
	"CLAUDE_CODE_CHILD_SESSION":    true,
	"CLAUDE_CODE_ENTRYPOINT":       true,
	"CLAUDE_CODE_SESSION_ID":       true,
	"CLAUDE_CODE_SSE_PORT":         true,
	"CLAUDE_CODE_DONT_INHERIT_ENV": true,
}

// Env builds the environment for a pane: the wrapper's own environment minus
// inherited Claude session markers, plus extra KEY=VALUE entries.
//
// An extra entry replaces an inherited one of the same name rather than
// joining it. A duplicated name in an environment block is resolved by the
// first copy, on Windows and on Unix alike, so appending alone would leave the
// stale value in force -- which is how a pane opened from inside another
// wrapper would tell its agent it was the pane that spawned it.
func Env(extra ...string) []string {
	replaced := make(map[string]bool, len(extra))
	for _, kv := range extra {
		if name, _, ok := strings.Cut(kv, "="); ok {
			replaced[envKey(name)] = true
		}
	}

	base := os.Environ()
	out := make([]string, 0, len(base)+len(extra))
	for _, kv := range base {
		name, _, ok := strings.Cut(kv, "=")
		if ok && (inheritedClaudeVars[strings.ToUpper(name)] || replaced[envKey(name)]) {
			continue
		}
		out = append(out, kv)
	}
	return append(out, extra...)
}

// envKey normalises an environment variable name for comparison: Windows
// matches them without regard to case, everywhere else they are exact.
func envKey(name string) string {
	if runtime.GOOS == "windows" {
		return strings.ToUpper(name)
	}
	return name
}

// LookClaude returns the path to the claude CLI, or an error explaining that
// it is not installed.
func LookClaude() (string, error) {
	exe, err := exec.LookPath("claude")
	if err != nil {
		return "", fmt.Errorf("the `claude` CLI was not found on PATH; install Claude Code first: %w", err)
	}
	return exe, nil
}

// ClaudeArgs builds the argv for a Claude pane.
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
	return append(argv, extra...)
}

// ShellArgs returns the argv for a plain shell pane on this platform.
func ShellArgs() []string {
	if runtime.GOOS == "windows" {
		if ps, err := exec.LookPath("pwsh"); err == nil {
			return []string{ps, "-NoLogo"}
		}
		if comspec := os.Getenv("COMSPEC"); comspec != "" {
			return []string{comspec}
		}
		return []string{"powershell", "-NoLogo"}
	}
	if sh := os.Getenv("SHELL"); sh != "" {
		return []string{sh, "-l"}
	}
	return []string{"/bin/sh"}
}

// hookEvents are the Claude Code lifecycle events the wrapper subscribes to.
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
// lifecycle to the wrapper, and returns its path.
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

// quoteArg wraps an argument in double quotes when it contains characters that
// would otherwise split it. Double quoting behaves the same way in cmd.exe and
// in POSIX shells for the paths and tokens we generate.
func quoteArg(s string) string {
	if s == "" {
		return `""`
	}
	if !strings.ContainsAny(s, " \t\"'&|<>()^%$`\\") {
		return s
	}
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
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
		return StatusExited, "", true
	default:
		return StatusIdle, "", false
	}
}

// claudeHome returns the directory Claude Code keeps its state in.
func claudeHome() string {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude")
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
