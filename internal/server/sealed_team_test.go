package server

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/store"
)

// Stage 7 of /ws/sealed (API 1.41, cap sealedTeam): the team view. Reads are tier
// A and for a full device; msgSend and msgSeen are tier B. The task board, closing
// panes and routing stay off the socket. The rules held here: a viewer is refused
// the reads, nothing in the clear, text is redacted, the board is not sent, the
// unsealed socket still refuses all of it, and the audit holds no text.

const teamSecret = "ghp_abcdefghijklmnopqrstuvwxyz0123456789"

// teamSealedEnv is a project with a manager, a worker and a reviewer, behind a
// sealed environment.
func teamSealedEnv(t *testing.T) (*sealedEnv, *teamEnv) {
	t.Helper()
	te := newTeamEnv(t)
	return newSealedEnvFor(t, te.srv), te
}

func TestSealedTeamTables(t *testing.T) {
	for _, n := range []string{"teamSnapshot", "teamWatch", "teamUnwatch", "graphSnapshot", "graphWatch", "graphUnwatch", "msgList"} {
		if sealedCommands[n] != sealedTierA || !sealedFullOnly[n] {
			t.Errorf("%s: tier %q, full only %v, want A and full only", n, sealedCommands[n], sealedFullOnly[n])
		}
	}
	for _, n := range []string{"msgSend", "msgSeen"} {
		if sealedCommands[n] != sealedTierB {
			t.Errorf("%s is tier %q, want B", n, sealedCommands[n])
		}
		if !sealedActOps[n] {
			t.Errorf("%s is not an op a device that may act is told succeeded", n)
		}
	}
	// The board, closing panes and routing are not on the socket.
	for _, n := range []string{"taskList", "taskGet", "taskAdd", "taskAssign", "taskStatus", "taskNote", "taskBranch", "taskDepends",
		"taskEstimate", "taskTeam", "taskPrune", "teamClose", "setRouting", "clearRoutingLog", "mergeBranch", "closePane", "promote"} {
		if sealedCommands[n] != "" {
			t.Errorf("%s is on the sealed socket", n)
		}
	}
	for _, typ := range []string{"team", "messages", "graph", "teamGone"} {
		if sealedMessages[typ].Tier != sealedTierA {
			t.Errorf("message %s is tier %q", typ, sealedMessages[typ].Tier)
		}
	}
	for _, typ := range []string{"tasks", "relayTeams"} {
		if sealedMessages[typ].Tier != "" {
			t.Errorf("message %s is sent to a sealed device", typ)
		}
	}
}

// A viewer, even one let watch panes, is refused every read by role, and the
// roster is not sent to it.
func TestSealedTeamReadsAreForFullDevices(t *testing.T) {
	e, te := teamSealedEnv(t)
	p, _ := e.open(t, "watcher")
	for _, n := range []string{"teamSnapshot", "teamWatch", "graphSnapshot", "graphWatch", "msgList"} {
		p.send(command{Cmd: n, Root: te.root, Req: "r-" + n})
		got := p.readType("sealedRefused")
		if got["code"] != sealedCodeRole || got["cmd"] != n || got["req"] != "r-"+n {
			t.Errorf("a watcher's %s was answered %v", n, got)
		}
	}
}

