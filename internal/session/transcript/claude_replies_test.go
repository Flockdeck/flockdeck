package transcript

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeReplies puts a transcript where Claude Code would have left one and
// returns the session id that finds it.
func writeReplies(t *testing.T, lines ...string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)

	const id = "11111111-2222-3333-4444-555555555555"
	writeTranscript(t, filepath.Join(home, "projects", "C--Users-someone-code-repo"), id, lines...)
	return id
}

// TestPathIsFoundUnderAHomeWithBrackets covers a Claude Code state directory
// whose path a glob would read as a pattern. Finding nothing there is not a
// missing history panel: it is every restored pane starting afresh instead of
// resuming its conversation.
func TestPathIsFoundUnderAHomeWithBrackets(t *testing.T) {
	home := filepath.Join(t.TempDir(), "[dev] home")
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	const id = "11111111-2222-3333-4444-555555555555"
	writeTranscript(t, filepath.Join(home, "projects", "C--Users-dev-repo"), id,
		`{"type":"user","message":{"role":"user","content":"hello"}}`)

	want := filepath.Join(home, "projects", "C--Users-dev-repo", id+".jsonl")
	if got := claudePath(id); got != want {
		t.Errorf("claudePath = %q, want %q", got, want)
	}
	// An id is a file name and nothing more.
	if got := claudePath(`..\` + id); got != "" {
		t.Errorf("claudePath of a path = %q, want nothing", got)
	}
}

// TestPathPrefersACopyWithSomethingInIt covers a conversation stored in two
// folders whose newer copy is empty -- a session interrupted before it
// recorded anything. The empty one is not the conversation, and choosing it
// tells a restored pane there is nothing to resume.
func TestPathPrefersACopyWithSomethingInIt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	const id = "11111111-2222-3333-4444-555555555555"
	full := writeTranscript(t, filepath.Join(home, "projects", "C--repo"), id,
		`{"type":"user","message":{"role":"user","content":"the conversation"}}`)
	empty := filepath.Join(home, "projects", "C--repo-wt", id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(empty), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := os.Chtimes(full, now.Add(-time.Hour), now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}

	if got := claudePath(id); got != full {
		t.Errorf("claudePath = %q, want the copy with the conversation in it, %q", got, full)
	}
	if !Exists(claudeSpec, id) {
		t.Error("a conversation with a copy holding it should be worth resuming")
	}
}

// TestRecentRepliesReadsWhatTheAgentSaid covers the source a fan-out reads its
// tasks from. What matters is that it is the agent's words and only its words:
// not its thinking, not the tools it ran, and not a subagent's answer.
func TestRecentRepliesReadsWhatTheAgentSaid(t *testing.T) {
	id := writeReplies(t,
		`{"type":"user","message":{"role":"user","content":"give me a plan"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"thinking","thinking":"muttering to myself"}]}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Here is the plan:\n\n- Do the first thing\n- Do the second thing"}]}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","name":"Read"}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"file contents"}]}}`,
		`{"type":"assistant","isSidechain":true,"message":{"role":"assistant","content":[{"type":"text","text":"a subagent's report"}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"thanks"}]}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"You are welcome."}]}}`,
	)

	got := claudeReplies(id, 4)
	if len(got) != 2 {
		t.Fatalf("read %d turns, want 2: %#v", len(got), got)
	}
	// Newest first: the follow-up, then the plan it followed.
	if got[0] != "You are welcome." {
		t.Errorf("newest turn = %q", got[0])
	}
	if !strings.Contains(got[1], "- Do the first thing") {
		t.Errorf("the plan was lost: %q", got[1])
	}
	for _, turn := range got {
		if strings.Contains(turn, "muttering") {
			t.Error("thinking is not what the agent said")
		}
		if strings.Contains(turn, "subagent") {
			t.Error("a subagent's answer is not this agent's")
		}
		if strings.Contains(turn, "file contents") {
			t.Error("a tool result is not what the agent said")
		}
	}

	// A tool result must not split a turn: everything said in reply to one
	// prompt belongs together.
	if n := len(claudeReplies(id, 1)); n != 1 {
		t.Errorf("asked for 1 turn, got %d", n)
	}
}

// TestRecentRepliesWithoutATranscript covers a shell pane, and an agent that
// has not said anything yet.
func TestRecentRepliesWithoutATranscript(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(t.TempDir(), "does-not-exist"))
	if got := claudeReplies("11111111-2222-3333-4444-555555555555", 4); got != nil {
		t.Errorf("RecentReplies = %#v, want nothing", got)
	}
	if got := claudeReplies("", 4); got != nil {
		t.Errorf("RecentReplies with no session = %#v, want nothing", got)
	}
}

// TestRecentRepliesSkipsUnparseableLines guards against a transcript being
// written to while it is read: the last line can be half a line.
func TestRecentRepliesSkipsUnparseableLines(t *testing.T) {
	id := writeReplies(t,
		`{"type":"user","message":{"role":"user","content":"go"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"- Do the thing"}]}}`,
		`{"type":"assistant","message":{"role":"assist`,
	)
	got := claudeReplies(id, 4)
	if len(got) != 1 || !strings.Contains(got[0], "Do the thing") {
		t.Errorf("RecentReplies = %#v", got)
	}
}

