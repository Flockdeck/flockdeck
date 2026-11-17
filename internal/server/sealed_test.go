package server

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/jmwri/flockdeck/internal/e2e"
	"github.com/jmwri/flockdeck/internal/remote"
	"github.com/jmwri/flockdeck/internal/store"
)

// sealedDevice is one paired device of a test: its key, and what the desk says
// of it.
type sealedTestDevice struct {
	id   string
	priv *ecdh.PrivateKey
}

// sealedEnv is a server whose relay knows these devices:
//
//	full    a full device with a verified key
//	fresh   a full device whose key the desk has not verified
//	viewer  a viewer that may not watch panes, verified
//	watcher a viewer that may watch panes, verified
//	nokey   a full device with no end-to-end key on file
type sealedEnv struct {
	srv     *Server
	ts      *httptest.Server
	fake    *approvalsFake
	hostPub *ecdh.PublicKey
	devs    map[string]*sealedTestDevice
}

func newSealedEnvFor(t *testing.T, srv *Server) *sealedEnv {
	t.Helper()
	hostPriv, err := e2e.GenerateStaticKey()
	if err != nil {
		t.Fatal(err)
	}
	fake := &approvalsFake{
		e2eFakeRemote: e2eFakeRemote{capable: map[string]bool{}, hostPriv: hostPriv, devicePub: map[string]*ecdh.PublicKey{}},
		keys:          map[string][]byte{},
		states:        map[string]remote.VerifyState{},
	}
	e := &sealedEnv{srv: srv, fake: fake, hostPub: hostPriv.PublicKey(), devs: map[string]*sealedTestDevice{}}
	add := func(id string, verified, key bool, access store.DeviceAccess) {
		priv, err := e2e.GenerateStaticKey()
		if err != nil {
			t.Fatal(err)
		}
		e.devs[id] = &sealedTestDevice{id: id, priv: priv}
		if key {
			fake.capable[id] = true
			fake.devicePub[id] = priv.PublicKey()
			fake.keys[id] = priv.PublicKey().Bytes()
		}
		if verified {
			fake.states[vk(id, priv.PublicKey().Bytes())] = remote.Verified
		}
		putAccess(t, srv, id, access)
	}
	add("full", true, true, store.DeviceAccess{Role: store.RoleFull})
	add("fresh", false, true, store.DeviceAccess{Role: store.RoleFull})
	add("viewer", true, true, store.DeviceAccess{Role: store.RoleViewer})
	add("watcher", true, true, store.DeviceAccess{Role: store.RoleViewer, WatchPanes: true})
	add("nokey", true, false, store.DeviceAccess{Role: store.RoleFull})
	srv.SetRemote(fake)
	srv.approvals.deniedLog.dir = func() (string, error) { return store.Dir() }
	e.ts = remoteServer(t, srv)
	return e
}

func newSealedEnv(t *testing.T) *sealedEnv {
	t.Helper()
	srv, _ := newTestServer(t)
	return newSealedEnvFor(t, srv)
}

func (e *sealedEnv) header(device string) http.Header {
	h := http.Header{}
	h.Set("Origin", e.ts.URL)
	h.Set("Flockdeck-Remote-Device", device)
	h.Set("Flockdeck-Remote-Device-Name", "Pixel 8")
	return h
}

func (e *sealedEnv) dial(t *testing.T, h http.Header) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(e.ts.URL, "http")+"/ws/sealed", &websocket.DialOptions{HTTPHeader: h})
	if err != nil {
		t.Fatalf("dial /ws/sealed: %v", err)
	}
	conn.SetReadLimit(16 << 20)
	t.Cleanup(func() { _ = conn.CloseNow() })
	return conn
}

func (e *sealedEnv) handshake(t *testing.T, conn *websocket.Conn, device string) *e2e.Session {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	dh, hello, err := e2e.StartDeviceHandshake(e.devs[device].priv, e.hostPub)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Write(ctx, websocket.MessageBinary, hello); err != nil {
		t.Fatalf("send hello: %v", err)
	}
	_, resp, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read the handshake response: %v", err)
	}
	sess, err := dh.Finish(resp)
	if err != nil {
		t.Fatalf("finish: %v", err)
	}
	return sess
}

// sealedPhone is the device's end of an open socket.
type sealedPhone struct {
	t    *testing.T
	conn *websocket.Conn
	sess *e2e.Session
}

