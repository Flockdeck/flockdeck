package server

import (
	"testing"

	"github.com/jmwri/flockdeck/internal/hooks"
	"github.com/jmwri/flockdeck/internal/session"
)

// A message edge carries the newest delivery state of its message.
func TestGraphMessageEdgeCarriesTheDeliveryState(t *testing.T) {
	e := newGraphEnv(t)
	hookSrv := e.ws.HookServer()
	if _, err := hooks.Msg(hookSrv.BaseURL(), hookSrv.Token(), e.dev, e.boss, "status please"); err != nil {
		t.Fatal(err)
	}
	if got := firstMessageEdge(e.snapshotGraph(t)).Delivery; got != "queued" {
		t.Errorf("a new message is %q, want queued", got)
	}
	edge := firstMessageEdge(e.snapshotGraph(t))
	if _, err := e.ws.ReadMessage(e.boss, edge.ID); err != nil {
		t.Fatal(err)
	}
	if got := firstMessageEdge(e.snapshotGraph(t)).Delivery; got != "read" {
		t.Errorf("a read message is %q, want read", got)
	}
}

// The edge follows the message through notified and woken as well as read.
func TestGraphMessageEdgeFollowsEachDeliveryState(t *testing.T) {
	e := newGraphEnv(t)
	hookSrv := e.ws.HookServer()
	if _, err := hooks.Msg(hookSrv.BaseURL(), hookSrv.Token(), e.dev, e.boss, "status please"); err != nil {
		t.Fatal(err)
	}
	id := firstMessageEdge(e.snapshotGraph(t)).ID
	ask(e.srv, func() bool {
		e.ws.Pane(e.boss).Agent = "claude" // a pane whose agent has hooks
		e.ws.Pane(e.boss).Sess.SetStatus(session.StatusIdle, "")
		return true
	})
	if notice, _ := ask(e.srv, func() string { return e.ws.HookNotice(e.boss) }); notice == "" {
		t.Fatal("no hook notice was given")
	}
	if got := firstMessageEdge(e.snapshotGraph(t)).Delivery; got != "notified" {
		t.Errorf("after the hook notice: %q, want notified", got)
	}
	ask(e.srv, func() bool { e.ws.MarkMessagesWoken(e.boss, id); return true })
	if got := firstMessageEdge(e.snapshotGraph(t)).Delivery; got != "woken" {
		t.Errorf("after the wake: %q, want woken", got)
	}
}
