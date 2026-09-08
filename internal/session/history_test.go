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

// TestConversationsSkipsEmptyTranscriptsWhenSearching covers a relocated folder
// whose first transcript is an abandoned session: it records no working
// directory, and giving up on the folder there hides every conversation in it.
func TestConversationsSkipsEmptyTranscriptsWhenSearching(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)

	cwd := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, "projects", "an-unexpected-folder-name")

	// Sorts first, and says nothing about where it ran.
	writeTranscript(t, dir, "00000000-0000-0000-0000-000000000000", `{"type":"summary"}`)
	writeTranscript(t, dir, "66666666-6666-6666-6666-666666666666",
		`{"type":"user","cwd":"`+jsonPath(cwd)+`","message":{"role":"user","content":"still findable"}}`)

	got, err := Conversations(cwd)
	if err != nil {
		t.Fatalf("conversations: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("found %d conversations, want 2", len(got))
	}
}

// TestConversationsDoNotLeakBetweenCollidingSlugs covers the lossiness of the
// derived folder name: every character that is not a letter or a digit becomes
// a dash, so neighbouring directories share one. Listing the wrong project's
// conversations would offer the user a resume that opens somebody else's work.
func TestConversationsDoNotLeakBetweenCollidingSlugs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)

	parent := t.TempDir()
	dashed := filepath.Join(parent, "my-app")
	scored := filepath.Join(parent, "my_app")
	for _, d := range []string{dashed, scored} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if projectSlug(dashed) != projectSlug(scored) {
		t.Fatalf("the two directories were expected to collide: %q vs %q", projectSlug(dashed), projectSlug(scored))
	}

	writeTranscript(t, filepath.Join(home, "projects", projectSlug(dashed)), "88888888-8888-8888-8888-888888888888",
		`{"type":"user","cwd":"`+jsonPath(dashed)+`","message":{"role":"user","content":"work on my-app"}}`)

	got, err := Conversations(dashed)
	if err != nil {
		t.Fatalf("conversations: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("the directory that owns the folder found %d conversations, want 1", len(got))
	}

	got, err = Conversations(scored)
	if err != nil {
		t.Fatalf("conversations: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("a colliding directory was shown %d conversations that are not its own: %+v", len(got), got)
	}
}

// TestConversationsOmitEmptyTranscriptsAndOrderStably covers two things a
// history list has to get right: it must not offer a row whose resume Claude
// Code will refuse, and it must not reshuffle itself between refreshes when
// several conversations share a timestamp.
func TestConversationsOmitEmptyTranscriptsAndOrderStably(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)

	cwd := filepath.Join(t.TempDir(), "busy")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, "projects", projectSlug(cwd))

	prompt := `{"type":"user","cwd":"` + jsonPath(cwd) + `","message":{"role":"user","content":"a real prompt"}}`
	together := time.Now().Add(-time.Hour)
	for _, id := range []string{"bbbbbbbb-0000-0000-0000-000000000000", "aaaaaaaa-0000-0000-0000-000000000000", "cccccccc-0000-0000-0000-000000000000"} {
		touch(t, writeTranscript(t, dir, id, prompt), together)
	}

	// Opened, never used: the file is there and holds nothing.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(dir, "dddddddd-0000-0000-0000-000000000000.jsonl")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	touch(t, empty, time.Now())

	got, err := Conversations(cwd)
	if err != nil {
		t.Fatalf("conversations: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("listed %d conversations, want the 3 with something in them: %+v", len(got), got)
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].ID > got[i].ID {
			t.Errorf("conversations sharing a timestamp are not in a stable order: %q before %q", got[i-1].ID, got[i].ID)
		}
	}
}

// TestConversationsCountPastAnEntryTooLargeToParse covers a transcript holding
// a pasted image or some other very large entry. Counting by parsing stops
// dead at a line that will not fit in memory, and reports the conversation as
// having ended there.
func TestConversationsCountPastAnEntryTooLargeToParse(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)

	cwd := filepath.Join(t.TempDir(), "pasted")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}

	huge := `{"type":"user","message":{"role":"user","content":"` + strings.Repeat("x", 9<<20) + `"}}`
	writeTranscript(t, filepath.Join(home, "projects", projectSlug(cwd)), "99999999-9999-9999-9999-999999999999",
		`{"type":"user","cwd":"`+jsonPath(cwd)+`","message":{"role":"user","content":"look at this"}}`,
		huge,
		`{"type":"assistant","message":{"role":"assistant","content":"I see it"}}`)

	got, err := Conversations(cwd)
	if err != nil {
		t.Fatalf("conversations: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("found %d conversations, want 1", len(got))
	}
	if got[0].Messages != 3 {
		t.Errorf("entry count = %d, want 3", got[0].Messages)
	}
	if got[0].Summary != "look at this" {
		t.Errorf("summary = %q", got[0].Summary)
	}
}

// TestCountEntriesCountsAnUnfinishedLastLine covers a transcript being written
// to as it is read: the entry in flight has no line break after it yet.
func TestCountEntriesCountsAnUnfinishedLastLine(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"one\n", 1},
		{"one\ntwo\n", 2},
		{"one\ntwo", 2},
		{"\n\n", 2},
	}
	for _, c := range cases {
		if got := countEntries(strings.NewReader(c.in)); got != c.want {
			t.Errorf("countEntries(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}
