package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/chat"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/session/transcript"
	"github.com/jmwri/flockdeck/internal/workspace"
)

// addAgentPane opens a pane and marks it as running the named agent, on the
// workspace goroutine, so a test can give it a transcript to read without
// starting a real CLI.
func addAgentPane(t *testing.T, srv *Server, ws *workspace.Workspace, agentID string) string {
	t.Helper()
	id, ok := ask(srv, func() string {
		id := ws.NewTab(session.KindShell, ws.ActiveRoot(), "agent").Tree.Panes()[0]
		if p := ws.Pane(id); p != nil {
			p.Kind = session.KindAgent
			p.Agent = agentID
		}
		return id
	})
	if !ok {
		t.Fatal("the workspace did not answer")
	}
	return id
}

// setPaneConversation moves a pane to a new conversation id, the way the
// SessionStart hook does after /clear.
func setPaneConversation(t *testing.T, srv *Server, ws *workspace.Workspace, paneID, conversation string) {
	t.Helper()
	_, ok := ask(srv, func() int {
		if p := ws.Pane(paneID); p != nil {
			p.Conversation = conversation
		}
		return 0
	})
	if !ok {
		t.Fatal("the workspace did not answer")
	}
}

// claudeTranscriptPath is where a pane's own transcript lives once
// CLAUDE_CONFIG_DIR is pointed at home.
func claudeTranscriptPath(home, root, sessionID string) string {
	return filepath.Join(home, "projects", slugFor(root), sessionID+".jsonl")
}

// writeClaudeJSONL writes a transcript, creating the project folder Claude
// Code would have made for it.
func writeClaudeJSONL(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	writeJSONL(t, path, lines...)
}

func nextRaw(t *testing.T, c *controlClient) []byte {
	t.Helper()
	select {
	case raw := <-c.out:
		return raw
	case <-time.After(5 * time.Second):
		t.Fatal("no message arrived")
		return nil
	}
}

func msgType(t *testing.T, raw []byte) string {
	t.Helper()
	var m struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal %s: %v", raw, err)
	}
	return m.Type
}

func expectNoMessage(t *testing.T, c *controlClient, within time.Duration) {
	t.Helper()
	select {
	case raw := <-c.out:
		t.Fatalf("unexpected message: %s", raw)
	case <-time.After(within):
	}
}

func decodePage(t *testing.T, raw []byte) conversationPageMsg {
	t.Helper()
	var msg conversationPageMsg
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("unmarshal conversationPage: %v", err)
	}
	return msg
}

// TestConversationOpenPagesThenAppends is the core protocol test: without
// conversationOpen resolving a pane's transcript and conversationHookEvent
// pushing what changed, a phone would never see anything past the state the
// pane was in the moment it opened the chat view.
func TestConversationOpenPagesThenAppends(t *testing.T) {
	srv, ws := newTestServer(t)
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	paneID := addAgentPane(t, srv, ws, "claude")
	root := ws.ActiveRoot()

	path := claudeTranscriptPath(home, root, paneID)
	writeClaudeJSONL(t, path,
		`{"type":"user","uuid":"u1","message":{"role":"user","content":"hello"}}`,
		`{"type":"assistant","uuid":"a1","message":{"role":"assistant","content":[{"type":"text","text":"hi there"}]}}`,
	)

	c := &controlClient{out: make(chan []byte, 8)}
	srv.conversationOpen(c, paneID, "")
	page := decodePage(t, nextRaw(t, c))
	if !page.Supported || page.Agent != "claude" {
		t.Fatalf("page = %+v, want a supported claude page", page)
	}
	if len(page.Entries) != 2 {
		t.Fatalf("entries = %+v, want 2", page.Entries)
	}
	if !page.AtStart {
		t.Error("a page holding the whole conversation should say atStart")
	}
	if page.Entries[0].Kind != transcript.KindPrompt || page.Entries[1].Kind != transcript.KindReply {
		t.Errorf("entries = %+v", page.Entries)
	}
	cursor := page.Cursor
	if cursor == "" {
		t.Fatal("no cursor on the first page")
	}

	// A new line is written, and the hook fires as it would for a real tool
	// call: the client watching this pane should be pushed the new entry
	// without asking again.
	appendJSONL(t, path,
		`{"type":"assistant","uuid":"a2","message":{"role":"assistant","content":[{"type":"text","text":"a second reply"}]}}`,
	)
	srv.ConversationHookEvent(paneID)

	raw := nextRaw(t, c)
	if msgType(t, raw) != "conversationAppend" {
		t.Fatalf("got %s, want conversationAppend", raw)
	}
	var app conversationAppendMsg
	if err := json.Unmarshal(raw, &app); err != nil {
		t.Fatal(err)
	}
	if len(app.Entries) != 1 || app.Entries[0].Markdown != "a second reply" {
		t.Errorf("appended entries = %+v", app.Entries)
	}
	if app.Cursor == cursor {
		t.Error("the cursor did not move past the appended entry")
	}
}

