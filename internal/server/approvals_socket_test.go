package server

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/jmwri/flockdeck/internal/acp"
	"github.com/jmwri/flockdeck/internal/acp/bridge"
	"github.com/jmwri/flockdeck/internal/e2e"
	"github.com/jmwri/flockdeck/internal/remote"
	"github.com/jmwri/flockdeck/internal/store"
)

// approvalsFake is remote access with the real handshake, device keys a test can
// change and a verification record, which is what the approvals socket needs of
// the relay side.
type approvalsFake struct {
	e2eFakeRemote
	kmu  sync.Mutex
	keys map[string][]byte
	// states is the desk's verification record, by device and exact key.
	states map[string]remote.VerifyState
}

func vk(device string, key []byte) string { return device + "|" + hex.EncodeToString(key) }

func (f *approvalsFake) E2EDeviceKey(ctx context.Context, device string, _ remote.KeyOrigin) ([]byte, bool) {
	if ctx.Err() != nil {
		return nil, false
	}
	f.kmu.Lock()
	defer f.kmu.Unlock()
	k, ok := f.keys[device]
	return k, ok
}

func (f *approvalsFake) DeviceVerifyState(id string, _ remote.KeyOrigin, key []byte) remote.VerifyState {
	f.kmu.Lock()
	defer f.kmu.Unlock()
	return f.states[vk(id, key)]
}

func (f *approvalsFake) setState(id string, key []byte, s remote.VerifyState) {
	f.kmu.Lock()
	f.states[vk(id, key)] = s
	f.kmu.Unlock()
}

func (f *approvalsFake) VerifyDevice(context.Context, string, remote.KeyOrigin, string) error {
	return nil
}
func (f *approvalsFake) UnverifyDevice(string, remote.KeyOrigin) error { return nil }
func (f *approvalsFake) ForgetDevice(string) error                     { return nil }
func (f *approvalsFake) ForgetDevicesNotIn([]string) error             { return nil }

// fakeApprovalPane is a structured pane with one card the test can change. Its
// AnswerPermission is atomic and takes the first answer, as the runtime's does.
type fakeApprovalPane struct {
	mu      sync.Mutex
	card    *bridge.Card
	token   string
	own     bool
	root    string
	answers []string
	// afterAnswer, when set, is called after an answer was taken.
	afterAnswer func()
}

func (p *fakeApprovalPane) PendingPermission() *bridge.Card {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.card == nil {
		return nil
	}
	c := *p.card
	return &c
}

func (p *fakeApprovalPane) AnswerPermission(seq uint64, requestID, optionID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.card == nil || p.card.Seq != seq || p.card.RequestID != requestID {
		return ErrPermissionStale
	}
	p.answers = append(p.answers, optionID)
	p.card = nil
	if p.afterAnswer != nil {
		defer p.afterAnswer()
	}
	return nil
}

func (p *fakeApprovalPane) OwnAdapter() bool { return p.own }
func (p *fakeApprovalPane) RuntimeToken() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.token
}
func (p *fakeApprovalPane) ProjectRoot() string { return p.root }

func (p *fakeApprovalPane) set(c *bridge.Card) {
	p.mu.Lock()
	p.card = c
	p.mu.Unlock()
}

func (p *fakeApprovalPane) setToken(t string) {
	p.mu.Lock()
	p.token = t
	p.mu.Unlock()
}

func (p *fakeApprovalPane) given() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.answers...)
}

type approvalsEnv struct {
	srv        *Server
	ts         *httptest.Server
	fake       *approvalsFake
	hostPriv   *ecdh.PrivateKey
	devicePriv *ecdh.PrivateKey
	key        []byte
	pane       *fakeApprovalPane
	paneID     string
}

// A marker in each field of a card that is the agent's own words; none of them
// may reach a phone.
const (
	agentReq  = "REQ-MARK-1"
	agentTool = "TOOL-MARK-2"
	agentSum  = "SUM-MARK-3"
)

func goodCard(seq uint64, command string) *bridge.Card {
	c := execCard(command)
	c.Seq, c.RequestID, c.Tool, c.Summary = seq, agentReq, agentTool, agentSum
	return c
}

// newApprovalsEnv is a server whose relay has device d1 with a verified usual
// key, approval switched on for d1 at the current wording and for its key, one
// structured pane (our own adapter) with a command waiting, and short time
// limits. d2 has a key of its own.
func newApprovalsEnv(t *testing.T) *approvalsEnv {
	t.Helper()
	srv, _ := newTestServer(t)
	hostPriv, err := e2e.GenerateStaticKey()
	if err != nil {
		t.Fatal(err)
	}
	devicePriv, err := e2e.GenerateStaticKey()
	if err != nil {
		t.Fatal(err)
	}
	otherPriv, err := e2e.GenerateStaticKey()
	if err != nil {
		t.Fatal(err)
	}
	key := devicePriv.PublicKey().Bytes()
	fake := &approvalsFake{
		e2eFakeRemote: e2eFakeRemote{
			capable:   map[string]bool{"d1": true, "d2": true},
			hostPriv:  hostPriv,
			devicePub: map[string]*ecdh.PublicKey{"d1": devicePriv.PublicKey(), "d2": otherPriv.PublicKey()},
		},
		keys:   map[string][]byte{"d1": key, "d2": otherPriv.PublicKey().Bytes()},
		states: map[string]remote.VerifyState{vk("d1", key): remote.Verified, vk("d2", otherPriv.PublicKey().Bytes()): remote.Verified},
	}
	srv.SetRemote(fake)
	srv.approvals.audit.dir = func() (string, error) { return store.Dir() }
	srv.approvals.deniedLog.dir = func() (string, error) { return store.Dir() }
	srv.approvals.shownLog.dir = func() (string, error) { return store.Dir() }
	srv.approvals.setTimings(func(tm *approvalTimings) {
		tm.poll, tm.minDisplay = 20*time.Millisecond, -1
	})
	e := &approvalsEnv{srv: srv, ts: remoteServer(t, srv), fake: fake, hostPriv: hostPriv, devicePriv: devicePriv, key: key}
	e.paneID, _ = ask(srv, func() string { return srv.ws.Tabs[0].Tree.Panes()[0] })
	e.pane = &fakeApprovalPane{token: "7.0", own: true, root: testRoot}
	e.pane.set(goodCard(7, "go test ./pkg"))
	srv.SetStructuredPanes(func(id string) (StructuredPane, bool) {
		if id == e.paneID {
			return e.pane, true
		}
		return nil, false
	})
	e.grant(t, "d1", key)
	return e
}

// grant records approval for a device at the current wording, for a key.
func (e *approvalsEnv) grant(t *testing.T, device string, key []byte) {
	t.Helper()
	putAccess(t, e.srv, device, store.DeviceAccess{Role: store.RoleFull, Approve: true, ApproveV: approveAckVersion, ApproveAt: time.Now().UTC(), ApproveKey: store.KeyHash(key)})
}

