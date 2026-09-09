package session

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// writeTranscript creates a transcript file with the given JSONL lines.
func writeTranscript(t testing.TB, dir, id string, lines ...string) string {
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

func touch(t testing.TB, path string, when time.Time) {
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
		counted := &countingReader{r: strings.NewReader(c.in), last: '\n'}
		drain(counted)
		facts := transcriptFacts{newlines: counted.newlines, partial: counted.last != '\n'}
		if got := facts.entries(); got != c.want {
			t.Errorf("entries(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

// TestConversationsReportAnUnreadableStateDirectory covers the difference
// between "you have never used Claude Code here" and "your Claude Code state
// could not be read". Both used to arrive as an empty list, which leaves
// someone hunting for conversations they know they had.
func TestConversationsReportAnUnreadableStateDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows reports a path through a non-directory as not existing, which is the quiet case")
	}
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)

	// Something that is not a directory where the projects directory belongs.
	if err := os.WriteFile(filepath.Join(home, "projects"), []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := Conversations(t.TempDir())
	if err == nil {
		t.Fatalf("Conversations = %+v with no error; the failure should be reported", got)
	}
	if !strings.Contains(err.Error(), "projects") {
		t.Errorf("error = %q; it should name what could not be read", err)
	}
}

// forgetTranscripts empties the cache of what transcripts said, so that a test
// or a benchmark sees a first listing rather than a refresh.
func forgetTranscripts() {
	transcriptCache.Lock()
	transcriptCache.dirs = make(map[string]cachedFolder)
	transcriptCache.clock = 0
	transcriptCache.Unlock()

	probedFolders.Lock()
	defer probedFolders.Unlock()
	probedFolders.dirs = make(map[string]folderProbe)
}

// historyFixture sets up a Claude state directory and returns a working
// directory whose transcripts live in dir.
func historyFixture(t testing.TB, name string) (cwd, dir string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	forgetTranscripts()
	t.Cleanup(forgetTranscripts)

	cwd = filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	return cwd, filepath.Join(home, "projects", projectSlug(cwd))
}

// TestConversationsReuseWhatTheyAlreadyRead pins the rule the cache turns on:
// a transcript whose size and modification time have not moved is not read
// again. Refreshing the panel otherwise re-reads every byte of every
// transcript in the project, which for a folder of long conversations is
// hundreds of megabytes for an answer that has not changed.
func TestConversationsReuseWhatTheyAlreadyRead(t *testing.T) {
	cwd, dir := historyFixture(t, "cached")

	path := writeTranscript(t, dir, "aaaaaaaa-0000-0000-0000-000000000000",
		`{"type":"user","cwd":"`+jsonPath(cwd)+`","message":{"role":"user","content":"the first prompt"}}`)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	got, err := Conversations(cwd)
	if err != nil {
		t.Fatalf("conversations: %v", err)
	}
	if len(got) != 1 || got[0].Summary != "the first prompt" {
		t.Fatalf("first listing: %+v", got)
	}

	// Replace the contents with something else of exactly the same length and
	// put the modification time back. Nothing the listing looks at has moved,
	// so nothing should be read: the answer is the one already in hand.
	replacement := []byte(strings.Repeat("x", int(before.Size()-1)) + "\n")
	if err := os.WriteFile(path, replacement, 0o600); err != nil {
		t.Fatal(err)
	}
	touch(t, path, before.ModTime())

	got, err = Conversations(cwd)
	if err != nil {
		t.Fatalf("conversations: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("second listing found %d conversations, want 1", len(got))
	}
	if got[0].Summary != "the first prompt" {
		t.Errorf("summary = %q; an unchanged transcript should not be read again", got[0].Summary)
	}
}

// TestConversationsFollowAGrowingTranscript covers the conversation that is
// still being had: the file grows between refreshes, and the count has to
// follow it. Only the new tail is read, so the arithmetic that adds it to what
// was counted before has to be right -- including when the last entry was
// still being written when the previous listing looked.
func TestConversationsFollowAGrowingTranscript(t *testing.T) {
	cwd, dir := historyFixture(t, "growing")

	path := writeTranscript(t, dir, "bbbbbbbb-0000-0000-0000-000000000000",
		`{"type":"user","cwd":"`+jsonPath(cwd)+`","message":{"role":"user","content":"keep going"}}`)

	// An entry half written: no line break after it yet.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"type":"assistant","message":{"role":"ass`); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	got, err := Conversations(cwd)
	if err != nil {
		t.Fatalf("conversations: %v", err)
	}
	if len(got) != 1 || got[0].Messages != 2 {
		t.Fatalf("entry count = %+v, want 2 entries", got)
	}

	// The rest of that entry arrives, and two more after it.
	f, err = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("istant\",\"content\":\"ok\"}}\n" +
		`{"type":"user","message":{"role":"user","content":"more"}}` + "\n" +
		`{"type":"assistant","message":{"role":"assistant","content":"done"}}` + "\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	got, err = Conversations(cwd)
	if err != nil {
		t.Fatalf("conversations: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("found %d conversations, want 1", len(got))
	}
	if got[0].Messages != 4 {
		t.Errorf("entry count = %d, want 4", got[0].Messages)
	}
	if got[0].Summary != "keep going" {
		t.Errorf("summary = %q, want the prompt the transcript opens with", got[0].Summary)
	}
}

// TestConversationsRereadAReplacedTranscript covers the other side of the
// cache: a file that did not simply grow is no longer the file that was read,
// and has to be read again from the top.
func TestConversationsRereadAReplacedTranscript(t *testing.T) {
	cwd, dir := historyFixture(t, "replaced")

	writeTranscript(t, dir, "cccccccc-0000-0000-0000-000000000000",
		`{"type":"user","cwd":"`+jsonPath(cwd)+`","message":{"role":"user","content":"the original prompt"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":"one"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":"two"}}`)

	if _, err := Conversations(cwd); err != nil {
		t.Fatalf("conversations: %v", err)
	}

	writeTranscript(t, dir, "cccccccc-0000-0000-0000-000000000000",
		`{"type":"user","cwd":"`+jsonPath(cwd)+`","message":{"role":"user","content":"a shorter one"}}`)

	got, err := Conversations(cwd)
	if err != nil {
		t.Fatalf("conversations: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("found %d conversations, want 1", len(got))
	}
	if got[0].Summary != "a shorter one" {
		t.Errorf("summary = %q, want the replacement read afresh", got[0].Summary)
	}
	if got[0].Messages != 1 {
		t.Errorf("entry count = %d, want 1", got[0].Messages)
	}
}

// benchTranscripts fills a project folder with plausible transcripts: a
// prompt, then enough exchanges to make a file worth not reading twice.
func benchTranscripts(b *testing.B, count, kb int) string {
	b.Helper()
	cwd, dir := historyFixture(b, "bench")
	filler := `{"type":"assistant","message":{"role":"assistant","content":"` + strings.Repeat("x", 500) + `"}}`
	lines := []string{
		`{"type":"user","cwd":"` + jsonPath(cwd) + `","message":{"role":"user","content":"benchmark me"}}`,
		`{"type":"ai-title","aiTitle":"what this conversation turned out to be about"}`,
	}
	for len(lines)*len(filler) < kb<<10 {
		lines = append(lines, filler)
	}
	for i := 0; i < count; i++ {
		writeTranscript(b, dir, fmt.Sprintf("%08d-0000-0000-0000-000000000000", i), lines...)
	}
	return cwd
}

// BenchmarkConversationsFirstListing is the cost of a folder nothing is known
// about yet: every transcript is opened, parsed at the top and read to the end.
func BenchmarkConversationsFirstListing(b *testing.B) {
	cwd := benchTranscripts(b, 100, 512)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		forgetTranscripts()
		if _, err := Conversations(cwd); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkConversationsRefresh is the cost of the same folder when the panel
// is opened again and nothing has changed, which is what most listings are.
func BenchmarkConversationsRefresh(b *testing.B) {
	cwd := benchTranscripts(b, 100, 512)
	if _, err := Conversations(cwd); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Conversations(cwd); err != nil {
			b.Fatal(err)
		}
	}
}

// TestConversationsIgnoreAFileWhereTheFolderBelongs covers a project whose
// derived folder name is taken by something that is not a folder. The listing
// has to fall back to searching rather than report the project as unreadable.
func TestConversationsIgnoreAFileWhereTheFolderBelongs(t *testing.T) {
	cwd, dir := historyFixture(t, "blocked")

	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir, []byte("not a folder"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeTranscript(t, filepath.Join(filepath.Dir(dir), "somewhere-else"), "eeeeeeee-0000-0000-0000-000000000000",
		`{"type":"user","cwd":"`+jsonPath(cwd)+`","message":{"role":"user","content":"found anyway"}}`)

	got, err := Conversations(cwd)
	if err != nil {
		t.Fatalf("conversations: %v", err)
	}
	if len(got) != 1 || got[0].Summary != "found anyway" {
		t.Fatalf("expected the folder to be found by search, got %+v (err %v)", got, err)
	}
}

// TestConversationsSummariseAfterAnEntryTooLargeToHold covers a conversation
// that opens by pasting something enormous: an image, or a file dropped in
// whole. It arrives as a single entry of many megabytes, and the prompt that
// goes with it is the entry underneath. Stopping at the paste leaves the row
// claiming the conversation has no prompt at all.
func TestConversationsSummariseAfterAnEntryTooLargeToHold(t *testing.T) {
	cwd, dir := historyFixture(t, "pasted-first")

	huge := `{"type":"user","message":{"role":"user","content":"` + strings.Repeat("x", 9<<20) + `"}}`
	writeTranscript(t, dir, "77777777-7777-7777-7777-777777777777",
		huge,
		`{"type":"user","cwd":"`+jsonPath(cwd)+`","message":{"role":"user","content":"what is in this image"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":"a cat"}}`)

	got, err := Conversations(cwd)
	if err != nil {
		t.Fatalf("conversations: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("found %d conversations, want 1", len(got))
	}
	if got[0].Summary != "what is in this image" {
		t.Errorf("summary = %q; the prompt after the paste should still be found", got[0].Summary)
	}
	if got[0].Messages != 3 {
		t.Errorf("entry count = %d, want 3", got[0].Messages)
	}
}

// TestTranscriptReaderWalksEveryEntry pins how a transcript is walked: one
// entry per line, the last one counting even while the file is still being
// written, and an entry too large to hold stepped over without ending the
// walk or being held in memory.
func TestTranscriptReaderWalksEveryEntry(t *testing.T) {
	oversized := strings.Repeat("x", 8<<20+1)
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", nil},
		{"one entry", "a\n", []string{"a"}},
		{"unfinished last entry", "a\nbc", []string{"a", "bc"}},
		{"blank lines are entries", "a\n\nb\n", []string{"a", "", "b"}},
		{"windows line endings", "a\r\nb\r\n", []string{"a", "b"}},
		{"oversized entry stepped over", "a\n" + oversized + "\nb\n", []string{"a", "", "b"}},
		{"oversized entry last", "a\n" + oversized, []string{"a", ""}},
		{"two oversized entries", oversized + "\n" + oversized + "\nb\n", []string{"", "", "b"}},
	}
	for _, c := range cases {
		var got []string
		r := newTranscriptReader(strings.NewReader(c.in))
		for i := 0; i < 10; i++ {
			raw, ok := r.next()
			if !ok {
				break
			}
			got = append(got, strings.TrimRight(string(raw), "\r\n"))
		}
		if len(got) != len(c.want) {
			t.Errorf("%s: read %q, want %q", c.name, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s: entry %d = %q, want %q", c.name, i, got[i], c.want[i])
			}
		}
	}
}

// TestConversationsSplitAFolderTwoProjectsShare covers the other half of the
// derived name being lossy. "my-app" and "my_app" do not merely collide in
// theory: Claude Code puts both directories' transcripts in the one folder,
// so the folder holds two projects' conversations at once. Handing the whole
// folder to whichever project asked offers a resume that opens somebody
// else's work; refusing the folder to the project that does not own its first
// transcript hides conversations that are sitting right there.
func TestConversationsSplitAFolderTwoProjectsShare(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	forgetTranscripts()
	t.Cleanup(forgetTranscripts)

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

	shared := filepath.Join(home, "projects", projectSlug(dashed))
	writeTranscript(t, shared, "aaaaaaaa-1111-1111-1111-111111111111",
		`{"type":"user","cwd":"`+jsonPath(dashed)+`","message":{"role":"user","content":"work on my-app"}}`)
	writeTranscript(t, shared, "bbbbbbbb-1111-1111-1111-111111111111",
		`{"type":"user","cwd":"`+jsonPath(scored)+`","message":{"role":"user","content":"work on my_app"}}`)
	// An abandoned session says nothing about where it ran, and belongs to
	// whoever asks: it is contentless either way.
	writeTranscript(t, shared, "cccccccc-1111-1111-1111-111111111111", `{"type":"summary"}`)

	for _, c := range []struct{ cwd, want string }{
		{dashed, "work on my-app"},
		{scored, "work on my_app"},
	} {
		got, err := Conversations(c.cwd)
		if err != nil {
			t.Fatalf("conversations in %s: %v", c.cwd, err)
		}
		if len(got) != 2 {
			t.Fatalf("%s listed %d conversations, want its own and the abandoned one: %+v", c.cwd, len(got), got)
		}
		var summaries []string
		for _, conv := range got {
			summaries = append(summaries, conv.Summary)
		}
		if summaries[0] != c.want && summaries[1] != c.want {
			t.Errorf("%s was not shown its own conversation: %q", c.cwd, summaries)
		}
		for _, s := range summaries {
			if s != c.want && s != "(no prompt recorded)" {
				t.Errorf("%s was shown a neighbour's conversation: %q", c.cwd, s)
			}
		}
	}
}

// TestConversationsDescribeEveryTranscriptCorrectly covers a folder large
// enough to be read by several readers at once: every transcript has to come
// back, and each row has to carry its own transcript's prompt and its own
// count rather than a neighbour's.
func TestConversationsDescribeEveryTranscriptCorrectly(t *testing.T) {
	cwd, dir := historyFixture(t, "many")

	const count = 60
	for i := 0; i < count; i++ {
		lines := []string{fmt.Sprintf(`{"type":"user","cwd":"%s","message":{"role":"user","content":"prompt number %d"}}`, jsonPath(cwd), i)}
		for j := 0; j < i; j++ {
			lines = append(lines, `{"type":"assistant","message":{"role":"assistant","content":"ok"}}`)
		}
		writeTranscript(t, dir, fmt.Sprintf("%08d-0000-0000-0000-000000000000", i), lines...)
	}

	for _, pass := range []string{"first listing", "refresh"} {
		got, err := Conversations(cwd)
		if err != nil {
			t.Fatalf("%s: %v", pass, err)
		}
		if len(got) != count {
			t.Fatalf("%s listed %d conversations, want %d", pass, len(got), count)
		}
		seen := map[string]bool{}
		for _, c := range got {
			var i int
			if _, err := fmt.Sscanf(c.ID, "%08d-0000-0000-0000-000000000000", &i); err != nil {
				t.Fatalf("%s: unexpected id %q", pass, c.ID)
			}
			if seen[c.ID] {
				t.Errorf("%s: %s listed twice", pass, c.ID)
			}
			seen[c.ID] = true
			if want := fmt.Sprintf("prompt number %d", i); c.Summary != want {
				t.Errorf("%s: %s has summary %q, want %q", pass, c.ID, c.Summary, want)
			}
			if want := i + 1; c.Messages != want {
				t.Errorf("%s: %s has %d entries, want %d", pass, c.ID, c.Messages, want)
			}
		}
	}
}

// TestConversationsFindAFolderWhoseTranscriptsOpenWithBookkeeping covers what
// a transcript actually starts with. Before the conversation there are
// entries Claude Code writes for itself -- the mode, the permission mode, a
// bridge record, a compaction summary -- and none of them names a directory,
// so the entry that does can be a long way down. A folder Claude named
// differently from the name derived here is found only by what its
// transcripts record, and giving up too early hides the whole project.
func TestConversationsFindAFolderWhoseTranscriptsOpenWithBookkeeping(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	forgetTranscripts()
	t.Cleanup(forgetTranscripts)

	cwd := filepath.Join(t.TempDir(), "renamed")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}

	var lines []string
	for i := 0; i < 25; i++ {
		lines = append(lines, `{"type":"mode","mode":"normal"}`)
	}
	lines = append(lines,
		`{"type":"user","cwd":"`+jsonPath(cwd)+`","message":{"role":"user","content":"buried under the preamble"}}`)

	// A folder whose name is not the one derived from the path, so the only
	// way to it is what the transcript records.
	writeTranscript(t, filepath.Join(home, "projects", "a-name-of-its-own"),
		"eeeeeeee-1111-1111-1111-111111111111", lines...)

	got, err := Conversations(cwd)
	if err != nil {
		t.Fatalf("conversations: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("found %d conversations, want 1", len(got))
	}
	if got[0].Summary != "buried under the preamble" {
		t.Errorf("summary = %q", got[0].Summary)
	}
}

// TestFolderRecordsAreKeptUntilTheFolderChanges covers the search for a
// project folder Claude named differently from the name derived here. It is
// the projects with no folder of their own -- a new worktree, a project never
// used here -- that walk every folder and open transcripts in each, and they
// do it on every refresh of a panel that will be empty either way. What a
// folder's transcripts recorded cannot change while the folder holds the
// transcripts it held, so the answer is kept until it gains or loses one.
func TestFolderRecordsAreKeptUntilTheFolderChanges(t *testing.T) {
	forgetTranscripts()
	t.Cleanup(forgetTranscripts)

	dir := filepath.Join(t.TempDir(), "a-project-folder")
	writeTranscript(t, dir, "aaaaaaaa-2222-2222-2222-222222222222",
		`{"type":"user","cwd":"`+jsonPath(`C:\first`)+`","message":{"role":"user","content":"one"}}`)

	got := folderRecords(dir)
	if len(got) != 1 || !sameDir(got[0], `C:\first`) {
		t.Fatalf("probed %q, want the directory the transcript records", got)
	}

	// The same transcript, saying something else. Nothing about the folder
	// has changed, so nothing should be opened again.
	writeTranscript(t, dir, "aaaaaaaa-2222-2222-2222-222222222222",
		`{"type":"user","cwd":"`+jsonPath(`C:\second`)+`","message":{"role":"user","content":"two"}}`)
	if got := folderRecords(dir); len(got) != 1 || !sameDir(got[0], `C:\first`) {
		t.Errorf("probed %q again; an unchanged folder should not be opened twice", got)
	}

	// A transcript appears, which is the one thing that can change the answer.
	writeTranscript(t, dir, "bbbbbbbb-2222-2222-2222-222222222222",
		`{"type":"user","cwd":"`+jsonPath(`C:\third`)+`","message":{"role":"user","content":"three"}}`)
	got = folderRecords(dir)
	if len(got) != 2 || !sameDir(got[0], `C:\second`) || !sameDir(got[1], `C:\third`) {
		t.Errorf("probed %q; a folder that has gained a transcript should be read again", got)
	}
}

// TestConversationsSeeAFolderThatFillsUp covers the other side of that: the
// project with no history is the one that walks every folder, so its empty
// answer is the one most worth keeping -- and the one that must not outlive
// the first conversation the project has.
func TestConversationsSeeAFolderThatFillsUp(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	forgetTranscripts()
	t.Cleanup(forgetTranscripts)

	cwd := filepath.Join(t.TempDir(), "brand-new")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	// A folder that exists and holds nothing of ours, so the search comes
	// back empty and is remembered as having done so.
	dir := filepath.Join(home, "projects", "a-name-of-its-own")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := Conversations(cwd)
	if err != nil {
		t.Fatalf("conversations: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("found %d conversations for a project with no history", len(got))
	}

	// The first session in the project records itself there.
	writeTranscript(t, dir, "bbbbbbbb-2222-2222-2222-222222222222",
		`{"type":"user","cwd":"`+jsonPath(cwd)+`","message":{"role":"user","content":"the first one"}}`)

	got, err = Conversations(cwd)
	if err != nil {
		t.Fatalf("conversations: %v", err)
	}
	if len(got) != 1 || got[0].Summary != "the first one" {
		t.Fatalf("the conversation that appeared was not found: %+v", got)
	}
}

// TestConversationsFallBackToTheNameClaudeGaveIt covers the rows that could
// say nothing about themselves. A session that was opened and never prompted
// leaves a transcript holding only the name Claude Code gave it, and a row
// reading "(no prompt recorded)" next to a date is not something anyone can
// pick out of a list. The name is right there in the file.
func TestConversationsFallBackToTheNameClaudeGaveIt(t *testing.T) {
	cwd, dir := historyFixture(t, "named")

	// Opened, named, never used.
	writeTranscript(t, dir, "aaaaaaaa-3333-3333-3333-333333333333",
		`{"type":"ai-title","aiTitle":"login-success","sessionId":"aaaaaaaa-3333-3333-3333-333333333333"}`,
		`{"type":"agent-name","agentName":"login-success"}`)

	// Named, then used: what the person typed wins, and a name Claude
	// settled on later does not overwrite it.
	writeTranscript(t, dir, "bbbbbbbb-3333-3333-3333-333333333333",
		`{"type":"ai-title","aiTitle":"Some generated title"}`,
		`{"type":"user","cwd":"`+jsonPath(cwd)+`","message":{"role":"user","content":"what I actually asked"}}`,
		`{"type":"ai-title","aiTitle":"A better generated title"}`)

	got, err := Conversations(cwd)
	if err != nil {
		t.Fatalf("conversations: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("found %d conversations, want 2", len(got))
	}
	byID := map[string]string{}
	for _, c := range got {
		byID[c.ID] = c.Summary
	}
	if got := byID["aaaaaaaa-3333-3333-3333-333333333333"]; got != "login-success" {
		t.Errorf("summary = %q, want the name Claude Code gave the conversation", got)
	}
	if got := byID["bbbbbbbb-3333-3333-3333-333333333333"]; got != "what I actually asked" {
		t.Errorf("summary = %q, want the prompt rather than the generated name", got)
	}
}

// TestDescribeTranscriptCountsOnlyWhatItWasToldAbout covers the transcript of
// an agent that is working while the panel is being drawn. The listing reads
// the directory, then opens the files, and the transcript grows in between:
// entries arrive that the size the listing recorded does not cover. Counting
// to the end of the file counts them, and the next refresh -- which picks up
// from that recorded size -- counts them again, so an agent that is busy has
// its entry count drift further out with every refresh.
func TestDescribeTranscriptCountsOnlyWhatItWasToldAbout(t *testing.T) {
	dir := t.TempDir()
	entry := `{"type":"assistant","message":{"role":"assistant","content":"ok"}}`
	path := writeTranscript(t, dir, "aaaaaaaa-4444-4444-4444-444444444444",
		`{"type":"user","cwd":"`+jsonPath(dir)+`","message":{"role":"user","content":"go"}}`,
		entry, entry)

	// What the directory listing knew, before the agent wrote anything more.
	listed, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(entry + "\n" + entry + "\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	facts, ok := describeTranscript(path, listed, transcriptFacts{})
	if !ok {
		t.Fatal("the transcript should have been read")
	}
	if facts.entries() != 3 {
		t.Errorf("counted %d entries, want the 3 the listing knew about", facts.entries())
	}

	// The next refresh sees the file as it now is and carries on from there.
	grown, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := describeTranscript(path, grown, facts); got.entries() != 5 {
		t.Errorf("counted %d entries after the transcript grew, want 5", got.entries())
	}
}

// TestConversationsForgetATranscriptTheyCouldNotRead covers the gap between
// reading the folder and reading the files in it. Claude Code deletes old
// transcripts, and a file can go -- or be held by something else -- in
// between. Remembering that as a conversation with nothing in it leaves the
// row saying nothing for as long as the file's size and date stay put, which
// for a transcript nobody is writing to is for good.
func TestConversationsForgetATranscriptTheyCouldNotRead(t *testing.T) {
	cwd, dir := historyFixture(t, "vanishing")

	line := `{"type":"user","cwd":"` + jsonPath(cwd) + `","message":{"role":"user","content":"still here"}}`
	path := writeTranscript(t, dir, "aaaaaaaa-5555-5555-5555-555555555555", line)
	kept, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	listed, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Gone between the folder being read and the file being opened.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	got := conversationsIn(dir, entries, cwd, func(recorded string) bool {
		return ours(dir, recorded, cwd)
	})
	if len(got) != 1 {
		t.Fatalf("listed %d conversations; the folder held one when it was read", len(got))
	}
	if n := len(cachedFacts(dir)); n != 0 {
		t.Errorf("remembered %d transcripts; one that could not be read has nothing to remember", n)
	}

	// Back, exactly as it was: same contents, same size, same date.
	if err := os.WriteFile(path, kept, 0o600); err != nil {
		t.Fatal(err)
	}
	touch(t, path, listed.ModTime())

	got, err = Conversations(cwd)
	if err != nil {
		t.Fatalf("conversations: %v", err)
	}
	if len(got) != 1 || got[0].Summary != "still here" {
		t.Fatalf("the transcript was not read again: %+v", got)
	}
}

// TestConversationsFromSeveralWindowsAtOnce covers what the panel is: a list
// several windows ask for at the same time, off the goroutine that owns the
// workspace, while the agents whose conversations it lists are writing to
// them. Everything remembered between listings -- what each transcript said,
// and which directory each project folder belongs to -- is shared across all
// of that, and a map written from two goroutines at once takes the process
// down with it.
func TestConversationsFromSeveralWindowsAtOnce(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	forgetTranscripts()
	t.Cleanup(forgetTranscripts)

	// Two projects, so the listings write different folders as well as the
	// same one, and one folder Claude named differently so the search for it
	// runs too.
	parent := t.TempDir()
	var cwds []string
	for _, name := range []string{"one", "two"} {
		cwd := filepath.Join(parent, name)
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		cwds = append(cwds, cwd)
	}
	writeTranscript(t, filepath.Join(home, "projects", projectSlug(cwds[0])), "aaaaaaaa-6666-6666-6666-666666666666",
		`{"type":"user","cwd":"`+jsonPath(cwds[0])+`","message":{"role":"user","content":"the first project"}}`)
	busy := writeTranscript(t, filepath.Join(home, "projects", "a-name-of-its-own"), "bbbbbbbb-6666-6666-6666-666666666666",
		`{"type":"user","cwd":"`+jsonPath(cwds[1])+`","message":{"role":"user","content":"the second project"}}`)

	// An agent writing to its transcript while the windows read it.
	stop := make(chan struct{})
	var writer sync.WaitGroup
	writer.Add(1)
	go func() {
		defer writer.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			f, err := os.OpenFile(busy, os.O_APPEND|os.O_WRONLY, 0o600)
			if err != nil {
				continue
			}
			f.WriteString(`{"type":"assistant","message":{"role":"assistant","content":"working"}}` + "\n")
			f.Close()
		}
	}()

	var windows sync.WaitGroup
	failed := make(chan string, 16)
	for w := 0; w < 8; w++ {
		windows.Add(1)
		go func(w int) {
			defer windows.Done()
			cwd := cwds[w%len(cwds)]
			want := "the first project"
			if w%len(cwds) == 1 {
				want = "the second project"
			}
			for i := 0; i < 25; i++ {
				got, err := Conversations(cwd)
				if err != nil {
					failed <- fmt.Sprintf("conversations: %v", err)
					return
				}
				if len(got) != 1 || got[0].Summary != want {
					failed <- fmt.Sprintf("listing %d of %s: %+v", i, cwd, got)
					return
				}
			}
		}(w)
	}
	windows.Wait()
	close(stop)
	writer.Wait()

	select {
	case msg := <-failed:
		t.Fatal(msg)
	default:
	}
}

// TestCountingReaderCountsWhatThePromptScanAlreadyRead covers the two jobs a
// read of a transcript does at once: the opening entries are parsed for a
// prompt, and every entry is counted. The counting has to include the ones
// the prompt scan pulled in -- including whatever it buffered past the entry
// it stopped at -- and must not count any of them twice.
func TestCountingReaderCountsWhatThePromptScanAlreadyRead(t *testing.T) {
	var lines []string
	lines = append(lines, `{"type":"user","cwd":"C:\\somewhere","message":{"role":"user","content":"the prompt"}}`)
	for len(lines) < 500 {
		lines = append(lines, `{"type":"assistant","message":{"role":"assistant","content":"`+strings.Repeat("x", 400)+`"}}`)
	}
	body := strings.Join(lines, "\n") + "\n"

	counted := &countingReader{r: strings.NewReader(body), last: '\n'}
	prompt, _, cwd := openingPrompt(counted)
	drain(counted)

	if prompt != "the prompt" {
		t.Errorf("prompt = %q", prompt)
	}
	if cwd != `C:\somewhere` {
		t.Errorf("cwd = %q", cwd)
	}
	if counted.newlines != len(lines) {
		t.Errorf("counted %d entries, want %d", counted.newlines, len(lines))
	}
	if counted.last != '\n' {
		t.Error("the transcript ends with a line break and should not be read as ending mid-entry")
	}
}

// TestSameDirFollowsWhetherTheFilesystemCaresAboutCase covers the comparison
// everything else here rests on: which transcripts are this directory's, and
// which project folder belongs to it. Case has to be seen through where the
// filesystem sees through it, because Claude Code recorded whichever spelling
// the session was started with -- and must not be where it does not, because
// on Linux the neighbouring directory really is a different project.
func TestSameDirFollowsWhetherTheFilesystemCaresAboutCase(t *testing.T) {
	was := pathsIgnoreCase
	t.Cleanup(func() { pathsIgnoreCase = was })

	pathsIgnoreCase = true
	if !sameDir(`C:\Repos\App`, `c:\repos\app`) {
		t.Error("a Windows path spelled differently was taken for another directory")
	}

	pathsIgnoreCase = false
	if sameDir("/home/j/src/App", "/home/j/src/app") {
		t.Error("two directories on a case-sensitive filesystem were taken for one")
	}
	if !sameDir("/home/j/src/app/", "/home/j/src/app") {
		t.Error("the same directory written with a trailing separator was not recognised")
	}
}

// TestConversationsOnACaseSensitiveFilesystemStayApart is the same thing
// through the front door: the folder search matches a project folder by the
// directory its transcripts record, and on Linux a neighbour whose name
// differs only in case is not that directory.
func TestConversationsOnACaseSensitiveFilesystemStayApart(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	forgetTranscripts()
	t.Cleanup(forgetTranscripts)

	was := pathsIgnoreCase
	pathsIgnoreCase = false
	t.Cleanup(func() { pathsIgnoreCase = was })

	// The directory that is asked about, and the neighbour that recorded the
	// conversations, spelled the way Linux would tell them apart.
	parent := t.TempDir()
	asked := filepath.Join(parent, "app")
	neighbour := filepath.Join(parent, "App")
	if err := os.MkdirAll(asked, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTranscript(t, filepath.Join(home, "projects", "a-name-of-its-own"), "aaaaaaaa-7777-7777-7777-777777777777",
		`{"type":"user","cwd":"`+jsonPath(neighbour)+`","message":{"role":"user","content":"the neighbour's work"}}`)

	got, err := Conversations(asked)
	if err != nil {
		t.Fatalf("conversations: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("listed %d of a differently spelled directory's conversations: %+v", len(got), got)
	}
}

// TestConversationsPickUpAPromptWrittenAfterTheFirstLook covers the panel
// with several agents just spawned into it. A pane that has been opened but
// not yet prompted has a transcript holding nothing but Claude Code's own
// bookkeeping, and the row for it can say nothing. The prompt arrives a
// moment later -- and because a transcript that has only grown is trusted to
// open the way it did, that row went on saying nothing for as long as the
// application ran.
func TestConversationsPickUpAPromptWrittenAfterTheFirstLook(t *testing.T) {
	cwd, dir := historyFixture(t, "just-spawned")

	// Two panes opened: one Claude Code has already named, one it has not.
	nameless := writeTranscript(t, dir, "aaaaaaaa-8888-8888-8888-888888888888",
		`{"type":"mode","mode":"normal"}`)
	named := writeTranscript(t, dir, "bbbbbbbb-8888-8888-8888-888888888888",
		`{"type":"ai-title","aiTitle":"a generated name"}`)

	got, err := Conversations(cwd)
	if err != nil {
		t.Fatalf("conversations: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("found %d conversations, want 2", len(got))
	}

	// Both are prompted, which is what a pane is for.
	for path, prompt := range map[string]string{
		nameless: "what I typed into the first pane",
		named:    "what I typed into the second pane",
	} {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteString(`{"type":"user","cwd":"` + jsonPath(cwd) + `","message":{"role":"user","content":"` + prompt + `"}}` + "\n"); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}

	got, err = Conversations(cwd)
	if err != nil {
		t.Fatalf("conversations: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("found %d conversations, want 2", len(got))
	}
	summaries := map[string]string{}
	for _, c := range got {
		summaries[c.ID] = c.Summary
		if c.Messages != 2 {
			t.Errorf("%s has %d entries, want 2", c.ID, c.Messages)
		}
	}
	if got := summaries["aaaaaaaa-8888-8888-8888-888888888888"]; got != "what I typed into the first pane" {
		t.Errorf("summary = %q, want the prompt that arrived after the first listing", got)
	}
	if got := summaries["bbbbbbbb-8888-8888-8888-888888888888"]; got != "what I typed into the second pane" {
		t.Errorf("summary = %q, want the prompt to replace the generated name", got)
	}
}

// TestConversationsOfferAConversationThatMovedIntoAWorktree covers what this
// application does to a session: it is started in a project and told to work
// in a worktree of it, and Claude Code follows. The transcript is stored
// under the worktree it ended in, while its entries record the directory it
// began in. It is a real conversation -- an hour of somebody's work -- and
// until it was looked for under the worktrees, the project it was done for
// could not offer it, and deleting the worktree took it out of reach for
// good.
func TestConversationsOfferAConversationThatMovedIntoAWorktree(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	forgetTranscripts()
	t.Cleanup(forgetTranscripts)

	parent := t.TempDir()
	project := filepath.Join(parent, "flipping")
	worktree := filepath.Join(project, ".claude-worktrees", "password-reset")
	// A neighbour whose folder name begins the same way but is nothing to do
	// with this project.
	sibling := filepath.Join(parent, "flipping-old")
	for _, d := range []string{worktree, sibling} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	underWorktree := filepath.Join(home, "projects", projectSlug(worktree))
	// Stored under the worktree, recording the project it was started in.
	writeTranscript(t, underWorktree, "aaaaaaaa-9999-9999-9999-999999999999",
		`{"type":"user","cwd":"`+jsonPath(project)+`","message":{"role":"user","content":"in a worktree, implement password reset"}}`)
	// The worktree's own work, and a session abandoned there: neither is the
	// project's.
	writeTranscript(t, underWorktree, "cccccccc-9999-9999-9999-999999999999",
		`{"type":"user","cwd":"`+jsonPath(worktree)+`","message":{"role":"user","content":"the worktree's own work"}}`)
	writeTranscript(t, underWorktree, "dddddddd-9999-9999-9999-999999999999", `{"type":"summary"}`)
	// The project's own folder.
	writeTranscript(t, filepath.Join(home, "projects", projectSlug(project)), "bbbbbbbb-9999-9999-9999-999999999999",
		`{"type":"user","cwd":"`+jsonPath(project)+`","message":{"role":"user","content":"work in the project itself"}}`)
	// The neighbour's, under a folder whose name starts with the project's.
	writeTranscript(t, filepath.Join(home, "projects", projectSlug(sibling)), "eeeeeeee-9999-9999-9999-999999999999",
		`{"type":"user","cwd":"`+jsonPath(sibling)+`","message":{"role":"user","content":"the neighbour's work"}}`)

	got, err := Conversations(project)
	if err != nil {
		t.Fatalf("conversations: %v", err)
	}
	summaries := map[string]bool{}
	for _, c := range got {
		summaries[c.Summary] = true
	}
	if !summaries["work in the project itself"] || !summaries["in a worktree, implement password reset"] {
		t.Errorf("the project was not offered its own conversations: %+v", got)
	}
	if len(got) != 2 {
		t.Errorf("the project listed %d conversations, want its own two: %+v", len(got), got)
	}

	// The worktree still offers what is stored under it, its own included.
	got, err = Conversations(worktree)
	if err != nil {
		t.Fatalf("conversations: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("the worktree listed %d conversations, want the three stored under it: %+v", len(got), got)
	}
}

// TestConversationsListAConversationCopiedIntoTwoFoldersOnce covers resuming
// a conversation somewhere else: Claude Code copies its transcript under that
// directory's folder and leaves the first behind, so the same conversation is
// stored twice. It is one conversation and belongs on one row, showing what
// the copy that is still being written to says.
func TestConversationsListAConversationCopiedIntoTwoFoldersOnce(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	forgetTranscripts()
	t.Cleanup(forgetTranscripts)

	project := filepath.Join(t.TempDir(), "twice")
	worktree := filepath.Join(project, "wt")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}

	const id = "ffffffff-9999-9999-9999-999999999999"
	line := `{"type":"user","cwd":"` + jsonPath(project) + `","message":{"role":"user","content":"the one conversation"}}`
	stale := writeTranscript(t, filepath.Join(home, "projects", projectSlug(worktree)), id, line)
	live := writeTranscript(t, filepath.Join(home, "projects", projectSlug(project)), id, line, line, line)
	touch(t, stale, time.Now().Add(-2*time.Hour))
	touch(t, live, time.Now())

	got, err := Conversations(project)
	if err != nil {
		t.Fatalf("conversations: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("listed %d rows for one conversation: %+v", len(got), got)
	}
	if got[0].Messages != 3 {
		t.Errorf("the row has %d entries; the copy still being written to has 3", got[0].Messages)
	}
}

// TestConversationsSkipATranscriptClaudeOrphaned covers the files Claude Code
// leaves in a project folder that are not conversations anyone can go back
// to. A transcript it decides is orphaned is renamed with a timestamp and a
// hash appended, and three of those are sitting in the folders on this
// machine. Listed, they look like conversations -- they have a name and a
// date and a size -- and resuming one fails, because the id in the row is
// not a session id.
func TestConversationsSkipATranscriptClaudeOrphaned(t *testing.T) {
	cwd, dir := historyFixture(t, "orphans")

	line := `{"type":"user","cwd":"` + jsonPath(cwd) + `","message":{"role":"user","content":"a real conversation"}}`
	writeTranscript(t, dir, "aaaaaaaa-0101-0101-0101-010101010101", line)
	writeTranscript(t, dir, "aaaaaaaa-0101-0101-0101-010101010101.orphaned-1788557454976-69dd3a08", line)

	got, err := Conversations(cwd)
	if err != nil {
		t.Fatalf("conversations: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("listed %d conversations, want the one that can be resumed: %+v", len(got), got)
	}
	if got[0].ID != "aaaaaaaa-0101-0101-0101-010101010101" {
		t.Errorf("id = %q, want the session id", got[0].ID)
	}
}

// TestTranscriptIDReadsTheSessionOutOfAFileName pins what counts as a
// transcript in a project folder.
func TestTranscriptIDReadsTheSessionOutOfAFileName(t *testing.T) {
	cases := map[string]string{
		"aaaaaaaa-0101-0101-0101-010101010101.jsonl":                                 "aaaaaaaa-0101-0101-0101-010101010101",
		"aaaaaaaa-0101-0101-0101-010101010101.orphaned-1788557454976-69dd3a08.jsonl": "",
		"aaaaaaaa-0101-0101-0101-010101010101.jsonl.bak":                             "",
		"aaaaaaaa-0101-0101-0101-010101010101":                                       "",
		".jsonl":                                                                     "",
		"memory":                                                                     "",
		"":                                                                           "",
	}
	for name, want := range cases {
		if got := transcriptID(name); got != want {
			t.Errorf("transcriptID(%q) = %q, want %q", name, got, want)
		}
	}
}

// TestConversationsTellApartTheAgentsSentTheSamePrompt covers a fan-out: the
// same instruction handed to several agents at once, which is what this
// application is for. Every one of their transcripts opens with the same
// words, so the panel lists rows identical down to the last character and
// nothing in them says which agent is which. Claude Code names each
// conversation after what it turned out to be about, and those names differ.
func TestConversationsTellApartTheAgentsSentTheSamePrompt(t *testing.T) {
	cwd, dir := historyFixture(t, "fanout")

	fanout := `{"type":"user","cwd":"` + jsonPath(cwd) + `","message":{"role":"user","content":"You are one of 10 agents working in parallel"}}`
	writeTranscript(t, dir, "aaaaaaaa-1212-1212-1212-121212121212",
		`{"type":"ai-title","aiTitle":"store layer fixes"}`, fanout)
	writeTranscript(t, dir, "bbbbbbbb-1212-1212-1212-121212121212",
		`{"type":"ai-title","aiTitle":"layout tree bugs"}`, fanout)
	// One that was asked something of its own, and named as well.
	writeTranscript(t, dir, "cccccccc-1212-1212-1212-121212121212",
		`{"type":"ai-title","aiTitle":"a name nobody needs"}`,
		`{"type":"user","cwd":"`+jsonPath(cwd)+`","message":{"role":"user","content":"tidy the imports"}}`)

	got, err := Conversations(cwd)
	if err != nil {
		t.Fatalf("conversations: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("found %d conversations, want 3", len(got))
	}
	summaries := map[string]string{}
	for _, c := range got {
		summaries[c.ID] = c.Summary
	}
	if got := summaries["aaaaaaaa-1212-1212-1212-121212121212"]; got != "store layer fixes" {
		t.Errorf("summary = %q, want the name that tells this agent from the others", got)
	}
	if got := summaries["bbbbbbbb-1212-1212-1212-121212121212"]; got != "layout tree bugs" {
		t.Errorf("summary = %q, want the name that tells this agent from the others", got)
	}
	if got := summaries["cccccccc-1212-1212-1212-121212121212"]; got != "tidy the imports" {
		t.Errorf("summary = %q; a conversation whose prompt is its own keeps it", got)
	}
}

// TestConversationsPickUpTheNameWrittenAfterThePrompt covers the window
// between the two things a row is built out of. Claude Code cannot name a
// conversation until there is one, so the name lands a few entries after the
// prompt. A panel opened in between reads a transcript that has the prompt
// and no name -- and it is the name that tells ten agents sent the same
// instruction apart, so freezing the row there is freezing ten identical
// rows.
func TestConversationsPickUpTheNameWrittenAfterThePrompt(t *testing.T) {
	cwd, dir := historyFixture(t, "naming")

	prompt := `{"type":"user","cwd":"` + jsonPath(cwd) + `","message":{"role":"user","content":"you are one of 10 agents"}}`
	first := writeTranscript(t, dir, "aaaaaaaa-1313-1313-1313-131313131313", prompt)
	second := writeTranscript(t, dir, "bbbbbbbb-1313-1313-1313-131313131313", prompt)

	got, err := Conversations(cwd)
	if err != nil {
		t.Fatalf("conversations: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("found %d conversations, want 2", len(got))
	}
	for _, c := range got {
		if c.Summary != "you are one of 10 agents" {
			t.Fatalf("summary = %q before either was named", c.Summary)
		}
	}

	// Claude Code works out what each of them is about.
	for path, name := range map[string]string{
		first:  "the first agent's lane",
		second: "the second agent's lane",
	} {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteString(`{"type":"ai-title","aiTitle":"` + name + `"}` + "\n"); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}

	got, err = Conversations(cwd)
	if err != nil {
		t.Fatalf("conversations: %v", err)
	}
	summaries := map[string]string{}
	for _, c := range got {
		summaries[c.ID] = c.Summary
		if c.Messages != 2 {
			t.Errorf("%s has %d entries, want 2", c.ID, c.Messages)
		}
	}
	if got := summaries["aaaaaaaa-1313-1313-1313-131313131313"]; got != "the first agent's lane" {
		t.Errorf("summary = %q, want the name that arrived after the first listing", got)
	}
	if got := summaries["bbbbbbbb-1313-1313-1313-131313131313"]; got != "the second agent's lane" {
		t.Errorf("summary = %q, want the name that arrived after the first listing", got)
	}
}

// TestTranscriptCacheForgetsTheFolderNobodyIsLookingAt covers what happens
// when the cache is full. A single listing draws on several folders -- a
// project's own, and one for every worktree of it -- so two or three projects
// can fill it between them. Emptying the whole cache at that point puts every
// project back to reading every transcript it has, which is the thing the
// cache is there to stop.
func TestTranscriptCacheForgetsTheFolderNobodyIsLookingAt(t *testing.T) {
	forgetTranscripts()
	t.Cleanup(forgetTranscripts)

	facts := map[string]transcriptFacts{"a.jsonl": {summary: "something", size: 1}}
	for i := 0; i < cachedFolderLimit; i++ {
		rememberFacts(fmt.Sprintf("folder-%03d", i), facts)
	}
	// Looked at again, so it is not the one to go.
	rememberFacts("folder-000", facts)
	rememberFacts("one-folder-too-many", facts)

	if got := cachedFacts("one-folder-too-many"); got == nil {
		t.Error("the folder that was just listed was not remembered")
	}
	if got := cachedFacts("folder-000"); got == nil {
		t.Error("a folder listed a moment ago was forgotten before an older one")
	}
	if got := cachedFacts("folder-001"); got != nil {
		t.Error("the folder nobody had looked at for longest was kept")
	}
	if got := cachedFacts("folder-050"); got == nil {
		t.Error("the whole cache was emptied to make room for one folder")
	}

	transcriptCache.Lock()
	defer transcriptCache.Unlock()
	if len(transcriptCache.dirs) > cachedFolderLimit {
		t.Errorf("the cache holds %d folders, more than the %d it is bounded to", len(transcriptCache.dirs), cachedFolderLimit)
	}
}

// FuzzDescribeATranscript throws damaged transcripts at the reading a listing
// does. Everything here is a file something else is writing while this reads
// it, and it is written by another program, on another schedule, in a format
// this only borrows: an entry cut off mid-word, bytes that are not JSON at
// all, a line that never ends. None of it may panic, hang, or come back with
// a count that does not describe the file or a summary too long for a row.
func FuzzDescribeATranscript(f *testing.F) {
	f.Add(`{"type":"user","cwd":"C:\\x","message":{"role":"user","content":"hello"}}` + "\n")
	f.Add(`{"type":"ai-title","aiTitle":"a name"}` + "\n" + `{"type":"user","message":{"role":"user","content":[{"type":"text","text":"blocks"}]}}` + "\n")
	f.Add(`{"type":"user","message":{"role":"user","content":"<system-reminder>skip me</system-reminder>"}}` + "\n" + `{"broken`)
	f.Add("\n\n\n")
	f.Add("")
	f.Add("{}\r\n{}\r\n")
	f.Add(`{"type":"user","message":{"role":"user","content":` + strings.Repeat("[", 64))

	f.Fuzz(func(t *testing.T, body string) {
		counted := &countingReader{r: strings.NewReader(body), last: '\n'}
		prompt, title, cwd := openingPrompt(counted)
		drain(counted)
		facts := transcriptFacts{
			summary:  prompt,
			title:    title,
			cwd:      cwd,
			newlines: counted.newlines,
			partial:  counted.last != '\n',
		}

		// One entry per line, and a last line with no break after it is an
		// entry that is still being written.
		want := strings.Count(body, "\n")
		if body != "" && !strings.HasSuffix(body, "\n") {
			want++
		}
		if got := facts.entries(); got != want {
			t.Fatalf("counted %d entries in %q, want %d", got, body, want)
		}
		for _, s := range []string{prompt, title} {
			if len([]rune(s)) > 161 {
				t.Fatalf("a row would be given %d runes to show: %q", len([]rune(s)), s)
			}
			if strings.ContainsAny(s, "\n\r\t") {
				t.Fatalf("a row would be given something that is not one line: %q", s)
			}
		}
	})
}
