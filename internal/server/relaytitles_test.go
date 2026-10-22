package server

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/workspace"
)

// A tab title nobody chose is made from the first words of the opening prompt,
// and so is the draft of a baton. Both are tried the way they are really made:
// a pane spawned with a task, which names its tab after it.
func TestRelayWindowGetsNoTitleOrBatonMadeFromAPrompt(t *testing.T) {
	srv, ws := newTestServer(t)
	batonAgent(t, ws)
	lead := firstPane(t, srv, ws)
	id, ok := ask(srv, func() string {
		id, err := ws.Spawn(lead, workspace.SpawnOptions{Task: "do " + planted, Agent: "gocli"})
		if err != nil {
			t.Error(err)
		}
		return id
	})
	if !ok || id == "" {
		t.Fatal("the agent pane did not start")
	}
	setAccess(t, srv, "full-dev", "full", false)
	ts := remoteServer(t, srv)

	// The desk is sent the title, so a clean relay window is the scrub's doing.
	local := dialControl(t, srv)
	nextState(t, local, func(s stateMsg) bool {
		for _, tab := range s.Tabs {
			if strings.Contains(tab.Title, planted) {
				return true
			}
		}
		return false
	})

	conn, err := dialRemoteControlDevice(ts, ts.URL, "full-dev")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })
	sendCmd(t, conn, command{Cmd: "makeBaton", ID: id})
	sendCmd(t, conn, command{Cmd: "agents"})
	types, raws := collectTypes(t, conn, 3*time.Second)
	var states, lists, refusals int
	for i, raw := range raws {
		if bytes.Contains(raw, []byte(planted)) {
			t.Fatalf("a %s holds the marker:\n%.500s", types[i], raw)
		}
		switch types[i] {
		case "state":
			states++
		case "agents":
			lists++
		case "batonDraft":
			t.Errorf("a baton draft was sent")
		case "notice":
			var n noticeMsg
			if json.Unmarshal(raw, &n) == nil && n.Text == conversationsOnDesktop {
				refusals++
			}
		}
	}
	if states == 0 || lists == 0 || refusals != 1 {
		t.Errorf("%d states, %d lists, %d refusals", states, lists, refusals)
	}
}

// The titles, the search flag and the names in the bytes sent.
func TestRelayTabsAndNamesAreCut(t *testing.T) {
	long := strings.Repeat("n", 100)
	tabs := []tabView{
		{ID: "t1", Title: "first words " + planted, Focus: "p1"},
		{ID: "t2", Title: "chosen by hand", Focus: "p1", Named: true},
		{ID: "t3", Title: "words " + planted, Focus: "gone"},
	}
	st := scrubStateMsg(mustJSON(t, stateMsg{Type: "state", Tabs: tabs, CanSearchConversation: true,
		Panes: map[string]paneView{"p1": {ID: "p1", Name: "work", PeerName: long}}}))
	if bytes.Contains(st, []byte(planted)) {
		t.Errorf("the state holds the marker:\n%s", st)
	}
	var back stateMsg
	if err := json.Unmarshal(st, &back); err != nil {
		t.Fatal(err)
	}
	if back.Tabs[0].Title != "work" || back.Tabs[1].Title != "chosen by hand" || back.Tabs[2].Title != "Tab" {
		t.Errorf("titles = %q, %q, %q", back.Tabs[0].Title, back.Tabs[1].Title, back.Tabs[2].Title)
	}
	if back.CanSearchConversation {
		t.Error("a relay window is told it can search a conversation")
	}
	if got := back.Panes["p1"].PeerName; len([]rune(got)) != maxRemoteName {
		t.Errorf("peer name has %d characters, want %d", len([]rune(got)), maxRemoteName)
	}

	ag := scrubAgentsMsg(mustJSON(t, agentsMsg{Type: "agents", Items: []agentView{
		{PaneID: "a", Name: "work", Tab: "words " + planted},
		{PaneID: "b", Name: long, Tab: "by hand", TabNamed: true},
		{PaneID: "c", Tab: "words " + planted}}}))
	if bytes.Contains(ag, []byte(planted)) {
		t.Errorf("the list holds the marker:\n%s", ag)
	}
	var list agentsMsg
	if err := json.Unmarshal(ag, &list); err != nil {
		t.Fatal(err)
	}
	if list.Items[0].Tab != "work" || list.Items[1].Tab != "by hand" || list.Items[2].Tab != "Tab" || len([]rune(list.Items[1].Name)) != maxRemoteName {
		t.Errorf("items = %+v", list.Items)
	}
}