func (e *approvalsEnv) header(device string) http.Header {
	h := http.Header{}
	h.Set("Origin", e.ts.URL)
	h.Set("Flockdeck-Remote-Device", device)
	h.Set("Flockdeck-Remote-Device-Name", "Pixel 8")
	return h
}

func (e *approvalsEnv) rawDial(t *testing.T, h http.Header) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(e.ts.URL, "http")+"/ws/approvals", &websocket.DialOptions{HTTPHeader: h})
	if conn != nil {
		conn.SetReadLimit(1 << 20)
		t.Cleanup(func() { _ = conn.CloseNow() })
	}
	return conn, resp, err
}

func (e *approvalsEnv) handshake(t *testing.T, conn *websocket.Conn) *e2e.Session {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	dh, hello, err := e2e.StartDeviceHandshake(e.devicePriv, e.hostPriv.PublicKey())
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

type phone struct {
	t    *testing.T
	conn *websocket.Conn
	sess *e2e.Session
}

func (e *approvalsEnv) open(t *testing.T) *phone {
	t.Helper()
	conn, _, err := e.rawDial(t, e.header("d1"))
	if err != nil {
		t.Fatalf("dial /ws/approvals: %v", err)
	}
	return &phone{t: t, conn: conn, sess: e.handshake(t, conn)}
}

func (p *phone) send(v any) {
	p.t.Helper()
	data, err := padded(v)
	if err != nil {
		p.t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := p.conn.Write(ctx, websocket.MessageBinary, p.sess.Seal(append([]byte{e2eTagText}, data...))); err != nil {
		p.t.Fatalf("write: %v", err)
	}
}

// sendQuiet writes a frame and ignores a failure: the host may have closed.
func (p *phone) sendQuiet(v any) {
	data, _ := padded(v)
	_ = p.conn.Write(context.Background(), websocket.MessageBinary, p.sess.Seal(append([]byte{e2eTagText}, data...)))
}

// sendRaw writes bytes as they are, sealed and tagged as text.
func (p *phone) sendRaw(data []byte) {
	p.t.Helper()
	if err := p.conn.Write(context.Background(), websocket.MessageBinary, p.sess.Seal(append([]byte{e2eTagText}, data...))); err != nil {
		p.t.Fatalf("write: %v", err)
	}
}

// raw reads the next sealed frame and returns its plaintext.
func (p *phone) raw() []byte {
	p.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	typ, frame, err := p.conn.Read(ctx)
	if err != nil {
		p.t.Fatalf("read: %v", err)
	}
	if typ != websocket.MessageBinary {
		p.t.Fatalf("a %v frame on the approvals socket", typ)
	}
	_, payload := openFrame(p.t, p.sess, frame)
	return payload
}

// rawOp is the plaintext of the first frame that has the op.
func (p *phone) rawOp(op string) []byte {
	p.t.Helper()
	for i := 0; i < 20; i++ {
		raw := p.raw()
		var m map[string]any
		if json.Unmarshal(raw, &m) == nil && m["op"] == op {
			return raw
		}
	}
	p.t.Fatalf("no %q frame", op)
	return nil
}

func (p *phone) read() map[string]any {
	p.t.Helper()
	var m map[string]any
	if err := json.Unmarshal(p.raw(), &m); err != nil {
		p.t.Fatalf("a frame that is not JSON: %v", err)
	}
	return m
}

// readOp reads frames until one has the op, and returns it.
func (p *phone) readOp(op string) map[string]any {
	p.t.Helper()
	for i := 0; i < 20; i++ {
		if m := p.read(); m["op"] == op {
			return m
		}
	}
	p.t.Fatalf("no %q frame", op)
	return nil
}

func (p *phone) list() []map[string]any {
	p.t.Helper()
	p.send(map[string]any{"op": "approvalList"})
	return items(p.readOp("approvals"))
}

func items(m map[string]any) []map[string]any {
	var out []map[string]any
	raw, _ := m["items"].([]any)
	for _, r := range raw {
		out = append(out, r.(map[string]any))
	}
	return out
}

func (p *phone) decide(it map[string]any, decision string) map[string]any {
	p.t.Helper()
	p.send(decideFor(it, decision))
	return p.readOp("approvalResult")
}

func decideFor(it map[string]any, decision string) map[string]any {
	return map[string]any{"op": "approvalDecide", "pane": it["pane"], "token": it["token"], "cardSeq": it["cardSeq"], "offer": it["offer"], "decision": decision}
}

// closed waits for the host to close the socket and returns the code and reason.
func (p *phone) closed() (websocket.StatusCode, string) {
	p.t.Helper()
	return closedWith(p.t, p.conn)
}

func refusedWith(t *testing.T, e *approvalsEnv, h http.Header, wantReason string) {
	t.Helper()
	conn, _, err := e.rawDial(t, h)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if wantReason == approvalReasonVerify || wantReason == approvalReasonGrant {
		// Decided with the key the handshake used, so the handshake is made first.
		e.handshake(t, conn)
	}
	code, reason := closedWith(t, conn)
	if code != artifactCloseRefused || reason != wantReason {
		t.Errorf("closed with %d %q, want %d %q", code, reason, artifactCloseRefused, wantReason)
	}
}

func approvalDir(t *testing.T) string {
	t.Helper()
	d, err := store.Dir()
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func approvalLog(t *testing.T) []map[string]any {
	t.Helper()
	var all []string
	for _, f := range []string{approvalAuditFile, approvalDeniedFile, approvalShownFile} {
		if data, err := os.ReadFile(filepath.Join(approvalDir(t), f)); err == nil {
			all = append(all, strings.Split(strings.TrimSpace(string(data)), "\n")...)
		}
	}
	var out []map[string]any
	for _, l := range all {
		if l == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("an audit line that is not JSON: %q", l)
		}
		out = append(out, m)
	}
	return out
}

func approvalLogHas(t *testing.T, event, reason string) bool {
	t.Helper()
	for _, l := range approvalLog(t) {
		if l["event"] == event && (reason == "" || l["reason"] == reason) {
			return true
		}
	}
	return false
}

func sha(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func TestApprovalsSocketIsNotServedLocally(t *testing.T) {
	srv, _ := newTestServer(t)
	resp, err := http.Get("http://" + srv.Addr() + "/ws/approvals?t=" + srv.Token())
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("the local listener answered %d, want 404", resp.StatusCode)
	}
}

func TestApprovalsSocketNeedsADeviceAndAnOrigin(t *testing.T) {
	e := newApprovalsEnv(t)
	for name, h := range map[string]http.Header{"no device": {"Origin": {e.ts.URL}}, "no origin": {"Flockdeck-Remote-Device": {"d1"}}} {
		_, resp, err := e.rawDial(t, h)
		if err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s: err %v resp %v, want a 403", name, err, resp)
		}
	}
}

// The gate: every one of these must be refused, with the one word the device is
// told, and the refusal must be in the log.
func TestApprovalsGateRefusesWhatItShould(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(t *testing.T, e *approvalsEnv)
		header func(h http.Header)
		reason string
	}{
		{"approval is off for the device", func(t *testing.T, e *approvalsEnv) {
			putAccess(t, e.srv, "d1", store.DeviceAccess{Role: store.RoleFull})
		}, nil, approvalReasonDisabled},
		{"an old wording", func(t *testing.T, e *approvalsEnv) {
			putAccess(t, e.srv, "d1", store.DeviceAccess{Role: store.RoleFull, Approve: true, ApproveV: approveAckVersion - 1, ApproveAt: time.Now(), ApproveKey: store.KeyHash(e.key)})
		}, nil, approvalReasonDisabled},
		{"the key is not verified", func(t *testing.T, e *approvalsEnv) { e.fake.setState("d1", e.key, remote.NotVerified) }, nil, approvalReasonVerify},
		{"the grant is for another device's key", func(t *testing.T, e *approvalsEnv) {
			e.fake.kmu.Lock()
			other := e.fake.keys["d2"]
			e.fake.kmu.Unlock()
			e.grant(t, "d1", other)
		}, nil, approvalReasonGrant},
		{"the device has no key on the relay", func(t *testing.T, e *approvalsEnv) { e.fake.capable["d1"] = false }, nil, approvalReasonKey},
		{"the desk-origin key", nil, func(h http.Header) { h.Set("Flockdeck-Remote-Origin", "desk") }, approvalReasonOrigin},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newApprovalsEnv(t)
			if c.setup != nil {
				c.setup(t, e)
			}
			h := e.header("d1")
			if c.header != nil {
				c.header(h)
			}
			refusedWith(t, e, h, c.reason)
			waitUntil(t, "the refusal to be logged", func() bool { return approvalLogHas(t, "denied", c.reason) })
		})
	}
}