// TestConversationOlderPagesBackward covers a conversation with more entries
// than one page holds.
func TestConversationOlderPagesBackward(t *testing.T) {
	srv, ws := newTestServer(t)
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	paneID := addAgentPane(t, srv, ws, "claude")
	root := ws.ActiveRoot()

	var lines []string
	for i := 0; i < 60; i++ {
		lines = append(lines, `{"type":"user","uuid":"u`+itoa(i)+`","message":{"role":"user","content":"message `+itoa(i)+`"}}`)
	}
	writeClaudeJSONL(t, claudeTranscriptPath(home, root, paneID), lines...)

	c := &controlClient{out: make(chan []byte, 8)}
	srv.conversationOpen(c, paneID, "")
	page := decodePage(t, nextRaw(t, c))
	if len(page.Entries) != conversationPageSize {
		t.Fatalf("first page = %d entries, want %d", len(page.Entries), conversationPageSize)
	}
	if page.AtStart {
		t.Error("a page that is not the whole conversation should not say atStart")
	}
	if page.Entries[0].Text != "message 10" {
		t.Errorf("first page starts at %q, want \"message 10\"", page.Entries[0].Text)
	}

	older := &controlClient{out: make(chan []byte, 8)}
	srv.conversationOlder(older, paneID, page.Entries[0].ID)
	olderPage := decodePage(t, nextRaw(t, older))
	if !olderPage.AtStart {
		t.Error("the page reaching the first entry should say atStart")
	}
	if len(olderPage.Entries) != 10 {
		t.Fatalf("older page = %d entries, want 10", len(olderPage.Entries))
	}
	if olderPage.Entries[0].Text != "message 0" || olderPage.Entries[9].Text != "message 9" {
		t.Errorf("older page = %+v", olderPage.Entries)
	}
}

// TestConversationDetailFetchesTrimmedBody covers a tool result too large to
// arrive inline.
func TestConversationDetailFetchesTrimmedBody(t *testing.T) {
	srv, ws := newTestServer(t)
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	paneID := addAgentPane(t, srv, ws, "claude")
	root := ws.ActiveRoot()

	full := strings.Repeat("a line of output\n", 400) // well over the 4KB cap
	resultJSON, err := json.Marshal(full)
	if err != nil {
		t.Fatal(err)
	}
	writeClaudeJSONL(t, claudeTranscriptPath(home, root, paneID),
		`{"type":"assistant","uuid":"a1","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu1","name":"Bash","input":{"command":"build"}}]}}`,
		`{"type":"user","uuid":"u1","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu1","content":`+string(resultJSON)+`}]}}`,
	)

	c := &controlClient{out: make(chan []byte, 8)}
	srv.conversationOpen(c, paneID, "")
	page := decodePage(t, nextRaw(t, c))
	if len(page.Entries) != 1 {
		t.Fatalf("entries = %+v", page.Entries)
	}
	tool := page.Entries[0]
	if !tool.HasDetail || tool.Diff != "" {
		t.Fatalf("tool entry = %+v, want hasDetail with no inline diff", tool)
	}
	if strings.Contains(page.Cursor, full) {
		t.Fatal("the full body must not travel in the page")
	}

	srv.conversationDetailReq(c, paneID, "tu1")
	raw := nextRaw(t, c)
	if msgType(t, raw) != "conversationDetail" {
		t.Fatalf("got %s, want conversationDetail", raw)
	}
	var d conversationDetailMsg
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	if d.Entry != "tu1" || d.Detail.Text != full {
		t.Errorf("detail = entry %q, %d bytes; want tu1, %d bytes", d.Entry, len(d.Detail.Text), len(full))
	}
}

