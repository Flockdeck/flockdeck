package server

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
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

func waitEntries(t *testing.T, e *structuredEnv, n int) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		if es, _ := e.ws.StructuredEntries(e.child); len(es) >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("fewer than %d entries", n)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// The state push that follows any change is what drives the appends, not a call
// made by the test to the function under it.
func TestTheStateBroadcastDrivesTheStructuredAppends(t *testing.T) {
	e := newStructuredEnv(t, "plain")
	e.status(t, session.StatusIdle)
	c := e.watcher(t, 256)
	structuredPage(t, e, c, "")
	if err := e.ws.StructuredRuntime(e.child).Send("from the broadcast"); err != nil {
		t.Fatal(err)
	}
	var got []transcript.Entry
	deadline := time.After(20 * time.Second)
	for !containsText(got, "from the broadcast") {
		e.srv.broadcastState()
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
			t.Fatal("the state broadcast never produced an append")
		}
	}
}

// Many pushes in one interval are one re-read.
func TestManyStatePushesAreOneReReadOfTheStructuredPanes(t *testing.T) {
	e := newStructuredEnv(t, "plain")
	e.status(t, session.StatusIdle)
	c := &controlClient{out: make(chan []byte, 256)}
	structuredPage(t, e, c, "")
	var n atomic.Int64
	structuredConversationRead = func() { n.Add(1) }
	t.Cleanup(func() { structuredConversationRead = nil })
	for i := 0; i < 20; i++ {
		e.srv.pushStructuredConversations()
	}
	time.Sleep(3 * structuredInterval)
	if got := n.Load(); got != 1 {
		t.Fatalf("%d re-reads for 20 pushes", got)
	}
	// Once it has run, the next push arms another.
	e.srv.pushStructuredConversations()
	time.Sleep(3 * structuredInterval)
	if got := n.Load(); got != 2 {
		t.Fatalf("%d re-reads after a later push", got)
	}
}

// Only a structured pane someone is watching is re-read.
func TestOnlyWatchedStructuredPanesAreReRead(t *testing.T) {
	e := newStructuredEnv(t, "plain")
	e.status(t, session.StatusIdle)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	term := addAgentPane(t, e.srv, e.ws, "claude")

	// A structured pane opened and closed again, and a transcript pane watched.
	a := &controlClient{out: make(chan []byte, 16)}
	structuredPage(t, e, a, "")
	e.srv.conversationClose(a, e.child)
	b := &controlClient{out: make(chan []byte, 16)}
	e.srv.conversationOpen(b, term, "")
	nextRaw(t, b)
	if got := e.srv.convos.watchedStructured(); len(got) != 0 {
		t.Fatalf("watched structured panes = %v", got)
	}
	// Watched again, it is the only one.
	structuredPage(t, e, a, "")
	if got := e.srv.convos.watchedStructured(); len(got) != 1 || got[0] != e.child {
		t.Fatalf("watched structured panes = %v, want [%s]", got, e.child)
	}
}

// A structured conversation of more than a page is paged back through the hub to
// its first entry, every entry once.
func TestAStructuredConversationPagesBackThroughTheHub(t *testing.T) {
	e := newStructuredEnv(t, "plain")
	e.status(t, session.StatusIdle)
	rt := e.ws.StructuredRuntime(e.child)
	for i := 0; i < 60; i++ {
		// The queue holds only a few prompts: send again once there is room.
		for tries := 0; rt.Send(fmt.Sprintf("prompt %d", i)) != nil; tries++ {
			if tries > 1200 {
				t.Fatal("the prompt queue never emptied")
			}
			time.Sleep(25 * time.Millisecond)
		}
	}
	waitEntries(t, e, 110)
	e.status(t, session.StatusIdle)
	want, _ := e.ws.StructuredEntries(e.child)
	c := &controlClient{out: make(chan []byte, 16)}
	page := structuredPage(t, e, c, "")
	if len(page.Entries) != conversationPageSize || page.AtStart {
		t.Fatalf("first page: %d entries, atStart %v", len(page.Entries), page.AtStart)
	}
	got := append([]string(nil), idsOf(page.Entries)...)
	first := page.Entries[0].ID
	for pages := 0; ; pages++ {
		if pages > len(want) {
			t.Fatal("paging does not end")
		}
		e.srv.conversationOlder(c, e.child, first)
		older := decodePage(t, nextRaw(t, c))
		if len(older.Entries) > conversationPageSize {
			t.Fatalf("a page of %d", len(older.Entries))
		}
		if !older.AtStart && len(older.Entries) != conversationPageSize {
			t.Fatalf("a page of %d that is not the start", len(older.Entries))
		}
		got = append(idsOf(older.Entries), got...)
		if older.AtStart {
			break
		}
		first = older.Entries[0].ID
	}
	if len(got) != len(want) {
		t.Fatalf("%d entries paged, %d held", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i].ID {
			t.Fatalf("entry %d is %s, want %s", i, got[i], want[i].ID)
		}
	}
}

func idsOf(entries []transcript.Entry) []string {
	var out []string
	for _, en := range entries {
		out = append(out, en.ID)
	}
	return out
}
