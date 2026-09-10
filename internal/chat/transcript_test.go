package chat

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// isolateConfig points the state directory at a temporary location, so a test
// cannot read or write the transcripts of a real installation.
func isolateConfig(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)         // Windows
	t.Setenv("XDG_CONFIG_HOME", dir) // Linux
	t.Setenv("HOME", dir)            // macOS and fallback
}

func TestLogAppendsOneObjectPerEntry(t *testing.T) {
	dir := t.TempDir()
	log, err := OpenLog(dir, "s1", `C:\work`)
	if err != nil {
		t.Fatal(err)
	}
	log.when = func() time.Time { return time.Date(2026, 9, 9, 10, 0, 0, 0, time.UTC) }
	for _, e := range []Entry{
		{Type: "user", Text: "hello"},
		{Type: "assistant", Text: "hi", Model: "m1", In: 3, Out: 4},
		{Type: "tool", Tool: "read_file", Text: "contents"},
	} {
		if err := log.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "s1.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("wrote %d lines, want 3: %q", len(lines), data)
	}
	var first map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("first line is not an object: %v", err)
	}
	if first["type"] != "user" || first["text"] != "hello" {
		t.Errorf("first entry = %v", first)
	}
	if first["ts"] != "2026-09-09T10:00:00Z" {
		t.Errorf("timestamp = %v", first["ts"])
	}
	if first["cwd"] != `C:\work` {
		t.Errorf("cwd = %v, want the working directory it was opened with", first["cwd"])
	}
}

func TestMessagesRebuildsAConversation(t *testing.T) {
	tests := []struct {
		name    string
		entries []Entry
		want    []Message
	}{
		{
			name: "a plain exchange",
			entries: []Entry{
				{Type: "user", Text: "one"},
				{Type: "assistant", Text: "two"},
			},
			want: []Message{
				{Role: RoleUser, Text: "one"},
				{Role: RoleAssistant, Text: "two"},
			},
		},
		{
			name: "a tool answer joins what the user said",
			entries: []Entry{
				{Type: "user", Text: "look"},
				{Type: "assistant", Text: "looking"},
				{Type: "tool", Tool: "read_file", Text: "abc"},
				{Type: "assistant", Text: "found it"},
			},
			want: []Message{
				{Role: RoleUser, Text: "look"},
				{Role: RoleAssistant, Text: "looking"},
				{Role: RoleUser, Text: "[read_file]\nabc"},
				{Role: RoleAssistant, Text: "found it"},
			},
		},
		{
			name: "clearing drops what came before it",
			entries: []Entry{
				{Type: "user", Text: "old"},
				{Type: "assistant", Text: "older"},
				{Type: entryClear},
				{Type: "user", Text: "new"},
			},
			want: []Message{{Role: RoleUser, Text: "new"}},
		},
		{
			name:    "an empty entry is not a message",
			entries: []Entry{{Type: "user", Text: "  "}, {Type: "assistant", Text: "said"}},
			want:    []Message{{Role: RoleAssistant, Text: "said"}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Messages(tc.entries)
			if len(got) != len(tc.want) {
				t.Fatalf("rebuilt %d messages, want %d: %+v", len(got), len(tc.want), got)
			}
			for i := range got {
				if got[i].Role != tc.want[i].Role || got[i].Text != tc.want[i].Text {
					t.Errorf("message %d = %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestReadEntriesAcceptsEitherKindOfTimestampAndSkipsRubbish(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
	body := strings.Join([]string{
		`{"type":"user","ts":"2026-09-09T10:00:00Z","text":"one"}`,
		`{"type":"assistant","ts":1757412000,"text":"two"}`,
		`not json at all`,
		`{"type":"user","text":"three"}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	entries, err := ReadEntries(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("read %d entries, want 3", len(entries))
	}
	if entries[0].TS.IsZero() || entries[1].TS.IsZero() {
		t.Errorf("timestamps = %v and %v, want both read", entries[0].TS, entries[1].TS)
	}
	if !entries[2].TS.IsZero() {
		t.Errorf("an entry with no timestamp read as %v", entries[2].TS)
	}
}

func TestRepliesAreTheAgentsLastTurnsNewestFirst(t *testing.T) {
	isolateConfig(t)
	dir, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	log, err := OpenLog(dir, "s2", "/work")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range []Entry{
		{Type: "user", Text: "first"},
		{Type: "assistant", Text: "a plan"},
		{Type: "tool", Tool: "read_file", Text: "not what was said"},
		{Type: "assistant", Text: "and the rest of it"},
		{Type: "user", Text: "second"},
		{Type: "assistant", Text: "a second answer"},
	} {
		if err := log.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	log.Close()

	got := Replies("s2", 2)
	want := []string{"a second answer", "a plan\n\nand the rest of it"}
	if len(got) != len(want) {
		t.Fatalf("got %d turns, want %d: %q", len(got), len(want), got)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("turn %d = %q, want %q", i, got[i], want[i])
		}
	}
	if Replies("nobody", 2) != nil {
		t.Errorf("a session with no transcript answered with turns")
	}
}

func TestPathIsEmptyUntilSomethingWasSaid(t *testing.T) {
	isolateConfig(t)
	dir, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	log, err := OpenLog(dir, "s3", "/work")
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	// A pane that was opened and never prompted leaves an empty file behind,
	// and resuming that is not resuming anything.
	if p := Path("s3"); p != "" {
		t.Errorf("path = %q for an empty transcript, want it to count as nothing", p)
	}
	if err := log.Append(Entry{Type: "user", Text: "at last"}); err != nil {
		t.Fatal(err)
	}
	if p := Path("s3"); p == "" {
		t.Errorf("path is empty after something was said")
	}
}

func TestConversationsListsWhatBelongsToADirectory(t *testing.T) {
	isolateConfig(t)
	dir, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		id, cwd, prompt string
	}{
		{"aaa", "/work/one", "the first thing"},
		{"bbb", "/work/two", "the second thing"},
	} {
		log, err := OpenLog(dir, c.id, c.cwd)
		if err != nil {
			t.Fatal(err)
		}
		log.Append(Entry{Type: "user", Text: c.prompt + "\nand a second line"})
		log.Append(Entry{Type: "assistant", Text: "answered"})
		log.Close()
	}

	all, err := Conversations("")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("listed %d conversations, want 2", len(all))
	}
	mine, err := Conversations("/work/one")
	if err != nil {
		t.Fatal(err)
	}
	if len(mine) != 1 || mine[0].ID != "aaa" {
		t.Fatalf("listed %+v, want only the one in /work/one", mine)
	}
	if mine[0].Summary != "the first thing" {
		t.Errorf("summary = %q, want the opening line of the prompt", mine[0].Summary)
	}
	if mine[0].Messages != 2 {
		t.Errorf("counted %d messages, want 2", mine[0].Messages)
	}
}

func TestNewSessionIDLooksLikeAUUID(t *testing.T) {
	id, err := newSessionID(func(b []byte) error {
		for i := range b {
			b[i] = 0xff
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := "ffffffff-ffff-4fff-bfff-ffffffffffff"; id != want {
		t.Errorf("id = %q, want %q", id, want)
	}
}