func TestApprovalsGateIsHeldToTheKeyTheHandshakeProved(t *testing.T) {
	e := newApprovalsEnv(t)
	e.fake.kmu.Lock()
	other := e.fake.keys["d2"]
	e.fake.kmu.Unlock()
	// Approval was granted to d2's key and the device d1 has that grant: the
	// roster key of another device is no stand-in for its own.
	if e.srv.mayApproveWithKey("d1", other) {
		t.Error("mayApproveWithKey said yes to another device's roster key")
	}
	if !e.srv.mayApproveWithKey("d1", e.key) {
		t.Error("mayApproveWithKey said no to the device's own key")
	}
}

// The relay serves the key the desk verified and granted (K1) to every lookup, and
// the handshake is made with another key (K2). The gate must be given K2, which
// the session was derived with, and so refuse.
func TestAHandshakeWithAnotherKeyThanTheRosterSaysIsRefused(t *testing.T) {
	e := newApprovalsEnv(t)
	k2, err := e2e.GenerateStaticKey()
	if err != nil {
		t.Fatal(err)
	}
	e.fake.devicePub["d1"] = k2.PublicKey()
	e.devicePriv = k2
	// The roster still says K1, which is verified and granted.
	if k, _ := e.fake.E2EDeviceKey(context.Background(), "d1", remote.KeyOriginUsual); !bytes.Equal(k, e.key) {
		t.Fatal("the roster does not say K1")
	}
	conn, _, err := e.rawDial(t, e.header("d1"))
	if err != nil {
		t.Fatal(err)
	}
	e.handshake(t, conn)
	code, reason := closedWith(t, conn)
	if code != artifactCloseRefused || reason != approvalReasonVerify {
		t.Errorf("closed with %d %q, want %d %q", code, reason, artifactCloseRefused, approvalReasonVerify)
	}
}

func TestAnEmptyKeyIsNeverVerified(t *testing.T) {
	e := newApprovalsEnv(t)
	// A record that says an empty key is verified must not matter.
	e.fake.setState("d1", nil, remote.Verified)
	if got := e.srv.approvalCheck("d1", remote.KeyOriginUsual, nil); got != approvalReasonVerify {
		t.Errorf("an empty key gave %q, want %q", got, approvalReasonVerify)
	}
	if got := e.srv.approvalCheck("d1", remote.KeyOriginUsual, e.key); got != "" {
		t.Errorf("the device's own key gave %q", got)
	}
}

func TestApprovalsGateIsCheckedAgainOnEveryFrame(t *testing.T) {
	for name, change := range map[string]struct {
		do     func(t *testing.T, e *approvalsEnv)
		reason string
	}{
		"switched off at the desk": {func(t *testing.T, e *approvalsEnv) {
			putAccess(t, e.srv, "d1", store.DeviceAccess{Role: store.RoleFull})
		}, approvalReasonDisabled},
		"key unverified": {func(t *testing.T, e *approvalsEnv) { e.fake.setState("d1", e.key, remote.NotVerified) }, approvalReasonVerify},
		"granted for another key": {func(t *testing.T, e *approvalsEnv) {
			e.fake.kmu.Lock()
			o := e.fake.keys["d2"]
			e.fake.kmu.Unlock()
			e.grant(t, "d1", o)
		}, approvalReasonGrant},
	} {
		t.Run(name, func(t *testing.T) {
			e := newApprovalsEnv(t)
			// No looking by the host, so only the check before a frame can notice.
			e.srv.approvals.setTimings(func(tm *approvalTimings) { tm.poll = time.Hour })
			p := e.open(t)
			if got := p.list(); len(got) != 1 {
				t.Fatalf("listed %d, want 1", len(got))
			}
			change.do(t, e)
			p.send(map[string]any{"op": "ping"})
			code, reason := p.closed()
			if code != artifactCloseRefused || reason != change.reason {
				t.Errorf("closed with %d %q, want %d %q", code, reason, artifactCloseRefused, change.reason)
			}
		})
	}
}

// Nothing is sent to a device that may no longer be told, even if it says nothing.
func TestApprovalsGateIsCheckedBeforeAnUnpromptedFrame(t *testing.T) {
	e := newApprovalsEnv(t)
	p := e.open(t)
	p.list()
	putAccess(t, e.srv, "d1", store.DeviceAccess{Role: store.RoleFull})
	e.pane.set(goodCard(8, "echo second"))
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		typ, frame, err := p.conn.Read(ctx)
		cancel()
		if err != nil {
			if websocket.CloseStatus(err) != artifactCloseRefused {
				t.Fatalf("ended with %v", err)
			}
			return
		}
		if typ == websocket.MessageBinary {
			_, payload := openFrame(t, p.sess, frame)
			if strings.Contains(string(payload), "echo second") {
				t.Fatal("a card was sent to a device that may not be told")
			}
		}
	}
}

func TestSwitchingApprovalOffEndsTheApprovalsSocket(t *testing.T) {
	e := newApprovalsEnv(t)
	p := e.open(t)
	p.list()
	e.srv.sockets.cut("d1")
	code, reason := p.closed()
	if code != artifactCloseRefused || reason != approvalReasonDisabled {
		t.Errorf("closed with %d %q", code, reason)
	}
}