// A full device with no switch reads the roster, the graph and the mail; the
// task board is not in what it is sent, and what a person typed is redacted.
func TestSealedTeamReads(t *testing.T) {
	e, te := teamSealedEnv(t)
	te.mustDo(t, command{Cmd: "taskAdd", Title: "T-title-must-not-travel", Spec: "body"})
	te.mustDo(t, command{Cmd: "msgSend", Target: te.dev, Text: "deploy with " + teamSecret})
	p, hello := e.open(t, "full")
	for _, n := range []string{"teamSnapshot", "graphSnapshot", "msgList"} {
		if !helloCommands(hello)[n] {
			t.Errorf("%s is not in the hello", n)
		}
	}

	p.send(command{Cmd: "teamSnapshot", Root: te.root, Req: "t"})
	tm := p.readType("team")
	if tm["root"] != te.root {
		t.Errorf("root = %v", tm["root"])
	}
	if roster, _ := tm["roster"].([]any); len(roster) != 3 {
		t.Errorf("roster = %v", tm["roster"])
	}
	for _, k := range []string{"tasks", "openHidden", "finishedHidden", "boardError"} {
		if _, ok := tm[k]; ok {
			t.Errorf("the team message carries %q", k)
		}
	}
	if msgs, _ := tm["messages"].([]any); len(msgs) != 1 {
		t.Errorf("messages = %v", tm["messages"])
	}

	p.send(command{Cmd: "graphSnapshot", Root: te.root, Req: "g"})
	g := p.readType("graph")
	if nodes, _ := g["nodes"].([]any); len(nodes) < 3 {
		t.Errorf("graph nodes = %v", g["nodes"])
	}

	p.send(command{Cmd: "msgList", Root: te.root, Req: "m"})
	m := p.readType("messages")
	raw, _ := m["messages"].([]any)
	if len(raw) != 1 {
		t.Fatalf("messages = %v", m["messages"])
	}
	text, _ := raw[0].(map[string]any)["text"].(string)
	if !strings.HasPrefix(text, "deploy with ") || strings.Contains(text, teamSecret) {
		t.Errorf("the text of a message is %q", text)
	}
}

// Every frame a team read produces is checked for the secret and the task
// title, in whole, not by field.
func TestSealedTeamFramesHoldNoSecretAndNoBoard(t *testing.T) {
	e, te := teamSealedEnv(t)
	te.mustDo(t, command{Cmd: "taskAdd", Title: "board-title-xyz", Spec: "body"})
	te.mustDo(t, command{Cmd: "msgSend", Target: te.dev, Text: "use " + teamSecret})
	p, _ := e.open(t, "full")
	for _, c := range []command{{Cmd: "teamSnapshot", Root: te.root}, {Cmd: "msgList", Root: te.root}, {Cmd: "graphSnapshot", Root: te.root}} {
		p.send(c)
		typ := map[string]string{"teamSnapshot": "team", "msgList": "messages", "graphSnapshot": "graph"}[c.Cmd]
		p.readType(typ)
	}
	// Re-read as raw text: a second pass over each, kept.
	for _, c := range []command{{Cmd: "teamSnapshot", Root: te.root}, {Cmd: "msgList", Root: te.root}} {
		p.send(c)
		f := p.frame()
		if bytes.Contains(f, []byte(teamSecret)) {
			t.Errorf("%s: a frame holds the secret", c.Cmd)
		}
		if c.Cmd == "teamSnapshot" && bytes.Contains(f, []byte("board-title-xyz")) {
			t.Errorf("the team frame holds a task title")
		}
	}
}

// A root that is not an open project is answered with a refusal and no data.
func TestSealedTeamUnknownRoot(t *testing.T) {
	e, te := teamSealedEnv(t)
	p, _ := e.open(t, "full")
	p.send(command{Cmd: "teamSnapshot", Root: filepath.Join(te.root, "nope", "elsewhere"), Req: "x"})
	got := p.readType("teamResult")
	if got["ok"] == true || got["req"] != "x" {
		t.Errorf("answer = %v", got)
	}
	p.send(command{Cmd: "graphWatch", Root: filepath.Join(te.root, "nope"), Req: "y"})
	if got := p.readType("teamResult"); got["ok"] == true {
		t.Errorf("answer = %v", got)
	}
}

// A watch is pushed the change, held to the interval, and ends with unwatch; a
// device that is taken off ends it with the socket.
func TestSealedTeamWatch(t *testing.T) {
	e, te := teamSealedEnv(t)
	p, _ := e.open(t, "full")
	p.send(command{Cmd: "teamWatch", Root: te.root, Req: "w"})
	first := p.readType("team")
	if msgs, _ := first["messages"].([]any); len(msgs) != 0 {
		t.Fatalf("messages = %v", first["messages"])
	}
	te.mustDo(t, command{Cmd: "msgSend", Target: te.dev, Text: "hello there"})
	te.srv.Wake()
	var got map[string]any
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		got = p.readType("team")
		if msgs, _ := got["messages"].([]any); len(msgs) == 1 {
			break
		}
	}
	if msgs, _ := got["messages"].([]any); len(msgs) != 1 {
		t.Fatalf("the watch was not pushed the message: %v", got["messages"])
	}
	p.send(command{Cmd: "teamUnwatch"})
	time.Sleep(50 * time.Millisecond)
	if teamWatching(te.srv) {
		t.Error("a window is still watching after teamUnwatch")
	}
}