// TestConversationReconnectWithACursor covers a phone that reconnects having
// missed some appends: it should be sent only what it missed.
func TestConversationReconnectWithACursor(t *testing.T) {
	srv, ws := newTestServer(t)
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	paneID := addAgentPane(t, srv, ws, "claude")
	root := ws.ActiveRoot()
	path := claudeTranscriptPath(home, root, paneID)

	writeClaudeJSONL(t, path, `{"type":"user","uuid":"u1","message":{"role":"user","content":"first"}}`)

	c := &controlClient{out: make(chan []byte, 8)}
	srv.conversationOpen(c, paneID, "")
	first := decodePage(t, nextRaw(t, c))
	cursor := first.Cursor

	// The phone goes away -- closes the pane -- and something happens while
	// it is gone.
	srv.conversationClose(c, paneID)
	appendJSONL(t, path, `{"type":"user","uuid":"u2","message":{"role":"user","content":"missed while away"}}`)

	reconnect := &controlClient{out: make(chan []byte, 8)}
	srv.conversationOpen(reconnect, paneID, cursor)
	page := decodePage(t, nextRaw(t, reconnect))
	if page.Reset {
		t.Error("a still-known cursor should not reset the phone's view")
	}
	if len(page.Entries) != 1 || page.Entries[0].Text != "missed while away" {
		t.Fatalf("entries = %+v, want only what was missed", page.Entries)
	}
}

// TestConversationReconnectWithAnUnknownCursorResets covers a desktop that
// restarted, or a cursor from a conversation that no longer exists: the
// contract says to answer with the newest page and reset:true rather than
// silently drop what was asked for.
func TestConversationReconnectWithAnUnknownCursorResets(t *testing.T) {
	srv, ws := newTestServer(t)
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	paneID := addAgentPane(t, srv, ws, "claude")
	root := ws.ActiveRoot()
	writeClaudeJSONL(t, claudeTranscriptPath(home, root, paneID),
		`{"type":"user","uuid":"u1","message":{"role":"user","content":"hello"}}`)

	c := &controlClient{out: make(chan []byte, 8)}
	srv.conversationOpen(c, paneID, "an-id-nobody-issued")
	page := decodePage(t, nextRaw(t, c))
	if !page.Reset {
		t.Error("an unknown cursor should be answered with reset:true")
	}
	if len(page.Entries) != 1 || page.Entries[0].Text != "hello" {
		t.Fatalf("entries = %+v, want the newest page", page.Entries)
	}
}

// TestConversationClearMidStreamResets covers /clear while a client has a
// pane's conversation open: the pane moves to a new conversation id, and
// every watcher should be told to drop what it has and redraw, not shown the
// old conversation's tail forever.
func TestConversationClearMidStreamResets(t *testing.T) {
	srv, ws := newTestServer(t)
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	paneID := addAgentPane(t, srv, ws, "claude")
	root := ws.ActiveRoot()

	writeClaudeJSONL(t, claudeTranscriptPath(home, root, paneID),
		`{"type":"user","uuid":"u1","message":{"role":"user","content":"before clear"}}`)

	c := &controlClient{out: make(chan []byte, 8)}
	srv.conversationOpen(c, paneID, "")
	before := decodePage(t, nextRaw(t, c))
	if len(before.Entries) != 1 || before.Entries[0].Text != "before clear" {
		t.Fatalf("entries before clear = %+v", before.Entries)
	}

	newSession := "99999999-0000-0000-0000-000000000000"
	writeClaudeJSONL(t, claudeTranscriptPath(home, root, newSession),
		`{"type":"user","uuid":"u2","message":{"role":"user","content":"after clear"}}`)
	setPaneConversation(t, srv, ws, paneID, newSession)
	srv.ConversationHookEvent(paneID)

	raw := nextRaw(t, c)
	if msgType(t, raw) != "conversationPage" {
		t.Fatalf("got %s, want a resetting conversationPage", raw)
	}
	after := decodePage(t, raw)
	if !after.Reset {
		t.Error("a conversation switch should arrive with reset:true")
	}
	if len(after.Entries) != 1 || after.Entries[0].Text != "after clear" {
		t.Fatalf("entries after clear = %+v", after.Entries)
	}
}

// TestConversationOpenUnsupportedAgent covers a pane whose agent has no chat
// view adapter: the phone must be told plainly so it can fall back to the
// terminal, not left guessing from a page of nothing.
func TestConversationOpenUnsupportedAgent(t *testing.T) {
	srv, ws := newTestServer(t)
	paneID := addAgentPane(t, srv, ws, "codex")

	c := &controlClient{out: make(chan []byte, 8)}
	srv.conversationOpen(c, paneID, "")
	page := decodePage(t, nextRaw(t, c))
	if page.Supported {
		t.Errorf("codex has no adapter yet; got a supported page: %+v", page)
	}
	if len(page.Entries) != 0 {
		t.Errorf("an unsupported page should carry no entries, got %+v", page.Entries)
	}
}