func TestTheListHasTheCardAndNothingOfTheAgentsOwnWords(t *testing.T) {
	e := newApprovalsEnv(t)
	p := e.open(t)
	p.send(map[string]any{"op": "hello"})
	hello := p.readOp("hello")
	if hello["minDisplayMs"].(float64) != 0 {
		t.Errorf("hello: %v", hello)
	}
	p.send(map[string]any{"op": "approvalList"})
	raw := p.raw()
	for _, marker := range []string{agentReq, agentTool, agentSum} {
		if bytes.Contains(raw, []byte(marker)) {
			t.Errorf("the frame holds the agent's %q", marker)
		}
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	got := items(m)
	if len(got) != 1 {
		t.Fatalf("items: %v", m)
	}
	it := got[0]
	if it["pane"] != e.paneID || it["kind"] != "execute" || it["text"] != "go test ./pkg" || it["canAllow"] != true || it["answerable"] != true || it["offer"] == "" || it["token"] != "7.0" || it["cardSeq"].(float64) != 7 {
		t.Errorf("the item is %v", it)
	}
	if _, has := it["requestId"]; has {
		t.Error("the agent's request id is sent")
	}
}

func TestEveryFrameIsPaddedToABucket(t *testing.T) {
	e := newApprovalsEnv(t)
	p := e.open(t)
	sizes := map[int]bool{}
	for i, cmd := range []string{"ls", strings.Repeat("a", 100), strings.Repeat("b", 300)} {
		e.pane.set(goodCard(uint64(10+i), cmd))
		p.send(map[string]any{"op": "approvalList"})
		raw := p.rawOp("approvals")
		if len(raw)%approvalPadBucket != 0 {
			t.Errorf("a frame of %d bytes is not a multiple of %d", len(raw), approvalPadBucket)
		}
		sizes[len(raw)] = true
	}
	if len(sizes) != 1 {
		t.Errorf("commands of different lengths gave frames of different sizes: %v", sizes)
	}
	p.send(map[string]any{"op": "ping"})
	if raw := p.rawOp("pong"); len(raw)%approvalPadBucket != 0 {
		t.Errorf("a pong of %d bytes is not padded", len(raw))
	}
	// A frame over a bucket goes to the next multiple.
	if n, err := padded(map[string]any{"op": "x", "text": strings.Repeat("z", 1500)}); err != nil || len(n) != 2*approvalPadBucket {
		t.Errorf("a 1500 byte frame padded to %d, %v", len(n), err)
	}
}

func TestAPaddedRequestIsAccepted(t *testing.T) {
	e := newApprovalsEnv(t)
	p := e.open(t)
	// A request already carrying its own pad field is padded again to the next
	// bucket and read.
	p.send(map[string]any{"op": "approvalList", "pad": strings.Repeat("0", 900)})
	if got := items(p.readOp("approvals")); len(got) != 1 {
		t.Errorf("items: %v", got)
	}
}

func TestAPlanIsListedWithNothingToAnswer(t *testing.T) {
	e := newApprovalsEnv(t)
	e.pane.set(&bridge.Card{Seq: 9, RequestID: agentReq, Kind: "switch_mode", Tool: agentTool, Body: "PLAN-BODY", Options: plainOptions})
	p := e.open(t)
	p.send(map[string]any{"op": "approvalList"})
	got := items(p.readOp("approvals"))
	if len(got) != 1 || got[0]["answerable"] != false || got[0]["canAllow"] != false || got[0]["why"] != whyPlan {
		t.Fatalf("items: %v", got)
	}
	if _, has := got[0]["offer"]; has {
		t.Error("a plan was given an offer")
	}
	if got[0]["text"] != "" {
		t.Errorf("a plan has text: %v", got[0]["text"])
	}
}

func TestAnAllowIsGivenToThePaneAsItsOwnOption(t *testing.T) {
	e := newApprovalsEnv(t)
	p := e.open(t)
	it := p.list()[0]
	res := p.decide(it, "allow")
	if res["ok"] != true || res["decision"] != "allow" {
		t.Fatalf("result %v", res)
	}
	if got := e.pane.given(); len(got) != 1 || got[0] != "allow" {
		t.Errorf("the pane was given %v, want [allow]", got)
	}
	// The card is gone: the device is told.
	if got := items(p.readOp("approvals")); len(got) != 0 {
		t.Errorf("the card is still listed: %v", got)
	}
}

func TestADenyIsGivenToThePaneAsItsRejectOption(t *testing.T) {
	e := newApprovalsEnv(t)
	p := e.open(t)
	res := p.decide(p.list()[0], "deny")
	if res["ok"] != true {
		t.Fatalf("result %v", res)
	}
	if got := e.pane.given(); len(got) != 1 || got[0] != "reject" {
		t.Errorf("the pane was given %v, want [reject]", got)
	}
}

func TestAnythingButAllowOrDenyIsRefusedAndNeverGiven(t *testing.T) {
	for _, d := range []string{"allow_always", "allow_once", "reject", "reject_once", "ALLOW", "allow ", ""} {
		t.Run("decision "+d, func(t *testing.T) {
			e := newApprovalsEnv(t)
			p := e.open(t)
			res := p.decide(p.list()[0], d)
			if res["ok"] == true || res["error"] != errPhoneDecision.Error() {
				t.Errorf("gave %v", res)
			}
			if len(e.pane.given()) != 0 {
				t.Errorf("reached the pane: %v", e.pane.given())
			}
		})
	}
}

func TestADecisionIsNotTakenSoonerThanTheMinimumDisplayTime(t *testing.T) {
	e := newApprovalsEnv(t)
	e.srv.approvals.setTimings(func(tm *approvalTimings) { tm.minDisplay = 0 }) // the default, 700 ms
	p := e.open(t)
	it := p.list()[0]
	res := p.decide(it, "allow")
	if res["ok"] == true || res["error"] != approvalErrEarly {
		t.Fatalf("an immediate decision gave %v", res)
	}
	if len(e.pane.given()) != 0 {
		t.Fatal("an immediate decision reached the pane")
	}
	// The card is offered again, with a new offer, and the time starts again.
	again := items(p.readOp("approvals"))
	if len(again) != 1 || again[0]["offer"] == it["offer"] || again[0]["offer"] == "" {
		t.Fatalf("not offered again: %v", again)
	}
	time.Sleep(approvalMinDisplay + 100*time.Millisecond)
	// The offer the refused tap used is dead, though the card is still waiting.
	if res := p.decide(it, "allow"); res["ok"] == true || len(e.pane.given()) != 0 {
		t.Fatalf("a used offer was honoured: %v", res)
	}
	if res := p.decide(again[0], "allow"); res["ok"] != true {
		t.Fatalf("a decision after the display time gave %v", res)
	}
	if len(e.pane.given()) != 1 {
		t.Errorf("given %v", e.pane.given())
	}
	if approvalMinDisplay != 700*time.Millisecond {
		t.Errorf("the minimum display time is %v, want 700ms", approvalMinDisplay)
	}
}

func TestAnOfferIsUsedOnce(t *testing.T) {
	e := newApprovalsEnv(t)
	p := e.open(t)
	it := p.list()[0]
	if res := p.decide(it, "deny"); res["ok"] != true {
		t.Fatalf("%v", res)
	}
	e.pane.set(goodCard(8, "echo again"))
	// The same offer again, for what is now waiting.
	res := p.decide(it, "allow")
	if res["ok"] == true || res["error"] != permissionStale {
		t.Errorf("a used offer gave %v", res)
	}
	if got := e.pane.given(); len(got) != 1 {
		t.Errorf("the pane was given %v", got)
	}
}

func TestAnOfferFromAnotherSocketIsNotHonoured(t *testing.T) {
	e := newApprovalsEnv(t)
	a, b := e.open(t), e.open(t)
	_ = a.list()
	itB := b.list()[0]
	res := a.decide(itB, "allow")
	if res["ok"] == true || res["error"] != permissionStale {
		t.Errorf("another socket's offer gave %v", res)
	}
	if len(e.pane.given()) != 0 {
		t.Error("it reached the pane")
	}
}

func TestADecisionThatDoesNotMatchTheOfferIsRefused(t *testing.T) {
	for name, change := range map[string]func(map[string]any){
		"another token":   func(m map[string]any) { m["token"] = "8.0" },
		"another card":    func(m map[string]any) { m["cardSeq"] = 99 },
		"another pane":    func(m map[string]any) { m["pane"] = "someone-else" },
		"no token":        func(m map[string]any) { delete(m, "token") },
		"no card number":  func(m map[string]any) { delete(m, "cardSeq") },
		"no offer":        func(m map[string]any) { m["offer"] = "" },
		"a guessed offer": func(m map[string]any) { m["offer"] = strings.Repeat("0", 32) },
	} {
		t.Run(name, func(t *testing.T) {
			e := newApprovalsEnv(t)
			p := e.open(t)
			d := decideFor(p.list()[0], "allow")
			change(d)
			p.send(d)
			res := p.readOp("approvalResult")
			if res["ok"] == true || res["error"] == nil {
				t.Errorf("gave %v", res)
			}
			if len(e.pane.given()) != 0 {
				t.Error("it reached the pane")
			}
		})
	}
}

// With the poll slowed so that the host has not yet noticed, the lock held
// against the offer is what refuses a decision for a card that has changed.
func TestTheLockRefusesACardThatChangedBeforeTheHostNoticed(t *testing.T) {
	cases := map[string]struct {
		change func(e *approvalsEnv)
		want   string
	}{
		"the pane restarted": {func(e *approvalsEnv) { e.pane.setToken("8.0") }, errPhoneStaleRun.Error()},
		"a new card with the same request id": {func(e *approvalsEnv) {
			e.pane.set(goodCard(8, "go test ./pkg"))
		}, errPhoneStale.Error()},
		"no card": {func(e *approvalsEnv) { e.pane.set(nil) }, errPhoneNoCard.Error()},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			e := newApprovalsEnv(t)
			e.srv.approvals.setTimings(func(tm *approvalTimings) { tm.poll = time.Hour })
			p := e.open(t)
			it := p.list()[0]
			c.change(e)
			res := p.decide(it, "allow")
			if res["ok"] == true || res["error"] != c.want {
				t.Errorf("gave %v, want the error %q", res, c.want)
			}
			if len(e.pane.given()) != 0 {
				t.Error("it reached the pane")
			}
		})
	}
}

