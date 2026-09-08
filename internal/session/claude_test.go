package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestConversationExists guards the check that decides between --resume and a
// fresh --session-id. Getting it wrong the optimistic way kills the pane:
// `claude --resume` exits immediately when there is no transcript.
func TestConversationExists(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)

	const withTranscript = "11111111-2222-3333-4444-555555555555"
	const withoutTranscript = "99999999-8888-7777-6666-555555555555"

	// Transcripts live in a per-working-directory folder whose name is derived
	// from that directory; the id is what identifies the conversation.
	projectDir := filepath.Join(home, "projects", "C--Users-someone-code-repo")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, withTranscript+".jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	if !ConversationExists(withTranscript) {
		t.Error("a session with a transcript should be resumable")
	}
	if ConversationExists(withoutTranscript) {
		t.Error("a session with no transcript must not be resumed")
	}
	if ConversationExists("") {
		t.Error("an empty session id is never resumable")
	}

	// A session interrupted before it recorded anything leaves the file
	// behind with nothing in it, which `claude --resume` rejects as roundly
	// as a missing one.
	const emptyTranscript = "12121212-3434-5656-7878-909090909090"
	if err := os.WriteFile(filepath.Join(projectDir, emptyTranscript+".jsonl"), nil, 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	if ConversationExists(emptyTranscript) {
		t.Error("an empty transcript is not something to resume")
	}
}

// TestConversationExistsWithoutClaudeState covers a machine where Claude Code
// has never run.
func TestConversationExistsWithoutClaudeState(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(t.TempDir(), "does-not-exist"))
	if ConversationExists("11111111-2222-3333-4444-555555555555") {
		t.Error("no state directory means nothing to resume")
	}
}

// TestClaudeArgsResumeVsFresh pins the two argument shapes.
func TestClaudeArgsResumeVsFresh(t *testing.T) {
	const id = "11111111-2222-3333-4444-555555555555"

	fresh := strings.Join(ClaudeArgs(id, "/tmp/s.json", false, nil), " ")
	if !strings.Contains(fresh, "--session-id "+id) {
		t.Errorf("a new pane should pin its session id, got %q", fresh)
	}
	if strings.Contains(fresh, "--resume") {
		t.Errorf("a new pane must not resume, got %q", fresh)
	}

	resumed := strings.Join(ClaudeArgs(id, "/tmp/s.json", true, nil), " ")
	if !strings.Contains(resumed, "--resume "+id) {
		t.Errorf("a restored pane should resume its conversation, got %q", resumed)
	}
	if strings.Contains(resumed, "--session-id") {
		t.Errorf("resuming must not also pin a session id, got %q", resumed)
	}
	for _, args := range []string{fresh, resumed} {
		if !strings.Contains(args, "--settings /tmp/s.json") {
			t.Errorf("lifecycle hooks must always be installed, got %q", args)
		}
	}
}

// TestHookSettingsRegisterSessionStart checks the generated settings subscribe
// to the event whose reply tells a pane's agent where it is running. It fires
// again on a compaction, which is what keeps that description from being
// summarised away.
func TestHookSettingsRegisterSessionStart(t *testing.T) {
	dir := t.TempDir()
	path, err := WriteHookSettings(dir, "pane-id", "/bin/agent-wrapper", "http://127.0.0.1:1/hook", "tok")
	if err != nil {
		t.Fatalf("write settings: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var got settingsFile
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	matchers, ok := got.Hooks["SessionStart"]
	if !ok || len(matchers) == 0 || len(matchers[0].Hooks) == 0 {
		t.Fatalf("no SessionStart hook in %s", raw)
	}
	if cmd := matchers[0].Hooks[0].Command; !strings.Contains(cmd, "--event SessionStart") {
		t.Errorf("SessionStart command = %q", cmd)
	}
}

// TestSessionStartDoesNotMoveTheStatusDot keeps a compaction, which reports as
// a session start of its own, from showing a busy agent as idle.
func TestSessionStartDoesNotMoveTheStatusDot(t *testing.T) {
	if _, _, ok := StatusForEvent("SessionStart", ""); ok {
		t.Error("SessionStart should not change a pane's status")
	}
}

// TestClaudeArgsGuardsATaskThatLooksLikeAFlag covers the opening prompt a pane
// is spawned with. Nothing stops someone starting a task with a dash, and the
// CLI would take it for an option it does not have and exit at once.
func TestClaudeArgsGuardsATaskThatLooksLikeAFlag(t *testing.T) {
	const id = "11111111-2222-3333-4444-555555555555"

	got := ClaudeArgs(id, "", false, []string{"--verbose is the wrong flag; fix the parser"})
	last := got[len(got)-2:]
	if last[0] != "--" {
		t.Errorf("argv = %q; the task should be shielded with --", got)
	}

	// The common case is left exactly as it was.
	plain := ClaudeArgs(id, "", false, []string{"fix the parser"})
	for _, a := range plain {
		if a == "--" {
			t.Errorf("argv = %q; an ordinary task needs no --", plain)
		}
	}
	if plain[len(plain)-1] != "fix the parser" {
		t.Errorf("argv = %q; the task should come last", plain)
	}
}
