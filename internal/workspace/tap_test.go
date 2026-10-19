package workspace

import (
	"encoding/json"
	"testing"

	"github.com/jmwri/flockdeck/internal/hooks"
	"github.com/jmwri/flockdeck/internal/session"
)

func tapPanes(t *testing.T) (ws *Workspace, a, b, c string) {
	t.Helper()
	isolateConfig(t)
	root := t.TempDir()
	ws, err := New(Options{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ws.Close)
	tab := ws.NewTab(session.KindShell, root, "one")
	a = tab.Tree.Panes()[0]
	b = ws.NewTab(session.KindShell, root, "two").Tree.Panes()[0]
	c = ws.NewTab(session.KindShell, root, "three").Tree.Panes()[0]
	return ws, a, b, c
}

func sendMessageEvent(pane, tool, input string) hooks.Event {
	return hooks.Event{
		SessionID: pane, Event: "PostToolUse", Tool: tool,
		Detail: &hooks.Detail{Input: json.RawMessage(input)},
	}
}

func TestATappedCallResolvesToThePaneByPeerName(t *testing.T) {
	ws, a, b, _ := tapPanes(t)
	ws.SetPanePeerName(b, "researcher")
	ws.handleHook(sendMessageEvent(a, "SendMessage", `{"to":"Researcher","message":"secret words"}`))
	taps := ws.Taps()
	if len(taps) != 1 {
		t.Fatalf("taps = %+v", taps)
	}
	if tp := taps[0]; !tp.Understood || tp.From != a || tp.To != b || tp.Tool != "SendMessage" {
		t.Fatalf("tap = %+v", tp)
	}
}

func TestATappedCallToAnUnknownOrSharedNameHasNoPane(t *testing.T) {
	ws, a, b, c := tapPanes(t)
	ws.handleHook(sendMessageEvent(a, "SendMessage", `{"to":"nobody","message":"hi"}`))
	ws.SetPanePeerName(b, "twin")
	ws.SetPanePeerName(c, "twin")
	ws.handleHook(sendMessageEvent(a, "SendMessage", `{"to":"twin","message":"hi"}`))
	ws.SetPanePeerName(a, "me")
	ws.handleHook(sendMessageEvent(a, "SendMessage", `{"to":"me","message":"hi"}`))
	for i, tp := range ws.Taps() {
		if !tp.Understood || tp.To != "" {
			t.Errorf("tap %d = %+v, want understood with no pane", i, tp)
		}
	}
	if len(ws.Taps()) != 3 {
		t.Fatalf("taps = %d", len(ws.Taps()))
	}
}

func TestACallNotUnderstoodIsCountedWithoutContent(t *testing.T) {
	ws, a, _, _ := tapPanes(t)
	ws.handleHook(sendMessageEvent(a, "SendMessage", `{"unexpected":"shape"}`))
	ws.handleHook(hooks.Event{SessionID: a, Event: "PostToolUse", Tool: "SendMessage"})
	taps := ws.Taps()
	if len(taps) != 2 {
		t.Fatalf("taps = %+v", taps)
	}
	for _, tp := range taps {
		if tp.Understood || tp.To != "" || tp.From != a {
			t.Errorf("tap = %+v", tp)
		}
	}
}

func TestOnlyAFinishedSendMessageCallIsTapped(t *testing.T) {
	ws, a, b, _ := tapPanes(t)
	ws.SetPanePeerName(b, "r")
	in := `{"to":"r","message":"hi"}`
	pre := sendMessageEvent(a, "SendMessage", in)
	pre.Event = "PreToolUse"
	fail := sendMessageEvent(a, "SendMessage", in)
	fail.Event = "PostToolUseFailure"
	ws.handleHook(pre)
	ws.handleHook(fail)
	ws.handleHook(sendMessageEvent(a, "Bash", in))
	ws.handleHook(sendMessageEvent("no-such-pane", "SendMessage", in))
	if n := len(ws.Taps()); n != 0 {
		t.Fatalf("%d taps", n)
	}
}