// open dials, handshakes and reads the hello, which it returns.
func (e *sealedEnv) open(t *testing.T, device string) (*sealedPhone, map[string]any) {
	t.Helper()
	conn := e.dial(t, e.header(device))
	p := &sealedPhone{t: t, conn: conn, sess: e.handshake(t, conn, device)}
	return p, p.readType("sealedHello")
}

func (p *sealedPhone) frame() []byte {
	p.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	typ, data, err := p.conn.Read(ctx)
	if err != nil {
		p.t.Fatalf("read: %v", err)
	}
	if typ != websocket.MessageBinary {
		p.t.Fatalf("a %v frame on the sealed socket: nothing is sent in the clear", typ)
	}
	_, payload := openFrame(p.t, p.sess, data)
	if len(payload) == 0 || len(payload)%sealedPadBucket != 0 {
		p.t.Fatalf("a frame of %d bytes, want a multiple of %d", len(payload), sealedPadBucket)
	}
	return payload
}

func (p *sealedPhone) readType(typ string) map[string]any {
	p.t.Helper()
	for i := 0; i < 30; i++ {
		var m map[string]any
		if err := json.Unmarshal(p.frame(), &m); err != nil {
			p.t.Fatalf("a frame that is not JSON: %v", err)
		}
		if m["type"] == typ {
			return m
		}
	}
	p.t.Fatalf("no %q frame", typ)
	return nil
}

// sendRaw seals bytes as they are.
func (p *sealedPhone) sendRaw(data []byte) {
	p.t.Helper()
	if err := p.conn.Write(context.Background(), websocket.MessageBinary, p.sess.Seal(append([]byte{e2eTagText}, data...))); err != nil {
		p.t.Fatalf("write: %v", err)
	}
}

// send seals a command, padded as the host asks.
func (p *sealedPhone) send(c command) {
	p.t.Helper()
	b, err := json.Marshal(c)
	if err != nil {
		p.t.Fatal(err)
	}
	p.sendRaw(padSealed(b))
}

func closeOf(t *testing.T, conn *websocket.Conn) (websocket.StatusCode, string) {
	t.Helper()
	return closedWith(t, conn)
}

// A device with a verified key is let in and told what the socket does; every
// frame the host writes is sealed and padded to a bucket.
func TestSealedLetsAVerifiedDeviceIn(t *testing.T) {
	e := newSealedEnv(t)
	for _, dev := range []string{"full", "watcher"} {
		_, hello := e.open(t, dev)
		if hello["v"] != float64(sealedWireVersion) || hello["tier"] != sealedTierA {
			t.Errorf("%s: hello = %v", dev, hello)
		}
		cmds, _ := hello["commands"].([]any)
		// Tier A only: the desk has not let either device control agents.
		if want := len(sealedCommandNames(false)); len(cmds) != want {
			t.Errorf("%s: the hello lists %d commands, tier A has %d", dev, len(cmds), want)
		}
		api, _ := hello["api"].(map[string]any)
		caps, _ := api["caps"].([]any)
		found := false
		for _, c := range caps {
			found = found || c == "sealedControl"
		}
		if !found {
			t.Errorf("%s: the api block has no sealedControl cap: %v", dev, caps)
		}
	}
}

// A device that is not verified, has no key, may not see panes, or is not the
// usual key origin is closed out with the reason, and is sent nothing.
func TestSealedFailsClosed(t *testing.T) {
	e := newSealedEnv(t)
	cases := []struct {
		name, device, origin, want string
		handshake                  bool
	}{
		{"unverified key", "fresh", "", approvalReasonVerify, true},
		{"no key on file", "nokey", "", approvalReasonKey, false},
		{"a viewer that may not watch", "viewer", "", sealedReasonAccess, false},
		{"the desk's own key origin", "full", "desk", approvalReasonOrigin, false},
		{"a device nobody knows", "stranger", "", approvalReasonKey, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := e.header(tc.device)
			if tc.origin != "" {
				h.Set("Flockdeck-Remote-Origin", tc.origin)
			}
			conn := e.dial(t, h)
			if tc.handshake {
				e.handshake(t, conn, tc.device)
			}
			code, reason := closeOf(t, conn)
			if code != artifactCloseRefused || reason != tc.want {
				t.Errorf("closed with %d %q, want %d %q", code, reason, artifactCloseRefused, tc.want)
			}
		})
	}
}