func TestADecisionOnACardTheDeskAnsweredIsStaleAndNotAbuse(t *testing.T) {
	e := newApprovalsEnv(t)
	p := e.open(t)
	it := p.list()[0]
	// The desk answers first.
	if err := e.pane.AnswerPermission(7, agentReq, "reject"); err != nil {
		t.Fatal(err)
	}
	if got := items(p.readOp("approvals")); len(got) != 0 {
		t.Fatalf("listed %v", got)
	}
	// More than the abuse limit of taps on the card that is gone: each is stale,
	// none counts as abuse, the socket stays open.
	for i := 0; i < approvalAbuseMax+1; i++ {
		res := p.decide(it, "allow")
		if res["ok"] == true || res["error"] != permissionStale {
			t.Fatalf("tap %d gave %v", i, res)
		}
	}
	p.send(map[string]any{"op": "ping"})
	p.readOp("pong")
	if got := e.pane.given(); len(got) != 1 || got[0] != "reject" {
		t.Errorf("given %v", got)
	}
}

func TestTwoDevicesAnsweringOneCardGiveOneAnswer(t *testing.T) {
	e := newApprovalsEnv(t)
	a, b := e.open(t), e.open(t)
	ia, ib := a.list()[0], b.list()[0]
	a.send(decideFor(ia, "allow"))
	b.send(decideFor(ib, "deny"))
	ra, rb := a.readOp("approvalResult"), b.readOp("approvalResult")
	if (ra["ok"] == true) == (rb["ok"] == true) {
		t.Fatalf("results %v and %v: exactly one must win", ra, rb)
	}
	if got := e.pane.given(); len(got) != 1 {
		t.Errorf("the pane was given %v", got)
	}
}

func TestADenyOnlyCardIsNotAllowedFromThePhone(t *testing.T) {
	e := newApprovalsEnv(t)
	e.pane.set(goodCard(7, strings.Repeat("echo hello ", 60)))
	p := e.open(t)
	it := p.list()[0]
	if it["canAllow"] != false || it["why"] != whyLong || it["offer"] == "" {
		t.Fatalf("item %v", it)
	}
	if res := p.decide(it, "allow"); res["ok"] == true || res["error"] != errPhoneNoAllow.Error() {
		t.Fatalf("allow gave %v", res)
	}
	if len(e.pane.given()) != 0 {
		t.Fatal("an allow of a deny-only card reached the pane")
	}
	it = items(p.readOp("approvals"))[0]
	if res := p.decide(it, "deny"); res["ok"] != true {
		t.Fatalf("deny gave %v", res)
	}
}

func TestAnAgentOnAnotherAdapterIsDenyOnly(t *testing.T) {
	e := newApprovalsEnv(t)
	e.pane.own = false
	p := e.open(t)
	it := p.list()[0]
	if it["canAllow"] != false || it["why"] != whyAgent {
		t.Fatalf("item %v", it)
	}
	if res := p.decide(it, "allow"); res["ok"] == true {
		t.Fatal("allowed")
	}
	if len(e.pane.given()) != 0 {
		t.Fatal("reached the pane")
	}
}

