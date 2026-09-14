package transcript

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestChatStreamTranslatesEveryKind covers the one Entry each of the four
// line types a chat transcript ever writes turns into -- prompt, reply, and
// a tool row for each of ok/error/declined -- against a synthetic fixture,
// never real content.
func TestChatStreamTranslatesEveryKind(t *testing.T) {
	const id = "11111111-1111-1111-1111-111111111111"
	longOutput := strings.Repeat("a line of output\n", 400) // several lines, over ToolOutputCap
	longOutputJSON, err := json.Marshal(longOutput)
	if err != nil {
		t.Fatal(err)
	}
	dir := writeChats(t, map[string][]string{id: {
		`{"type":"user","ts":"2026-09-14T10:00:00Z","text":"add a health endpoint"}`,
		`{"type":"assistant","ts":"2026-09-14T10:00:01Z","text":"Right away. Let me look around."}`,
		`{"type":"tool","ts":"2026-09-14T10:00:02Z","tool":"read_file","call":"read_file main.go","text":"1\tpackage main\n"}`,
		`{"type":"tool","ts":"2026-09-14T10:00:03Z","tool":"run_command","call":"run_command go test ./...","text":` + string(longOutputJSON) + `}`,
		`{"type":"tool","ts":"2026-09-14T10:00:04Z","tool":"edit_file","call":"edit_file main.go","text":"the tool failed: old_string does not appear in main.go"}`,
		`{"type":"tool","ts":"2026-09-14T10:00:05Z","tool":"write_file","call":"write_file main.go","text":"the user declined this"}`,
	}})

	stream, ok := (Chat{}).Stream(chatSpec, id)
	if !ok {
		t.Fatal("Stream answered ok=false for a real session id")
	}
	stream.Refresh()
	entries := stream.Snapshot()
	if len(entries) != 6 {
		t.Fatalf("got %d entries, want 6: %+v", len(entries), entries)
	}

	if entries[0].Kind != KindPrompt || entries[0].Text != "add a health endpoint" {
		t.Errorf("prompt entry = %+v", entries[0])
	}
	if entries[1].Kind != KindReply || entries[1].Markdown != "Right away. Let me look around." {
		t.Errorf("reply entry = %+v", entries[1])
	}

	read := entries[2]
	if read.Kind != KindTool || read.Name != "read_file" || read.Label != "Read main.go" {
		t.Errorf("read tool entry = %+v", read)
	}
	if read.Status != StatusOK {
		t.Errorf("read tool status = %q, want ok", read.Status)
	}
	if read.HasDetail {
		t.Errorf("a one-line tool result should not need detail: %+v", read)
	}

	ran := entries[3]
	if ran.Kind != KindTool || ran.Name != "run_command" || ran.Label != "Ran go test ./..." {
		t.Errorf("run tool entry = %+v", ran)
	}
	if !ran.HasDetail {
		t.Errorf("a multi-line tool result over the cap should carry hasDetail: %+v", ran)
	}
	detail, ok := stream.Detail(ran.ID)
	if !ok || detail.Text != longOutput {
		t.Errorf("Detail(%q) = %+v, %v; want the full output", ran.ID, detail, ok)
	}
	if !strings.HasPrefix(ran.Summary, "a line of output") {
		t.Errorf("run tool summary = %q", ran.Summary)
	}

	failed := entries[4]
	if failed.Kind != KindTool || failed.Name != "edit_file" || failed.Label != "Edited main.go" {
		t.Errorf("failed tool entry = %+v", failed)
	}
	if failed.Status != StatusError {
		t.Errorf("a failed edit's status = %q, want error", failed.Status)
	}
	if failed.Diff != "" {
		t.Errorf("a chat tool never carries a diff (no before/after in the transcript): %+v", failed)
	}

	declined := entries[5]
	if declined.Kind != KindTool || declined.Name != "write_file" || declined.Status != StatusError {
		t.Errorf("declined tool entry = %+v", declined)
	}

	// The chats dir is only exercised through Stream here, but confirm the
	// fixture actually lives where the reader looks, so a future refactor of
	// writeChats cannot silently make this test pass for the wrong reason.
	if _, err := os.Stat(filepath.Join(dir, id+".jsonl")); err != nil {
		t.Fatalf("fixture not where Stream reads it: %v", err)
	}
}