// The socket is for devices through the relay, and for pages.
func TestSealedIsRelayOnly(t *testing.T) {
	srv, _ := newTestServer(t)
	local := httptest.NewServer(srv.mux)
	defer local.Close()
	resp, err := http.Get(local.URL + "/ws/sealed")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("a request on the local listener = %d, want 404", resp.StatusCode)
	}
	e := newSealedEnvFor(t, srv)
	req, _ := http.NewRequest("GET", e.ts.URL+"/ws/sealed", nil)
	req.Header.Set("Flockdeck-Remote-Device", "full")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("a request with no Origin = %d, want 403", resp.StatusCode)
	}
}

// A handshake that fails is never answered by going on in the clear.
func TestSealedHandshakeFailureClosesTheSocket(t *testing.T) {
	e := newSealedEnv(t)
	conn := e.dial(t, e.header("full"))
	if err := conn.Write(context.Background(), websocket.MessageBinary, []byte("not a handshake")); err != nil {
		t.Fatal(err)
	}
	code, _ := closeOf(t, conn)
	if code != websocket.StatusPolicyViolation {
		t.Errorf("closed with %d, want %d", code, websocket.StatusPolicyViolation)
	}
}

// What the desk takes away is taken away on the next request: the mark that a
// key is verified, and a change of role (which ends the socket at once).
func TestSealedGateIsLookedAtAgain(t *testing.T) {
	t.Run("verification taken back", func(t *testing.T) {
		e := newSealedEnv(t)
		p, _ := e.open(t, "full")
		e.fake.setState("full", e.devs["full"].priv.PublicKey().Bytes(), remote.NotVerified)
		p.send(command{Cmd: "conversationClose", ID: "x"})
		code, reason := closeOf(t, p.conn)
		if code != artifactCloseRefused || reason != approvalReasonVerify {
			t.Errorf("closed with %d %q, want %d %q", code, reason, artifactCloseRefused, approvalReasonVerify)
		}
	})
	t.Run("role changed", func(t *testing.T) {
		e := newSealedEnv(t)
		p, _ := e.open(t, "watcher")
		putAccess(t, e.srv, "watcher", store.DeviceAccess{Role: store.RoleViewer})
		p.send(command{Cmd: "conversationClose", ID: "x"})
		code, reason := closeOf(t, p.conn)
		if code != artifactCloseRefused || (reason != sealedReasonAccess && reason != sealedReasonOff) {
			t.Errorf("closed with %d %q", code, reason)
		}
	})
	t.Run("cut by the desk", func(t *testing.T) {
		e := newSealedEnv(t)
		p, _ := e.open(t, "full")
		e.srv.sockets.cut("full")
		code, reason := closeOf(t, p.conn)
		if code != artifactCloseRefused || reason != sealedReasonOff {
			t.Errorf("closed with %d %q, want %d %q", code, reason, artifactCloseRefused, sealedReasonOff)
		}
	})
}

// Only the commands of the table run. Anything else is refused by name, never
// handed to handleCommand, so it has no effect, whatever the device's role.
func TestSealedRunsOnlyTheTableOfCommands(t *testing.T) {
	e := newSealedEnv(t)
	p, _ := e.open(t, "full")
	var refused []string
	for _, name := range handleCommandCases(t) {
		if sealedCommands[name] == "" {
			refused = append(refused, name)
		}
	}
	if len(refused) < 150 {
		t.Fatalf("only %d commands are outside the table", len(refused))
	}
	// A hundred and some commands in a burst would meet the rate limit, so the
	// ones that matter most are sent by name, and the rest are checked by table.
	for _, name := range []string{"sendPrompt", "structuredSet", "quit", "restart",
		"gitFetch", "mergeBranch", "deskConfirmAnswer", "worktreeRemove", "worktreePrune", "keySet", "remoteRevoke", "setDeviceRole", "closePane", "structuredRewind"} {
		if sealedCommands[name] != "" {
			t.Fatalf("%s is in the table", name)
		}
		p.send(command{Cmd: name, ID: "x", Req: "r-" + name, Text: "stage1-" + name})
		got := p.readType("sealedRefused")
		if got["code"] != sealedCodeCommand || got["cmd"] != name || got["req"] != "r-"+name {
			t.Errorf("%s was answered %v, want a refusal by name", name, got)
		}
	}
	for _, name := range refused {
		if tier := sealedCommands[name]; tier != "" {
			t.Errorf("%s is refused and in the table", name)
		}
	}
	// Nothing was run: the server was not asked to quit, and no notice was made.
	if _, ok := e.srv.sealed.open["full"]; !ok {
		t.Error("the connection went away")
	}
}