// TestRecentRepliesKeepsATurnWholeAcrossSyntheticEntries covers the entries
// Claude Code writes as if the user had typed them: a slash command, its
// output, an injected reminder. Ending a turn on one leaves the fan-out dialog
// showing the tail of a plan instead of the plan.
func TestRecentRepliesKeepsATurnWholeAcrossSyntheticEntries(t *testing.T) {
	id := writeReplies(t,
		`{"type":"user","message":{"role":"user","content":"give me a plan"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"- Do the first thing"}]}}`,
		`{"type":"user","message":{"role":"user","content":"<command-name>/cost</command-name>"}}`,
		`{"type":"user","message":{"role":"user","content":"<local-command-stdout>$0.42</local-command-stdout>"}}`,
		`{"type":"user","isMeta":true,"message":{"role":"user","content":"Caveat: the messages below were generated"}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"<system-reminder>be nice</system-reminder>"}]}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"- Do the second thing"}]}}`,
	)

	got := claudeReplies(id, 4)
	if len(got) != 1 {
		t.Fatalf("read %d turns, want 1: %#v", len(got), got)
	}
	for _, want := range []string{"- Do the first thing", "- Do the second thing"} {
		if !strings.Contains(got[0], want) {
			t.Errorf("turn lost %q: %q", want, got[0])
		}
	}
	if strings.Contains(got[0], "0.42") {
		t.Errorf("slash command output is not what the agent said: %q", got[0])
	}
}

// TestTranscriptPathPrefersTheLiveCopy covers a conversation resumed from a
// different working directory: Claude Code files it under the new directory's
// folder and the old copy stays behind, so the same id matches twice.
func TestTranscriptPathPrefersTheLiveCopy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)

	const id = "77777777-7777-7777-7777-777777777777"
	// "a-..." sorts before "z-...", so picking the first match picks the stale
	// one.
	stale := writeTranscript(t, filepath.Join(home, "projects", "a-old-place"), id,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"stale answer"}]}}`)
	live := writeTranscript(t, filepath.Join(home, "projects", "z-new-place"), id,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"live answer"}]}}`)

	now := time.Now()
	touch(t, stale, now.Add(-24*time.Hour))
	touch(t, live, now)

	if got := claudePath(id); got != live {
		t.Errorf("TranscriptPath = %q, want the most recently written copy %q", got, live)
	}
	got := claudeReplies(id, 1)
	if len(got) != 1 || !strings.Contains(got[0], "live answer") {
		t.Errorf("RecentReplies read the stale transcript: %#v", got)
	}
}
