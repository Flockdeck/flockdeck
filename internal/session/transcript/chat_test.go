package transcript

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/agent"
)

// chatSpec is an API agent as the catalog will describe one: Flockdeck's own chat
// client, whatever endpoint it was pointed at.
var chatSpec = agent.Spec{ID: "anthropic", Runner: agent.RunnerAPI, Caps: agent.Caps{Transcript: true, Resume: true}}

// writeChats puts chat transcripts where Flockdeck's chat client would have left
// them and returns the folder they are in.
func writeChats(t *testing.T, chats map[string][]string) string {
	t.Helper()
	base := t.TempDir()
	t.Setenv("APPDATA", base)         // Windows
	t.Setenv("XDG_CONFIG_HOME", base) // Linux
	t.Setenv("HOME", base)            // macOS

	dir, err := chatsDir()
	if err != nil {
		t.Fatalf("chats dir: %v", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for id, lines := range chats {
		path := filepath.Join(dir, id+".jsonl")
		if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestChatConversationsListWhatRanHere covers the history overlay's rows for
// an API pane. The chats all sit in one folder, so the directory each one
// records is the only thing that says which project it belongs to.
func TestChatConversationsListWhatRanHere(t *testing.T) {
	cwd := filepath.Join(t.TempDir(), "myrepo")
	elsewhere := filepath.Join(t.TempDir(), "other")

	dir := writeChats(t, map[string][]string{
		"11111111-1111-1111-1111-111111111111": {
			`{"type":"user","ts":1,"cwd":"` + jsonPath(cwd) + `","text":"add a health endpoint"}`,
			`{"type":"assistant","ts":2,"text":"Right away."}`,
		},
		"22222222-2222-2222-2222-222222222222": {
			`{"type":"user","ts":1,"cwd":"` + jsonPath(elsewhere) + `","text":"somebody else's work"}`,
		},
		"33333333-3333-3333-3333-333333333333": {
			`{"type":"assistant","ts":1,"text":"a chat that never said where it ran"}`,
		},
	})

	got, err := (Chat{}).Conversations(chatSpec, cwd)
	if err != nil {
		t.Fatalf("conversations: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("listed %d chats, want only the one that ran here: %+v", len(got), got)
	}
	if got[0].ID != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("id = %q, want the file name", got[0].ID)
	}
	if got[0].Agent != chatSpec.ID {
		t.Errorf("agent = %q, want %q", got[0].Agent, chatSpec.ID)
	}
	if got[0].Summary != "add a health endpoint" {
		t.Errorf("summary = %q, want the opening prompt", got[0].Summary)
	}
	if got[0].Messages != 2 {
		t.Errorf("entries = %d, want 2", got[0].Messages)
	}
	if got[0].Cwd != cwd {
		t.Errorf("cwd = %q, want %q", got[0].Cwd, cwd)
	}

	// An empty file is a session opened and abandoned: nothing to resume, and
	// nothing to put in a row but a date.
	if err := os.WriteFile(filepath.Join(dir, "44444444-4444-4444-4444-444444444444.jsonl"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err = (Chat{}).Conversations(chatSpec, cwd)
	if err != nil {
		t.Fatalf("conversations: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("an empty chat was offered as a conversation: %+v", got)
	}
}

// TestChatConversationsWithNoChats covers every install that has never run an
// API agent, which is all of them until somebody does.
func TestChatConversationsWithNoChats(t *testing.T) {
	base := t.TempDir()
	t.Setenv("APPDATA", base)
	t.Setenv("XDG_CONFIG_HOME", base)
	t.Setenv("HOME", base)

	got, err := (Chat{}).Conversations(chatSpec, t.TempDir())
	if err != nil {
		t.Fatalf("a missing chats folder is not a failure: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("listed %d chats, want none", len(got))
	}
}

// TestChatConversationsOrderMostRecentFirst pins the one column a history row
// is scanned by.
func TestChatConversationsOrderMostRecentFirst(t *testing.T) {
	cwd := filepath.Join(t.TempDir(), "myrepo")
	dir := writeChats(t, map[string][]string{
		"11111111-1111-1111-1111-111111111111": {`{"type":"user","cwd":"` + jsonPath(cwd) + `","text":"the older one"}`},
		"22222222-2222-2222-2222-222222222222": {`{"type":"user","cwd":"` + jsonPath(cwd) + `","text":"the newer one"}`},
	})
	now := time.Now()
	touch(t, filepath.Join(dir, "11111111-1111-1111-1111-111111111111.jsonl"), now.Add(-time.Hour))
	touch(t, filepath.Join(dir, "22222222-2222-2222-2222-222222222222.jsonl"), now)

	got, err := (Chat{}).Conversations(chatSpec, cwd)
	if err != nil {
		t.Fatalf("conversations: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("listed %d chats, want 2", len(got))
	}
	if got[0].Summary != "the newer one" || got[1].Summary != "the older one" {
		t.Errorf("out of order: %q then %q", got[0].Summary, got[1].Summary)
	}
}

// TestChatRepliesReadWhatTheModelSaid covers the source a fan-out reads its
// tasks from when the pane is an API agent. A turn is everything said in
// answer to one prompt, so the tools run in the middle of it do not cut a plan
// in half.
func TestChatRepliesReadWhatTheModelSaid(t *testing.T) {
	const id = "11111111-1111-1111-1111-111111111111"
	writeChats(t, map[string][]string{id: {
		`{"type":"user","ts":1,"text":"give me a plan"}`,
		`{"type":"assistant","ts":2,"text":"- Do the first thing"}`,
		`{"type":"tool","ts":3,"text":"read_file main.go"}`,
		`{"type":"assistant","ts":4,"text":"- Do the second thing"}`,
		`{"type":"user","ts":5,"text":"thanks"}`,
		`{"type":"assistant","ts":6,"text":"You are welcome."}`,
	}})

	got := (Chat{}).Replies(chatSpec, id, 4)
	if len(got) != 2 {
		t.Fatalf("read %d turns, want 2: %#v", len(got), got)
	}
	// Newest first: the follow-up, then the plan it followed.
	if got[0] != "You are welcome." {
		t.Errorf("newest turn = %q", got[0])
	}
	for _, want := range []string{"- Do the first thing", "- Do the second thing"} {
		if !strings.Contains(got[1], want) {
			t.Errorf("the plan lost %q: %q", want, got[1])
		}
	}
	if strings.Contains(got[1], "read_file") {
		t.Errorf("a tool call is not what the model said: %q", got[1])
	}
	if n := len((Chat{}).Replies(chatSpec, id, 1)); n != 1 {
		t.Errorf("asked for 1 turn, got %d", n)
	}
}

// TestChatRepliesWithoutATranscript covers a pane that has not said anything
// yet, which every API pane is for its first few seconds.
func TestChatRepliesWithoutATranscript(t *testing.T) {
	writeChats(t, nil)
	if got := (Chat{}).Replies(chatSpec, "11111111-1111-1111-1111-111111111111", 4); got != nil {
		t.Errorf("Replies = %#v, want nothing", got)
	}
	if got := (Chat{}).Replies(chatSpec, "", 4); got != nil {
		t.Errorf("Replies with no session = %#v, want nothing", got)
	}
}

// TestChatPathFindsTheStoredChat covers what resume and Exists are decided on.
func TestChatPathFindsTheStoredChat(t *testing.T) {
	const id = "11111111-1111-1111-1111-111111111111"
	dir := writeChats(t, map[string][]string{id: {`{"type":"user","ts":1,"text":"hello"}`}})

	if got := (Chat{}).Path(chatSpec, id); got != filepath.Join(dir, id+".jsonl") {
		t.Errorf("Path = %q, want the stored chat", got)
	}
	if got := (Chat{}).Path(chatSpec, "22222222-2222-2222-2222-222222222222"); got != "" {
		t.Errorf("Path for a chat that was never held = %q, want nothing", got)
	}
}

// BenchmarkChatRows measures listing one project's chats from a folder shared
// with every other project's, which is what opening the history overlay costs
// an API agent's pane: two hundred chats of a quarter of a megabyte each, ten
// of them this project's.
func BenchmarkChatRows(b *testing.B) {
	base := b.TempDir()
	b.Setenv("APPDATA", base)
	b.Setenv("XDG_CONFIG_HOME", base)
	b.Setenv("HOME", base)
	dir, err := chatsDir()
	if err != nil {
		b.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		b.Fatal(err)
	}
	here := filepath.Join(base, "here")
	answer := `{"type":"assistant","ts":2,"text":"` + strings.Repeat("output of a command ", 256<<10/20) + `"}`
	for i := 0; i < 200; i++ {
		cwd := filepath.Join(base, "elsewhere")
		if i%20 == 0 {
			cwd = here
		}
		chat := `{"type":"user","ts":1,"cwd":"` + jsonPath(cwd) + `","text":"a task"}` + "\n" + answer + "\n"
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%08d-0000-0000-0000-000000000000.jsonl", i)), []byte(chat), 0o600); err != nil {
			b.Fatal(err)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rows, err := chatRows(here)
		if err != nil || len(rows) != 10 {
			b.Fatalf("listed %d chats (%v), want 10", len(rows), err)
		}
	}
}