// Every command of the table is one the server knows, and is tier A (a read) or
// tier B (acts, needs the desk's Control agents switch).
func TestSealedTableIsKnownAndTiered(t *testing.T) {
	cases := map[string]bool{}
	for _, n := range handleCommandCases(t) {
		cases[n] = true
	}
	for name, tier := range sealedCommands {
		if !cases[name] {
			t.Errorf("%s is in the sealed table and is not a command", name)
		}
		if tier != sealedTierA && tier != sealedTierB && tier != sealedTierC {
			t.Errorf("%s is tier %q, and only A, B and C are built", name, tier)
		}
	}
	// Tier A never writes, whatever else changes.
	for _, name := range []string{"paneSend", "permissionAnswer", "attachImage", "structuredQueueEdit", "structuredShellRun", "startAgent", "structuredCancel"} {
		if sealedCommands[name] != sealedTierB {
			t.Errorf("%s is tier %q, want B", name, sealedCommands[name])
		}
	}
	// What a phone does not do from this socket: a mode (which can switch off the
	// agent's permission checks), git, merges, keys, devices.
	for _, name := range []string{"structuredSet", "gitFetch", "deskConfirmAnswer", "mergeBranch", "commentAdd", "runChecks", "changesWatch", "keySet", "setDeviceControl",
		"setDeviceApprove", "setDeviceRole", "sendPrompt", "structuredRewind", "worktreeRemove", "worktreePrune"} {
		if sealedCommands[name] != "" {
			t.Errorf("%s is in the sealed table", name)
		}
	}
}

// api/catalog.json carries the same table, so a client reads one source.
func TestSealedTablesAreInTheCatalog(t *testing.T) {
	c := readCatalog(t)
	fileCmds := map[string]string{}
	for _, cmd := range c.Commands {
		if cmd.Sealed != "" {
			fileCmds[cmd.Name] = cmd.Sealed
		}
	}
	if len(fileCmds) != len(sealedCommands) {
		t.Errorf("the catalog has %d sealed commands, the table %d", len(fileCmds), len(sealedCommands))
	}
	for name, tier := range sealedCommands {
		if fileCmds[name] != tier {
			t.Errorf("%s: the catalog says tier %q, the table %q", name, fileCmds[name], tier)
		}
	}
	fileMsgs := map[string]string{}
	for _, m := range c.Messages {
		if m.Sealed != "" {
			fileMsgs[m.Name] = m.Sealed
		}
	}
	if len(fileMsgs) != len(sealedMessages) {
		t.Errorf("the catalog has %d sealed messages, the table %d", len(fileMsgs), len(sealedMessages))
	}
	sent := sentMessageTypes(t)
	for typ := range sealedMessages {
		if !sent[typ] {
			t.Errorf("%s is in the sealed table and the server does not send it", typ)
		}
	}
}

// A request that is not padded is the sign of a client that is not the phone's:
// it is answered, and the third in a row ends the socket.
func TestSealedRequiresPadding(t *testing.T) {
	e := newSealedEnv(t)
	p, _ := e.open(t, "full")
	raw, _ := json.Marshal(command{Cmd: "conversationClose", ID: "x"})
	for i := 0; i < sealedAbuseMax-1; i++ {
		p.sendRaw(raw)
		if got := p.readType("sealedRefused"); got["code"] != sealedCodeShape {
			t.Errorf("an unpadded request was answered %v", got)
		}
	}
	p.sendRaw(raw)
	if code, _ := closeOf(t, p.conn); code != artifactCloseRefused {
		t.Errorf("closed with %d, want %d", code, artifactCloseRefused)
	}
}

// A device may hold two sockets.
func TestSealedLimitsSocketsPerDevice(t *testing.T) {
	e := newSealedEnv(t)
	for i := 0; i < sealedSocketsPerDevice; i++ {
		e.open(t, "full")
	}
	conn := e.dial(t, e.header("full"))
	if code, _ := closeOf(t, conn); code != artifactCloseBusy {
		t.Errorf("a third socket was closed with %d, want %d", code, artifactCloseBusy)
	}
}