// TestConversationOpenRefusesAPaneItCannotSee covers a client naming a pane
// the workspace does not have -- closed already, or never existed. It must
// be refused silently, the same as any other unknown pane id on this
// protocol, rather than answered with something to act on.
func TestConversationOpenRefusesAPaneItCannotSee(t *testing.T) {
	srv, _ := newTestServer(t)
	c := &controlClient{out: make(chan []byte, 8)}
	srv.conversationOpen(c, "no-such-pane", "")
	expectNoMessage(t, c, 500*time.Millisecond)
}

// TestConversationDetailReturnsAnImagesBytes covers the phone's fetch-on-tap
// path for a picture: the page carries only the entry's size and dimensions,
// and conversationDetail is what actually hands over the bytes to draw.
func TestConversationDetailReturnsAnImagesBytes(t *testing.T) {
	srv, ws := newTestServer(t)
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	paneID := addAgentPane(t, srv, ws, "claude")
	root := ws.ActiveRoot()

	const tinyPNGBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII="
	writeClaudeJSONL(t, claudeTranscriptPath(home, root, paneID),
		`{"type":"user","uuid":"u1","message":{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"`+tinyPNGBase64+`"}}]}}`,
	)

	c := &controlClient{out: make(chan []byte, 8)}
	srv.conversationOpen(c, paneID, "")
	page := decodePage(t, nextRaw(t, c))
	if len(page.Entries) != 1 {
		t.Fatalf("entries = %+v", page.Entries)
	}
	img := page.Entries[0]
	if img.Kind != transcript.KindImage || img.MediaType != "image/png" || !img.HasDetail {
		t.Fatalf("image entry = %+v", img)
	}
	if strings.Contains(page.Cursor, tinyPNGBase64) {
		t.Fatal("the image bytes must not travel in the page")
	}

	srv.conversationDetailReq(c, paneID, img.ID)
	raw := nextRaw(t, c)
	if msgType(t, raw) != "conversationDetail" {
		t.Fatalf("got %s, want conversationDetail", raw)
	}
	var d conversationDetailMsg
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	if d.Detail.Data != tinyPNGBase64 {
		t.Errorf("detail data = %q, want the picture's own base64", d.Detail.Data)
	}
}

// TestConversationOpenAfterBeingTailedLightGivesFullHistory covers the
// promotion path: a pane tailed light for a while, because nobody had it
// open, must still show its whole history when finally opened -- not only
// what arrived after that.
func TestConversationOpenAfterBeingTailedLightGivesFullHistory(t *testing.T) {
	srv, ws := newTestServer(t)
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	paneID := addAgentPane(t, srv, ws, "claude")
	root := ws.ActiveRoot()
	path := claudeTranscriptPath(home, root, paneID)

	writeClaudeJSONL(t, path,
		`{"type":"user","uuid":"u1","message":{"role":"user","content":"hello"}}`,
		`{"type":"assistant","uuid":"a1","message":{"role":"assistant","content":[{"type":"text","text":"first reply"}]}}`,
	)
	srv.ConversationHookEvent(paneID)

	pc, ok := srv.convos.lookup(paneID)
	if !ok {
		t.Fatal("no conversation entry after a hook event")
	}
	pc.mu.Lock()
	n := pc.stream.EntryCount()
	pc.mu.Unlock()
	if n != 0 {
		t.Fatalf("EntryCount = %d before opening, want 0 (light mode)", n)
	}

	appendJSONL(t, path,
		`{"type":"assistant","uuid":"a2","message":{"role":"assistant","content":[{"type":"text","text":"second reply"}]}}`,
	)
	srv.ConversationHookEvent(paneID)

	c := &controlClient{out: make(chan []byte, 8)}
	srv.conversationOpen(c, paneID, "")
	page := decodePage(t, nextRaw(t, c))
	if len(page.Entries) != 3 {
		t.Fatalf("entries = %+v, want all 3 lines, including those written before the pane was ever opened", page.Entries)
	}
	if page.Entries[0].Text != "hello" || page.Entries[1].Markdown != "first reply" || page.Entries[2].Markdown != "second reply" {
		t.Errorf("entries = %+v", page.Entries)
	}
}

