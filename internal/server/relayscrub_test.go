package server

import (
	"bytes"
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/jmwri/flockdeck/internal/record"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/workspace"
)

// These tests hold the scrub of what goes to a window reached through the relay
// (relayscrub.go): a command, a diff, a file or a question does not leave the
// desk, a secret in a tool row is redacted, a structured pane says nothing but
// its roster fields, and a window on this machine is sent what it always was.

// Secrets of the forms record.Redact knows. Each test checks that it still
// finds them, so a change to the redactor cannot empty the table.
var relaySecrets = []string{
	"sk-ant-api03-abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGH",
	"ghp_abcdefghijklmnopqrstuvwxyz0123456789",
	"AKIAIOSFODNN7EXAMPLE",
	"https://deploy:hunter2hunter2@git.example.com/org/repo.git",
}

func TestRelaySecretsAreOnesTheRedactorFinds(t *testing.T) {
	for _, s := range relaySecrets {
		if record.Redact(s) == s {
			t.Errorf("record.Redact leaves %q alone: change the table", s)
		}
	}
}

// relayLeaks says which of the secrets, and which other strings, are in raw.
func relayLeaks(raw []byte, others ...string) []string {
	var found []string
	for _, s := range append(slices.Clone(relaySecrets), others...) {
		if s != "" && bytes.Contains(raw, []byte(s)) {
			found = append(found, s)
		}
	}
	return found
}

// A prompt as a waiting pane's hooks give it.
type relayPrompt struct {
	name, tool, input string
	// others are strings from the input that must not be sent either.
	others []string
	// summary is the summary a relay window is told.
	summary string
}

func relayPrompts() []relayPrompt {
	secret := strings.Join(relaySecrets, " ")
	return []relayPrompt{
		{"bash", "Bash", `{"command":"curl -H 'Authorization: ` + relaySecrets[0] + `' ` + relaySecrets[3] + ` && echo done","description":"call the api with ` + relaySecrets[1] + `"}`,
			[]string{"curl -H", "call the api", "echo done"}, "Run a command, " + detailsOnDesktop},
		{"edit", "Edit", `{"filePath":"/home/dev/app/config.go","oldString":"key := \"\"","newString":"key := \"` + relaySecrets[2] + `\""}`,
			[]string{"/home/dev/app", "key :=", "config.go"}, "Edit a file, " + detailsOnDesktop},
		{"write", "Write", `{"filePath":"/home/dev/.env","content":"TOKEN=` + relaySecrets[1] + `"}`,
			[]string{"/home/dev", "TOKEN=", ".env"}, "Write a file, " + detailsOnDesktop},
		{"mcp", "mcp__vault__read", `{"command":"read ` + secret + `"}`, nil, "Use a tool, " + detailsOnDesktop},
		{"ask", "AskUserQuestion",
			`{"questions":[{"header":"Key","question":"Use ` + relaySecrets[0] + ` for prod?","options":[{"label":"Yes, ` + relaySecrets[1] + `","description":"` + relaySecrets[2] + `"}]}]}`,
			[]string{"for prod?"}, ""},
	}
}

// readFor reads a socket until a state message satisfies cond, and returns every
// message read on the way (raw) and that state.
func readStateFor(t *testing.T, conn *websocket.Conn, cond func(stateMsg) bool) (all [][]byte, st stateMsg) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		_, data, err := conn.Read(ctx)
		cancel()
		if err != nil {
			t.Fatalf("read control: %v", err)
		}
		all = append(all, data)
		if messageType(data) != "state" {
			continue
		}
		var s stateMsg
		if json.Unmarshal(data, &s) == nil && cond(s) {
			return all, s
		}
	}
	t.Fatal("no state satisfied the condition")
	return nil, st
}

