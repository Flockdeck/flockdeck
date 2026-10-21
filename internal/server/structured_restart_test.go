package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/session"
)

// A restart starts a fresh agent session. The pane keeps its earlier
// conversation and a notice that says where it was restarted, and a window that
// reads the pane through structuredSnapshot, structuredWatch or structuredDetail
// sees all of it, not only the new session.

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
		replies := 0
		for _, en := range m.Entries {
			if en.Kind == "reply" {
				replies++
			}
		}
		if m.Status == "idle" && replies >= 2 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no second reply: status %q, %d entries", m.Status, len(m.Entries))
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func kindsOf(m structuredMsg) []string {
	var out []string
	for _, en := range m.Entries {
		out = append(out, en.Kind)
	}
	return out
}

func idsUnique(m structuredMsg) (string, bool) {
	seen := map[string]bool{}
	for _, en := range m.Entries {
		if seen[en.ID] {
			return en.ID, false
		}
		seen[en.ID] = true
	}
	return "", true
}

// After a restart the snapshot holds the first session's prompt and reply, the
// restart notice, and the second session's prompt and reply, in that order, each
// with an id of its own.
func TestStructuredSnapshotKeepsTheEarlierSessionAfterARestart(t *testing.T) {
	e := newStructuredEnv(t, "plain")
	e.status(t, session.StatusIdle)
	before := e.snapshotOf(t, e.child)
	e.restart(t)
	e.promptAndWait(t, "plain")

	m := e.snapshotOf(t, e.child)
	want := []string{"prompt", "reply", "notice", "prompt", "reply"}
	if got := kindsOf(m); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("entries after a restart = %v, want %v", got, want)
	}
	if id, ok := idsUnique(m); !ok {
		t.Errorf("entry id %q appears twice, so a window keying rows by id would mix them up", id)
	}
	if !strings.Contains(m.Entries[2].Text, "restarted") {
		t.Errorf("the notice says %q", m.Entries[2].Text)
	}
	if len(before.Entries) != 2 || m.Entries[1].Markdown != before.Entries[1].Markdown {
		t.Errorf("the first session's reply changed: %+v then %+v", before.Entries, m.Entries[:2])
	}
	if m.Older != 0 {
		t.Errorf("older = %d for a short conversation", m.Older)
	}
}

// A watching window is pushed the earlier session too, and the push and the
// snapshot say the same.
func TestStructuredWatchKeepsTheEarlierSessionAfterARestart(t *testing.T) {
	e := newStructuredEnv(t, "plain")
	e.status(t, session.StatusIdle)
	c := e.watcher(t, 64)
	structuredReply(t, e.srv, c, command{Cmd: "structuredWatch", ID: e.child, Req: "w1"}, "structured")
	e.restart(t)
	e.promptAndWait(t, "plain")
	e.push()

	var last structuredMsg
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && len(last.Entries) < 5 {
		for _, raw := range drain(c) {
			if typeOf(raw) == "structured" {
				_ = json.Unmarshal(raw, &last)
			}
		}
		if len(last.Entries) < 5 {
			e.push()
			time.Sleep(50 * time.Millisecond)
		}
	}
	snap := e.snapshotOf(t, e.child)
	if strings.Join(kindsOf(last), ",") != strings.Join(kindsOf(snap), ",") || len(last.Entries) != 5 {
		t.Fatalf("the push has %v, the snapshot %v", kindsOf(last), kindsOf(snap))
	}
	for i := range snap.Entries {
		if last.Entries[i].ID != snap.Entries[i].ID {
			t.Errorf("entry %d: push id %q, snapshot id %q", i, last.Entries[i].ID, snap.Entries[i].ID)
		}
	}
}

// An entry of the earlier session can still be opened: its detail is what the
// entry carried, and the current session's entries keep their own detail.
func TestStructuredDetailOfAnEarlierSessionsEntryAfterARestart(t *testing.T) {
	e := newStructuredEnv(t, "plain")
	e.status(t, session.StatusIdle)
	first := e.snapshotOf(t, e.child).Entries[1]
	e.restart(t)
	e.promptAndWait(t, "plain")
	snap := e.snapshotOf(t, e.child)

	detail := func(entry string) (structuredDetailMsg, bool) {
		raw := structuredReply(t, e.srv, e.c, command{Cmd: "structuredDetail", ID: e.child, Entry: entry, Req: "d"}, "structuredDetail", "teamResult")
		var m structuredDetailMsg
		if typeOf(raw) != "structuredDetail" || json.Unmarshal(raw, &m) != nil {
			return m, false
		}
		return m, true
	}
	old := snap.Entries[1]
	if old.Markdown != first.Markdown {
		t.Fatalf("setup: %q vs %q", old.Markdown, first.Markdown)
	}
	if m, ok := detail(old.ID); !ok || !strings.Contains(m.Text, "Hello world") {
		t.Errorf("detail of the earlier reply %q = %+v (found %v)", old.ID, m, ok)
	}
	cur := snap.Entries[4]
	if m, ok := detail(cur.ID); !ok || !strings.Contains(m.Text, "Hello world") {
		t.Errorf("detail of the current reply %q = %+v (found %v)", cur.ID, m, ok)
	}
}