// TestConversationCloseDropsBackToLightAfterADelay covers giving memory
// back: once the last client watching a pane closes it, the stream should
// drop back to light mode -- after conversationIdleDowngradeDelay, not
// immediately, so a quick reopen is not punished with a full rebuild.
func TestConversationCloseDropsBackToLightAfterADelay(t *testing.T) {
	old := conversationIdleDowngradeDelay
	conversationIdleDowngradeDelay = 50 * time.Millisecond
	t.Cleanup(func() { conversationIdleDowngradeDelay = old })

	srv, ws := newTestServer(t)
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	paneID := addAgentPane(t, srv, ws, "claude")
	root := ws.ActiveRoot()
	writeClaudeJSONL(t, claudeTranscriptPath(home, root, paneID),
		`{"type":"user","uuid":"u1","message":{"role":"user","content":"hello"}}`,
	)

	c := &controlClient{out: make(chan []byte, 8)}
	srv.conversationOpen(c, paneID, "")
	decodePage(t, nextRaw(t, c))

	pc, ok := srv.convos.lookup(paneID)
	if !ok {
		t.Fatal("no conversation entry after opening")
	}
	pc.mu.Lock()
	n := pc.stream.EntryCount()
	pc.mu.Unlock()
	if n == 0 {
		t.Fatal("EntryCount = 0 right after opening, want the full history retained")
	}

	srv.conversationClose(c, paneID)

	deadline := time.Now().Add(2 * time.Second)
	for {
		pc.mu.Lock()
		n = pc.stream.EntryCount()
		pc.mu.Unlock()
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("EntryCount stayed %d after closing the last client and waiting past the downgrade delay", n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func appendJSONL(t *testing.T, path string, lines ...string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(strings.Join(lines, "\n") + "\n"); err != nil {
		t.Fatal(err)
	}
}

// chatTranscriptPath is where a pane running Flockdeck's own chat client keeps
// its transcript, once newTestServer's APPDATA/XDG_CONFIG_HOME/HOME point at
// the test's own state directory.
func chatTranscriptPath(t *testing.T, sessionID string) string {
	t.Helper()
	dir, err := chat.Dir()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, sessionID+".jsonl")
}

// writeChatJSONL writes a chat transcript fixture, creating its folder.
func writeChatJSONL(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	writeJSONL(t, path, lines...)
}

// TestConversationOpenChatPane is the phase-2 counterpart of
// TestConversationOpenPagesThenAppends: a pane running Flockdeck's own chat
// client (an API agent) answers conversationOpen with supported:true and
// entries built from its own transcript format, and streams appends the
// same way a Claude pane does.
func TestConversationOpenChatPane(t *testing.T) {
	srv, ws := newTestServer(t)
	paneID := addAgentPane(t, srv, ws, "anthropic")

	path := chatTranscriptPath(t, paneID)
	writeChatJSONL(t, path,
		`{"type":"user","ts":"2026-09-14T10:00:00Z","text":"add a health endpoint"}`,
		`{"type":"assistant","ts":"2026-09-14T10:00:01Z","text":"Right away."}`,
		`{"type":"tool","ts":"2026-09-14T10:00:02Z","tool":"read_file","call":"read_file main.go","text":"1\tpackage main\n"}`,
	)

	c := &controlClient{out: make(chan []byte, 8)}
	srv.conversationOpen(c, paneID, "")
	page := decodePage(t, nextRaw(t, c))
	if !page.Supported || page.Agent != "anthropic" {
		t.Fatalf("page = %+v, want a supported chat page", page)
	}
	if len(page.Entries) != 3 {
		t.Fatalf("entries = %+v, want 3", page.Entries)
	}
	if page.Entries[0].Kind != transcript.KindPrompt || page.Entries[0].Text != "add a health endpoint" {
		t.Errorf("prompt entry = %+v", page.Entries[0])
	}
	if page.Entries[1].Kind != transcript.KindReply || page.Entries[1].Markdown != "Right away." {
		t.Errorf("reply entry = %+v", page.Entries[1])
	}
	tool := page.Entries[2]
	if tool.Kind != transcript.KindTool || tool.Label != "Read main.go" || tool.Status != transcript.StatusOK {
		t.Errorf("tool entry = %+v", tool)
	}

	appendJSONL(t, path, `{"type":"assistant","ts":"2026-09-14T10:00:03Z","text":"a second reply"}`)
	srv.ConversationHookEvent(paneID)
	raw := nextRaw(t, c)
	if msgType(t, raw) != "conversationAppend" {
		t.Fatalf("got %s, want conversationAppend", raw)
	}
	var app conversationAppendMsg
	if err := json.Unmarshal(raw, &app); err != nil {
		t.Fatal(err)
	}
	if len(app.Entries) != 1 || app.Entries[0].Markdown != "a second reply" {
		t.Errorf("appended entries = %+v", app.Entries)
	}
}

// TestConversationOlderAndDetailChatPane covers the other two commands the
// design says must "just work" for a chat pane with no changes of their
// own: conversationOlder pages backward, and conversationDetail fetches a
// tool result too large to arrive inline -- both are generic over
// transcript.Entry/Stream and never mention which adapter produced them.
func TestConversationOlderAndDetailChatPane(t *testing.T) {
	srv, ws := newTestServer(t)
	paneID := addAgentPane(t, srv, ws, "anthropic")
	path := chatTranscriptPath(t, paneID)

	var lines []string
	for i := 0; i < 60; i++ {
		lines = append(lines, `{"type":"user","ts":"2026-09-14T10:00:00Z","text":"message `+itoa(i)+`"}`)
	}
	full := strings.Repeat("a line of output\n", 400) // well over the 4KB cap
	fullJSON, err := json.Marshal(full)
	if err != nil {
		t.Fatal(err)
	}
	lines = append(lines, `{"type":"tool","ts":"2026-09-14T10:00:01Z","tool":"run_command","call":"run_command go test ./...","text":`+string(fullJSON)+`}`)
	writeChatJSONL(t, path, lines...)

	c := &controlClient{out: make(chan []byte, 8)}
	srv.conversationOpen(c, paneID, "")
	page := decodePage(t, nextRaw(t, c))
	if len(page.Entries) != conversationPageSize {
		t.Fatalf("first page = %d entries, want %d", len(page.Entries), conversationPageSize)
	}
	if page.Entries[0].Text != "message 11" {
		t.Errorf("first page starts at %q, want \"message 11\"", page.Entries[0].Text)
	}
	tool := page.Entries[len(page.Entries)-1]
	if !tool.HasDetail {
		t.Fatalf("tool entry = %+v, want hasDetail", tool)
	}

	older := &controlClient{out: make(chan []byte, 8)}
	srv.conversationOlder(older, paneID, page.Entries[0].ID)
	olderPage := decodePage(t, nextRaw(t, older))
	if !olderPage.AtStart {
		t.Error("the page reaching the first entry should say atStart")
	}
	if len(olderPage.Entries) != 11 || olderPage.Entries[0].Text != "message 0" {
		t.Fatalf("older page = %+v", olderPage.Entries)
	}

	srv.conversationDetailReq(c, paneID, tool.ID)
	raw := nextRaw(t, c)
	if msgType(t, raw) != "conversationDetail" {
		t.Fatalf("got %s, want conversationDetail", raw)
	}
	var d conversationDetailMsg
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	if d.Detail.Text != full {
		t.Errorf("detail = %d bytes, want the full %d-byte output", len(d.Detail.Text), len(full))
	}
}

// TestConversationChatClearResetsInPlace covers a chat pane's /clear, which,
// unlike Claude Code's, keeps the same session id and file (see
// transcript.Stream's Reset method, added for exactly this): a client
// already watching must still be told to drop what it has, not shown a
// growing tail underneath entries that are no longer part of the
// conversation.
func TestConversationChatClearResetsInPlace(t *testing.T) {
	srv, ws := newTestServer(t)
	paneID := addAgentPane(t, srv, ws, "anthropic")
	path := chatTranscriptPath(t, paneID)
	writeChatJSONL(t, path, `{"type":"user","ts":"2026-09-14T10:00:00Z","text":"before clear"}`)

	c := &controlClient{out: make(chan []byte, 8)}
	srv.conversationOpen(c, paneID, "")
	before := decodePage(t, nextRaw(t, c))
	if len(before.Entries) != 1 || before.Entries[0].Text != "before clear" {
		t.Fatalf("entries before clear = %+v", before.Entries)
	}

	appendJSONL(t, path, `{"type":"clear"}`, `{"type":"user","ts":"2026-09-14T10:05:00Z","text":"after clear"}`)
	srv.ConversationHookEvent(paneID)

	raw := nextRaw(t, c)
	if msgType(t, raw) != "conversationPage" {
		t.Fatalf("got %s, want a resetting conversationPage", raw)
	}
	after := decodePage(t, raw)
	if !after.Reset {
		t.Error("a /clear that keeps the same session id should still arrive with reset:true")
	}
	if len(after.Entries) != 1 || after.Entries[0].Text != "after clear" {
		t.Fatalf("entries after clear = %+v", after.Entries)
	}
}