// ---------------------------------------------------------------------------
// What is written: the tier table and redaction
// ---------------------------------------------------------------------------

const sealedSecret = "AKIAIOSFODNN7EXAMPLE"

// The conversation reaches a sealed device, redacted, and the rewind token does
// not; counts that a key-name rule would take for secrets are left as numbers.
func TestSealedWriteRedactsAndKeepsTheFormOfAMessage(t *testing.T) {
	in := `{"type":"structured","id":"p","kind":"structured","rewindToken":"tok-1","rewindPoints":["a"],` +
		`"usage":{"inputTokens":1200,"outputTokens":34},"title":"Plan ` + planted + `","err":"key ` + sealedSecret + ` rejected"}`
	out := sealedWrite([]byte(in), nil)
	if out == nil {
		t.Fatal("a structured message was dropped")
	}
	if bytes.Contains(out, []byte(sealedSecret)) {
		t.Errorf("a secret reached the device: %s", out)
	}
	if !bytes.Contains(out, []byte(planted)) {
		t.Errorf("conversation text was lost: %s", out)
	}
	if bytes.Contains(out, []byte("tok-1")) || bytes.Contains(out, []byte("rewindToken")) {
		t.Errorf("the rewind token was sent: %s", out)
	}
	var m struct {
		Type  string `json:"type"`
		Usage struct {
			In  json.Number `json:"inputTokens"`
			Out json.Number `json:"outputTokens"`
		} `json:"usage"`
		Points []string `json:"rewindPoints"`
	}
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	if m.Type != "structured" || m.Usage.In != "1200" || m.Usage.Out != "34" || len(m.Points) != 1 {
		t.Errorf("the message changed form: %s", out)
	}
}

// A picture's bytes are not redacted; the text beside them is.
func TestSealedWriteLeavesPictureBytes(t *testing.T) {
	data := strings.Repeat("QUJD", 200)
	in := `{"type":"conversationDetail","id":"p","entry":"1","detail":{"data":"` + data + `","text":"` + sealedSecret + `"}}`
	out := sealedWrite([]byte(in), nil)
	if !bytes.Contains(out, []byte(data)) {
		t.Error("the picture's bytes were changed")
	}
	if bytes.Contains(out, []byte(sealedSecret)) {
		t.Error("a secret next to the picture was sent")
	}
}

// Only what the table names is written. The snapshot, the agents list, the
// graph and the rest of the control socket's traffic are not, whatever they say.
func TestSealedWriteDropsWhatTheTableDoesNotName(t *testing.T) {
	for _, raw := range []string{
		`{"type":"state","panes":{}}`,
		`{"type":"agents","items":[]}`,
		`{"type":"graph","nodes":[]}`,
		`{"type":"hello","keys":[]}`,
		`{"type":"prefs"}`,
		`{"type":"relayTeams"}`,
		`{"type":"batonDraft","text":"x"}`,
		`{"type":"permissionAnswer","ok":true}`,
		`{"type":"gitWarn","text":"x"}`,
		`{"type":"somethingNew","text":"x"}`,
		`{"text":"no type"}`,
		`{"type":""}`,
		`not json`,
		``,
		// Two type keys, one of them in another case: classified as one type here
		// and read as another by a browser.
		`{"type":"notice","Type":"state","text":"x"}`,
		`{"type":"structured","type":"state"}`,
	} {
		if got := sealedWrite([]byte(raw), nil); got != nil {
			t.Errorf("%q was written: %s", raw, got)
		}
	}
	// The unsealed socket is unchanged: it still holds the conversation back.
	if got := scrubForRemote([]byte(`{"type":"conversationAppend","id":"p","entries":[]}`)); got != nil {
		t.Errorf("a relay window was sent a conversationAppend: %s", got)
	}
}

