package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/session/transcript"
)

// A restart starts a fresh agent session. The pane keeps its earlier
// conversation and a notice that says where it was restarted, and a window that
// reads the pane's conversation sees all of it, not only the new session.

// restart restarts the structured pane and waits for the new session to be idle.
func (e *structuredEnv) restart(t *testing.T) {
	t.Helper()
	old, _ := ask(e.srv, func() any { return e.ws.StructuredRuntime(e.child) })
	e.srv.handleCommand(e.c, command{Cmd: "restartPane", ID: e.child})
	deadline := time.Now().Add(30 * time.Second)
	for {
		now, _ := ask(e.srv, func() any { return e.ws.StructuredRuntime(e.child) })
		if now != old && now != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the pane kept its old runtime after restartPane")
		}
		time.Sleep(25 * time.Millisecond)
	}
	e.status(t, session.StatusIdle)
}

// prompt sends text to the pane as a window does and waits for the turn to end.
func (e *structuredEnv) promptAndWait(t *testing.T, text string) {
	t.Helper()
	e.srv.handleCommand(e.c, command{Cmd: "paneSend", ID: e.child, Text: text, Req: "p"})
	deadline := time.Now().Add(30 * time.Second)
	for {
		m := e.snapshotOf(t, e.child)
		entries := e.entries(t)
		replies := 0
		for _, en := range entries {
			if en.Kind == "reply" {
				replies++
			}
		}
		if m.Status == "idle" && replies >= 2 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no second reply: status %q, %d entries", m.Status, len(entries))
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func kindsOf(entries []transcript.Entry) []string {
	var out []string
	for _, en := range entries {
		out = append(out, en.Kind)
	}
	return out
}

func idsUnique(entries []transcript.Entry) (string, bool) {
	seen := map[string]bool{}
	for _, en := range entries {
		if seen[en.ID] {
			return en.ID, false
		}
		seen[en.ID] = true
	}
	return "", true
}

// After a restart the conversation holds the first session's prompt and reply, the
// restart notice, and the second session's prompt and reply, in that order, each
// with an id of its own.
func TestStructuredConversationKeepsTheEarlierSessionAfterARestart(t *testing.T) {
	e := newStructuredEnv(t, "plain")
	e.status(t, session.StatusIdle)
	before := e.entries(t)
	e.restart(t)
	e.promptAndWait(t, "plain")

	m := e.entries(t)
	want := []string{"prompt", "reply", "notice", "prompt", "reply"}
	if got := kindsOf(m); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("entries after a restart = %v, want %v", got, want)
	}
	if id, ok := idsUnique(m); !ok {
		t.Errorf("entry id %q appears twice, so a window keying rows by id would mix them up", id)
	}
	if !strings.Contains(m[2].Text, "restarted") {
		t.Errorf("the notice says %q", m[2].Text)
	}
	if len(before) != 2 || m[1].Markdown != before[1].Markdown {
		t.Errorf("the first session's reply changed: %+v then %+v", before, m[:2])
	}
}

// An entry of the earlier session can still be opened through conversationDetail:
// its detail is what the entry carried, and the current session's entries keep
// their own detail.
func TestConversationDetailOfAnEarlierSessionsEntryAfterARestart(t *testing.T) {
	e := newStructuredEnv(t, "plain")
	e.status(t, session.StatusIdle)
	first := e.entries(t)[1]
	e.restart(t)
	e.promptAndWait(t, "plain")
	entries := e.entries(t)

	c := &controlClient{out: make(chan []byte, 32)}
	structuredPage(t, e, c, "")
	detail := func(entry string) (conversationDetailMsg, bool) {
		e.srv.conversationDetailReq(c, e.child, entry)
		var m conversationDetailMsg
		if json.Unmarshal(nextRaw(t, c), &m) != nil || m.Gone {
			return m, false
		}
		return m, true
	}
	old := entries[1]
	if old.Markdown != first.Markdown {
		t.Fatalf("setup: %q vs %q", old.Markdown, first.Markdown)
	}
	if m, ok := detail(old.ID); !ok || !strings.Contains(m.Detail.Text, "Hello world") {
		t.Errorf("detail of the earlier reply %q = %+v (found %v)", old.ID, m, ok)
	}
	cur := entries[4]
	if m, ok := detail(cur.ID); !ok || !strings.Contains(m.Detail.Text, "Hello world") {
		t.Errorf("detail of the current reply %q = %+v (found %v)", cur.ID, m, ok)
	}
}
