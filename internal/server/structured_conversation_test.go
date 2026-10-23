package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/session/transcript"
)

// A structured pane is read through the same conversation commands as any other
// agent pane, from the runtime's entries.

func structuredPage(t *testing.T, e *structuredEnv, c *controlClient, after string) conversationPageMsg {
	t.Helper()
	e.srv.conversationOpen(c, e.child, after)
	return decodePage(t, nextRaw(t, c))
}

func TestAStructuredPaneIsOpenedPagedAndDetailedThroughTheConversationCommands(t *testing.T) {
	e := newStructuredEnv(t, "plain")
	e.status(t, session.StatusIdle)
	c := &controlClient{out: make(chan []byte, 16)}
	page := structuredPage(t, e, c, "")
	if !page.Supported || len(page.Entries) == 0 || page.Cursor == "" {
		t.Fatalf("page = %+v", page)
	}
	if page.Agent == "" {
		t.Error("the page names no agent")
	}
	// Older with the first entry's id has nothing before it; with an unknown id
	// the hub stays silent, as it does for any pane.
	e.srv.conversationOlder(c, e.child, page.Entries[0].ID)
	older := decodePage(t, nextRaw(t, c))
	if !older.Supported || len(older.Entries) != 0 || !older.AtStart {
		t.Errorf("older = %+v", older)
	}
	e.srv.conversationOlder(c, e.child, "no-such-entry")
	expectNoMessage(t, c, 100*time.Millisecond)

	e.srv.conversationDetailReq(c, e.child, page.Entries[0].ID)
	var d conversationDetailMsg
	if err := json.Unmarshal(nextRaw(t, c), &d); err != nil || d.Entry != page.Entries[0].ID {
		t.Fatalf("detail = %+v, %v", d, err)
	}
	e.srv.conversationDetailReq(c, e.child, "no-such-entry")
	if err := json.Unmarshal(nextRaw(t, c), &d); err != nil || d.Detail.Text == "" {
		t.Fatalf("an unknown entry gave %+v, %v; want a notice", d, err)
	}

	e.srv.conversationSearch(c, e.child, "e")
	if m := nextRaw(t, c); msgType(t, m) != "conversationSearch" {
		t.Errorf("search answered %s", m)
	}
}

func TestAStructuredPaneAppendsArePushedAsConversationAppends(t *testing.T) {
	e := newStructuredEnv(t, "plain")
	e.status(t, session.StatusIdle)
	c := &controlClient{out: make(chan []byte, 64)}
	page := structuredPage(t, e, c, "")
	before := len(page.Entries)

	rt := e.ws.StructuredRuntime(e.child)
	if rt == nil {
		t.Fatal("no runtime")
	}
	if err := rt.Send("again"); err != nil {
		t.Fatal(err)
	}
	var got []transcript.Entry
	deadline := time.After(20 * time.Second)
	for !containsText(got, "again") {
		e.srv.pushStructuredConversations()
		select {
		case raw := <-c.out:
			if msgType(t, raw) == "conversationAppend" {
				var a conversationAppendMsg
				if err := json.Unmarshal(raw, &a); err != nil {
					t.Fatal(err)
				}
				got = append(got, a.Entries...)
			}
		case <-time.After(100 * time.Millisecond):
		case <-deadline:
			t.Fatalf("no append carried the new prompt; got %+v after %d", got, before)
		}
	}
}

func containsText(entries []transcript.Entry, s string) bool {
	for _, en := range entries {
		if strings.Contains(en.Text, s) || strings.Contains(en.Markdown, s) {
			return true
		}
	}
	return false
}

