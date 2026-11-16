package server

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/acp"
	"github.com/jmwri/flockdeck/internal/acp/bridge"
	"github.com/jmwri/flockdeck/internal/store"
)

// Tier B of /ws/sealed (stage 3): acting on agents. These tests hold the rules
// that matter: the desk's switch is off by default and tied to the key, it is
// looked at again for every request and every message, allow-always and tool
// approvals never come from here, and the unsealed socket still refuses it all.

// grantControl records the desk's "Control agents" switch for a test device, for
// the key the device handshakes with.
func (e *sealedEnv) grantControl(t *testing.T, device string) {
	t.Helper()
	e.setControl(t, device, store.RoleFull, true, store.KeyHash(e.devs[device].priv.PublicKey().Bytes()))
}

func (e *sealedEnv) setControl(t *testing.T, device string, role store.Role, on bool, keyHash string) {
	t.Helper()
	a := store.DeviceAccess{Role: role, WatchPanes: role == store.RoleViewer, Control: on}
	if on {
		a.ControlAt, a.ControlKey = time.Now().UTC(), keyHash
	}
	putAccess(t, e.srv, device, a)
}

func helloCommands(hello map[string]any) map[string]bool {
	out := map[string]bool{}
	list, _ := hello["commands"].([]any)
	for _, c := range list {
		if s, ok := c.(string); ok {
			out[s] = true
		}
	}
	return out
}

// Without the desk's switch a device is told tier A and nothing else, and a tier
// B request is refused by name and not run, however the device got the idea.
func TestSealedActIsOffUntilTheDeskTurnsItOn(t *testing.T) {
	e := newSealedEnv(t)
	p, hello := e.open(t, "full")
	if hello["tier"] != sealedTierA {
		t.Errorf("tier = %v, want A", hello["tier"])
	}
	cmds := helloCommands(hello)
	for name, tier := range sealedCommands {
		if cmds[name] != (tier == sealedTierA) {
			t.Errorf("%s (tier %s) in the hello = %v", name, tier, cmds[name])
		}
	}
	for name, tier := range sealedCommands {
		if tier != sealedTierB {
			continue
		}
		p.send(command{Cmd: name, ID: "x", Req: "r-" + name, Text: "hello"})
		got := p.readType("sealedRefused")
		if got["code"] != sealedCodeControl || got["cmd"] != name || got["req"] != "r-"+name {
			t.Errorf("%s was answered %v, want a refusal by name", name, got)
		}
	}
}