// The state push to a window through the relay never carries the text of a
// prompt: not to a device with the full role, and not to a viewer, whether or
// not the desk lets it watch panes.
func TestRelayStatePushCarriesNoPromptText(t *testing.T) {
	srv, ws := newTestServer(t)
	id := firstPane(t, srv, ws)
	setAccess(t, srv, "full-dev", "full", false)
	setAccess(t, srv, "viewer-dev", "viewer", false)
	setAccess(t, srv, "watch-dev", "viewer", true)
	ts := remoteServer(t, srv)
	local := dialControl(t, srv)
	nextState(t, local, nil)

	p, _ := ask(srv, func() *workspace.Pane { return ws.Pane(id) })
	for _, dev := range []string{"full-dev", "viewer-dev", "watch-dev"} {
		conn, err := dialRemoteControlDevice(ts, ts.URL, dev)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.CloseNow() })
		for _, pr := range relayPrompts() {
			p.Sess.SetStatusFull(session.StatusWorking, "", "")
			p.Sess.SetStatusFull(session.StatusWaiting, pr.tool, pr.input)

			// What the desk is sent is the whole prompt.
			lst := nextState(t, local, func(s stateMsg) bool {
				pv, ok := s.Panes[id]
				return ok && pv.Status == "waiting" && (pv.Permission != nil && pv.Permission.Tool == pr.tool || pr.tool == "AskUserQuestion" && pv.Ask != nil)
			})
			if lpv := lst.Panes[id]; lpv.DetailsOnDesktop || (lpv.Permission != nil && lpv.Permission.Redacted) {
				t.Fatalf("%s: a window on this machine was sent the scrubbed form: %+v", pr.name, lpv)
			}

			if dev == "viewer-dev" {
				// A viewer the desk has not let watch panes is sent none, and
				// whatever it is sent holds nothing of the prompt.
				_, raws := collectTypes(t, conn, 300*time.Millisecond)
				for _, raw := range raws {
					if leaks := relayLeaks(raw, pr.others...); len(leaks) > 0 {
						t.Fatalf("%s to %s: the push holds %q", pr.name, dev, leaks)
					}
				}
				continue
			}
			all, st := readStateFor(t, conn, func(s stateMsg) bool {
				pv, ok := s.Panes[id]
				if pr.tool == "AskUserQuestion" {
					return ok && pv.Status == "waiting" && pv.DetailsOnDesktop && pv.Permission == nil
				}
				return ok && pv.Status == "waiting" && pv.Permission != nil && pv.Permission.Tool == pr.tool
			})
			for _, raw := range all {
				if leaks := relayLeaks(raw, pr.others...); len(leaks) > 0 {
					t.Fatalf("%s to %s: the push holds %q:\n%.400s", pr.name, dev, leaks, raw)
				}
			}
			pv := st.Panes[id]
			if pv.Ask != nil {
				t.Errorf("%s to %s: ask = %+v", pr.name, dev, pv.Ask)
			}
			if pr.tool == "AskUserQuestion" {
				if pv.Permission != nil {
					t.Errorf("%s to %s: permission = %+v", pr.name, dev, pv.Permission)
				}
				continue
			}
			perm := pv.Permission
			if perm == nil || !perm.Redacted || perm.Tool != pr.tool || perm.Summary != pr.summary || len(perm.Options) != 0 ||
				perm.Command != "" || perm.Description != "" || perm.File != "" || perm.Diff != "" {
				t.Errorf("%s to %s: permission = %+v", pr.name, dev, perm)
			}
		}
	}
}

