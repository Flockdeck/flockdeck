package server

import (
	"testing"

	"github.com/jmwri/flockdeck/internal/hooks"
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
