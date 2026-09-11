package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/session/transcript"
	"github.com/jmwri/flockdeck/internal/store"
)

// listen waits for the next message of a type the window would receive.
func listen(t *testing.T, c *controlClient, kind string) conversationsMsg {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case raw := <-c.out:
			var msg conversationsMsg
			if err := json.Unmarshal(raw, &msg); err != nil {
				t.Fatalf("unmarshal %s: %v", raw, err)
			}
			if msg.Type == kind {
				return msg
			}
		case <-deadline:
			t.Fatalf("no %s message arrived", kind)
		}
	}
}

// TestListConversationsAnswersTheWindow covers the history panel's message:
// the rows it draws, and the mark that says a conversation is already in a
// pane and must not be opened twice.
func TestListConversationsAnswersTheWindow(t *testing.T) {
	srv, ws := newTestServer(t)
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)

	root := ws.ActiveRoot()
	dir := filepath.Join(home, "projects", slugFor(root))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	// One conversation that is already open in a pane, and one that is not.
	var openID string
	for id := range srv.openConversationIDs() {
		openID = id
	}
	if openID == "" {
		t.Fatal("the test server was expected to have a pane")
	}
	writeJSONL(t, filepath.Join(dir, openID+".jsonl"),
		`{"type":"user","cwd":"`+jsonEscape(root)+`","message":{"role":"user","content":"the open one"}}`)
	writeJSONL(t, filepath.Join(dir, "12345678-0000-0000-0000-000000000000.jsonl"),
		`{"type":"user","cwd":"`+jsonEscape(root)+`","message":{"role":"user","content":"the stored one"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":"done"}}`)

	c := &controlClient{out: make(chan []byte, 8)}
	srv.listConversations(c, root)
	msg := listen(t, c, "conversations")

	if msg.Error != "" {
		t.Fatalf("error = %q", msg.Error)
	}
	if msg.Cwd != root {
		t.Errorf("cwd = %q, want %q", msg.Cwd, root)
	}
	if len(msg.Items) != 2 {
		t.Fatalf("listed %d conversations, want 2: %+v", len(msg.Items), msg.Items)
	}
	byID := map[string]conversationView{}
	for _, item := range msg.Items {
		byID[item.ID] = item
	}
	stored := byID["12345678-0000-0000-0000-000000000000"]
	if stored.Summary != "the stored one" {
		t.Errorf("summary = %q", stored.Summary)
	}
	if stored.Agent != "claude" {
		t.Errorf("agent = %q; a row has to say which agent held the conversation", stored.Agent)
	}
	if stored.Messages != 2 {
		t.Errorf("entries = %d, want 2", stored.Messages)
	}
	if stored.Ago != "just now" {
		t.Errorf("ago = %q, want a freshly written transcript to read as just now", stored.Ago)
	}
	if stored.Open {
		t.Error("a conversation with no pane was marked as already open")
	}
	if !byID[openID].Open {
		t.Errorf("the conversation in a pane was not marked as open: %+v", byID[openID])
	}

	listings.Lock()
	defer listings.Unlock()
	if len(listings.seq) != 0 {
		t.Errorf("an answered listing was still being tracked: %v", listings.seq)
	}
}

// TestListConversationsLabelsEachAgentsOwn covers a history panel with more
// than one agent's conversations in it. Two rows can otherwise be identical --
// the same project, the same prompt, one held by Claude Code and one by a
// model spoken to directly -- and which agent a row belongs to decides what
// opening it does.
func TestListConversationsLabelsEachAgentsOwn(t *testing.T) {
	srv, ws := newTestServer(t)
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	root := ws.ActiveRoot()

	claudeDir := filepath.Join(home, "projects", slugFor(root))
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeJSONL(t, filepath.Join(claudeDir, "11111111-0000-0000-0000-000000000000.jsonl"),
		`{"type":"user","cwd":"`+jsonEscape(root)+`","message":{"role":"user","content":"the same prompt"}}`)

	// newTestServer points the state directory at a temporary one, so this is
	// where Flockdeck's own chat client would have left its record.
	stateDir, err := store.Dir()
	if err != nil {
		t.Fatalf("state dir: %v", err)
	}
	chats := filepath.Join(stateDir, "chats")
	if err := os.MkdirAll(chats, 0o700); err != nil {
		t.Fatal(err)
	}
	writeJSONL(t, filepath.Join(chats, "22222222-0000-0000-0000-000000000000.jsonl"),
		`{"type":"user","ts":1,"cwd":"`+jsonEscape(root)+`","text":"the same prompt"}`)

	was := transcript.Agents
	t.Cleanup(func() { transcript.Agents = was })
	transcript.Agents = func() []agent.Spec {
		return []agent.Spec{
			{ID: "claude", Caps: agent.Caps{Transcript: true, Resume: true}},
			{ID: "anthropic", Runner: agent.RunnerAPI, Caps: agent.Caps{Transcript: true, Resume: true}},
		}
	}

	c := &controlClient{out: make(chan []byte, 8)}
	srv.listConversations(c, root)
	msg := listen(t, c, "conversations")

	if msg.Error != "" {
		t.Fatalf("error = %q", msg.Error)
	}
	byID := map[string]conversationView{}
	for _, item := range msg.Items {
		byID[item.ID] = item
	}
	if got := byID["11111111-0000-0000-0000-000000000000"].Agent; got != "claude" {
		t.Errorf("the Claude conversation is labelled %q", got)
	}
	if got := byID["22222222-0000-0000-0000-000000000000"].Agent; got != "anthropic" {
		t.Errorf("the API agent's conversation is labelled %q; it should be listed and named", got)
	}
}