// TestChatStreamTailsIncrementally covers appending to the file after the
// stream has already read some of it, and a last line that arrives before
// its newline -- the same "held-back partial line" tailing claudeStream
// does, exercised against chat's own format.
func TestChatStreamTailsIncrementally(t *testing.T) {
	const id = "22222222-2222-2222-2222-222222222222"
	dir := writeChats(t, map[string][]string{id: {
		`{"type":"user","ts":"2026-09-14T10:00:00Z","text":"first"}`,
	}})
	path := filepath.Join(dir, id+".jsonl")

	stream, ok := (Chat{}).Stream(chatSpec, id)
	if !ok {
		t.Fatal("Stream answered ok=false")
	}
	if got := stream.Refresh(); len(got) != 1 {
		t.Fatalf("first refresh got %d entries, want 1", len(got))
	}

	// A second complete line arrives, followed by a third still being
	// written -- no trailing newline yet.
	appendRaw(t, path, `{"type":"assistant","ts":"2026-09-14T10:00:01Z","text":"second"}`+"\n"+
		`{"type":"user","ts":"2026-09-14T10:00:02Z","text":"thi`)
	changed := stream.Refresh()
	if len(changed) != 1 || changed[0].Markdown != "second" {
		t.Fatalf("refresh with a partial last line = %+v, want just the complete one", changed)
	}
	if got := stream.Snapshot(); len(got) != 2 {
		t.Fatalf("snapshot after a partial tail = %d entries, want 2 (the partial line withheld)", len(got))
	}

	// The rest of the third line, and its newline, arrive.
	appendRaw(t, path, `rd"}`+"\n")
	changed = stream.Refresh()
	if len(changed) != 1 || changed[0].Text != "third" {
		t.Fatalf("refresh once the partial line completed = %+v", changed)
	}
	if got := stream.Snapshot(); len(got) != 3 {
		t.Fatalf("snapshot after the partial line completed = %d entries, want 3", len(got))
	}
}

// TestChatStreamClearResetsInPlace covers a /clear, which -- unlike Claude
// Code's -- keeps the same session id and file: the stream must drop what
// came before it and say Reset() once, exactly the gap this adapter's
// Stream.Reset addition exists for (see NOTES.md).
func TestChatStreamClearResetsInPlace(t *testing.T) {
	const id = "33333333-3333-3333-3333-333333333333"
	writeChats(t, map[string][]string{id: {
		`{"type":"user","ts":"2026-09-14T10:00:00Z","text":"before the clear"}`,
		`{"type":"assistant","ts":"2026-09-14T10:00:01Z","text":"replying before the clear"}`,
		`{"type":"clear"}`,
		`{"type":"user","ts":"2026-09-14T10:05:00Z","text":"after the clear"}`,
	}})

	stream, ok := (Chat{}).Stream(chatSpec, id)
	if !ok {
		t.Fatal("Stream answered ok=false")
	}
	stream.Refresh()

	entries := stream.Snapshot()
	if len(entries) != 1 || entries[0].Text != "after the clear" {
		t.Fatalf("snapshot after /clear = %+v, want only what was said after it", entries)
	}
	if !stream.Reset() {
		t.Fatal("Reset() = false right after a /clear line was read, want true")
	}
	if stream.Reset() {
		t.Fatal("Reset() = true a second time without another /clear, want it to have been consumed")
	}
}

// TestChatStreamWithNoTranscriptYet covers a pane that has been opened but
// never prompted, the same state a fresh Claude pane is in.
func TestChatStreamWithNoTranscriptYet(t *testing.T) {
	writeChats(t, nil)
	stream, ok := (Chat{}).Stream(chatSpec, "44444444-4444-4444-4444-444444444444")
	if !ok {
		t.Fatal("Stream answered ok=false for a pane that simply has not said anything yet")
	}
	if got := stream.Refresh(); got != nil {
		t.Errorf("Refresh on a transcript that does not exist yet = %+v, want nothing", got)
	}
	if got := stream.Snapshot(); len(got) != 0 {
		t.Errorf("Snapshot = %+v, want empty", got)
	}
}

func appendRaw(t *testing.T, path, s string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(s); err != nil {
		t.Fatal(err)
	}
}