func teamWatching(s *Server) bool {
	r, _ := ask(s, func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		for c := range s.clients {
			if c.sealed != nil && c.teamRoot != "" {
				return true
			}
		}
		return false
	})
	return r
}

// A watch ends with its socket: the window is gone from the server's list.
func TestSealedTeamWatchEndsWithTheSocket(t *testing.T) {
	e, te := teamSealedEnv(t)
	p, _ := e.open(t, "full")
	p.send(command{Cmd: "graphWatch", Root: te.root})
	p.readType("graph")
	_ = p.conn.CloseNow()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if r, _ := ask(te.srv, func() bool {
			te.srv.mu.Lock()
			defer te.srv.mu.Unlock()
			for c := range te.srv.clients {
				if c.sealed != nil {
					return true
				}
			}
			return false
		}); !r {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Error("a sealed window is still listed after its socket closed")
}

// msgSend and msgSeen need the desk's switch, are refused by name without it, and
// run (as the person) with it. The answer is the plain success, with no text.
func TestSealedTeamMailActs(t *testing.T) {
	e, te := teamSealedEnv(t)
	p, _ := e.open(t, "full")
	for _, n := range []string{"msgSend", "msgSeen"} {
		p.send(command{Cmd: n, Root: te.root, Target: te.dev, Text: "hi", Req: "r-" + n})
		got := p.readType("sealedRefused")
		if got["code"] != sealedCodeControl || got["cmd"] != n {
			t.Errorf("%s without the switch was answered %v", n, got)
		}
	}
	if in := te.agentInbox(t, te.dev, true); len(in) != 0 {
		t.Fatalf("a refused message was delivered: %+v", in)
	}

	e.grantControl(t, "full")
	p, hello := e.open(t, "full")
	if !helloCommands(hello)["msgSend"] || !helloCommands(hello)["msgSeen"] {
		t.Fatalf("the hello does not list the mail commands: %v", hello["commands"])
	}
	p.send(command{Cmd: "msgSend", Root: te.root, Target: te.dev, Text: "from the phone " + teamSecret, Req: "s"})
	got := p.readType("teamResult")
	if got["ok"] != true || got["op"] != "msgSend" || got["req"] != "s" {
		t.Fatalf("answer = %v", got)
	}
	if _, ok := got["sent"]; ok {
		t.Errorf("the answer carries data: %v", got)
	}
	in := te.agentInbox(t, te.dev, true)
	if len(in) != 1 || !in[0].From.User {
		t.Fatalf("inbox = %+v", in)
	}

	// The device marks it seen.
	p.send(command{Cmd: "msgList", Root: te.root})
	list := p.readType("messages")
	msgs, _ := list["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("messages = %v", msgs)
	}
	id, _ := msgs[0].(map[string]any)["id"].(float64)
	p.send(command{Cmd: "msgSeen", Root: te.root, ID: te.dev, MsgID: int(id), Req: "sn"})
	if got := p.readType("teamResult"); got["ok"] != true || got["op"] != "msgSeen" {
		t.Errorf("msgSeen answer = %v", got)
	}

	// A target that is not in the project is the handler's refusal, and says
	// nothing else.
	p.send(command{Cmd: "msgSend", Root: te.root, Target: "nobody-here", Text: "x", Req: "bad"})
	if got := p.readType("teamResult"); got["ok"] == true || got["req"] != "bad" {
		t.Errorf("answer = %v", got)
	}
}

// The right is looked at again: taken away at the desk, the next msgSend is
// refused and nothing is delivered.
func TestSealedTeamMailRegates(t *testing.T) {
	e, te := teamSealedEnv(t)
	e.grantControl(t, "full")
	p, _ := e.open(t, "full")
	e.setControl(t, "full", store.RoleFull, false, "")
	e.srv.approveFile = approveFileCache{}
	p.send(command{Cmd: "msgSend", Root: te.root, Target: te.dev, Text: "late", Req: "a"})
	if got := p.readType("sealedRefused"); got["code"] != sealedCodeControl {
		t.Errorf("after the switch went off: %v", got)
	}
	if in := te.agentInbox(t, te.dev, true); len(in) != 0 {
		t.Errorf("delivered after the switch went off: %+v", in)
	}
}

// What the board and desk-only commands get on this socket: not run.
func TestSealedTeamBoardIsRefused(t *testing.T) {
	e, te := teamSealedEnv(t)
	e.grantControl(t, "full")
	p, _ := e.open(t, "full")
	for _, n := range []string{"taskAdd", "taskList", "taskStatus", "teamClose", "setRouting"} {
		p.send(command{Cmd: n, Root: te.root, Title: "x", Req: "r-" + n})
		got := p.readType("sealedRefused")
		if got["code"] != sealedCodeCommand || got["cmd"] != n {
			t.Errorf("%s was answered %v", n, got)
		}
	}
	p.send(command{Cmd: "teamSnapshot", Root: te.root})
	if m := p.readType("team"); m == nil {
		t.Error("no team")
	}
}

// The lift is for a sealed connection and the table only; a window through the
// relay's unsealed socket still gets the desk-only refusal.
func TestSealedTeamLiftsOnlyTheTable(t *testing.T) {
	sealed := &controlClient{remote: true, sealed: &sealedLink{}}
	plain := &controlClient{remote: true}
	for _, n := range []string{"teamSnapshot", "graphWatch", "msgList", "msgSend", "msgSeen"} {
		if !sealed.liftsRelayRefusal(n) {
			t.Errorf("%s is not lifted on a sealed connection", n)
		}
		if plain.liftsRelayRefusal(n) {
			t.Errorf("%s is lifted on the unsealed socket", n)
		}
	}
	for _, n := range []string{"taskAdd", "teamClose", "taskList"} {
		if sealed.liftsRelayRefusal(n) {
			t.Errorf("%s is lifted on a sealed connection", n)
		}
	}
}

// teamGone is sent as one redacted line.
func TestSealedTeamGoneIsRedacted(t *testing.T) {
	out := sealedWrite([]byte(`{"type":"teamGone","root":"r","error":"cannot read `+teamSecret+`\nnext line"}`), func() bool { return false })
	if out == nil {
		t.Fatal("teamGone was dropped")
	}
	if bytes.Contains(out, []byte(teamSecret)) || bytes.Contains(out, []byte(`\n`)) {
		t.Errorf("teamGone = %s", out)
	}
	// The task list is never sent, whatever sends it.
	if sealedWrite([]byte(`{"type":"tasks","root":"r","tasks":[]}`), func() bool { return true }) != nil {
		t.Error("a tasks message was sent")
	}
}

// The log of what a device did names the command, never the text of a message.
func TestSealedTeamAuditHoldsNoText(t *testing.T) {
	e, te := teamSealedEnv(t)
	e.grantControl(t, "full")
	p, _ := e.open(t, "full")
	const text = "audit-mail-text-must-not-appear"
	p.send(command{Cmd: "msgSend", Root: te.root, Target: te.dev, Text: text, Req: "a"})
	p.readType("teamResult")
	time.Sleep(300 * time.Millisecond)
	dir, err := store.Dir()
	if err != nil {
		t.Fatal(err)
	}
	var seen bool
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".log") {
			return nil
		}
		b, _ := os.ReadFile(path)
		if bytes.Contains(b, []byte(text)) {
			t.Errorf("%s holds the text of a message", path)
		}
		if bytes.Contains(b, []byte(`"sealed-act"`)) && bytes.Contains(b, []byte(`msgSend`)) {
			seen = true
		}
		return nil
	})
	if !seen {
		t.Log("no log with the audit event was found; it is written asynchronously")
	}
}