func TestThreeRefusalsInARowCloseTheSocketAndTellTheDesk(t *testing.T) {
	e := newApprovalsEnv(t)
	desk := dialControlPage(t, e.srv)
	p := e.open(t)
	it := p.list()[0]
	it["offer"] = strings.Repeat("z", 32)
	for i := 0; i < approvalAbuseMax; i++ {
		res := p.decide(it, "allow")
		if res["ok"] == true {
			t.Fatal("accepted")
		}
	}
	code, reason := p.closed()
	if code != artifactCloseRefused || reason != "refused" {
		t.Errorf("closed with %d %q", code, reason)
	}
	var note noticeMsg
	readUntil(t, desk, "notice", &note)
	if !strings.Contains(note.Text, "refused phone approval") {
		t.Errorf("the desk was told %q", note.Text)
	}
	waitUntil(t, "the abuse to be logged", func() bool { return approvalLogHas(t, "denied", "refused-answers") })
}

func TestASuccessfulAnswerStartsTheCountOfRefusalsAgain(t *testing.T) {
	e := newApprovalsEnv(t)
	p := e.open(t)
	it := p.list()[0]
	bad := map[string]any{"op": "approvalDecide", "pane": it["pane"], "token": it["token"], "cardSeq": it["cardSeq"], "offer": strings.Repeat("z", 32), "decision": "allow"}
	for i := 0; i < approvalAbuseMax-1; i++ {
		p.send(bad)
		p.readOp("approvalResult")
	}
	if res := p.decide(it, "deny"); res["ok"] != true {
		t.Fatalf("%v", res)
	}
	e.pane.set(goodCard(20, "ls"))
	for i := 0; i < approvalAbuseMax-1; i++ {
		p.send(bad)
		p.readOp("approvalResult")
	}
	p.send(map[string]any{"op": "ping"})
	p.readOp("pong")
}

func TestDecisionsAreRateLimitedPerDevice(t *testing.T) {
	e := newApprovalsEnv(t)
	p := e.open(t)
	it := p.list()[0]
	// Taps on a card that has ended are refused as stale and are not abuse, so
	// they show the rate and nothing else.
	e.pane.set(nil)
	p.readOp("approvals")
	var busy int
	for i := 0; i < approvalDecideBurst+2; i++ {
		res := p.decide(it, "allow")
		if res["error"] == approvalErrBusy {
			busy++
		}
	}
	if busy != 2 {
		t.Errorf("%d decisions were turned away for rate, want 2 of %d", busy, approvalDecideBurst+2)
	}
	// It refills.
	time.Sleep(1200 * time.Millisecond)
	if res := p.decide(it, "allow"); res["error"] == approvalErrBusy {
		t.Errorf("still limited after a second: %v", res)
	}
	// A second socket of the same device shares the allowance.
	q := e.open(t)
	if res := q.decide(it, "allow"); res["error"] != approvalErrBusy {
		t.Errorf("a second socket got its own allowance: %v", res)
	}
}

func TestADeviceThatKeepsHammeringIsCut(t *testing.T) {
	e := newApprovalsEnv(t)
	p := e.open(t)
	it := p.list()[0]
	e.pane.set(nil)
	p.readOp("approvals")
	for i := 0; i < 3*approvalBusyMax; i++ {
		p.sendQuiet(decideFor(it, "allow"))
	}
	code, reason := p.closed()
	if code != artifactCloseBusy || reason != artifactErrBusy {
		t.Errorf("closed with %d %q", code, reason)
	}
}

func TestOnlyAFewSocketsPerDevice(t *testing.T) {
	e := newApprovalsEnv(t)
	var socks []*phone
	for i := 0; i < approvalSocketsPerDevice; i++ {
		p := e.open(t)
		p.list()
		socks = append(socks, p)
	}
	conn, _, err := e.rawDial(t, e.header("d1"))
	if err != nil {
		t.Fatal(err)
	}
	if code, reason := closedWith(t, conn); code != artifactCloseBusy || reason != artifactErrBusy {
		t.Errorf("an extra socket closed with %d %q", code, reason)
	}
	_ = socks
}

