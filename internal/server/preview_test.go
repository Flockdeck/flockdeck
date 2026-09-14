package server

import (
	"strings"
	"testing"
)

// TestPreviewPresentForAgentPaneNeverOpened covers the inbox's whole point:
// a phone that has never opened this pane's chat view still gets a preview
// of its latest reply, built from the hook signal alone -- no conversationOpen
// call at all, which is how the real hook-driven push works while nobody's
// phone is looking.
func TestPreviewPresentForAgentPaneNeverOpened(t *testing.T) {
	srv, ws := newTestServer(t)
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	paneID := addAgentPane(t, srv, ws, "claude")
	root := ws.ActiveRoot()

	writeClaudeJSONL(t, claudeTranscriptPath(home, root, paneID),
		`{"type":"user","uuid":"u1","timestamp":"2026-09-14T10:00:00Z","message":{"role":"user","content":"hello"}}`,
		`{"type":"assistant","uuid":"a1","timestamp":"2026-09-14T10:00:01Z","message":{"role":"assistant","content":[{"type":"text","text":"**hi** there, friend"}]}}`,
	)
	srv.ConversationHookEvent(paneID)

	pv, ok := ask(srv, func() paneView { return srv.snapshot().Panes[paneID] })
	if !ok {
		t.Fatal("the workspace did not answer")
	}
	if pv.ID != paneID {
		t.Fatalf("pane %q missing from the snapshot: %+v", paneID, pv)
	}
	if pv.Last == nil {
		t.Fatal("an agent pane with a reply should carry a preview, even unopened")
	}
	if pv.Last.Kind != "reply" {
		t.Errorf("kind = %q, want reply", pv.Last.Kind)
	}
	if pv.Last.TS == "" {
		t.Error("preview has no timestamp")
	}
	if pv.Last.Text != "hi there, friend" {
		t.Errorf("text = %q, want markdown stripped", pv.Last.Text)
	}
}

// TestPreviewPresentForChatPaneNeverOpened is TestPreviewPresentForAgentPaneNeverOpened's
// phase-2 counterpart: a pane running Flockdeck's own chat client gets an
// inbox preview the same way, from notePreview's own generic reading of
// transcript.Entry -- nothing here is specific to which adapter produced it.
func TestPreviewPresentForChatPaneNeverOpened(t *testing.T) {
	srv, ws := newTestServer(t)
	paneID := addAgentPane(t, srv, ws, "anthropic")

	writeChatJSONL(t, chatTranscriptPath(t, paneID),
		`{"type":"user","ts":"2026-09-14T10:00:00Z","text":"hello"}`,
		`{"type":"assistant","ts":"2026-09-14T10:00:01Z","text":"**hi** there, friend"}`,
	)
	srv.ConversationHookEvent(paneID)

	pv, ok := ask(srv, func() paneView { return srv.snapshot().Panes[paneID] })
	if !ok {
		t.Fatal("the workspace did not answer")
	}
	if pv.Last == nil {
		t.Fatal("a chat pane with a reply should carry a preview, even unopened")
	}
	if pv.Last.Kind != "reply" {
		t.Errorf("kind = %q, want reply", pv.Last.Kind)
	}
	if pv.Last.Text != "hi there, friend" {
		t.Errorf("text = %q, want markdown stripped", pv.Last.Text)
	}
}

// TestPreviewUpdatesOnGrowth covers the "updated when the conversation grows"
// half of the contract: a second reply replaces the first as the preview,
// again from ConversationHookEvent alone.
func TestPreviewUpdatesOnGrowth(t *testing.T) {
	srv, ws := newTestServer(t)
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	paneID := addAgentPane(t, srv, ws, "claude")
	root := ws.ActiveRoot()
	path := claudeTranscriptPath(home, root, paneID)

	writeClaudeJSONL(t, path,
		`{"type":"assistant","uuid":"a1","message":{"role":"assistant","content":[{"type":"text","text":"first reply"}]}}`,
	)
	srv.ConversationHookEvent(paneID)

	appendJSONL(t, path,
		`{"type":"assistant","uuid":"a2","message":{"role":"assistant","content":[{"type":"text","text":"second reply"}]}}`,
	)
	srv.ConversationHookEvent(paneID)

	last, ok := srv.preview.get(paneID)
	if !ok {
		t.Fatal("no preview after two replies")
	}
	if last.Text != "second reply" {
		t.Errorf("text = %q, want the newest reply", last.Text)
	}
}

// TestPreviewAbsentForShellPane covers the "only compute it for agent panes"
// rule: a shell pane never gets a preview, whatever hook events arrive for
// it (which, in practice, a shell never sends -- but the field must still be
// left out of its state if one somehow did).
func TestPreviewAbsentForShellPane(t *testing.T) {
	srv, ws := newTestServer(t)
	// newTestServer already opened one shell pane, so nothing needs to be
	// started here.
	paneID, ok := ask(srv, func() string { return ws.Tabs[0].Tree.Panes()[0] })
	if !ok {
		t.Fatal("the workspace did not answer")
	}

	srv.ConversationHookEvent(paneID)

	if _, ok := srv.preview.get(paneID); ok {
		t.Error("a shell pane should never get a preview")
	}
	pv, ok := ask(srv, func() paneView { return srv.snapshot().Panes[paneID] })
	if !ok {
		t.Fatal("the workspace did not answer")
	}
	if pv.ID != paneID {
		t.Fatalf("pane %q missing from the snapshot: %+v", paneID, pv)
	}
	if pv.Last != nil {
		t.Errorf("last = %+v, want nil for a shell pane", pv.Last)
	}
}

// TestPreviewTextStripsMarkdownAndCaps covers previewText directly: common
// markdown syntax removed, whitespace collapsed, and a long reply capped
// rather than sent whole -- an inbox row is a line or two, not the chat view.
func TestPreviewTextStripsMarkdownAndCaps(t *testing.T) {
	md := "# Heading\n\nSome **bold** and _italic_ and `code`, a [link](https://example.com), and:\n- one\n- two\n\n> a quote"
	got := previewText(md)
	for _, bad := range []string{"#", "**", "_italic_", "`code`", "[link]", "(https://example.com)", "- one", ">"} {
		if strings.Contains(got, bad) {
			t.Errorf("previewText(%q) = %q, still contains markdown %q", md, got, bad)
		}
	}
	if strings.Contains(got, "\n") {
		t.Errorf("previewText(%q) = %q, whitespace not collapsed", md, got)
	}

	long := strings.Repeat("word ", 100)
	capped := previewText(long)
	if r := []rune(capped); len(r) != lastReplyCap+1 || r[len(r)-1] != '…' {
		t.Errorf("previewText of a long reply = %q (len %d), want %d runes plus an ellipsis", capped, len(r), lastReplyCap)
	}
}