// The agents list says the same of a waiting pane as the card does.
func TestRelayAgentsListSaysWhatTheCardSays(t *testing.T) {
	srv, ws := newTestServer(t)
	id := firstPane(t, srv, ws)
	setAccess(t, srv, "full-dev", "full", false)
	ts := remoteServer(t, srv)
	conn, err := dialRemoteControlDevice(ts, ts.URL, "full-dev")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })
	p, _ := ask(srv, func() *workspace.Pane { return ws.Pane(id) })

	for _, pr := range relayPrompts() {
		p.Sess.SetStatusFull(session.StatusWaiting, pr.tool, pr.input)
		want := pr.summary
		if pr.tool == "AskUserQuestion" {
			want = "asking a question"
		}
		deadline := time.Now().Add(10 * time.Second)
		for {
			sendCmd(t, conn, command{Cmd: "agents"})
			var ag agentsMsg
			readUntil(t, conn, "agents", &ag)
			var got string
			for _, a := range ag.Items {
				if a.PaneID == id {
					got = a.Waiting
				}
			}
			raw, _ := json.Marshal(ag)
			if leaks := relayLeaks(raw, pr.others...); len(leaks) > 0 {
				t.Fatalf("%s: the list holds %q", pr.name, leaks)
			}
			if got == want {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: waiting = %q, want %q", pr.name, got, want)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
}

// Everything that is not a prompt in a state push is sent as it was: the scrub
// of a state with nothing to scrub gives back the same bytes.
func TestRelayScrubLeavesAStateWithNoPromptAlone(t *testing.T) {
	srv, ws := newTestServer(t)
	conn := dialControl(t, srv)
	firstPane(t, srv, ws)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	for i := 0; i < 20; i++ {
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if messageType(data) != "state" {
			continue
		}
		if got := scrubForRemote(data); !bytes.Equal(got, data) {
			t.Fatalf("the scrub changed a state with no prompt:\n%.300s\n%.300s", data, got)
		}
		return
	}
	t.Fatal("no state")
}

// The scrub of a message it cannot read drops it, and a type with none is let
// through.
func TestRelayScrubFailsClosed(t *testing.T) {
	for typ := range relayScrubs {
		if got := scrubForRemote([]byte(`{"type":"` + typ + `","panes":"not an object","items":7,"entries":3,"detail":4,"teams":5}`)); got != nil {
			t.Errorf("%s: a message the scrub cannot read was sent: %s", typ, got)
		}
	}
	other := []byte(`{"type":"notice","text":"hello"}`)
	if got := scrubForRemote(other); !bytes.Equal(got, other) {
		t.Errorf("a notice was changed: %s", got)
	}
}

// A structured pane's content is for the desk: a relay window is sent its
// roster fields and the flag, and a structured message is not sent at all.
func TestRelayStatePushCarriesNoStructuredPaneContent(t *testing.T) {
	e := newStructuredEnv(t, "hold-perm")
	e.status(t, session.StatusWaiting)
	setAccess(t, e.srv, "full-dev", "full", false)
	setAccess(t, e.srv, "watch-dev", "viewer", true)
	ts := remoteServer(t, e.srv)
	local := dialControl(t, e.srv)

	lst := nextState(t, local, func(s stateMsg) bool {
		pv, ok := s.Panes[e.child]
		return ok && pv.Structured && pv.Permission != nil
	})
	lpv := lst.Panes[e.child]
	if lpv.Task == "" {
		t.Fatalf("the desk's view has no task: %+v", lpv)
	}

	for _, dev := range []string{"full-dev", "watch-dev"} {
		conn, err := dialRemoteControlDevice(ts, ts.URL, dev)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.CloseNow() })
		all, st := readStateFor(t, conn, func(s stateMsg) bool {
			pv, ok := s.Panes[e.child]
			return ok && pv.Structured
		})
		pv := st.Panes[e.child]
		if pv.Permission != nil || pv.Ask != nil || pv.Last != nil || pv.Detail != "" || pv.Task != "" || pv.Err != "" || !pv.DetailsOnDesktop {
			t.Errorf("%s: structured pane view = %+v", dev, pv)
		}
		if pv.Name != lpv.Name || pv.Status != "waiting" || pv.Activity != lpv.Activity {
			t.Errorf("%s: the roster fields changed: %+v", dev, pv)
		}
		for _, raw := range all {
			if bytes.Contains(raw, []byte(lpv.Permission.Tool+`","summary"`)) && bytes.Contains(raw, []byte(lpv.Task)) {
				t.Errorf("%s: the push holds the pane's task and card: %.300s", dev, raw)
			}
		}
	}

	for _, typ := range []string{"structured", "structuredDetail"} {
		if got := scrubForRemote([]byte(`{"type":"` + typ + `","id":"p"}`)); got != nil {
			t.Errorf("%s was not dropped", typ)
		}
	}
}