func TestASocketThatSaysNothingIsClosed(t *testing.T) {
	e := newApprovalsEnv(t)
	e.srv.approvals.setTimings(func(tm *approvalTimings) { tm.first = 150 * time.Millisecond })
	p := e.open(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, _, err := p.conn.Read(ctx); err == nil || ctx.Err() != nil {
		t.Errorf("the socket stayed open: %v", err)
	}
}

func TestMalformedFramesAreCountedAndEndTheSocket(t *testing.T) {
	e := newApprovalsEnv(t)
	p := e.open(t)
	for i := 0; i < approvalAbuseMax; i++ {
		p.sendQuiet(map[string]any{"op": 5})
	}
	if code, _ := p.closed(); code != artifactCloseRefused {
		t.Errorf("closed with %d", code)
	}
}

func TestTheAuditLogHasHashAndLengthAndNotTheText(t *testing.T) {
	e := newApprovalsEnv(t)
	const cmd = "go test ./pkg"
	p := e.open(t)
	it := p.list()[0]
	if res := p.decide(it, "allow"); res["ok"] != true {
		t.Fatalf("%v", res)
	}
	data, err := os.ReadFile(filepath.Join(approvalDir(t), approvalAuditFile))
	if err != nil {
		t.Fatal(err)
	}
	log := string(data)
	for _, bad := range []string{cmd, "pkg", agentReq, agentTool, agentSum} {
		if strings.Contains(log, bad) {
			t.Errorf("the log holds %q", bad)
		}
	}
	var shown, decided map[string]any
	for _, l := range approvalLog(t) {
		switch l["event"] {
		case "shown":
			shown = l
		case "decision":
			decided = l
		}
	}
	for what, l := range map[string]map[string]any{"shown": shown, "decision": decided} {
		if l == nil {
			t.Fatalf("no %s line in %s", what, log)
		}
		if l["hash"] != sha(cmd) || l["len"].(float64) != float64(len(cmd)) || l["device"] != "d1" || l["pane"] != e.paneID || l["seq"].(float64) != 7 || l["kind"] != "execute" {
			t.Errorf("the %s line is %v", what, l)
		}
	}
	if decided["decision"] != "allow_once" {
		t.Errorf("decision %v", decided["decision"])
	}
	if !approvalLogHas(t, "connect", "") {
		t.Error("no connect line")
	}
}

func TestADeniedDecisionIsLoggedAsRejectOnce(t *testing.T) {
	e := newApprovalsEnv(t)
	p := e.open(t)
	p.decide(p.list()[0], "deny")
	for _, l := range approvalLog(t) {
		if l["event"] == "decision" && l["decision"] == "reject_once" {
			return
		}
	}
	t.Error("no reject_once line")
}

func TestARefusedDecisionIsLoggedWithItsReason(t *testing.T) {
	e := newApprovalsEnv(t)
	p := e.open(t)
	it := p.list()[0]
	it["offer"] = strings.Repeat("d", 32)
	p.decide(it, "allow")
	if !approvalLogHas(t, "decision-refused", "offer") {
		t.Errorf("log: %v", approvalLog(t))
	}
}

func TestADecisionThatCannotBeRecordedIsNotGiven(t *testing.T) {
	e := newApprovalsEnv(t)
	p := e.open(t)
	it := p.list()[0]
	e.srv.approvals.audit.dir = func() (string, error) { return "", errors.New("no folder") }
	res := p.decide(it, "allow")
	if res["ok"] == true || res["error"] != approvalErrRecord {
		t.Fatalf("gave %v", res)
	}
	if len(e.pane.given()) != 0 {
		t.Error("it reached the pane")
	}
}

func TestASocketThatCannotBeRecordedIsRefused(t *testing.T) {
	e := newApprovalsEnv(t)
	e.srv.approvals.audit.dir = func() (string, error) { return "", errors.New("no folder") }
	conn, _, err := e.rawDial(t, e.header("d1"))
	if err != nil {
		t.Fatal(err)
	}
	sess := e.handshake(t, conn)
	// The opening is recorded when the first request has been opened.
	p := &phone{t: t, conn: conn, sess: sess}
	p.sendQuiet(map[string]any{"op": "ping"})
	if code, _ := closedWith(t, conn); code != websocket.StatusInternalError {
		t.Errorf("closed with %d", code)
	}
}

func TestACardThatCannotBeRecordedIsNotShown(t *testing.T) {
	e := newApprovalsEnv(t)
	p := e.open(t)
	p.send(map[string]any{"op": "ping"})
	p.readOp("pong")
	e.srv.approvals.shownLog.dir = func() (string, error) { return "", errors.New("no folder") }
	p.send(map[string]any{"op": "approvalList"})
	if code, _ := p.closed(); code != websocket.StatusInternalError {
		t.Errorf("closed with %d", code)
	}
}

func TestTheAuditLogRotates(t *testing.T) {
	e := newApprovalsEnv(t)
	e.srv.approvals.audit.max = 500
	for i := 0; i < 12; i++ {
		_ = e.srv.approvals.audit.write(auditEvent{Event: "shown", Device: "d1", Reason: strings.Repeat("x", 80)})
	}
	dir := approvalDir(t)
	for _, f := range []string{approvalAuditFile, approvalAuditFile + ".1", approvalAuditFile + ".2"} {
		fi, err := os.Stat(filepath.Join(dir, f))
		if err != nil {
			t.Errorf("%s: %v", f, err)
		} else if fi.Size() > 600 {
			t.Errorf("%s is %d bytes", f, fi.Size())
		}
	}
	if _, err := os.Stat(filepath.Join(dir, artifactAuditFile)); err == nil {
		t.Error("the artifacts log was written")
	}
}

func TestTheDesksChangesAreInTheLog(t *testing.T) {
	e := newApproveEnv(t)
	e.srv.approvals.audit.dir = func() (string, error) { return store.Dir() }
	e.allow(t, "d1")
	waitUntil(t, "the line for turning approval on", func() bool { return approvalLogHas(t, "device-approve-on", "") })
	sendCmd(t, dialControlPage(t, e.srv), command{Cmd: "setApprovalsOff"})
	waitUntil(t, "the line for the kill switch", func() bool { return approvalLogHas(t, "approvals-off", "") })
	for _, l := range approvalLog(t) {
		if l["event"] == "device-approve-on" && l["device"] != "d1" {
			t.Errorf("line %v", l)
		}
	}
}

func TestNothingIsSentUnpromptedBeforeTheListIsAsked(t *testing.T) {
	e := newApprovalsEnv(t)
	p := e.open(t)
	e.pane.set(goodCard(30, "echo hi"))
	time.Sleep(200 * time.Millisecond)
	p.send(map[string]any{"op": "ping"})
	if m := p.read(); m["op"] != "pong" {
		t.Errorf("the first frame was %v, want pong", m["op"])
	}
}

// A decision in flight when approval is switched off must not be given.

func TestADecisionIsNotGivenIfApprovalIsSwitchedOffAfterThePaneWasRead(t *testing.T) {
	e := newApprovalsEnv(t)
	e.srv.approvals.setTimings(func(tm *approvalTimings) {
		tm.hook = func(stage string) {
			if stage == "after-ask" {
				putAccess(t, e.srv, "d1", store.DeviceAccess{Role: store.RoleFull})
			}
		}
	})
	p := e.open(t)
	p.send(decideFor(p.list()[0], "allow"))
	if code, reason := p.closed(); code != artifactCloseRefused || reason != approvalReasonDisabled {
		t.Errorf("closed with %d %q", code, reason)
	}
	if len(e.pane.given()) != 0 {
		t.Fatalf("an answer was given after approval was switched off: %v", e.pane.given())
	}
}

func TestADecisionIsNotGivenIfApprovalIsSwitchedOffWhileTheLineIsBeingWritten(t *testing.T) {
	e := newApprovalsEnv(t)
	e.srv.approvals.setTimings(func(tm *approvalTimings) {
		tm.hook = func(stage string) {
			if stage == "after-audit" {
				putAccess(t, e.srv, "d1", store.DeviceAccess{Role: store.RoleFull})
			}
		}
	})
	p := e.open(t)
	p.send(decideFor(p.list()[0], "allow"))
	if code, reason := p.closed(); code != artifactCloseRefused || reason != approvalReasonDisabled {
		t.Errorf("closed with %d %q", code, reason)
	}
	if len(e.pane.given()) != 0 {
		t.Fatalf("an answer was given after approval was switched off: %v", e.pane.given())
	}
}

// The switch is the desk's cut of the device's sockets (kill switch, unpair, a
// changed key): a decision stuck in a slow audit write is stopped by it, though the
// preferences still say yes.
func TestACutThatLandsDuringTheAuditWriteStopsTheAnswer(t *testing.T) {
	e := newApprovalsEnv(t)
	passed := make(chan struct{})
	var once, once2 sync.Once
	// Set before the socket opens: the limits are read when it is accepted.
	e.srv.approvals.setTimings(func(tm *approvalTimings) {
		tm.hook = func(stage string) {
			if stage == "after-audit" {
				once2.Do(func() { close(passed) })
			}
		}
	})
	p := e.open(t)
	it := p.list()[0]
	entered, release := make(chan struct{}), make(chan struct{})
	e.srv.approvals.audit.dir = func() (string, error) {
		once.Do(func() { close(entered); <-release })
		return store.Dir()
	}
	p.send(decideFor(it, "allow"))
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the decision never reached the audit write")
	}
	e.srv.sockets.cut("d1")
	close(release)
	// Wait for the decision to pass the point where the answer would follow, then
	// give it time to be given: the socket's close alone says nothing about it.
	select {
	case <-passed:
	case <-time.After(5 * time.Second):
		t.Fatal("the decision never got past the audit write")
	}
	time.Sleep(300 * time.Millisecond)
	if code, reason := p.closed(); code != artifactCloseRefused || reason != approvalReasonDisabled {
		t.Errorf("closed with %d %q", code, reason)
	}
	if len(e.pane.given()) != 0 {
		t.Fatalf("an answer was given after the socket was cut: %v", e.pane.given())
	}
}