// TestOnlyTheNewestListingAnswersAWindow covers two requests from the same
// window overlapping. Reading a project's transcripts takes as long as the
// project is old, so an answer about a large project can finish after an
// answer about a small one that was asked for later -- and the panel draws
// whichever message arrived last, so the older answer would leave the window
// looking at another project's conversations.
func TestOnlyTheNewestListingAnswersAWindow(t *testing.T) {
	c := &controlClient{out: make(chan []byte, 8)}
	other := &controlClient{out: make(chan []byte, 8)}

	slow := askedForListing(c)
	quick := askedForListing(c)
	elsewhere := askedForListing(other)

	if answerListing(c, slow) {
		t.Error("a listing the window has already asked again about was allowed to answer")
	}
	if !answerListing(other, elsewhere) {
		t.Error("a second window's listing was overtaken by the first window's")
	}
	if !answerListing(c, quick) {
		t.Error("the newest listing was not allowed to answer")
	}
	if answerListing(c, quick) {
		t.Error("a listing answered twice")
	}

	listings.Lock()
	defer listings.Unlock()
	if len(listings.seq) != 0 {
		t.Errorf("answered listings were still being tracked: %v", listings.seq)
	}
}

// TestListConversationsGivesUpWhenTheServerCloses covers a window asking for
// its history as the application is shutting down. The answer has to come
// from the goroutine that owns the workspace, and that goroutine is gone: a
// request handed to it is never run, so anything waiting on the reply waits
// for good. It waits on the goroutine reading that window's socket, so a
// window left there cannot be closed and the shutdown does not finish.
func TestListConversationsGivesUpWhenTheServerCloses(t *testing.T) {
	srv, ws := newTestServer(t)
	root := ws.ActiveRoot()
	if err := srv.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	c := &controlClient{out: make(chan []byte, 8)}
	returned := make(chan struct{})
	go func() {
		defer close(returned)
		srv.listConversations(c, root)
	}()

	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("listConversations never returned after the server closed")
	}
}

// TestResumeAConversationThatIsNoLongerStored covers the row that has gone
// stale. The panel lists what was on disk when it was drawn, and a
// conversation can be deleted -- Claude Code removes old transcripts -- while
// somebody is looking at it. Resuming one starts an agent with `--resume`,
// which Claude Code refuses outright, so it has to be refused here instead,
// and the window has to be told why rather than left watching for a pane
// that never arrives.
func TestResumeAConversationThatIsNoLongerStored(t *testing.T) {
	srv, ws := newTestServer(t)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())

	before := len(srv.openConversationIDs())
	c := &controlClient{out: make(chan []byte, 8)}
	srv.resumeConversation(c, "12345678-0000-0000-0000-000000000000", ws.ActiveRoot(), "gone")

	var notice struct {
		Type  string `json:"type"`
		Text  string `json:"text"`
		Error bool   `json:"error"`
	}
	select {
	case raw := <-c.out:
		if err := json.Unmarshal(raw, &notice); err != nil {
			t.Fatalf("unmarshal %s: %v", raw, err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("nothing was said about a conversation that could not be resumed")
	}
	if notice.Type != "notice" || !notice.Error {
		t.Errorf("got %+v, want an error notice", notice)
	}
	if !strings.Contains(notice.Text, "stored") {
		t.Errorf("notice = %q; it should say the conversation is not there", notice.Text)
	}
	if after := len(srv.openConversationIDs()); after != before {
		t.Errorf("panes went from %d to %d; nothing should have been opened", before, after)
	}
}

// TestHumanAgoReadsLikeAList pins the wording of the only column a history row
// is scanned by.
func TestHumanAgoReadsLikeAList(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{0, "just now"},
		{-time.Hour, "just now"},
		{30 * time.Second, "just now"},
		{time.Minute, "1 minute"},
		{90 * time.Second, "1 minute"},
		{5 * time.Minute, "5 minutes"},
		{time.Hour, "1 hour"},
		{5 * time.Hour, "5 hours"},
		{25 * time.Hour, "1 day"},
		{10 * 24 * time.Hour, "10 days"},
		{45 * 24 * time.Hour, "1 month"},
		{200 * 24 * time.Hour, "6 months"},
	}
	for _, c := range cases {
		want := c.want
		if want != "just now" {
			want += " ago"
		}
		if got := humanAgo(c.in); got != want {
			t.Errorf("humanAgo(%s) = %q, want %q", c.in, got, want)
		}
	}
}

// TestTitleForNamesATabAfterThePrompt covers what a resumed pane is called.
func TestTitleForNamesATabAfterThePrompt(t *testing.T) {
	if got := titleFor("add a health endpoint", `C:\repos\app`); got != "add a health endpoint" {
		t.Errorf("title = %q", got)
	}
	if got := titleFor("", `C:\repos\app`); got != "app" {
		t.Errorf("a conversation with no prompt should be named after its directory, got %q", got)
	}
	if got := titleFor(session.NoPrompt, `C:\repos\app`); got != "app" {
		t.Errorf("a conversation that says nothing about itself should name its pane after the directory, got %q", got)
	}
	long := titleFor(strings.Repeat("a", 100), `C:\repos\app`)
	if len([]rune(long)) != 25 {
		t.Errorf("a long prompt should be cut to fit a tab, got %d runes", len([]rune(long)))
	}
}

// slugFor reproduces the folder name Claude Code derives from a path, which
// is where the transcripts a listing reads live.
func slugFor(path string) string {
	var b strings.Builder
	for _, r := range path {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

func writeJSONL(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// jsonEscape escapes a path for embedding in a JSON string literal.
func jsonEscape(p string) string { return strings.ReplaceAll(p, `\`, `\\`) }