// A relay window is refused the graph commands, whatever its role, and is not
// subscribed. Lifting this needs the scrub above and a sealed channel (X1).
func TestRelayWindowCannotWatchTheGraph(t *testing.T) {
	srv, _ := newTestServer(t)
	for _, c := range []*controlClient{
		{out: make(chan []byte, 8), remote: true, device: "full-dev"},
		{out: make(chan []byte, 8), remote: true, device: "watch-dev"},
	} {
		srv.handleCommand(c, command{Cmd: "graphWatch", Req: "r"})
		var res teamResultMsg
		if err := json.Unmarshal(<-c.out, &res); err != nil || res.OK || res.Error != teamDeskOnly {
			t.Errorf("%s: %+v", c.device, res)
		}
		if root, _ := ask(srv, func() string { return c.graphRoot }); root != "" {
			t.Errorf("%s is subscribed", c.device)
		}
	}
}

// The decision is made on the connection being a relay one, not on a device's
// role: a full device is scrubbed, a window on this machine is not.
func TestRelayScrubKeysOnTheConnection(t *testing.T) {
	src, err := os.ReadFile("control.go")
	if err != nil {
		t.Fatal(err)
	}
	i := bytes.Index(src, []byte("func (c *controlClient) write("))
	if i < 0 || !bytes.Contains(src[i:i+600], []byte("if c.remote {")) || !bytes.Contains(src[i:i+600], []byte("scrubForRemote(data)")) {
		t.Error("controlClient.write does not scrub when c.remote")
	}
}

// Everything that builds a permission or a question for a window is covered by
// a scrub. The non-test files are scanned: a struct with a *permissionView or
// *askView field, and each file that calls waitingViews, must be in a table
// that names the message type carrying it, and that type must be registered. A
// new message that holds a prompt fails here until it is.
func TestRelayScrubCoversEveryCarrier(t *testing.T) {
	structs := map[string]string{
		"paneView":              "state",
		"conversationPageMsg":   "conversationPage",
		"conversationAppendMsg": "conversationAppend",
		"conversationDetailMsg": "conversationDetail",
	}
	// The structured pane messages arrive with the structured pane commands
	// (internal/server/structured.go). They are listed so that the day they are
	// there they are covered; until then they are not a stale key.
	optional := map[string]string{
		"structuredMsg":       "structured",
		"structuredEntry":     "structured",
		"structuredDetailMsg": "structuredDetail",
	}
	for k, v := range optional {
		structs[k] = v
	}
	seen := map[string]bool{}
	callers := map[string]string{"agents.go": "agents", "control.go": "state", "teamgraph.go": "graph"}

	fset := token.NewFileSet()
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no files: %v", err)
	}
	foundStruct := false
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.TypeSpec:
				st, ok := x.Type.(*ast.StructType)
				if !ok || x.Name.Name == "permissionView" || x.Name.Name == "askView" || x.Name.Name == "lastReplyView" {
					return true
				}
				for _, fld := range st.Fields.List {
					if carriesPrompt(fld.Type) || carriesText(fld.Type) {
						typ, ok := structs[x.Name.Name]
						seen[x.Name.Name] = true
						foundStruct = foundStruct || x.Name.Name == "paneView"
						if !ok {
							t.Errorf("%s (%s) holds a permission or a question and is not in the carrier table: add it and register the scrub of its message in relayscrub.go", x.Name.Name, f)
						} else if _, ok := relayScrubs[typ]; !ok {
							t.Errorf("%s is carried by %q, which has no scrub registered", x.Name.Name, typ)
						}
					}
				}
			case *ast.CallExpr:
				if id, ok := x.Fun.(*ast.Ident); ok && id.Name == "waitingViews" {
					typ, ok := callers[f]
					if !ok {
						t.Errorf("%s calls waitingViews and is not in the carrier table: add it and register the scrub of its message in relayscrub.go", f)
					} else if _, ok := relayScrubs[typ]; !ok {
						t.Errorf("%s feeds %q, which has no scrub registered", f, typ)
					}
				}
			}
			return true
		})
	}
	for k := range structs {
		if _, opt := optional[k]; !seen[k] && !opt {
			t.Errorf("the carrier table lists %s, which holds no prompt or conversation text in this package: drop the key", k)
		}
	}
	if !foundStruct {
		t.Error("the scan did not find paneView: it is looking in the wrong place")
	}
}

