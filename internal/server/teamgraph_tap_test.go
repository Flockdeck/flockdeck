package server

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/hooks"
	"github.com/jmwri/flockdeck/internal/team"
)

func emitSendMessage(t *testing.T, e *graphEnv, pane, input string) {
	t.Helper()
	// A test run inside a Flockdeck pane inherits its launch id, which no pane of
	// the test workspace has.
	t.Setenv(hooks.LaunchEnv, "")
	srv := e.ws.HookServer()
	body := `{"session_id":"x","tool_name":"SendMessage","tool_input":` + input + `}`
	if _, err := hooks.Emit(strings.NewReader(body), srv.Endpoint(), srv.Token(), pane, "PostToolUse"); err != nil {
		t.Fatal(err)
	}
}

// A SendMessage call between two panes is a sent edge with a time and nothing
// else, and one the parser cannot read is counted, not drawn.
func TestGraphDrawsASentEdgeForATappedCall(t *testing.T) {
	schema := loadGraphSchema(t)
	e := newGraphEnv(t)
	ask(e.srv, func() bool { return e.ws.SetPanePeerName(e.dev, "developer") })
	emitSendMessage(t, e, e.boss, `{"to":"developer","message":"private words"}`)
	emitSendMessage(t, e, e.eyes, `{"surprise":true}`)

	raw := rawGraph(t, e)
	if errs := schema.Validate(raw); len(errs) != 0 {
		t.Fatalf("the graph does not match the schema: %v\n%s", errs, raw)
	}
	if strings.Contains(string(raw), "private words") {
		t.Fatal("the message text is in the graph")
	}
	var g graphMsg
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatal(err)
	}
	var sent []graphEdge
	for _, ed := range g.Edges {
		if ed.Kind == graphEdgeSent {
			sent = append(sent, ed)
		}
	}
	if len(sent) != 1 || sent[0].From != e.boss || sent[0].To != e.dev || sent[0].Time == "" || sent[0].ID != 0 || sent[0].Text != "" {
		t.Fatalf("sent edges = %+v", sent)
	}
	if g.SendMessageUnread != 1 {
		t.Errorf("sendMessageUnread = %d, want 1", g.SendMessageUnread)
	}
}

func TestSentEdgesNeverClaimRead(t *testing.T) {
	nodes := []graphNodeView{{}, {}}
	nodes[0].Pane, nodes[1].Pane = "a", "b"
	edges, unread := appendSentEdges(nil, nil, nodes)
	if len(edges) != 0 || unread != 0 {
		t.Fatalf("edges %v, unread %d", edges, unread)
	}
}

// A tap is drawn only when both ends are panes of the graph, and a call from a
// pane outside it is not counted as one of the project's.
func TestSentEdgesNeedBothEndsInTheGraph(t *testing.T) {
	nodes := []graphNodeView{{}, {}}
	nodes[0].Pane, nodes[1].Pane = "a", "b"
	taps := []team.Tap{
		{From: "a", To: "b", Understood: true},
		{From: "a", To: "elsewhere", Understood: true},
		{From: "elsewhere", To: "b", Understood: true},
		{From: "elsewhere", Understood: false},
		{From: "a", Understood: false},
	}
	edges, unread := appendSentEdges(nil, taps, nodes)
	if len(edges) != 1 || edges[0].From != "a" || edges[0].To != "b" {
		t.Fatalf("edges = %+v, want only a to b", edges)
	}
	if unread != 1 {
		t.Fatalf("unread = %d, want 1 (the pane outside the graph is not counted)", unread)
	}
}
