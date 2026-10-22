package server

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/session/transcript"
	"github.com/jmwri/flockdeck/internal/team"
	"github.com/jmwri/flockdeck/internal/workspace"
)

// These tests hold the rule that no conversation text goes to a window reached
// through the relay until the scrub task is done: not through the conversation
// commands, not in a state push, the agents list or the graph, and not in any
// field a message holds. A window on this machine is sent what it always was.

// planted is put in every field that can hold conversation text. It is not a
// secret in the form record.Redact knows, so the redactor never hides it: only
// the gates in relayscrub.go can keep it off the wire.
const planted = "ZXQ-PLANTED-9431"

func TestRelayTextCommandsAreEveryOneThatReturnsConversation(t *testing.T) {
	want := []string{"conversationDetail", "conversationOlder", "conversationOpen", "conversationSearch", "conversations", "makeBaton"}
	var got []string
	for name := range remoteTextCommands {
		got = append(got, name)
	}
	if len(got) != len(want) {
		t.Fatalf("remoteTextCommands = %v, want %v", got, want)
	}
	for _, name := range want {
		if !remoteTextCommands[name] {
			t.Errorf("%s is not refused to a relay window", name)
		}
		if _, ok := commandAccess[name]; !ok {
			t.Errorf("%s is not a command", name)
		}
	}
}

// Every message type that carries conversation text is dropped for a relay
// window, except the page, which is cut to an unsupported one with no entries.
func TestRelayDropsEveryConversationMessage(t *testing.T) {
	entries := []transcript.Entry{{ID: "1", Kind: transcript.KindReply, TS: "t", Text: planted, Label: planted, Summary: planted,
		Description: planted, Report: planted, Diff: planted, HasDetail: true}}
	for typ, msg := range map[string]any{
		"conversationAppend": conversationAppendMsg{Type: "conversationAppend", ID: "p", Entries: entries, Cursor: planted},
		"conversationDetail": conversationDetailMsg{Type: "conversationDetail", ID: "p", Entry: "1", Detail: transcript.Detail{Text: planted, Diff: planted, Entries: entries}},
		"conversationSearch": conversationSearchMsg{Type: "conversationSearch", ID: "p", Q: planted, Matches: []conversationSearchHit{{EntryID: "1", Snippet: planted}}},
		"conversations":      conversationsMsg{Type: "conversations", Cwd: "/r", Items: []conversationView{{ID: "c", Summary: planted}}},
	} {
		if got := scrubForRemote(mustJSON(t, msg)); got != nil {
			t.Errorf("%s was sent to a relay window: %s", typ, got)
		}
	}

	out := scrubForRemote(mustJSON(t, conversationPageMsg{Type: "conversationPage", ID: "p", Supported: true, Agent: "claude", Entries: entries, Cursor: planted}))
	if out == nil || bytes.Contains(out, []byte(planted)) {
		t.Fatalf("the page = %s", out)
	}
	var page conversationPageMsg
	if err := json.Unmarshal(out, &page); err != nil {
		t.Fatal(err)
	}
	if page.Supported || len(page.Entries) != 0 || page.ID != "p" || !bytes.Contains(out, []byte(`"entries":[]`)) {
		t.Errorf("the page = %+v\n%s", page, out)
	}
}

// The graph is dropped whole: its live line is a pane's last reply, a structured
// pane's included, and its edges carry the text of messages.
func TestRelayDropsTheGraphWhole(t *testing.T) {
	g := graphMsg{Type: "graph", Root: "/r", Project: "p",
		Teams: []graphTeamView{{ID: "a", Manager: "a", Members: []string{"a"}, Goal: planted, Caps: []graphCap{},
			Needs: []team.NeedsItem{{ID: "y", Kind: team.NeedReturned, Pane: "a", Text: planted}}}},
		Nodes: []graphNodeView{{GraphNode: team.GraphNode{Pane: "a", WaitingOn: team.WaitingOn{Kind: team.WaitUser, Text: planted}},
			Name: "n", Live: planted, Task: &graphTaskChip{ID: "t", Title: planted, State: "doing"}}},
		Edges: []graphEdge{{Kind: graphEdgeMessage, From: "a", To: "b", Text: planted}},
		Solo:  []string{}, SoloNeeds: []team.NeedsItem{{ID: "z", Kind: team.NeedAsk, Pane: "b", Text: planted}}}
	if got := scrubForRemote(mustJSON(t, g)); got != nil {
		t.Errorf("the graph was sent to a relay window: %s", got)
	}
}

