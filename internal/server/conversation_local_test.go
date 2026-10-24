package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/session"
)

// offScreenAgentPane opens an agent pane and then a second tab, so the pane
// is open but not in the tab on screen.
func offScreenAgentPane(t *testing.T, srv *Server, agentID string) string {
	t.Helper()
	ws := srv.ws
	paneID := addAgentPane(t, srv, ws, agentID)
	ask(srv, func() bool {
		ws.NewTab(session.KindShell, ws.ActiveRoot(), "on screen")
		return true
	})
	if on, _ := ask(srv, func() bool { return ws.CurrentTab().Tree.Find(paneID) != nil }); on {
		t.Fatal("the agent pane is still on screen; the test needs it in another tab")
	}
	return paneID
}

// The five commands, sent as a window on this machine sends them through
// handleCommand, work for a pane in a tab that is not on screen: first page,
// a live entry after the agent writes one, older entries, an entry's detail,
// and close.
func TestConversationCommandsWorkForALocalWindowOnAPaneOffScreen(t *testing.T) {
	srv, ws := newTestServer(t)
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	paneID := offScreenAgentPane(t, srv, "claude")
	root := ws.ActiveRoot()
	path := claudeTranscriptPath(home, root, paneID)

	full := strings.Repeat("a line of output\n", 400)
	resultJSON, err := json.Marshal(full)
	if err != nil {
		t.Fatal(err)
	}
	lines := []string{
		`{"type":"assistant","uuid":"a0","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu1","name":"Bash","input":{"command":"build"}}]}}`,
		`{"type":"user","uuid":"r0","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu1","content":` + string(resultJSON) + `}]}}`,
	}
	for i := 0; i < 59; i++ {
		lines = append(lines, `{"type":"user","uuid":"u`+itoa(i)+`","message":{"role":"user","content":"message `+itoa(i)+`"}}`)
	}
	writeClaudeJSONL(t, path, lines...)

	c := &controlClient{out: make(chan []byte, 16)}
	if c.remote {
		t.Fatal("the test client should be a local window")
	}

	// Open: the newest page.
	srv.handleCommand(c, command{Cmd: "conversationOpen", ID: paneID})
	page := decodePage(t, nextRaw(t, c))
	if !page.Supported || page.Agent != "claude" || len(page.Entries) != conversationPageSize || page.AtStart {
		t.Fatalf("first page = supported %v agent %q, %d entries, atStart %v", page.Supported, page.Agent, len(page.Entries), page.AtStart)
	}

	// Live tail.
	appendJSONL(t, path, `{"type":"assistant","uuid":"live1","message":{"role":"assistant","content":[{"type":"text","text":"written after the open"}]}}`)
	srv.ConversationHookEvent(paneID)
	raw := nextRaw(t, c)
	if msgType(t, raw) != "conversationAppend" {
		t.Fatalf("got %s, want conversationAppend", raw)
	}
	var app conversationAppendMsg
	if err := json.Unmarshal(raw, &app); err != nil {
		t.Fatal(err)
	}
	if len(app.Entries) != 1 || app.Entries[0].Markdown != "written after the open" {
		t.Fatalf("appended entries = %+v", app.Entries)
	}

	// Older.
	srv.handleCommand(c, command{Cmd: "conversationOlder", ID: paneID, Before: page.Entries[0].ID})
	older := decodePage(t, nextRaw(t, c))
	if !older.AtStart || len(older.Entries) != 10 {
		t.Fatalf("older page = %d entries, atStart %v; want the remaining 10 and atStart", len(older.Entries), older.AtStart)
	}

	// Detail.
	srv.handleCommand(c, command{Cmd: "conversationDetail", ID: paneID, Entry: "tu1"})
	raw = nextRaw(t, c)
	if msgType(t, raw) != "conversationDetail" {
		t.Fatalf("got %s, want conversationDetail", raw)
	}
	var d conversationDetailMsg
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	if d.Entry != "tu1" || d.Detail.Text != full {
		t.Errorf("detail = entry %q with %d bytes, want tu1 with %d", d.Entry, len(d.Detail.Text), len(full))
	}

	// Close: nothing more is pushed.
	srv.handleCommand(c, command{Cmd: "conversationClose", ID: paneID})
	appendJSONL(t, path, `{"type":"assistant","uuid":"live2","message":{"role":"assistant","content":[{"type":"text","text":"after the close"}]}}`)
	srv.ConversationHookEvent(paneID)
	expectNoMessage(t, c, 500*time.Millisecond)
}

// A pane whose agent has written nothing yet is answered with a supported,
// empty page that says it holds the whole conversation, not with the page an
// agent without a chat view gets, and the first line written reaches the
// window as a live entry.
func TestConversationOpenOnAPaneWithNoStoredConversation(t *testing.T) {
	srv, ws := newTestServer(t)
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	paneID := offScreenAgentPane(t, srv, "claude")
	path := claudeTranscriptPath(home, ws.ActiveRoot(), paneID)

	c := &controlClient{out: make(chan []byte, 8)}
	srv.handleCommand(c, command{Cmd: "conversationOpen", ID: paneID})
	page := decodePage(t, nextRaw(t, c))
	if !page.Supported || len(page.Entries) != 0 || !page.AtStart {
		t.Fatalf("page = %+v, want a supported, empty page at the start", page)
	}

	// The older request of a window that is showing the empty state is not a
	// failure either: it is ignored. A detail request is answered with gone, so
	// the window stops waiting for an entry that is not held.
	srv.handleCommand(c, command{Cmd: "conversationOlder", ID: paneID, Before: "x"})
	expectNoMessage(t, c, 300*time.Millisecond)
	srv.handleCommand(c, command{Cmd: "conversationDetail", ID: paneID, Entry: "x"})
	var gone conversationDetailMsg
	if err := json.Unmarshal(nextRaw(t, c), &gone); err != nil || !gone.Gone || gone.Entry != "x" || gone.ID != paneID {
		t.Fatalf("an unknown entry gave %+v, %v; want gone", gone, err)
	}
	expectNoMessage(t, c, 100*time.Millisecond)

	writeClaudeJSONL(t, path, `{"type":"user","uuid":"u1","message":{"role":"user","content":"first words"}}`)
	srv.ConversationHookEvent(paneID)
	raw := nextRaw(t, c)
	if msgType(t, raw) != "conversationAppend" {
		t.Fatalf("got %s, want conversationAppend for the first entry", raw)
	}
	srv.handleCommand(c, command{Cmd: "conversationClose", ID: paneID})
}
