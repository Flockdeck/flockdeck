package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeTranscript creates a transcript file with the given JSONL lines.
func writeTranscript(t *testing.T, dir, id string, lines ...string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, id+".jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestConversationsListsAndSummarises covers the history panel's data.
func TestConversationsListsAndSummarises(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)

	cwd := filepath.Join(t.TempDir(), "myrepo")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, "projects", projectSlug(cwd))

	// The opening prompt is the summary, and content may be a plain string or
	// a list of typed blocks.
	writeTranscript(t, dir, "11111111-1111-1111-1111-111111111111",
		`{"type":"user","cwd":"`+jsonPath(cwd)+`","message":{"role":"user","content":"add a health endpoint"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":"sure"}}`)
	writeTranscript(t, dir, "22222222-2222-2222-2222-222222222222",
		`{"type":"user","cwd":"`+jsonPath(cwd)+`","message":{"role":"user","content":[{"type":"text","text":"fix the flaky test"}]}}`)

	// Synthetic entries must not be mistaken for the user's first prompt.
	writeTranscript(t, dir, "33333333-3333-3333-3333-333333333333",
		`{"type":"user","cwd":"`+jsonPath(cwd)+`","message":{"role":"user","content":"<command-name>/clear</command-name>"}}`,
		`{"type":"user","cwd":"`+jsonPath(cwd)+`","message":{"role":"user","content":"Caveat: something"}}`,
		`{"type":"user","cwd":"`+jsonPath(cwd)+`","message":{"role":"user","content":"the real question"}}`)

	// Make the ordering deterministic: most recently used first.
	now := time.Now()
	touch(t, filepath.Join(dir, "11111111-1111-1111-1111-111111111111.jsonl"), now.Add(-2*time.Hour))
	touch(t, filepath.Join(dir, "22222222-2222-2222-2222-222222222222.jsonl"), now.Add(-1*time.Hour))
	touch(t, filepath.Join(dir, "33333333-3333-3333-3333-333333333333.jsonl"), now)

	got, err := Conversations(cwd)
	if err != nil {
		t.Fatalf("conversations: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("found %d conversations, want 3", len(got))
	}
	if got[0].Summary != "the real question" {
		t.Errorf("first summary = %q; synthetic entries should be skipped", got[0].Summary)
	}
	if got[1].Summary != "fix the flaky test" {
		t.Errorf("block content not read: %q", got[1].Summary)
	}
	if got[2].Summary != "add a health endpoint" {
		t.Errorf("string content not read: %q", got[2].Summary)
	}
	if got[2].Messages != 2 {
		t.Errorf("entry count = %d, want 2", got[2].Messages)
	}
	if got[0].ID != "33333333-3333-3333-3333-333333333333" {
		t.Errorf("id = %q, want the file name", got[0].ID)
	}
}

// TestConversationsFindsRelocatedProjectFolder covers the fallback for when the
// folder name cannot be derived from the path.
func TestConversationsFindsRelocatedProjectFolder(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)

	cwd := filepath.Join(t.TempDir(), "somewhere")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	// A folder whose name does not match the derived slug, but whose
	// transcripts record this working directory.
	dir := filepath.Join(home, "projects", "an-unexpected-folder-name")
	writeTranscript(t, dir, "44444444-4444-4444-4444-444444444444",
		`{"type":"user","cwd":"`+jsonPath(cwd)+`","message":{"role":"user","content":"hello there"}}`)

	got, err := Conversations(cwd)
	if err != nil {
		t.Fatalf("conversations: %v", err)
	}
	if len(got) != 1 || got[0].Summary != "hello there" {
		t.Fatalf("expected the relocated folder to be found, got %+v", got)
	}
}

// TestConversationsWithNoHistory covers a project that has never been used.
func TestConversationsWithNoHistory(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	got, err := Conversations(t.TempDir())
	if err != nil {
		t.Fatalf("conversations: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected no conversations, got %d", len(got))
	}
}

// TestFirstPromptTidiesText pins the summary formatting.
func TestFirstPromptTidiesText(t *testing.T) {
	if got := firstPrompt("  lots\n  of   space  "); got != "lots of space" {
		t.Errorf("whitespace not collapsed: %q", got)
	}
	if got := firstPrompt("<system-reminder>hi</system-reminder>"); got != "" {
		t.Errorf("system reminders should be skipped, got %q", got)
	}
	long := strings.Repeat("a", 300)
	got := firstPrompt(long)
	if len([]rune(got)) != 161 {
		t.Errorf("long prompt not truncated: %d runes", len([]rune(got)))
	}
}

func touch(t *testing.T, path string, when time.Time) {
	t.Helper()
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
}

// jsonPath escapes a path for embedding in a JSON string literal.
func jsonPath(p string) string { return strings.ReplaceAll(p, `\`, `\\`) }

// TestConversationsCountsEveryEntry guards the entry count against the bound
// on how far the opening prompt is looked for: a long conversation whose first
// prompt is on line one must not be reported as a short one.
func TestConversationsCountsEveryEntry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)

	cwd := filepath.Join(t.TempDir(), "chatty")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}

	const entries = summaryScanLimit * 3
	lines := make([]string, 0, entries)
	lines = append(lines, `{"type":"user","cwd":"`+jsonPath(cwd)+`","message":{"role":"user","content":"first prompt"}}`)
	for len(lines) < entries {
		lines = append(lines, `{"type":"assistant","message":{"role":"assistant","content":"ok"}}`)
	}
	writeTranscript(t, filepath.Join(home, "projects", projectSlug(cwd)), "55555555-5555-5555-5555-555555555555", lines...)

	got, err := Conversations(cwd)
	if err != nil {
		t.Fatalf("conversations: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("found %d conversations, want 1", len(got))
	}
	if got[0].Messages != entries {
		t.Errorf("entry count = %d, want %d", got[0].Messages, entries)
	}
	if got[0].Summary != "first prompt" {
		t.Errorf("summary = %q", got[0].Summary)
	}
}