// carriesText says whether a field holds conversation text that is scrubbed: a
// transcript.Entry or transcript.Detail (alone, behind a pointer or in a slice,
// embedded or named) or a lastReplyView.
func carriesText(e ast.Expr) bool {
	switch x := e.(type) {
	case *ast.StarExpr:
		return carriesText(x.X)
	case *ast.ArrayType:
		return carriesText(x.Elt)
	case *ast.Ident:
		return x.Name == "lastReplyView"
	case *ast.SelectorExpr:
		pkg, ok := x.X.(*ast.Ident)
		return ok && pkg.Name == "transcript" && (x.Sel.Name == "Entry" || x.Sel.Name == "Detail")
	}
	return false
}

// carriesPrompt says whether a field is a *permissionView or *askView.
func carriesPrompt(e ast.Expr) bool {
	star, ok := e.(*ast.StarExpr)
	if !ok {
		return false
	}
	id, ok := star.X.(*ast.Ident)
	return ok && (id.Name == "permissionView" || id.Name == "askView")
}

// The text a person or an agent chose for a node is cut when the graph is built.
func TestGraphCutsNamePeerBranchAndAgent(t *testing.T) {
	e := newGraphEnv(t)
	long := strings.Repeat("日", 400)
	ask(e.srv, func() bool {
		p := e.ws.Pane(e.eyes)
		p.Name, p.Branch, p.Agent, p.PeerName = long, long, long, long
		return true
	})
	g := e.snapshotGraph(t)
	n, _ := g.node(e.eyes)
	for what, c := range map[string][2]any{"name": {n.Name, graphNameCut}, "peer": {n.Peer, graphPeerCut}, "branch": {n.Branch, graphBranchCut}, "agent": {n.Agent, graphAgentCut}} {
		if got := len([]rune(c[0].(string))); got == 0 || got > c[1].(int) {
			t.Errorf("%s has %d characters, limit %d", what, got, c[1])
		}
	}
}