// Each text field of a pane and of the agents list is cleared, in the bytes sent.
func TestRelayPaneAndAgentFieldsHoldNoText(t *testing.T) {
	work := []backgroundView{{ID: "b1", Kind: "shell", Description: planted, Command: planted, Evidence: planted, Since: "s"}}
	for _, structured := range []bool{false, true} {
		pv := paneView{ID: "p", Name: "n", Status: "waiting", Structured: structured, Detail: planted, Task: planted,
			Last: &lastReplyView{Text: planted, TS: "ts1", Kind: "reply"}, BackgroundWork: work, BackgroundEnded: work,
			Ask: &askView{Questions: []askQuestionView{{Question: planted}}},
			Permission: &permissionView{Tool: "Bash", Summary: "Run: " + planted, Command: planted, Description: planted, File: planted, Diff: planted,
				Options: []permissionOption{{ID: "yes"}}}}
		out := scrubStateMsg(mustJSON(t, stateMsg{Type: "state", Panes: map[string]paneView{"p": pv}}))
		if bytes.Contains(out, []byte(planted)) {
			t.Errorf("structured=%v: the state holds the marker:\n%s", structured, out)
		}
		var back stateMsg
		if err := json.Unmarshal(out, &back); err != nil {
			t.Fatal(err)
		}
		if b := back.Panes["p"]; b.Name != "n" || b.Status != "waiting" || (!structured && (b.Last == nil || b.Last.TS != "ts1" || b.Last.Kind != "reply")) || len(b.BackgroundWork) != 1 || b.BackgroundWork[0].Kind != "shell" || b.BackgroundWork[0].ID != "b1" {
			t.Errorf("structured=%v: the roster fields changed: %+v", structured, b)
		}
	}

	for _, waiting := range []string{"Run: " + planted, "Edit " + planted, "Write " + planted, "Use " + planted, "blocked: " + planted, "anything " + planted} {
		out := scrubAgentsMsg(mustJSON(t, agentsMsg{Type: "agents", Items: []agentView{
			{PaneID: "p", Detail: planted, Waiting: waiting, BackgroundWork: work, BackgroundEnded: work}}}))
		if bytes.Contains(out, []byte(planted)) {
			t.Errorf("waiting %q: the list holds the marker:\n%s", waiting, out)
		}
		if !strings.Contains(string(out), detailsOnDesktop) {
			t.Errorf("waiting %q: the list does not say the details are on the desktop:\n%s", waiting, out)
		}
	}
}

