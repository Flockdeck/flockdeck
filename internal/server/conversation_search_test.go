package server

import (
	"encoding/json"
	"testing"
)

func decodeSearch(t *testing.T, raw []byte) conversationSearchMsg {
	t.Helper()
	var msg conversationSearchMsg
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("unmarshal conversationSearch: %v", err)
	}
	return msg
}

// TestConversationSearchFindsOldEntryBeyondFirstPage covers the reason this
// searches the stream's own snapshot rather than one page at a time: a hit
// far older than the newest conversationPageSize entries must still be
// found.
func TestConversationSearchFindsOldEntryBeyondFirstPage(t *testing.T) {
	srv, ws := newTestServer(t)
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	paneID := addAgentPane(t, srv, ws, "claude")
	root := ws.ActiveRoot()

	var lines []string
	lines = append(lines, `{"type":"user","uuid":"u0","message":{"role":"user","content":"which port did we pick"}}`)
	for i := 1; i < 60; i++ {
		lines = append(lines, `{"type":"user","uuid":"u`+itoa(i)+`","message":{"role":"user","content":"message `+itoa(i)+`"}}`)
	}
	writeClaudeJSONL(t, claudeTranscriptPath(home, root, paneID), lines...)

	c := &controlClient{out: make(chan []byte, 8)}
	srv.conversationOpen(c, paneID, "")
	decodePage(t, nextRaw(t, c))

	srv.conversationSearch(c, paneID, "port")
	res := decodeSearch(t, nextRaw(t, c))
	if len(res.Matches) != 1 {
		t.Fatalf("matches = %+v, want exactly the one old entry", res.Matches)
	}
	if res.Matches[0].EntryID != "u0" {
		t.Errorf("match entry = %q, want u0", res.Matches[0].EntryID)
	}
	if res.Matches[0].Kind != "prompt" {
		t.Errorf("match kind = %q, want prompt", res.Matches[0].Kind)
	}
	if res.Matches[0].Snippet == "" {
		t.Error("no snippet on the match")
	}
}

// TestConversationSearchIsCaseInsensitive covers matching regardless of case
// in either the query or the conversation.
func TestConversationSearchIsCaseInsensitive(t *testing.T) {
	srv, ws := newTestServer(t)
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	paneID := addAgentPane(t, srv, ws, "claude")
	root := ws.ActiveRoot()
	writeClaudeJSONL(t, claudeTranscriptPath(home, root, paneID),
		`{"type":"assistant","uuid":"a1","message":{"role":"assistant","content":"Started the Server on Port 4000."}}`,
	)

	c := &controlClient{out: make(chan []byte, 8)}
	srv.conversationOpen(c, paneID, "")
	decodePage(t, nextRaw(t, c))

	srv.conversationSearch(c, paneID, "SERVER")
	res := decodeSearch(t, nextRaw(t, c))
	if len(res.Matches) != 1 || res.Matches[0].EntryID != "a1" {
		t.Fatalf("matches = %+v, want a case-insensitive hit on a1", res.Matches)
	}
	want := "Started the Server"
	if len(res.Matches[0].Snippet) < len(want) || res.Matches[0].Snippet[:len(want)] != want {
		t.Errorf("snippet = %q, want it to start with %q", res.Matches[0].Snippet, want)
	}
	if got := []rune(res.Matches[0].Snippet)[res.Matches[0].Offset : res.Matches[0].Offset+6]; string(got) != "Server" {
		t.Errorf("offset %d into %q = %q, want \"Server\"", res.Matches[0].Offset, res.Matches[0].Snippet, string(got))
	}
}

// TestConversationSearchCapsAndSaysMore covers the ~50-match cap: more hits
// than that are truncated, newest first, with more:true.
func TestConversationSearchCapsAndSaysMore(t *testing.T) {
	srv, ws := newTestServer(t)
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	paneID := addAgentPane(t, srv, ws, "claude")
	root := ws.ActiveRoot()

	var lines []string
	for i := 0; i < 60; i++ {
		lines = append(lines, `{"type":"user","uuid":"u`+itoa(i)+`","message":{"role":"user","content":"needle `+itoa(i)+`"}}`)
	}
	writeClaudeJSONL(t, claudeTranscriptPath(home, root, paneID), lines...)

	c := &controlClient{out: make(chan []byte, 8)}
	srv.conversationOpen(c, paneID, "")
	decodePage(t, nextRaw(t, c))

	srv.conversationSearch(c, paneID, "needle")
	res := decodeSearch(t, nextRaw(t, c))
	if len(res.Matches) != conversationSearchMaxMatches {
		t.Fatalf("matches = %d, want the cap of %d", len(res.Matches), conversationSearchMaxMatches)
	}
	if !res.More {
		t.Error("more = false, want true past the cap")
	}
	if res.Matches[0].EntryID != "u59" {
		t.Errorf("first match = %q, want the newest entry u59", res.Matches[0].EntryID)
	}
}

