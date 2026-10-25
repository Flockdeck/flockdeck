package server

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/hooks"
	"github.com/jmwri/flockdeck/internal/team"
)

// The window names a pane with paneName, which any window of the desk may do,
// and the label is in the state the window reads; empty text clears it. A gone
// pane is refused by name, as closePane and mutePane are.
func TestPaneNameCommandSetsAndClearsTheLabel(t *testing.T) {
	srv, ws := newTestServer(t)
	conn := dialControl(t, srv)
	nextState(t, conn, nil)
	id := firstPane(t, srv, ws)

	sendCmd(t, conn, command{Cmd: "paneName", ID: id, Text: "  rate limiter\nPR  "})
	st := nextState(t, conn, func(s stateMsg) bool { return s.Panes[id].Label != "" })
	if got := st.Panes[id].Label; got != "rate limiter PR" || st.Panes[id].LabelAuto {
		t.Errorf("label = %q (auto %v), want the cleaned text the person chose", got, st.Panes[id].LabelAuto)
	}

	sendCmd(t, conn, command{Cmd: "paneName", ID: id, Text: ""})
	nextState(t, conn, func(s stateMsg) bool { return s.Panes[id].Label == "" })

	sendCmd(t, conn, command{Cmd: "paneName", ID: "a-pane-that-has-gone", Text: "x"})
	var note noticeMsg
	readUntil(t, conn, "notice", &note)
	if !note.Error || note.Text != paneGone {
		t.Errorf("paneName for a gone pane = %+v, want an error notice %q", note, paneGone)
	}
}

// The label is in the graph node, cut to the label limit however it was set.
func TestGraphNodeCarriesTheLabel(t *testing.T) {
	e := newGraphEnv(t)
	ask(e.srv, func() bool {
		if _, err := e.ws.SetPaneLabel("", e.dev, strings.Repeat("日本", 100)); err != nil {
			t.Error(err)
		}
		e.ws.Pane(e.eyes).Label = strings.Repeat("x", 200) // a layout written by hand
		return true
	})
	g := e.snapshotGraph(t)
	dev, _ := g.node(e.dev)
	if n := len([]rune(dev.Label)); n == 0 || n > team.MaxPaneLabel {
		t.Errorf("dev label has %d characters, limit %d", n, team.MaxPaneLabel)
	}
	eyes, _ := g.node(e.eyes)
	if n := len([]rune(eyes.Label)); n != team.MaxPaneLabel {
		t.Errorf("eyes label has %d characters, want it cut to %d", n, team.MaxPaneLabel)
	}
	if boss, _ := g.node(e.boss); boss.Label != "" {
		t.Errorf("a pane with no label has %q", boss.Label)
	}
}

// pane-name through the hook server: a pane names itself, a manager names a
// helper, and a helper cannot name another pane.
func TestPaneNameThroughTheHookServer(t *testing.T) {
	e := newGraphEnv(t)
	hookSrv := e.ws.HookServer()
	api, token := hookSrv.BaseURL(), hookSrv.Token()

	if res, err := hooks.PaneName(api, token, e.dev, "", "tabs per goal"); err != nil || res.Pane != e.dev || res.Label != "tabs per goal" {
		t.Fatalf("a pane naming itself: %+v, %v", res, err)
	}
	if _, err := hooks.PaneName(api, token, e.dev, e.eyes, "mine"); err == nil || !strings.Contains(err.Error(), "only a manager") {
		t.Errorf("a helper naming another pane: %v", err)
	}
	if res, err := hooks.PaneName(api, token, e.boss, e.eyes, "review of 612"); err != nil || res.Label != "review of 612" {
		t.Fatalf("the manager naming a helper: %+v, %v", res, err)
	}
	g := e.snapshotGraph(t)
	if n, _ := g.node(e.dev); n.Label != "tabs per goal" {
		t.Errorf("dev label in the graph = %q", n.Label)
	}
	if n, _ := g.node(e.eyes); n.Label != "review of 612" {
		t.Errorf("eyes label in the graph = %q", n.Label)
	}
}

// A relay window is sent the label a pane was given, redacted, but not one made
// from the start of its task, which stays on the desk like the task.
func TestRelayStateKeepsAChosenLabelAndDropsOneMadeFromTheTask(t *testing.T) {
	secret := relaySecrets[0]
	st := scrubStateMsg(mustJSON(t, stateMsg{Type: "state", Panes: map[string]paneView{
		"chosen": {ID: "chosen", Name: "n", Label: "PR " + secret},
		"made":   {ID: "made", Name: "n", Label: "Fix the login with " + secret, LabelAuto: true},
	}}))
	if leaks := relayLeaks(st); len(leaks) > 0 {
		t.Errorf("the state holds %q:\n%s", leaks, st)
	}
	var back stateMsg
	if err := json.Unmarshal(st, &back); err != nil {
		t.Fatal(err)
	}
	if got := back.Panes["chosen"].Label; !strings.HasPrefix(got, "PR ") {
		t.Errorf("a chosen label reached the relay window as %q", got)
	}
	if got := back.Panes["made"]; got.Label != "" || got.LabelAuto {
		t.Errorf("a label made from the task reached the relay window: %+v", got)
	}
}
