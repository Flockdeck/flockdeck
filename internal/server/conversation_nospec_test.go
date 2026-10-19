package server

import (
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/session"
)

// A pane that exists but runs no agent, a shell, is told its conversation is
// not supported, so a window opening it does not wait for a page that is never
// coming. The message is the one an agent without a chat view gets.
func TestConversationOpenOnAShellPaneSaysUnsupported(t *testing.T) {
	srv, ws := newTestServer(t)
	shell, _ := ask(srv, func() string { return ws.FocusedPane().ID })

	c := &controlClient{out: make(chan []byte, 8)}
	srv.handleCommand(c, command{Cmd: "conversationOpen", ID: shell})
	page := decodePage(t, nextRaw(t, c))
	if page.Type != "conversationPage" || page.ID != shell || page.Supported || len(page.Entries) != 0 {
		t.Fatalf("page = %+v, want an unsupported page with no entries for the shell", page)
	}

	// Nothing is retained for it: no watcher, so no later push.
	srv.ConversationHookEvent(shell)
	expectNoMessage(t, c, 300*time.Millisecond)
}

// An id the workspace does not have is still answered with nothing, so a
// client probing for panes learns nothing from the reply.
func TestConversationOpenOnAnUnknownPaneStaysSilent(t *testing.T) {
	srv, _ := newTestServer(t)
	c := &controlClient{out: make(chan []byte, 8)}
	srv.handleCommand(c, command{Cmd: "conversationOpen", ID: "no-such-pane"})
	srv.handleCommand(c, command{Cmd: "conversationOpen", ID: ""})
	expectNoMessage(t, c, 500*time.Millisecond)
}

// A pane closed after the window saw it is unknown, not a shell, and stays
// silent.
func TestConversationOpenOnAClosedPaneStaysSilent(t *testing.T) {
	srv, ws := newTestServer(t)
	id := addAgentPane(t, srv, ws, "claude")
	if closed, _ := ask(srv, func() bool { return ws.ClosePaneByID(id) }); !closed {
		t.Fatal("the pane was not closed")
	}
	if still, _ := ask(srv, func() bool { return ws.Pane(id) != nil }); still {
		t.Fatal("closing the pane left it in the workspace")
	}
	c := &controlClient{out: make(chan []byte, 8)}
	srv.handleCommand(c, command{Cmd: "conversationOpen", ID: id})
	expectNoMessage(t, c, 500*time.Millisecond)
}

// conversationSearch sent through handleCommand as a window on this machine
// sends it, against a pane in a tab that is not on screen: a hit older than
// the first page comes back with its kind and snippet, and the newest first.
func TestConversationSearchForALocalWindowOnAPaneOffScreen(t *testing.T) {
	srv, ws := newTestServer(t)
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	paneID := addAgentPane(t, srv, ws, "claude")
	ask(srv, func() bool {
		ws.NewTab(session.KindShell, ws.ActiveRoot(), "on screen")
		return true
	})
	if on, _ := ask(srv, func() bool { return ws.CurrentTab().Tree.Find(paneID) != nil }); on {
		t.Fatal("the agent pane is still on screen; the test needs it in another tab")
	}
	root := ws.ActiveRoot()

	lines := []string{`{"type":"user","uuid":"u0","message":{"role":"user","content":"which port did we pick"}}`}
	for i := 1; i < 60; i++ {
		lines = append(lines, `{"type":"user","uuid":"u`+itoa(i)+`","message":{"role":"user","content":"message `+itoa(i)+`"}}`)
	}
	lines = append(lines, `{"type":"assistant","uuid":"a1","message":{"role":"assistant","content":[{"type":"text","text":"We picked PORT 8080."}]}}`)
	writeClaudeJSONL(t, claudeTranscriptPath(home, root, paneID), lines...)

	c := &controlClient{out: make(chan []byte, 8)}
	srv.handleCommand(c, command{Cmd: "conversationOpen", ID: paneID})
	decodePage(t, nextRaw(t, c))

	srv.handleCommand(c, command{Cmd: "conversationSearch", ID: paneID, Q: "port"})
	raw := nextRaw(t, c)
	if msgType(t, raw) != "conversationSearch" {
		t.Fatalf("got %s, want conversationSearch", raw)
	}
	res := decodeSearch(t, raw)
	if res.ID != paneID || res.Q != "port" || res.More || len(res.Matches) != 2 {
		t.Fatalf("result = %+v, want two matches for port", res)
	}
	if !strings.HasPrefix(res.Matches[0].EntryID, "a1") || res.Matches[0].Kind != "reply" || res.Matches[1].EntryID != "u0" || res.Matches[1].Kind != "prompt" {
		t.Errorf("matches = %+v, want the newest first: the reply a1, then the prompt u0", res.Matches)
	}
	for _, m := range res.Matches {
		if m.Snippet == "" {
			t.Errorf("no snippet on %+v", m)
		}
	}
}

// Searching a shell pane, which has no conversation, is answered with no
// matches rather than not at all.
func TestConversationSearchOnAShellPaneAnswersEmpty(t *testing.T) {
	srv, ws := newTestServer(t)
	shell, _ := ask(srv, func() string { return ws.FocusedPane().ID })
	c := &controlClient{out: make(chan []byte, 8)}
	srv.handleCommand(c, command{Cmd: "conversationSearch", ID: shell, Q: "anything"})
	res := decodeSearch(t, nextRaw(t, c))
	if res.ID != shell || len(res.Matches) != 0 || res.More {
		t.Errorf("result = %+v, want an empty answer", res)
	}
}