// TestConversationSearchUnopenedPaneAnswersEmpty covers a pane the asking
// phone has never opened: search must not promote its stream to full just to
// search it, and answers empty instead.
func TestConversationSearchUnopenedPaneAnswersEmpty(t *testing.T) {
	srv, ws := newTestServer(t)
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	paneID := addAgentPane(t, srv, ws, "claude")
	root := ws.ActiveRoot()
	writeClaudeJSONL(t, claudeTranscriptPath(home, root, paneID),
		`{"type":"user","uuid":"u1","message":{"role":"user","content":"hello there"}}`,
	)
	// A hook event, never a conversationOpen: this is the inbox-preview path,
	// which keeps the stream light -- nobody has this pane's chat view open.
	srv.ConversationHookEvent(paneID)

	c := &controlClient{out: make(chan []byte, 8)}
	srv.conversationSearch(c, paneID, "hello")
	res := decodeSearch(t, nextRaw(t, c))
	if len(res.Matches) != 0 {
		t.Fatalf("matches = %+v, want none for a pane nobody has open", res.Matches)
	}

	pc, ok := srv.convos.lookup(paneID)
	if !ok {
		t.Fatal("no conversation entry after a hook event")
	}
	pc.mu.Lock()
	n := pc.stream.EntryCount()
	pc.mu.Unlock()
	if n != 0 {
		t.Errorf("EntryCount = %d after a search, want the stream still light (0)", n)
	}
}

// TestConversationSearchNonPlainQueryMatchesLiterally covers refusing to
// treat a query as a regular expression: ".*" must match only that literal
// two-character substring, not "anything".
func TestConversationSearchNonPlainQueryMatchesLiterally(t *testing.T) {
	srv, ws := newTestServer(t)
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	paneID := addAgentPane(t, srv, ws, "claude")
	root := ws.ActiveRoot()
	writeClaudeJSONL(t, claudeTranscriptPath(home, root, paneID),
		`{"type":"user","uuid":"u1","message":{"role":"user","content":"the pattern .* is not special here"}}`,
		`{"type":"user","uuid":"u2","message":{"role":"user","content":"nothing to match in this one"}}`,
	)

	c := &controlClient{out: make(chan []byte, 8)}
	srv.conversationOpen(c, paneID, "")
	decodePage(t, nextRaw(t, c))

	srv.conversationSearch(c, paneID, ".*")
	res := decodeSearch(t, nextRaw(t, c))
	if len(res.Matches) != 1 || res.Matches[0].EntryID != "u1" {
		t.Fatalf("matches = %+v, want only u1's literal \".*\"", res.Matches)
	}
}

// TestConversationSearchIgnoresBlankQuery covers a whitespace-only q: refused
// with an empty answer rather than matching every entry.
func TestConversationSearchIgnoresBlankQuery(t *testing.T) {
	srv, ws := newTestServer(t)
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	paneID := addAgentPane(t, srv, ws, "claude")
	root := ws.ActiveRoot()
	writeClaudeJSONL(t, claudeTranscriptPath(home, root, paneID),
		`{"type":"user","uuid":"u1","message":{"role":"user","content":"hello there"}}`,
	)

	c := &controlClient{out: make(chan []byte, 8)}
	srv.conversationOpen(c, paneID, "")
	decodePage(t, nextRaw(t, c))

	srv.conversationSearch(c, paneID, "   ")
	res := decodeSearch(t, nextRaw(t, c))
	if len(res.Matches) != 0 {
		t.Fatalf("matches = %+v, want none for a whitespace-only query", res.Matches)
	}
}

// TestConversationSearchMatchesToolSummary covers the third field this
// searches -- a tool's own summary, not just its label -- and that the query
// finds it even though it names neither the tool nor the command that
// produced it.
func TestConversationSearchMatchesToolSummary(t *testing.T) {
	srv, ws := newTestServer(t)
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	paneID := addAgentPane(t, srv, ws, "claude")
	root := ws.ActiveRoot()
	writeClaudeJSONL(t, claudeTranscriptPath(home, root, paneID),
		`{"type":"assistant","uuid":"a1","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu1","name":"Bash","input":{"command":"go run ."}}]}}`,
		`{"type":"user","uuid":"u1","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu1","content":"listening on port 4000"}]}}`,
	)

	c := &controlClient{out: make(chan []byte, 8)}
	srv.conversationOpen(c, paneID, "")
	decodePage(t, nextRaw(t, c))

	srv.conversationSearch(c, paneID, "port 4000")
	res := decodeSearch(t, nextRaw(t, c))
	if len(res.Matches) != 1 || res.Matches[0].Kind != "tool" {
		t.Fatalf("matches = %+v, want one tool hit on its summary", res.Matches)
	}
}