// The right is the desk's, for one device, one key, and a full role.
func TestSealedActNeedsTheRightDeviceKeyAndRole(t *testing.T) {
	e := newSealedEnv(t)
	e.grantControl(t, "full")
	if _, hello := e.open(t, "full"); hello["tier"] != sealedTierB {
		t.Errorf("a device with the switch is told tier %v", hello["tier"])
	} else if c := helloCommands(hello); !c["paneSend"] || !c["permissionAnswer"] || !c["startAgent"] || c["structuredSet"] || c["commit"] {
		t.Errorf("the tier B commands are %v", c)
	}

	// A grant made for another key is no grant.
	e.setControl(t, "full", store.RoleFull, true, store.KeyHash([]byte("another key")))
	if _, hello := e.open(t, "full"); hello["tier"] != sealedTierA {
		t.Errorf("a grant for another key gave tier %v", hello["tier"])
	}

	// A viewer, even one let watch, never holds it: a record that says so is read
	// as off.
	e.setControl(t, "watcher", store.RoleViewer, true, store.KeyHash(e.devs["watcher"].priv.PublicKey().Bytes()))
	p, hello := e.open(t, "watcher")
	if hello["tier"] != sealedTierA {
		t.Errorf("a viewer was told tier %v", hello["tier"])
	}
	p.send(command{Cmd: "paneSend", ID: "x", Text: "hi", Req: "v"})
	if got := p.readType("sealedRefused"); got["code"] != sealedCodeControl {
		t.Errorf("a viewer's paneSend was answered %v", got)
	}

	// Taking the right away in the preferences file ends it even if the running
	// program was not told.
	e.grantControl(t, "full")
	if !e.srv.mayControlWithKey("full", e.devs["full"].priv.PublicKey().Bytes()) {
		t.Fatal("the grant does not hold")
	}
	if err := store.SavePrefs(store.Prefs{}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	e.srv.approveFile = approveFileCache{}
	if e.srv.mayControlWithKey("full", e.devs["full"].priv.PublicKey().Bytes()) {
		t.Error("a device may act though the preferences file no longer says so")
	}
}

// The right is looked at again for every request: taken away at the desk, the
// next request is refused and the one after a new grant runs.
func TestSealedActIsLookedAtAgain(t *testing.T) {
	e := newSealedEnv(t)
	e.grantControl(t, "full")
	p, _ := e.open(t, "full")

	// A request that runs: the pane is not there, which is the notice of the real
	// handler, not the desk-only one the unsealed socket gives.
	p.send(command{Cmd: "paneSend", ID: "nope", Text: "hi", Req: "a"})
	n := p.readType("notice")
	if txt, _ := n["text"].(string); txt == "" || txt == paneSendDeskOnly {
		t.Errorf("paneSend answered %q, want the handler's own refusal", txt)
	}

	e.setControl(t, "full", store.RoleFull, false, "")
	e.srv.approveFile = approveFileCache{}
	p.send(command{Cmd: "paneSend", ID: "nope", Text: "hi", Req: "b"})
	if got := p.readType("sealedRefused"); got["code"] != sealedCodeControl {
		t.Errorf("after the switch went off: %v", got)
	}
	// The socket stays open for tier A.
	p.send(command{Cmd: "conversationClose", ID: "x"})

	e.grantControl(t, "full")
	e.srv.approveFile = approveFileCache{}
	p.send(command{Cmd: "paneSend", ID: "nope", Text: "hi", Req: "c"})
	p.readType("notice")
}

// The answers of tier B are written only to a device that may act, and a success
// is passed only for the ops tier B runs.
func TestSealedWriteOfTierBMessages(t *testing.T) {
	yes, no := func() bool { return true }, func() bool { return false }
	paneSendMsg := []byte(`{"type":"paneSend","id":"p","req":"r","queued":true}`)
	if got := sealedWrite(paneSendMsg, no); got != nil {
		t.Errorf("paneSend was written to a device that may not act: %s", got)
	}
	if got := sealedWrite(paneSendMsg, nil); got != nil {
		t.Errorf("paneSend was written with no check: %s", got)
	}
	if got := sealedWrite(paneSendMsg, yes); got == nil || !bytes.Contains(got, []byte(`"queued":true`)) {
		t.Errorf("paneSend = %s", got)
	}
	if got := sealedWrite([]byte(`{"type":"permissionAnswer","req":"r","ok":true,"error":"`+sealedSecret+`"}`), yes); got == nil || bytes.Contains(got, []byte(sealedSecret)) {
		t.Errorf("permissionAnswer = %s", got)
	}
	if got := sealedWrite([]byte(`{"type":"agentStarted","paneId":"p1","id":"r"}`), yes); got == nil {
		t.Error("agentStarted was dropped")
	}
	if got := sealedWrite([]byte(`{"type":"agentStarted","paneId":"p1","id":"r"}`), no); got != nil {
		t.Errorf("agentStarted was written: %s", got)
	}
	pic := strings.Repeat("QUJD", 100)
	if got := sealedWrite([]byte(`{"type":"attachImageResult","id":"p","req":"r","path":"C:/u/x.jpg","data":"`+pic+`"}`), yes); got == nil || !bytes.Contains(got, []byte(pic)) {
		t.Errorf("attachImageResult = %s", got)
	}

	ok := func(op string) []byte { return []byte(`{"type":"teamResult","req":"r","op":"` + op + `","ok":true}`) }
	if got := sealedWrite(ok("structuredCancel"), no); got != nil {
		t.Errorf("a success was written to a device that may not act: %s", got)
	}
	for op := range sealedActOps {
		if got := sealedWrite(ok(op), yes); got == nil {
			t.Errorf("the success of %s was dropped", op)
		}
	}
	for _, op := range []string{"taskAdd", "msgSend", "teamSnapshot", "structuredSet", ""} {
		if got := sealedWrite(ok(op), yes); got != nil {
			t.Errorf("the success of %q was written: %s", op, got)
		}
	}
	// A success holds no more than the op: any other field is cut.
	if got := sealedWrite([]byte(`{"type":"teamResult","req":"r","op":"structuredCancel","ok":true,"task":{"title":"secret plan"}}`), yes); got == nil || bytes.Contains(got, []byte("secret plan")) {
		t.Errorf("a success carried team data: %s", got)
	}
	// A refusal is told as before, with a device that may act or not.
	for _, act := range []func() bool{yes, no} {
		if got := sealedWrite([]byte(`{"type":"teamResult","req":"r","op":"structuredCancel","error":"`+sealedSecret+` no"}`), act); got == nil || bytes.Contains(got, []byte(sealedSecret)) {
			t.Errorf("a refusal = %s", got)
		}
	}
}

// What a phone may answer: a question or a plan, once. Never a tool request, an
// allow-always, the plan's accept-edits, or an allow for an agent Flockdeck did
// not write.
func TestSealedAnswerRefusal(t *testing.T) {
	plan := bridge.Card{Seq: 1, RequestID: "r", Kind: "switch_mode", Options: []acp.PermissionOption{
		{OptionID: "allow", Kind: "allow_once"}, {OptionID: "allow-edits", Kind: "allow_once"},
		{OptionID: "always", Kind: "allow_always"}, {OptionID: "reject", Kind: "reject_once"}, {OptionID: "never", Kind: "reject_always"}}}
	question := bridge.Card{Seq: 2, RequestID: "r", Kind: bridge.QuestionKind, Questions: []bridge.Question{{Question: "Which?"}}, Options: []acp.PermissionOption{
		{OptionID: "answer", Kind: "allow_once"}, {OptionID: "dismiss", Kind: "reject_once"}, {OptionID: "always", Kind: "allow_always"}}}
	tool := bridge.Card{Seq: 3, RequestID: "r", Kind: "execute", Options: []acp.PermissionOption{
		{OptionID: "allow", Kind: "allow_once"}, {OptionID: "reject", Kind: "reject_once"}}}
	cases := []struct {
		name string
		card bridge.Card
		opt  string
		kind string
		own  bool
		want string // "" is allowed
	}{
		{"plan allow once", plan, "allow", "allow_once", true, ""},
		{"plan reject once", plan, "reject", "reject_once", true, ""},
		{"plan accept edits", plan, "allow-edits", "allow_once", true, sealedAnswerPlain},
		{"plan allow always", plan, "always", "allow_always", true, sealedAnswerOnce},
		{"plan reject always", plan, "never", "reject_always", true, sealedAnswerOnce},
		{"plan with an option of no kind", plan, "x", "", true, sealedAnswerOnce},
		{"plan allow, foreign agent", plan, "allow", "allow_once", false, sealedAnswerForeign},
		{"plan reject, foreign agent", plan, "reject", "reject_once", false, ""},
		{"question answer", question, "answer", "allow_once", true, ""},
		{"question dismiss", question, "dismiss", "reject_once", true, ""},
		{"question allow always", question, "always", "allow_always", true, sealedAnswerOnce},
		{"question answer, foreign agent", question, "answer", "allow_once", false, sealedAnswerForeign},
		{"tool allow", tool, "allow", "allow_once", true, sealedAnswerNotQP},
		{"tool reject", tool, "reject", "reject_once", true, sealedAnswerNotQP},
	}
	for _, tc := range cases {
		if got := sealedAnswerRefusal(&tc.card, tc.opt, tc.kind, tc.own); got != tc.want {
			t.Errorf("%s: refused with %q, want %q", tc.name, got, tc.want)
		}
	}
}

// actPane is a pane with one card, that takes whatever answer reaches it.
type actPane struct {
	lenient
	own bool
}

func (a *actPane) OwnAdapter() bool { return a.own }

// permissionAnswer over the sealed socket: a plan or question is answered once,
// and the rest is refused before the pane is asked.
func TestSealedAnswersAPlanOnce(t *testing.T) {
	srv, _ := newTestServer(t)
	e := newSealedEnvFor(t, srv)
	e.grantControl(t, "full")

	plan := bridge.Card{Seq: 1, RequestID: "req-1", SessionID: "s1", Kind: "switch_mode", Tool: "Exit plan mode", Options: []acp.PermissionOption{
		{OptionID: "allow", Kind: "allow_once"}, {OptionID: "allow-edits", Kind: "allow_once"},
		{OptionID: "always", Kind: "allow_always"}, {OptionID: "reject", Kind: "reject_once"}}}
	pane := &actPane{own: true}
	pane.card = &plan
	tool := plan
	tool.Kind, tool.Seq, tool.RequestID = "execute", 2, "req-2"
	toolPane := &actPane{own: true}
	toolPane.card = &tool
	srv.SetStructuredPanes(func(id string) (StructuredPane, bool) {
		switch id {
		case "plan":
			return pane, true
		case "tool":
			return toolPane, true
		}
		return nil, false
	})
	old := permissionLog
	permissionLog = func(string, ...any) {}
	t.Cleanup(func() { permissionLog = old })

	p, _ := e.open(t, "full")
	ask := func(pane string, seq uint64, req, opt string) map[string]any {
		p.send(command{Cmd: "permissionAnswer", ID: pane, CardSeq: seq, RequestID: req, OptionID: opt, Req: "q-" + opt})
		return p.readType("permissionAnswer")
	}
	for _, opt := range []string{"always", "allow-edits"} {
		if got := ask("plan", 1, "req-1", opt); got["ok"] == true || got["error"] == nil || got["error"] == "" {
			t.Errorf("%s was answered %v", opt, got)
		}
	}
	if got := ask("tool", 2, "req-2", "allow"); got["ok"] == true {
		t.Errorf("a tool request was answered from the sealed socket: %v", got)
	}
	if n := len(pane.got()) + len(toolPane.got()); n != 0 {
		t.Fatalf("the pane was asked %d times for answers that had to be refused", n)
	}
	if got := ask("plan", 1, "req-1", "allow"); got["ok"] != true {
		t.Errorf("the plain approval was answered %v", got)
	}
	if a := pane.got(); len(a) != 1 || a[0].OptionID != "allow" || a[0].Seq != 1 {
		t.Errorf("the pane was given %v", a)
	}

	// The same device on the unsealed socket is still refused.
	phone, err := dialRemoteControlDevice(e.ts, e.ts.URL, "full")
	if err != nil {
		t.Fatal(err)
	}
	defer phone.CloseNow()
	sendCmd(t, phone, command{Cmd: "permissionAnswer", ID: "plan", CardSeq: 1, RequestID: "req-1", OptionID: "reject", Req: "u"})
	var res permissionAnswerMsg
	readQuick(t, phone, "permissionAnswer", &res)
	if res.OK || res.Error != permissionAnswerDeskOnly {
		t.Errorf("the unsealed socket answered %+v", res)
	}
	if n := len(pane.got()); n != 1 {
		t.Errorf("the pane was given %d answers, want the one", n)
	}
}

// Sending to and stopping a real structured pane over the sealed socket, and the
// same on the unsealed one still refused. Text for a shell is not sent.
func TestSealedSendsAndStops(t *testing.T) {
	se := newStructuredEnv(t, "plain")
	e := newSealedEnvFor(t, se.srv)
	e.grantControl(t, "full")
	p, _ := e.open(t, "full")

	p.send(command{Cmd: "paneSend", ID: se.child, Text: "go on", Req: "s1"})
	m := p.readType("paneSend")
	if m["id"] != se.child || m["req"] != "s1" {
		t.Errorf("paneSend = %v", m)
	}
	// A stop answers with a refusal or a success, in the words of the handler,
	// and never the desk-only one.
	p.send(command{Cmd: "structuredCancel", ID: se.child, Req: "c1"})
	res := p.readType("teamResult")
	if res["op"] != "structuredCancel" || res["error"] == structuredDeskOnly {
		t.Errorf("structuredCancel = %v", res)
	}

	phone, err := dialRemoteControlDevice(e.ts, e.ts.URL, "full")
	if err != nil {
		t.Fatal(err)
	}
	defer phone.CloseNow()
	sendCmd(t, phone, command{Cmd: "paneSend", ID: se.child, Text: "from the relay", Req: "u1"})
	var n noticeMsg
	readQuick(t, phone, "notice", &n)
	if n.Text != paneSendDeskOnly {
		t.Errorf("the unsealed socket answered %q", n.Text)
	}
	sendCmd(t, phone, command{Cmd: "structuredCancel", ID: se.child, Req: "u2"})
	var tr teamResultMsg
	readQuick(t, phone, "teamResult", &tr)
	if tr.Error != structuredDeskOnly {
		t.Errorf("the unsealed socket answered %+v", tr)
	}
}

// A task is a prompt: the unsealed socket does not start an agent from it, and
// the sealed one does not start a shell or work outside a known project.
func TestSealedStartAgentRules(t *testing.T) {
	srv, _ := newTestServer(t)
	e := newSealedEnvFor(t, srv)
	e.grantControl(t, "full")

	phone, err := dialRemoteControlDevice(e.ts, e.ts.URL, "full")
	if err != nil {
		t.Fatal(err)
	}
	defer phone.CloseNow()
	sendCmd(t, phone, command{Cmd: "startAgent", Task: "secret task", Root: t.TempDir()})
	var n noticeMsg
	readQuick(t, phone, "notice", &n)
	if n.Text != startAgentSealedOnly {
		t.Errorf("the unsealed socket answered %q", n.Text)
	}

	p, _ := e.open(t, "full")
	p.send(command{Cmd: "startAgent", Task: "go", Kind: "shell", Root: t.TempDir(), Req: "k"})
	if txt, _ := p.readType("notice")["text"].(string); !strings.Contains(txt, "shell") {
		t.Errorf("a shell was answered %q", txt)
	}
	p.send(command{Cmd: "startAgent", Task: "go", Root: t.TempDir(), Req: "o"})
	if txt, _ := p.readType("notice")["text"].(string); txt == "" || txt == startAgentSealedOnly {
		t.Errorf("a folder that is no project was answered %q", txt)
	}
}

// A request is the size of a picture only for attachImage.
func TestSealedOnlyAPictureMayBeLong(t *testing.T) {
	e := newSealedEnv(t)
	e.grantControl(t, "full")
	p, _ := e.open(t, "full")
	long := strings.Repeat("A", sealedMaxRequest)
	p.send(command{Cmd: "paneSend", ID: "x", Text: long, Req: "big"})
	if got := p.readType("sealedRefused"); got["code"] != sealedCodeShape || got["req"] != "big" {
		t.Errorf("a long paneSend was answered %v", got)
	}
	p.send(command{Cmd: "attachImage", ID: "nope", Name: "a.jpg", Data: long, Req: "img"})
	if res := p.readType("attachImageResult"); res["req"] != "img" || res["error"] == nil {
		t.Errorf("attachImage = %v", res)
	}
}

// The grant of control is the desk's: a relay window cannot give it, and a
// viewer's role change takes it away.
func TestSetDeviceControlIsDeskOnly(t *testing.T) {
	srv, _ := newTestServer(t)
	relay := &controlClient{out: make(chan []byte, 16), ready: make(chan struct{}, 1), remote: true, device: "full"}
	srv.handleCommand(relay, command{Cmd: "setDeviceControl", ID: "full", On: true, Confirmed: true})
	if srv.deviceAccess("full").Control {
		t.Error("a relay window turned control on")
	}
	if got := commandAccess["setDeviceControl"]; got != accessFull {
		t.Errorf("setDeviceControl is %v, want full only", got)
	}
	// Going to a viewer takes the grant away.
	srv.prefs, _ = srv.prefs.WithAccess("full", store.DeviceAccess{Role: store.RoleFull, Control: true, ControlKey: "k", ControlAt: time.Now()})
	srv.publishAccess()
	local := &controlClient{out: make(chan []byte, 16), ready: make(chan struct{}, 1), page: true}
	srv.applyDeviceRole(local, "full", store.DeviceAccess{Role: store.RoleViewer})
	time.Sleep(300 * time.Millisecond)
	if a := srv.deviceAccess("full"); a.Control || a.ControlKey != "" {
		t.Errorf("a viewer still holds control: %+v", a)
	}
}