// What a panic quotes is for the desk. A window reached through the relay is
// told what was being done and that the details are on the desktop.
func TestRelayPanicNoticesDoNotQuoteThePanic(t *testing.T) {
	srv, _ := newTestServer(t)
	phone := &controlClient{out: make(chan []byte, 4), remote: true, device: "full-dev"}
	desk := &controlClient{out: make(chan []byte, 4)}
	srv.reportPanic(phone, "while testing", "boom "+planted)
	srv.reportPanic(desk, "while testing", "boom "+planted)
	var n noticeMsg
	if err := json.Unmarshal(<-phone.out, &n); err != nil || strings.Contains(n.Text, planted) || !strings.Contains(n.Text, "while testing") {
		t.Errorf("remote notice = %q, %v", n.Text, err)
	}
	if err := json.Unmarshal(<-desk.out, &n); err != nil || !strings.Contains(n.Text, planted) {
		t.Errorf("desk notice = %q, %v", n.Text, err)
	}
}

// A panic reported to every window reaches a relay window in its fixed words.
func TestRelayPanicToEveryWindowIsFixedForTheRelay(t *testing.T) {
	srv, _ := newTestServer(t)
	ts := remoteServer(t, srv)
	conn, err := dialRemoteControlDevice(ts, ts.URL, "full-dev")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })
	time.Sleep(300 * time.Millisecond) // the window is registered once it has its hello
	srv.reportPanic(nil, "while testing", "boom "+planted)
	_, raws := collectTypes(t, conn, 2*time.Second)
	found := false
	for _, raw := range raws {
		if bytes.Contains(raw, []byte(planted)) {
			t.Fatalf("the notice holds the panic value: %s", raw)
		}
		found = found || bytes.Contains(raw, []byte("while testing"))
	}
	if !found {
		t.Errorf("the relay window was told nothing; it was sent %d messages", len(raws))
	}
}

// Every message type the server sends is on one of three lists: scrubbed, passed as it is, or desk only. A
// type added later is dropped for a relay window until it is classified. A type that carries
// text must have a scrub; the others are said not to carry any.
func TestEveryMessageTypeIsClassifiedForTheRelay(t *testing.T) {
	found := map[string]bool{}
	fset := token.NewFileSet()
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no files: %v", err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			// A type given any way but as a literal in the message is not seen by
			// this scan, so one is an error: write the type as a literal where the
			// message is made.
			if as, ok := n.(*ast.AssignStmt); ok {
				for _, lhs := range as.Lhs {
					if sel, ok := lhs.(*ast.SelectorExpr); ok && sel.Sel.Name == "Type" {
						t.Errorf("%s assigns a message's Type after it is made: write the type as a literal in the message, so the relay classification sees it", f)
					}
				}
				return true
			}
			kv, ok := n.(*ast.KeyValueExpr)
			if !ok {
				return true
			}
			var key string
			switch k := kv.Key.(type) {
			case *ast.Ident:
				key = k.Name
			case *ast.BasicLit:
				key = strings.Trim(k.Value, `"`)
			}
			if key != "Type" && key != "type" {
				return true
			}
			if lit, ok := kv.Value.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				found[strings.Trim(lit.Value, `"`)] = true
			} else {
				t.Errorf("%s gives a message type that is not a literal: write the type as a literal, so the relay classification sees it", f)
			}
			return true
		})
	}
	if len(found) < 40 {
		t.Fatalf("found only %d message types: the scan is looking in the wrong place", len(found))
	}
	for typ := range found {
		_, scrubbed := relayScrubs[typ]
		_, clean := relayPass[typ]
		_, isRepo := relayDeskOnly[typ]
		n := 0
		for _, b := range []bool{scrubbed, clean, isRepo} {
			if b {
				n++
			}
		}
		switch {
		case n == 0:
			t.Errorf("the server sends a %q message that is not classified for the relay: add it to relayScrubs if it carries text, to relayPass if it does not, or to relayDeskOnly if no relay window is sent it", typ)
		case n > 1:
			t.Errorf("%q is in more than one list", typ)
		}
	}
	for _, m := range []map[string]string{relayPass, relayDeskOnly} {
		for typ := range m {
			if !found[typ] {
				t.Errorf("%q is classified here and no longer sent: drop it", typ)
			}
		}
	}
	for typ := range relayScrubs {
		if !found[typ] {
			t.Errorf("relayScrubs has %q, which the server no longer sends", typ)
		}
	}
}