// After an answer, nothing more is sent to a device that may no longer be told.
func TestNothingIsSentAfterADecisionToADeviceSwitchedOffMeanwhile(t *testing.T) {
	e := newApprovalsEnv(t)
	e.pane.afterAnswer = func() { putAccess(t, e.srv, "d1", store.DeviceAccess{Role: store.RoleFull}) }
	p := e.open(t)
	p.send(decideFor(p.list()[0], "deny"))
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		typ, frame, err := p.conn.Read(ctx)
		cancel()
		if err != nil {
			if websocket.CloseStatus(err) != artifactCloseRefused {
				t.Fatalf("ended with %v", err)
			}
			return
		}
		if typ == websocket.MessageBinary {
			_, payload := openFrame(t, p.sess, frame)
			if strings.Contains(string(payload), `"op":"approvals"`) {
				t.Fatalf("a frame was sent after approval was switched off: %.80s", payload)
			}
		}
	}
}

// Requests are padded to a multiple of the bucket.

func TestAnUnpaddedRequestIsRefusedAndCounted(t *testing.T) {
	e := newApprovalsEnv(t)
	p := e.open(t)
	for i := 0; i < approvalAbuseMax-1; i++ {
		p.sendRaw([]byte(`{"op":"approvalList"}`))
		if m := p.readOp("error"); m["code"] != "padding" {
			t.Fatalf("got %v", m)
		}
	}
	if got := p.list(); len(got) != 1 {
		t.Errorf("a padded request after refusals gave %v", got)
	}
	// An allow and a deny cannot be told apart by length: both are padded to one bucket.
	a, _ := padded(map[string]any{"op": "approvalDecide", "pane": "x", "token": "7.0", "cardSeq": 7, "offer": strings.Repeat("a", 32), "decision": "allow"})
	d, _ := padded(map[string]any{"op": "approvalDecide", "pane": "x", "token": "7.0", "cardSeq": 7, "offer": strings.Repeat("a", 32), "decision": "deny"})
	if len(a) != len(d) || len(a)%approvalPadBucket != 0 {
		t.Errorf("allow is %d bytes and deny %d", len(a), len(d))
	}
	p.sendRaw([]byte(`{"op":"ping"}`))
	if code, _ := p.closed(); code != artifactCloseRefused {
		t.Errorf("closed with %d", code)
	}
}

// The denied log is a file of its own, and a flood of refusals under forged device
// ids is held to a fixed number of lines and notices.

func TestForgedDeviceIdsCannotFillTheLogOrRotateOutDecisions(t *testing.T) {
	e := newApprovalsEnv(t)
	desk := dialControlPage(t, e.srv)
	p := e.open(t)
	p.decide(p.list()[0], "deny")
	for i := 0; i < 500; i++ {
		e.srv.approvalDenied("forged-"+strconv.Itoa(i), "x", "disabled")
	}
	denied, kept := 0, 0
	for _, l := range approvalLog(t) {
		switch l["event"] {
		case "denied":
			denied++
		case "decision", "shown":
			kept++
		}
	}
	if denied > approvalDeniedPerMinute+1 {
		t.Errorf("%d denied lines were written, want at most %d", denied, approvalDeniedPerMinute)
	}
	if kept != 2 {
		t.Errorf("%d shown and decision lines, want 2", kept)
	}
	data, err := os.ReadFile(filepath.Join(approvalDir(t), approvalAuditFile))
	if err != nil || bytes.Contains(data, []byte(`"event":"denied"`)) {
		t.Errorf("denied lines are in the decisions file (%v)", err)
	}
	// Notices to the desk are capped too.
	notes := 0
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
		_, data, err := desk.Read(ctx)
		cancel()
		if err != nil {
			break
		}
		if strings.Contains(string(data), "refused phone approval") {
			notes++
		}
	}
	if notes == 0 || notes > approvalNoticesPerMinute {
		t.Errorf("%d notices, want between 1 and %d", notes, approvalNoticesPerMinute)
	}
}

func TestALogThatCannotBeWrittenIsToldToTheDesk(t *testing.T) {
	e := newApprovalsEnv(t)
	desk := dialControlPage(t, e.srv)
	e.srv.approvals.deniedLog.dir = func() (string, error) { return "", errors.New("disk full") }
	e.srv.approvalDenied("d9", "x", "disabled")
	var note noticeMsg
	for {
		readUntil(t, desk, "notice", &note)
		if strings.Contains(note.Text, "could not be written") {
			break
		}
	}
	if !strings.Contains(note.Text, "1 line lost") {
		t.Errorf("notice %q", note.Text)
	}
}

func TestOldOffersAreForgottenOldestFirst(t *testing.T) {
	var o offerSet
	for i := 0; i < approvalOffersKept+50; i++ {
		o.add(strconv.Itoa(i))
	}
	if o.has("0") || o.has("49") {
		t.Error("the oldest offers are still held")
	}
	if !o.has("50") || !o.has(strconv.Itoa(approvalOffersKept+49)) {
		t.Error("recent offers were forgotten")
	}
	if len(o.have) != approvalOffersKept || len(o.order) != approvalOffersKept {
		t.Errorf("holds %d", len(o.have))
	}
}

func TestACardMustReadNowInEveryPartAsItDidWhenShown(t *testing.T) {
	base := approvalItem{Kind: "edit", Text: "/p/a.go", Added: 2, Removed: 1, CanAllow: true, Answerable: true}
	same := phoneCard{Answerable: true, Kind: "edit", Text: "/p/a.go", Added: 2, Removed: 1, CanAllow: true}
	if !sameCard(base, same) {
		t.Fatal("the same card read as changed")
	}
	for name, change := range map[string]func(*phoneCard){
		"text":       func(c *phoneCard) { c.Text = "/p/b.go" },
		"kind":       func(c *phoneCard) { c.Kind = "execute" },
		"added":      func(c *phoneCard) { c.Added = 3 },
		"removed":    func(c *phoneCard) { c.Removed = 0 },
		"can allow":  func(c *phoneCard) { c.CanAllow = false },
		"why":        func(c *phoneCard) { c.Why = whyLong },
		"answerable": func(c *phoneCard) { c.Answerable = false },
	} {
		c := same
		change(&c)
		if sameCard(base, c) {
			t.Errorf("a change of %s was not seen", name)
		}
	}
}

func TestADecisionOnACardWhoseCountsChangedIsStale(t *testing.T) {
	e := newApprovalsEnv(t)
	// The projection is read again at the decision; a change in any part refuses it.
	e.srv.approvals.setTimings(func(tm *approvalTimings) { tm.poll = time.Hour })
	p := e.open(t)
	it := p.list()[0]
	e.pane.mu.Lock()
	e.pane.card.Options = []acp.PermissionOption{optReject} // no plain Allow any more: same card number
	e.pane.mu.Unlock()
	res := p.decide(it, "deny")
	if res["ok"] == true || res["error"] != permissionStale {
		t.Fatalf("gave %v", res)
	}
	if len(e.pane.given()) != 0 {
		t.Fatal("it reached the pane")
	}
}