func TestARestartOfAStructuredPaneIsPushedAsAReset(t *testing.T) {
	e := newStructuredEnv(t, "plain")
	e.status(t, session.StatusIdle)
	c := &controlClient{out: make(chan []byte, 64)}
	page := structuredPage(t, e, c, "")
	last := page.Cursor

	if ok, _ := ask(e.srv, func() bool { return e.ws.RestartPaneByID(e.child) }); !ok {
		t.Fatal("restart refused")
	}
	e.status(t, session.StatusIdle)
	pc, _ := e.srv.convos.lookup(e.child)
	e.srv.refreshConversation(e.child, pc)
	var reset conversationPageMsg
	deadline := time.After(10 * time.Second)
	for !reset.Reset {
		select {
		case raw := <-c.out:
			if msgType(t, raw) == "conversationPage" {
				reset = decodePage(t, raw)
			}
		case <-deadline:
			t.Fatal("no reset page after a restart")
		}
	}
	if len(reset.Entries) == 0 || reset.Entries[0].ID == last && len(reset.Entries) == 1 {
		t.Errorf("reset = %+v", reset)
	}
	// A cursor of the old session is not one the new stream has: the window is
	// told to start again, not handed a gap.
	again := structuredPage(t, e, &controlClient{out: make(chan []byte, 8)}, last)
	if !again.Reset {
		t.Errorf("a cursor from before the restart gave %+v", again)
	}
}

// A window reached through the relay is given no conversation text of a
// structured pane, by any of the four commands, and is not made a watcher.
func TestARelayWindowGetsNothingOfAStructuredPanesConversation(t *testing.T) {
	e := newStructuredEnv(t, "plain")
	e.status(t, session.StatusIdle)
	for _, dev := range []string{"full-dev", "watch-dev"} {
		phone := &controlClient{out: make(chan []byte, 16), remote: true, device: dev}
		for _, cmd := range []command{
			{Cmd: "conversationOpen", ID: e.child},
			{Cmd: "conversationOlder", ID: e.child, Before: "e1"},
			{Cmd: "conversationDetail", ID: e.child, Entry: "e1"},
			{Cmd: "conversationSearch", ID: e.child, Q: "e"},
		} {
			if !phone.refuseRemoteText(cmd) {
				t.Errorf("%s %s: not refused", dev, cmd.Cmd)
			}
		}
		for len(phone.out) > 0 {
			raw := <-phone.out
			switch msgType(t, raw) {
			case "conversationPage":
				p := decodePage(t, raw)
				if p.Supported || len(p.Entries) != 0 || p.Cursor != "" {
					t.Errorf("%s: a page with content: %s", dev, raw)
				}
			case "notice":
			default:
				t.Errorf("%s: sent %s", dev, raw)
			}
		}
	}
	if pc, ok := e.srv.convos.lookup(e.child); ok {
		pc.mu.Lock()
		n := len(pc.watchers)
		pc.mu.Unlock()
		if n != 0 {
			t.Errorf("%d watchers", n)
		}
	}
}

// What the relay would hear of a structured pane's conversation, were the desk
// to send it, is dropped or emptied: every conversation message type is in the
// scrub registry, and nothing of the text survives.
func TestEveryConversationMessageOfAStructuredPaneIsScrubbedForTheRelay(t *testing.T) {
	words := []transcript.Entry{{ID: "e1", Kind: transcript.KindReply, Markdown: "secret words", Text: "secret words"}}
	msgs := map[string]any{
		"conversationPage":   conversationPageMsg{Type: "conversationPage", ID: "p", Supported: true, Entries: words, Cursor: "e1"},
		"conversationAppend": conversationAppendMsg{Type: "conversationAppend", ID: "p", Entries: words, Cursor: "e1"},
		"conversationDetail": conversationDetailMsg{Type: "conversationDetail", ID: "p", Entry: "e1", Detail: transcript.Detail{Text: "secret words"}},
		"conversationSearch": conversationSearchMsg{Type: "conversationSearch", ID: "p", Q: "secret", Matches: []conversationSearchHit{{EntryID: "e1", Snippet: "secret words"}}},
	}
	for typ, m := range msgs {
		if _, ok := relayScrubs[typ]; !ok {
			t.Errorf("%s is not in the relay scrub registry", typ)
		}
		raw, _ := json.Marshal(m)
		if out := scrubForRemote(raw); strings.Contains(string(out), "secret") {
			t.Errorf("%s reached the relay with its text: %s", typ, out)
		}
	}
}