// A notice and a baton error quote error strings, so a relay window is sent them
// redacted and cut to a line, with every other field as it was.
func TestRelayNoticeAndBatonErrorAreRedactedAndCut(t *testing.T) {
	long := strings.Repeat("x", 2*maxRemoteNotice)
	out := scrubForRemote(mustJSON(t, noticeMsg{Type: "notice", Text: "failed with " + relaySecrets[0] + "\n" + long, Error: true, Kind: "approval"}))
	if leaks := relayLeaks(out); len(leaks) > 0 {
		t.Errorf("the notice holds %q", leaks)
	}
	var n noticeMsg
	if err := json.Unmarshal(out, &n); err != nil || !n.Error || n.Kind != "approval" || len([]rune(n.Text)) != maxRemoteNotice || !strings.HasPrefix(n.Text, "failed with ") {
		t.Errorf("notice = %+v, %v", n, err)
	}
	out = scrubForRemote(mustJSON(t, batonErrorMsg{Type: "batonError", PaneID: "p", Req: "r", Cmd: "saveBaton", Error: "bad " + relaySecrets[1] + " " + long}))
	if leaks := relayLeaks(out); len(leaks) > 0 {
		t.Errorf("the baton error holds %q", leaks)
	}
	var e batonErrorMsg
	if err := json.Unmarshal(out, &e); err != nil || e.PaneID != "p" || e.Req != "r" || e.Cmd != "saveBaton" || len([]rune(e.Error)) != maxRemoteNotice {
		t.Errorf("baton error = %+v, %v", e, err)
	}
	if scrubForRemote([]byte(`{"type":"notice"}`)) != nil || scrubForRemote([]byte(`{"type":"batonError","error":7}`)) != nil {
		t.Error("a message with no text to cut was sent")
	}
}

// The agents list fills TabNamed from the tab: a tab renamed by hand says so, and
// one that took its title from its prompt does not. The relay scrub depends on it.
func TestAgentsListFillsTabNamedFromTheTab(t *testing.T) {
	srv, ws := newTestServer(t)
	lead := firstPane(t, srv, ws)
	named, ok := ask(srv, func() string {
		tab := ws.NewTab(session.KindShell, ws.ActiveRoot(), "agent")
		ws.NameTab(tab.ID, "mine")
		return tab.Tree.Panes()[0]
	})
	if !ok {
		t.Fatal("the workspace did not answer")
	}
	conn := dialControl(t, srv)
	sendCmd(t, conn, command{Cmd: "agents"})
	var list agentsMsg
	readUntil(t, conn, "agents", &list)
	seen := map[string]agentView{}
	for _, it := range list.Items {
		seen[it.PaneID] = it
	}
	if it, ok := seen[named]; !ok || !it.TabNamed || it.Tab != "mine" {
		t.Errorf("the renamed tab's pane = %+v", it)
	}
	if it, ok := seen[lead]; !ok || it.TabNamed {
		t.Errorf("the pane of a tab nobody renamed = %+v", it)
	}
}