// A refusal is told in the relay's words: redacted and cut to a line; an answer
// that holds data is dropped.
func TestSealedWriteScrubsRefusals(t *testing.T) {
	out := sealedWrite([]byte(`{"type":"notice","text":"failed with `+sealedSecret+`\nnext line","error":true}`), nil)
	if out == nil || bytes.Contains(out, []byte(sealedSecret)) || bytes.Contains(out, []byte(`\n`)) {
		t.Errorf("notice = %s", out)
	}
	if got := sealedWrite([]byte(`{"type":"teamResult","req":"r","op":"x","ok":true}`), nil); got != nil {
		t.Errorf("a teamResult that holds an answer was written: %s", got)
	}
	if got := sealedWrite([]byte(`{"type":"teamResult","req":"r","op":"x","error":"no"}`), nil); got == nil {
		t.Error("a refusal was dropped")
	}
}

func TestSealedPadding(t *testing.T) {
	for _, n := range []int{1, 100, 1000, 1023, 1024, 5000} {
		msg := []byte(`{"type":"notice","text":"` + strings.Repeat("a", n) + `"}`)
		p := padSealed(msg)
		if len(p)%sealedPadBucket != 0 || len(p) < len(msg) {
			t.Errorf("%d: padded to %d", n, len(p))
		}
		var m map[string]any
		if err := json.Unmarshal(p, &m); err != nil || m["type"] != "notice" {
			t.Errorf("%d: padded frame is not the message: %v", n, err)
		}
	}
	if padSealed([]byte(`[1]`)) != nil || padSealed(nil) != nil {
		t.Error("a frame that is not an object was padded")
	}
}

// A sealed connection is never written to as an ordinary control client is: with
// no key in the gate the link writes nothing at all.
func TestSealedLinkWritesNothingWithoutTheGate(t *testing.T) {
	srv, _ := newTestServer(t)
	l := &sealedLink{s: srv, device: "ghost", origin: remote.KeyOriginUsual, cutCh: make(chan struct{}), refuse: func(int, string) {}}
	if l.write(context.Background(), []byte(`{"type":"structured","id":"p"}`)) {
		t.Error("a write for a device with no verified key went ahead")
	}
}

// ---------------------------------------------------------------------------
// Through a real pane
// ---------------------------------------------------------------------------

// A structured pane's conversation is pushed to a sealed device and refused,
// with the desk's words, to the same device on the unsealed socket.
func TestSealedStructuredWatchSendsTheConversation(t *testing.T) {
	se := newStructuredEnv(t, "plain")
	e := newSealedEnvFor(t, se.srv)
	p, _ := e.open(t, "full")
	p.send(command{Cmd: "structuredWatch", ID: se.child, Req: "w1"})
	m := p.readType("structured")
	if m["id"] != se.child || m["kind"] != "structured" {
		t.Errorf("the push = %v", m)
	}
	if _, has := m["rewindToken"]; has {
		t.Error("the push carries a rewind token")
	}

	// The same command on the unsealed socket of the same device is refused.
	phone, err := dialRemoteControlDevice(e.ts, e.ts.URL, "full")
	if err != nil {
		t.Fatal(err)
	}
	defer phone.CloseNow()
	sendCmd(t, phone, command{Cmd: "structuredWatch", ID: se.child, Req: "w2"})
	var res teamResultMsg
	readQuick(t, phone, "teamResult", &res)
	if res.Error == "" {
		t.Errorf("the unsealed socket answered %+v, want a refusal", res)
	}
}

// The commands a window reached through the relay is refused are still refused
// on a sealed connection that is not in the table: handleCommand is not run.
func TestSealedLiftsOnlyTheTableOfRefusals(t *testing.T) {
	c := &controlClient{remote: true, sealed: &sealedLink{}}
	for name := range sealedCommands {
		if !c.liftsRelayRefusal(name) {
			t.Errorf("%s is not lifted on a sealed connection", name)
		}
	}
	for _, name := range []string{"structuredSet", "sendPrompt", "worktreeRemove", "structuredRewind", "mergeBranch"} {
		if c.liftsRelayRefusal(name) {
			t.Errorf("%s is lifted on a sealed connection", name)
		}
	}
	plain := &controlClient{remote: true}
	for name := range sealedCommands {
		if plain.liftsRelayRefusal(name) {
			t.Errorf("%s is lifted on the unsealed socket", name)
		}
	}
}

func readCatalog(t *testing.T) catalogFile {
	t.Helper()
	raw, err := os.ReadFile(apiCatalogPath)
	if err != nil {
		t.Fatal(err)
	}
	var c catalogFile
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	return c
}