// A full session against a real server: the marker is in the conversation, the
// opening prompt, a permission command and a question, and a relay window sends
// every command that could read it back. Nothing it is sent holds the marker,
// the conversation commands are refused, and the window on this machine is sent
// the same things it always was.
func TestRelayWindowSeesNoConversationTextInAFullSession(t *testing.T) {
	srv, ws := newTestServer(t)
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	paneID := addAgentPane(t, srv, ws, "claude")
	writeClaudeJSONL(t, claudeTranscriptPath(home, ws.ActiveRoot(), paneID),
		`{"type":"user","uuid":"u1","message":{"role":"user","content":"prompt `+planted+`"}}`,
		`{"type":"assistant","uuid":"a1","message":{"role":"assistant","content":[{"type":"text","text":"reply `+planted+`"}]}}`)
	p, _ := ask(srv, func() *workspace.Pane {
		p := ws.Pane(paneID)
		p.Task = "task " + planted
		return p
	})
	setAccess(t, srv, "full-dev", "full", false)
	setAccess(t, srv, "watch-dev", "viewer", true)
	ts := remoteServer(t, srv)

	// The desk is sent the text, so a clean relay window is the scrub's doing
	// and not an empty fixture.
	local := dialControl(t, srv)
	nextState(t, local, func(s stateMsg) bool {
		pv := s.Panes[paneID]
		return strings.Contains(pv.Task, planted)
	})
	sendCmd(t, local, command{Cmd: "conversationOpen", ID: paneID})
	var deskPage conversationPageMsg
	readUntil(t, local, "conversationPage", &deskPage)
	if !deskPage.Supported || len(deskPage.Entries) == 0 || !strings.Contains(string(mustJSON(t, deskPage)), planted) {
		t.Fatalf("the desk's page = %+v", deskPage)
	}

	for _, dev := range []string{"full-dev", "watch-dev"} {
		conn, err := dialRemoteControlDevice(ts, ts.URL, dev)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.CloseNow() })

		for _, c := range []command{
			{Cmd: "conversationOpen", ID: paneID},
			{Cmd: "conversationOlder", ID: paneID, Before: "u1"},
			{Cmd: "conversationDetail", ID: paneID, Entry: "a1"},
			{Cmd: "conversationSearch", ID: paneID, Q: "PLANTED"},
			{Cmd: "conversations", Path: ws.ActiveRoot()},
			{Cmd: "makeBaton", ID: paneID},
			{Cmd: "agents"},
			{Cmd: "graphSnapshot", Req: "g"},
		} {
			sendCmd(t, conn, c)
		}
		// A prompt with the marker in its command, then one with it in a question.
		p.Sess.SetStatusFull(session.StatusWaiting, "Bash", `{"command":"echo `+planted+`"}`)
		time.Sleep(300 * time.Millisecond)
		p.Sess.SetStatusFull(session.StatusWaiting, "AskUserQuestion",
			`{"questions":[{"header":"h","question":"`+planted+`?","options":[{"label":"`+planted+`","description":"d"}]}]}`)
		sendCmd(t, conn, command{Cmd: "conversationOpen", ID: paneID})
		sendCmd(t, conn, command{Cmd: "agents"})

		_, raws := collectTypes(t, conn, 3*time.Second)
		if len(raws) == 0 {
			t.Fatalf("%s: nothing was sent", dev)
		}
		var page, notices, states int
		for _, raw := range raws {
			if bytes.Contains(raw, []byte(planted)) {
				t.Fatalf("%s: a message holds the marker:\n%.500s", dev, raw)
			}
			switch messageType(raw) {
			case "conversationPage":
				var pg conversationPageMsg
				if json.Unmarshal(raw, &pg) != nil || pg.Supported || len(pg.Entries) != 0 {
					t.Errorf("%s: page = %s", dev, raw)
				}
				page++
			case "notice":
				var n noticeMsg
				if json.Unmarshal(raw, &n) == nil && n.Text == conversationsOnDesktop {
					notices++
				}
			case "state":
				states++
			case "conversationAppend", "conversationDetail", "conversationSearch", "conversations", "graph":
				t.Errorf("%s: sent a %s: %.200s", dev, messageType(raw), raw)
			}
		}
		wantNotices := 5
		if dev == "watch-dev" {
			// conversations is refused to a viewer before this gate, as it always was.
			wantNotices = 3 // conversations and makeBaton are refused to a viewer before this gate
		}
		if page != 2 || notices != wantNotices || states == 0 {
			t.Errorf("%s: %d pages, %d refusals, %d states; want 2, %d and some", dev, page, notices, states, wantNotices)
		}
	}
}

// The refusal is made on the connection and not on the device: a window on this
// machine is not refused, and conversationClose is let through for a relay one.
func TestRefuseRemoteTextKeysOnTheConnection(t *testing.T) {
	desk := &controlClient{out: make(chan []byte, 4)}
	if desk.refuseRemoteText(command{Cmd: "conversationOpen", ID: "p"}) {
		t.Error("a window on this machine was refused")
	}
	phone := &controlClient{out: make(chan []byte, 4), remote: true, device: "full-dev"}
	if phone.refuseRemoteText(command{Cmd: "conversationClose", ID: "p"}) {
		t.Error("conversationClose was refused")
	}
	if !phone.refuseRemoteText(command{Cmd: "conversationSearch", ID: "p", Q: "x"}) {
		t.Fatal("conversationSearch was not refused")
	}
	var n noticeMsg
	if err := json.Unmarshal(<-phone.out, &n); err != nil || !n.Error || n.Text != conversationsOnDesktop {
		t.Errorf("notice = %+v, %v", n, err)
	}
}