// A structured pane's view is cut to its roster fields, whatever it held, and a
// card of another pane has empty options in the bytes, not only after decoding.
func TestRelayPaneScrubUnit(t *testing.T) {
	secret := relaySecrets[0]
	structured := paneView{ID: "s", Name: "n " + secret, Status: "waiting", Structured: true,
		Detail: "d", Task: "t", Err: "e", Last: &lastReplyView{Text: "reply " + secret, TS: "ts1", Kind: "reply"},
		Permission: &permissionView{Tool: "Bash", Summary: "Run: x", Command: "x", Options: []permissionOption{{ID: "yes"}}},
		Ask:        &askView{Questions: []askQuestionView{{Question: "q"}}}}
	plain := paneView{ID: "p", Name: "n", Status: "waiting", Detail: "d " + secret, Task: "t " + secret,
		Last: &lastReplyView{Text: "reply " + secret, TS: "ts1", Kind: "reply"},
		Cwd:  "/home/" + secret, Branch: "b-" + secret, PeerName: "peer " + secret, Project: "pr " + secret,
		Conflicts:  []conflictView{{ID: "c", Name: "c " + secret, Branch: "x " + secret, Paths: []string{"f-" + secret}}},
		Permission: &permissionView{Tool: "Bash", Summary: "Run: x", Command: "x", Options: []permissionOption{{ID: "yes"}}}}
	st := scrubStateMsg(mustJSON(t, stateMsg{Type: "state", Panes: map[string]paneView{"s": structured, "p": plain}}))
	if leaks := relayLeaks(st); len(leaks) > 0 {
		t.Errorf("the state holds %q:\n%s", leaks, st)
	}
	var back stateMsg
	if err := json.Unmarshal(st, &back); err != nil {
		t.Fatal(err)
	}
	s := back.Panes["s"]
	if s.Last != nil || s.Detail != "" || s.Task != "" || s.Err != "" || s.Permission != nil || s.Ask != nil || !s.DetailsOnDesktop || s.Status != "waiting" {
		t.Errorf("structured = %+v", s)
	}
	p := back.Panes["p"]
	if p.Last == nil || p.Last.Text != "" || p.Last.Kind != "reply" || p.Last.TS != "ts1" || p.Detail != "" || p.Task != "" || !p.DetailsOnDesktop {
		t.Errorf("plain = %+v", p)
	}
	if !bytes.Contains(st, []byte(`"options":[]`)) {
		t.Errorf("the scrubbed card does not have empty options:\n%s", st)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The agents list says nothing of a structured pane's detail, as the state push
// does not, and redacts the rest.
func TestRelayAgentsScrubUnit(t *testing.T) {
	secret := relaySecrets[1]
	raw := scrubAgentsMsg(mustJSON(t, agentsMsg{Type: "agents", Items: []agentView{
		{PaneID: "s", Structured: true, Detail: "typed " + secret, Name: "n " + secret},
		{PaneID: "p", Detail: "typed " + secret, Branch: "b-" + secret, Root: "/r/" + secret}}}))
	if leaks := relayLeaks(raw); len(leaks) > 0 {
		t.Errorf("the list holds %q:\n%s", leaks, raw)
	}
	var back agentsMsg
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back.Items[0].Detail != "" || back.Items[1].Detail != "" {
		t.Errorf("details = %q, %q", back.Items[0].Detail, back.Items[1].Detail)
	}
}

// The scan knows each shape of a field that holds text, so removing a branch of
// carriesText fails here.
func TestRelayCarriesTextRecognisesEveryShape(t *testing.T) {
	for _, src := range []string{"*lastReplyView", "lastReplyView", "[]transcript.Entry", "transcript.Entry", "*transcript.Entry", "transcript.Detail", "[]*transcript.Entry"} {
		e, err := parser.ParseExpr(src)
		if err != nil {
			t.Fatal(err)
		}
		if !carriesText(e) {
			t.Errorf("%s is not recognised as a field that holds text", src)
		}
	}
	for _, src := range []string{"string", "[]string", "*stateMsg", "time.Time", "transcript.Kind"} {
		e, _ := parser.ParseExpr(src)
		if carriesText(e) {
			t.Errorf("%s is recognised as a field that holds text", src)
		}
	}
}

// The agents list reaches a relay window with the structured flag set for a
// structured pane. That flag is what keeps the pane's detail off the phone, so
// it is checked in the bytes sent, and the detail is empty.
func TestRelayAgentsListMarksAStructuredPane(t *testing.T) {
	e := newStructuredEnv(t, "plain")
	e.status(t, session.StatusIdle)
	setAccess(t, e.srv, "full-dev", "full", false)
	ts := remoteServer(t, e.srv)
	conn, err := dialRemoteControlDevice(ts, ts.URL, "full-dev")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		sendCmd(t, conn, command{Cmd: "agents"})
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, data, err := conn.Read(ctx)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		if messageType(data) != "agents" {
			continue
		}
		var ag agentsMsg
		if json.Unmarshal(data, &ag) != nil {
			continue
		}
		for _, it := range ag.Items {
			if it.PaneID != e.child {
				continue
			}
			if !it.Structured || it.Detail != "" || !bytes.Contains(data, []byte(`"structured":true`)) {
				t.Fatalf("the structured pane in the list = %+v\n%s", it, data)
			}
			return
		}
	}
	t.Fatal("the structured pane was not listed")
}
