package server

import (
	"context"
	"crypto/ecdh"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/jmwri/flockdeck/internal/artifacts"
	"github.com/jmwri/flockdeck/internal/e2e"
	"github.com/jmwri/flockdeck/internal/remote"
	"github.com/jmwri/flockdeck/internal/store"
)

// fakeVerifier is the desk's verified-device record: it says yes only for the
// exact (device, origin, key) it was given.
type fakeVerifier struct {
	mu   sync.Mutex
	yes  map[string]bool
	seen int
}

func vkey(device string, origin remote.KeyOrigin, key []byte) string {
	return device + "|" + string(rune('0'+int(origin))) + "|" + string(key)
}

func (v *fakeVerifier) verify(device string, origin remote.KeyOrigin, key []byte) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.yes == nil {
		v.yes = map[string]bool{}
	}
	v.yes[vkey(device, origin, key)] = true
}

func (v *fakeVerifier) DeviceVerified(device string, origin remote.KeyOrigin, key []byte) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.seen++
	return v.yes[vkey(device, origin, key)]
}

// artifactFake is remote access with the real handshake and a device key the
// test can change.
type artifactFake struct {
	e2eFakeRemote
	kmu  sync.Mutex
	keys map[string][]byte
}

func (f *artifactFake) E2EDeviceKey(_ context.Context, device string, _ remote.KeyOrigin) ([]byte, bool) {
	f.kmu.Lock()
	defer f.kmu.Unlock()
	k, ok := f.keys[device]
	return k, ok
}

func (f *artifactFake) setKey(device string, k []byte) {
	f.kmu.Lock()
	f.keys[device] = k
	f.kmu.Unlock()
}

type artifactEnv struct {
	srv        *Server
	ts         *httptest.Server
	fake       *artifactFake
	ver        *fakeVerifier
	hostPriv   *ecdh.PrivateKey
	devicePriv *ecdh.PrivateKey
	key        []byte
}

// newArtifactEnv is a server with remote access whose device d1 has a key, and
// nothing switched on.
func newArtifactEnv(t *testing.T) *artifactEnv {
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
	key := devicePriv.PublicKey().Bytes()
	fake := &artifactFake{
		e2eFakeRemote: e2eFakeRemote{
			capable:   map[string]bool{"d1": true},
			hostPriv:  hostPriv,
			devicePub: map[string]*ecdh.PublicKey{"d1": devicePriv.PublicKey()},
		},
		keys: map[string][]byte{"d1": key},
	}
	ver := &fakeVerifier{}
	srv.SetRemote(fake)
	srv.SetArtifactVerifier(ver)
	srv.artifacts.audit.dir = func() (string, error) { return store.Dir() }
	return &artifactEnv{srv: srv, ts: remoteServer(t, srv), fake: fake, ver: ver, hostPriv: hostPriv, devicePriv: devicePriv, key: key}
}

// allow switches recordings on, acknowledged, for device d1, and verifies it.
func (e *artifactEnv) allow(t *testing.T) {
	t.Helper()
	e.ver.verify("d1", remote.KeyOriginDesk, e.key)
	savePrefs(t, func(p *store.Prefs) {
		p.RemoteArtifacts.SetAck("recordings", store.ArtifactAck{Version: artifactAckVersion, At: time.Now()})
		p.RemoteArtifacts.SetKind("recordings", true)
		p.RemoteArtifacts.AddDevice("d1")
	})
}

func savePrefs(t *testing.T, change func(*store.Prefs)) {
	t.Helper()
	p, err := store.ReadPrefs()
	if err != nil {
		t.Fatal(err)
	}
	change(&p)
	if err := store.SavePrefs(p); err != nil {
		t.Fatal(err)
	}
}

func (e *artifactEnv) header(device string) http.Header {
	h := http.Header{}
	h.Set("Origin", e.ts.URL)
	h.Set("Flockdeck-Remote-Device", device)
	h.Set("Flockdeck-Remote-Device-Name", "Pixel 8")
	h.Set("Flockdeck-Remote-Origin", "desk")
	return h
}

func (e *artifactEnv) rawDial(t *testing.T, h http.Header) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(e.ts.URL, "http")+"/ws/artifacts", &websocket.DialOptions{HTTPHeader: h})
	if conn != nil {
		conn.SetReadLimit(1 << 20)
		t.Cleanup(func() { _ = conn.CloseNow() })
	}
	return conn, resp, err
}

// session runs the device's side of the handshake on conn.
func (e *artifactEnv) session(t *testing.T, conn *websocket.Conn) *e2e.Session {
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

// open dials and handshakes.
func (e *artifactEnv) open(t *testing.T) (*websocket.Conn, *e2e.Session) {
	t.Helper()
	conn, _, err := e.rawDial(t, e.header("d1"))
	if err != nil {
		t.Fatalf("dial /ws/artifacts: %v", err)
	}
	return conn, e.session(t, conn)
}

// ask sends one request and returns the decoded reply, or fails the test.
func ask2(t *testing.T, conn *websocket.Conn, sess *e2e.Session, req any) map[string]any {
	t.Helper()
	sendReq(t, conn, sess, req)
	return readReply(t, conn, sess)
}

func sendReq(t *testing.T, conn *websocket.Conn, sess *e2e.Session, req any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageBinary, sealCtl(t, sess, req)); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func readReply(t *testing.T, conn *websocket.Conn, sess *e2e.Session) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	typ, frame, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if typ != websocket.MessageBinary {
		t.Fatalf("a %v frame on the artifacts socket", typ)
	}
	_, payload := openFrame(t, sess, frame)
	var m map[string]any
	if err := json.Unmarshal(payload, &m); err != nil {
		t.Fatalf("reply is not JSON: %v", err)
	}
	return m
}

// closedWith waits for the host to close conn and returns the close code and
// reason.
func closedWith(t *testing.T, conn *websocket.Conn) (websocket.StatusCode, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for {
		_, _, err := conn.Read(ctx)
		if err == nil {
			continue
		}
		code := websocket.CloseStatus(err)
		if code == -1 {
			t.Fatalf("the socket ended without a close frame: %v", err)
		}
		reason := ""
		var ce websocket.CloseError
		if errors.As(err, &ce) {
			reason = ce.Reason
		}
		return code, reason
	}
}

func TestArtifactsSocketIsNotServedLocally(t *testing.T) {
	srv, _ := newTestServer(t)
	// The local listener, with the token: the desk has no use for this socket.
	resp, err := http.Get("http://" + srv.Addr() + "/ws/artifacts?t=" + srv.Token())
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("a local request for /ws/artifacts = %d, want 404", resp.StatusCode)
	}
}

func TestArtifactsSocketServesNothingWhileOff(t *testing.T) {
	e := newArtifactEnv(t)
	// Verified and allowed, but no kind is on.
	e.ver.verify("d1", remote.KeyOriginDesk, e.key)
	savePrefs(t, func(p *store.Prefs) { p.RemoteArtifacts.AddDevice("d1") })
	conn, _, err := e.rawDial(t, e.header("d1"))
	if err != nil {
		t.Fatal(err)
	}
	code, reason := closedWith(t, conn)
	if code != artifactCloseRefused || reason != artifactReasonDisabled {
		t.Errorf("closed with %d %q, want 4403 %q", code, reason, artifactReasonDisabled)
	}
}

func TestArtifactsKindNeedsItsAcknowledgement(t *testing.T) {
	e := newArtifactEnv(t)
	e.ver.verify("d1", remote.KeyOriginDesk, e.key)
	// Switched on by editing the file, without the acknowledgement.
	savePrefs(t, func(p *store.Prefs) {
		p.RemoteArtifacts.SetKind("recordings", true)
		p.RemoteArtifacts.AddDevice("d1")
	})
	conn, _, _ := e.rawDial(t, e.header("d1"))
	if code, reason := closedWith(t, conn); code != artifactCloseRefused || reason != artifactReasonDisabled {
		t.Errorf("closed with %d %q, want a refusal as disabled", code, reason)
	}
	// An acknowledgement of an older wording does not count either.
	savePrefs(t, func(p *store.Prefs) {
		p.RemoteArtifacts.SetAck("recordings", store.ArtifactAck{Version: artifactAckVersion - 1})
	})
	conn, _, _ = e.rawDial(t, e.header("d1"))
	if code, _ := closedWith(t, conn); code != artifactCloseRefused {
		t.Errorf("an old acknowledgement was accepted: closed with %d", code)
	}
}

func TestArtifactsDeviceMustBeAllowedAndVerified(t *testing.T) {
	e := newArtifactEnv(t)
	e.allow(t)

	// Not on the allowlist: another device, with a key of its own.
	e.fake.capable["d2"] = true
	e.fake.setKey("d2", []byte("some key"))
	e.ver.verify("d2", remote.KeyOriginDesk, []byte("some key"))
	conn, _, _ := e.rawDial(t, e.header("d2"))
	if code, reason := closedWith(t, conn); code != artifactCloseRefused || reason != artifactReasonDevice {
		t.Errorf("a device off the allowlist: closed with %d %q", code, reason)
	}

	// On the allowlist but never verified.
	e.ver.mu.Lock()
	e.ver.yes = nil
	e.ver.mu.Unlock()
	conn, _, _ = e.rawDial(t, e.header("d1"))
	if code, reason := closedWith(t, conn); code != artifactCloseRefused || reason != artifactReasonVerify {
		t.Errorf("an unverified device: closed with %d %q", code, reason)
	}

	// Verified for one key, then the key changes: verification does not follow it.
	e.ver.verify("d1", remote.KeyOriginDesk, e.key)
	e.fake.setKey("d1", []byte("a replacement key"))
	conn, _, _ = e.rawDial(t, e.header("d1"))
	if code, reason := closedWith(t, conn); code != artifactCloseRefused || reason != artifactReasonVerify {
		t.Errorf("a device whose key changed: closed with %d %q", code, reason)
	}
}

func TestArtifactsVerifiedForTheOtherOriginIsNotVerified(t *testing.T) {
	e := newArtifactEnv(t)
	e.allow(t) // verified for the desk origin only
	h := e.header("d1")
	h.Del("Flockdeck-Remote-Origin") // the usual origin
	conn, _, _ := e.rawDial(t, h)
	if code, reason := closedWith(t, conn); code != artifactCloseRefused || reason != artifactReasonVerify {
		t.Errorf("closed with %d %q, want a refusal to verify", code, reason)
	}
}

func TestArtifactsNoVerifierMeansNobodyIsServed(t *testing.T) {
	e := newArtifactEnv(t)
	e.allow(t)
	e.srv.SetArtifactVerifier(nil)
	conn, _, _ := e.rawDial(t, e.header("d1"))
	if code, reason := closedWith(t, conn); code != artifactCloseRefused || reason != artifactReasonVerify {
		t.Errorf("closed with %d %q, want a refusal to verify", code, reason)
	}
}

func TestArtifactsNeedsEndToEndKeys(t *testing.T) {
	e := newArtifactEnv(t)
	e.allow(t)
	// The device has no key registered: the terminal would be served in the
	// clear here, and this must not be.
	e.fake.capable["d1"] = false
	conn, _, _ := e.rawDial(t, e.header("d1"))
	if code, reason := closedWith(t, conn); code != artifactCloseRefused || reason != artifactReasonKey {
		t.Errorf("closed with %d %q, want a refusal for want of a key", code, reason)
	}

	// No remote access at all.
	e.fake.capable["d1"] = true
	e.srv.SetRemote(nil)
	conn, _, _ = e.rawDial(t, e.header("d1"))
	if code, _ := closedWith(t, conn); code != artifactCloseRefused {
		t.Errorf("closed with %d without remote access", code)
	}
}

func TestArtifactsHandshakeIsNotOptional(t *testing.T) {
	e := newArtifactEnv(t)
	e.allow(t)
	conn, _, err := e.rawDial(t, e.header("d1"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// A request in the clear, where the handshake hello belongs.
	_ = conn.Write(ctx, websocket.MessageText, []byte(`{"op":"hello","v":1}`))
	code, _ := closedWith(t, conn)
	if code != websocket.StatusPolicyViolation {
		t.Errorf("a plaintext first frame closed with %d, want %d", code, websocket.StatusPolicyViolation)
	}
	// And a hello that does not parse as one.
	conn, _, _ = e.rawDial(t, e.header("d1"))
	_ = conn.Write(ctx, websocket.MessageBinary, []byte("not a public key"))
	if code, _ := closedWith(t, conn); code != websocket.StatusPolicyViolation {
		t.Errorf("a garbage hello closed with %d", code)
	}
}

func TestArtifactsRefusesForeignAndMissingOrigin(t *testing.T) {
	e := newArtifactEnv(t)
	e.allow(t)

	h := e.header("d1")
	h.Del("Origin")
	if _, resp, err := e.rawDial(t, h); err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Errorf("no Origin: err %v resp %v, want 403", err, resp)
	}
	h = e.header("d1")
	h.Set("Origin", "https://evil.example")
	if conn, _, err := e.rawDial(t, h); err == nil {
		t.Errorf("a foreign Origin was accepted (%v)", conn)
	}
	h = e.header("")
	if _, resp, err := e.rawDial(t, h); err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Errorf("no device: err %v resp %v, want 403", err, resp)
	}
}

func TestArtifactsHelloAndEmptyLists(t *testing.T) {
	e := newArtifactEnv(t)
	e.allow(t)
	conn, sess := e.open(t)

	hello := ask2(t, conn, sess, map[string]any{"op": "hello", "v": 1})
	if hello["op"] != "hello" || hello["v"] != float64(1) {
		t.Fatalf("hello = %v", hello)
	}
	kinds, _ := hello["kinds"].([]any)
	if len(kinds) != 1 || kinds[0] != "recordings" {
		t.Errorf("kinds = %v, want [recordings]", hello["kinds"])
	}
	lim, _ := hello["limits"].(map[string]any)
	if lim["request"] != float64(artifacts.MaxRequestBytes) || lim["chunk"] != float64(artifacts.ChunkBytes) {
		t.Errorf("limits = %v", lim)
	}

	list := ask2(t, conn, sess, map[string]any{"op": "list", "kind": "recordings"})
	items, ok := list["items"].([]any)
	if list["op"] != "list" || !ok || len(items) != 0 {
		t.Errorf("list = %v, want an empty items array", list)
	}
	// A kind that exists but is off, and one that does not exist.
	if r := ask2(t, conn, sess, map[string]any{"op": "list", "kind": "files"}); r["code"] != "disabled" {
		t.Errorf("list files = %v, want disabled", r)
	}
	if r := ask2(t, conn, sess, map[string]any{"op": "list", "kind": "nope"}); r["code"] != "unavailable" {
		t.Errorf("list nope = %v, want unavailable", r)
	}
}

// Nothing a client sends can name a path, URL or port: those fields are not
// read, and an id this socket never issued is the same error as any other.
func TestArtifactsClientCannotNameAThing(t *testing.T) {
	e := newArtifactEnv(t)
	e.allow(t)
	conn, sess := e.open(t)
	secret := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(secret, []byte("TOP-SECRET-CONTENT"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, req := range []map[string]any{
		{"op": "open", "id": secret, "kind": "recordings", "path": secret, "file": secret},
		{"op": "open", "path": secret, "url": "http://127.0.0.1:1/", "port": 5173, "cwd": "/"},
		{"op": "open", "id": "../../etc/passwd"},
		{"op": "list", "kind": "recordings", "path": secret, "root": "/"},
	} {
		reply := ask2(t, conn, sess, req)
		b, _ := json.Marshal(reply)
		if strings.Contains(string(b), "TOP-SECRET") || strings.Contains(string(b), secret) {
			t.Fatalf("a reply carried what the request named: %s", b)
		}
		if req["op"] == "open" && reply["code"] != "unavailable" {
			t.Errorf("open %v = %v, want unavailable", req, reply)
		}
		if req["op"] == "list" && len(reply["items"].([]any)) != 0 {
			t.Errorf("list with a path field returned items: %v", reply)
		}
	}
}

func TestArtifactsUnknownIDsCloseTheSocketAndTellTheDesk(t *testing.T) {
	e := newArtifactEnv(t)
	e.allow(t)
	desk := dialControl(t, e.srv)
	nextHello(t, desk)
	conn, sess := e.open(t)
	for i := 0; i < artifactUnknownMax; i++ {
		if r := ask2(t, conn, sess, map[string]any{"op": "open", "id": "AAAAAAAAAAAAAAAAAAAAAA"}); r["code"] != "unavailable" {
			t.Fatalf("open %d = %v", i, r)
		}
	}
	sendReq(t, conn, sess, map[string]any{"op": "open", "id": "AAAAAAAAAAAAAAAAAAAAAA"})
	if code, _ := closedWith(t, conn); code != artifactCloseRefused {
		t.Errorf("the socket closed with %d, want a refusal", code)
	}
	var note noticeMsg
	for {
		readUntil(t, desk, "notice", &note)
		if strings.Contains(note.Text, "refused") {
			break
		}
	}
	if !note.Error {
		t.Errorf("the refusal notice is not an error: %+v", note)
	}
}

func TestArtifactsOversizedAndMalformedRequests(t *testing.T) {
	e := newArtifactEnv(t)
	e.allow(t)
	conn, sess := e.open(t)

	// Not JSON.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	plain := append([]byte{1}, []byte("{not json")...)
	if err := conn.Write(ctx, websocket.MessageBinary, sess.Seal(plain)); err != nil {
		t.Fatal(err)
	}
	if r := readReply(t, conn, sess); r["code"] != "unavailable" {
		t.Errorf("malformed request = %v", r)
	}
	// Binary-tagged where a request is text.
	if err := conn.Write(ctx, websocket.MessageBinary, sess.Seal([]byte{0, '{', '}'})); err != nil {
		t.Fatal(err)
	}
	if r := readReply(t, conn, sess); r["code"] != "unavailable" {
		t.Errorf("binary request = %v", r)
	}
	// Over the request limit: the library read limit ends the socket.
	big := append([]byte{1}, []byte(`{"op":"hello","pad":"`+strings.Repeat("a", artifacts.MaxRequestBytes+2048)+`"}`)...)
	_ = conn.Write(ctx, websocket.MessageBinary, sess.Seal(big))
	if _, _, err := conn.Read(ctx); err == nil {
		t.Error("an oversized request was answered")
	}
}

func TestArtifactsRateLimitAndSocketCap(t *testing.T) {
	e := newArtifactEnv(t)
	e.allow(t)
	conn, sess := e.open(t)
	busy := 0
	for i := 0; i < artifacts.RequestsPerWindow+10; i++ {
		r := ask2(t, conn, sess, map[string]any{"op": "hello"})
		if r["code"] == "busy" {
			busy++
		}
	}
	if busy < 5 {
		t.Errorf("only %d of %d requests over the rate were turned away", busy, 10)
	}

	// The rate is the device's, not the socket's: a second socket starts empty-handed.
	conn2, sess2 := e.open(t)
	if r := ask2(t, conn2, sess2, map[string]any{"op": "hello"}); r["code"] != "busy" {
		t.Errorf("a second socket got its own allowance: %v", r)
	}

	// Three sockets in all; the fourth is turned away.
	e.open(t)
	conn4, _, err := e.rawDial(t, e.header("d1"))
	if err != nil {
		t.Fatal(err)
	}
	if code, reason := closedWith(t, conn4); code != artifactCloseBusy {
		t.Errorf("the fourth socket closed with %d %q, want %d", code, reason, artifactCloseBusy)
	}
}

func TestArtifactsTurnedOffAtTheDeskTakesEffectOnTheNextFrame(t *testing.T) {
	e := newArtifactEnv(t)
	e.allow(t)
	conn, sess := e.open(t)
	if r := ask2(t, conn, sess, map[string]any{"op": "hello"}); r["op"] != "hello" {
		t.Fatalf("hello = %v", r)
	}
	// Switched off in the file, as `flockdeck remote artifacts off` does with no
	// window open and nobody told.
	savePrefs(t, func(p *store.Prefs) { p.RemoteArtifacts.AllOff() })
	sendReq(t, conn, sess, map[string]any{"op": "hello"})
	if code, reason := closedWith(t, conn); code != artifactCloseRefused || reason != artifactReasonDisabled {
		t.Errorf("closed with %d %q", code, reason)
	}
}

func TestArtifactsVerificationRevokedClosesTheSocket(t *testing.T) {
	e := newArtifactEnv(t)
	e.allow(t)
	conn, sess := e.open(t)
	e.ver.mu.Lock()
	e.ver.yes = nil
	e.ver.mu.Unlock()
	sendReq(t, conn, sess, map[string]any{"op": "hello"})
	if code, reason := closedWith(t, conn); code != artifactCloseRefused || reason != artifactReasonVerify {
		t.Errorf("closed with %d %q", code, reason)
	}
}

func TestArtifactsKeyChangedDuringHandshakeIsRefused(t *testing.T) {
	e := newArtifactEnv(t)
	e.allow(t)
	// The key the check saw is not the key a second look finds.
	e.fake.keys = nil
	calls := 0
	swap := &swappingKeys{artifactFake: e.fake, first: e.key, after: []byte("rotated"), calls: &calls}
	e.srv.SetRemote(swap)
	conn, _, err := e.rawDial(t, e.header("d1"))
	if err != nil {
		t.Fatal(err)
	}
	e.session(t, conn)
	if code, reason := closedWith(t, conn); code != artifactCloseRefused || reason != artifactReasonVerify {
		t.Errorf("closed with %d %q, want a refusal to verify", code, reason)
	}
}

type swappingKeys struct {
	*artifactFake
	first, after []byte
	calls        *int
}

func (s *swappingKeys) E2EDeviceKey(context.Context, string, remote.KeyOrigin) ([]byte, bool) {
	*s.calls++
	if *s.calls == 1 {
		return s.first, true
	}
	return s.after, true
}

func TestArtifactsNothingOnTheWireIsPlaintext(t *testing.T) {
	e := newArtifactEnv(t)
	e.allow(t)
	conn, sess := e.open(t)
	sendReq(t, conn, sess, map[string]any{"op": "list", "kind": "recordings"})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	typ, frame, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if typ != websocket.MessageBinary || strings.Contains(string(frame), "items") || strings.Contains(string(frame), "recordings") {
		t.Errorf("a reply crossed the wire readable: %q", frame)
	}
}

func TestArtifactsReplayedFrameFailsOnANewSocket(t *testing.T) {
	e := newArtifactEnv(t)
	e.allow(t)
	conn, sess := e.open(t)
	frame := sealCtl(t, sess, map[string]any{"op": "hello"})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageBinary, frame); err != nil {
		t.Fatal(err)
	}
	readReply(t, conn, sess)

	// A fresh socket and handshake, then the captured frame.
	conn2, sess2 := e.open(t)
	_ = sess2
	if err := conn2.Write(ctx, websocket.MessageBinary, frame); err != nil {
		t.Fatal(err)
	}
	if _, _, err := conn2.Read(ctx); err == nil {
		t.Error("a frame captured on one socket was answered on another")
	}
}

func TestArtifactsRepliesCarryNoTokenOrPath(t *testing.T) {
	e := newArtifactEnv(t)
	e.allow(t)
	conn, sess := e.open(t)
	dir, _ := store.Dir()
	home, _ := os.UserHomeDir()
	var all []string
	for _, req := range []map[string]any{
		{"op": "hello"}, {"op": "list", "kind": "recordings"}, {"op": "open", "id": "x"}, {"op": "list", "kind": "files"}, {"op": "close"},
	} {
		b, _ := json.Marshal(ask2(t, conn, sess, req))
		all = append(all, string(b))
	}
	joined := strings.Join(all, "\n")
	for _, bad := range []string{e.srv.Token(), dir, home} {
		if bad != "" && strings.Contains(joined, bad) {
			t.Errorf("a reply contains %q", bad)
		}
	}
}

func TestArtifactsAuditLogRecordsConnectionsAndRefusals(t *testing.T) {
	e := newArtifactEnv(t)
	// Refused first: nothing is on.
	conn, _, _ := e.rawDial(t, e.header("d1"))
	closedWith(t, conn)
	e.allow(t)
	e.open(t)

	dir, _ := store.Dir()
	path := filepath.Join(dir, artifactAuditFile)
	waitFor(t, func() bool {
		b, _ := os.ReadFile(path)
		return strings.Contains(string(b), `"connect"`)
	})
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var events []auditEvent
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var ev auditEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("log line %q: %v", line, err)
		}
		events = append(events, ev)
	}
	if len(events) < 2 || events[0].Event != "denied" || events[0].Reason != artifactReasonDisabled || events[0].Device != "d1" || events[0].DeviceName != "Pixel 8" {
		t.Errorf("the first event should be the refusal, got %+v", events)
	}
	last := events[len(events)-1]
	if last.Event != "connect" || last.Device != "d1" {
		t.Errorf("the last event = %+v, want the connection", last)
	}
	if runtime.GOOS != "windows" {
		if fi, err := os.Stat(path); err == nil && fi.Mode().Perm()&0o077 != 0 {
			t.Errorf("the log is mode %v, want owner only", fi.Mode().Perm())
		}
	}
}

func TestArtifactsConnectionIsRefusedWhenItCannotBeRecorded(t *testing.T) {
	e := newArtifactEnv(t)
	e.allow(t)
	// The log's folder is a file, so nothing can be appended.
	blocker := filepath.Join(t.TempDir(), "not-a-folder")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	e.srv.artifacts.audit.dir = func() (string, error) { return blocker, nil }
	conn, _, err := e.rawDial(t, e.header("d1"))
	if err != nil {
		t.Fatal(err)
	}
	e.session(t, conn)
	if code, _ := closedWith(t, conn); code == -1 || code == websocket.StatusNormalClosure {
		t.Errorf("the socket was served although it could not be recorded (code %d)", code)
	}
}

func TestArtifactsDeskIsToldOncePerDevice(t *testing.T) {
	e := newArtifactEnv(t)
	e.allow(t)
	desk := dialControl(t, e.srv)
	nextHello(t, desk)
	e.open(t)
	var note noticeMsg
	readUntil(t, desk, "notice", &note)
	if !strings.Contains(note.Text, "Pixel 8") || note.Error {
		t.Errorf("notice = %+v", note)
	}
	if e.srv.artifacts.announce("d1") {
		t.Error("the desk would be told of the same device twice")
	}
}

func TestArtifactAuditLogRotates(t *testing.T) {
	dir := t.TempDir()
	a := &auditLog{dir: func() (string, error) { return dir, nil }, max: 400}
	for i := 0; i < 40; i++ {
		if err := a.write(auditEvent{Event: "connect", Device: "d1"}); err != nil {
			t.Fatal(err)
		}
	}
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, en := range entries {
		names = append(names, en.Name())
		fi, _ := en.Info()
		if fi.Size() > 400 {
			t.Errorf("%s is %d bytes, over the cap", en.Name(), fi.Size())
		}
	}
	if len(names) != auditFiles {
		t.Errorf("files = %v, want %d", names, auditFiles)
	}
}

func TestArtifactAuditLogCleansFields(t *testing.T) {
	dir := t.TempDir()
	a := &auditLog{dir: func() (string, error) { return dir, nil }}
	hostile := "Pixel\n{\"event\":\"connect\",\"device\":\"forged\"}‮" + strings.Repeat("x", 1000)
	if err := a.write(auditEvent{Event: "denied", DeviceName: hostile}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, artifactAuditFile))
	if lines := strings.Count(string(b), "\n"); lines != 1 {
		t.Errorf("one event made %d lines: %q", lines, b)
	}
	var ev auditEvent
	if err := json.Unmarshal(b, &ev); err != nil {
		t.Fatal(err)
	}
	if len([]rune(ev.DeviceName)) > auditFieldMax || strings.ContainsAny(ev.DeviceName, "\n‮") {
		t.Errorf("device name = %q", ev.DeviceName)
	}
}

func TestArtifactDenialsAreThrottled(t *testing.T) {
	var a artifactState
	rec, note, _ := a.denial("d1\x00disabled")
	if !rec || !note {
		t.Fatal("the first refusal should be recorded and shown")
	}
	rec, note, _ = a.denial("d1\x00disabled")
	if rec || note {
		t.Error("an immediate repeat was recorded or shown")
	}
	if rec, _, _ := a.denial("d1\x00verify"); !rec {
		t.Error("a different reason shares the throttle")
	}
	if rec, _, _ := a.denial("d2\x00disabled"); !rec {
		t.Error("a different device shares the throttle")
	}
}

// ---------------------------------------------------------------------------
// Consent: the desk-only commands
// ---------------------------------------------------------------------------

func TestArtifactCommandsAreDeskOnly(t *testing.T) {
	e := newArtifactEnv(t)
	phone, err := dialRemoteControl(e.ts, e.ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer phone.CloseNow()
	for _, cmd := range []command{
		{Cmd: "setRemoteArtifacts", Kind: "on", Text: "recordings", Confirmed: true},
		{Cmd: "setRemoteArtifacts", Kind: "on", ID: "dev-1"},
		{Cmd: "revokeArtifactDevice", ID: "d1"},
		{Cmd: "stopRemoteArtifacts"},
	} {
		sendCmd(t, phone, cmd)
		var note noticeMsg
		readUntil(t, phone, "notice", &note)
		if !note.Error || !strings.Contains(note.Text, "machine") {
			t.Errorf("%s through the relay was answered %+v, want it refused", cmd.Cmd, note)
		}
	}
	if got := store.LoadPrefs().RemoteArtifacts; len(got.Kinds) != 0 || len(got.Devices) != 0 || len(got.Acks) != 0 {
		t.Errorf("a window through the relay changed the artifact settings: %+v", got)
	}
}

func TestArtifactKindNeedsConfirmationFirstTime(t *testing.T) {
	e := newArtifactEnv(t)
	desk := dialControlPage(t, e.srv)
	nextHello(t, desk)

	// A client that skips the question is answered with it.
	sendCmd(t, desk, command{Cmd: "setRemoteArtifacts", Kind: "on", Text: "recordings"})
	var note noticeMsg
	readUntil(t, desk, "notice", &note)
	if !note.Error || !strings.Contains(note.Text, "shell") || !strings.Contains(note.Text, "redact") {
		t.Errorf("notice = %+v, want the acknowledgement text", note)
	}
	if store.LoadPrefs().RemoteArtifacts.KindOn("recordings") {
		t.Fatal("recordings were switched on without confirmation")
	}

	sendCmd(t, desk, command{Cmd: "setRemoteArtifacts", Kind: "on", Text: "recordings", Confirmed: true})
	waitFor(t, func() bool { return store.LoadPrefs().RemoteArtifacts.KindOn("recordings") })
	got := store.LoadPrefs().RemoteArtifacts
	if !got.Acked("recordings", artifactAckVersion) {
		t.Errorf("no acknowledgement was recorded: %+v", got)
	}
	// No device is allowed yet, so nothing is available to any.
	if len(got.Devices) != 0 {
		t.Errorf("devices = %v", got.Devices)
	}

	// Off again, and on again with no new question.
	sendCmd(t, desk, command{Cmd: "setRemoteArtifacts", Kind: "off", Text: "recordings"})
	waitFor(t, func() bool { return !store.LoadPrefs().RemoteArtifacts.KindOn("recordings") })
	sendCmd(t, desk, command{Cmd: "setRemoteArtifacts", Kind: "on", Text: "recordings"})
	waitFor(t, func() bool { return store.LoadPrefs().RemoteArtifacts.KindOn("recordings") })
}

func TestArtifactKindsNotYetOfferedCannotBeSwitchedOn(t *testing.T) {
	e := newArtifactEnv(t)
	desk := dialControlPage(t, e.srv)
	nextHello(t, desk)
	for _, kind := range []string{"files", "links", "pdf", ""} {
		sendCmd(t, desk, command{Cmd: "setRemoteArtifacts", Kind: "on", Text: kind, Confirmed: true})
		var note noticeMsg
		readUntil(t, desk, "notice", &note)
		if !note.Error {
			t.Errorf("%q: notice %+v, want an error", kind, note)
		}
	}
	if got := store.LoadPrefs().RemoteArtifacts; len(got.Kinds) != 0 {
		t.Errorf("kinds = %v, want none", got.Kinds)
	}
}

func TestArtifactDeviceNeedsVerificationToBeAllowed(t *testing.T) {
	e := newArtifactEnv(t)
	desk := dialControlPage(t, e.srv)
	nextHello(t, desk)

	sendCmd(t, desk, command{Cmd: "setRemoteArtifacts", Kind: "on", ID: "d1"})
	var note noticeMsg
	readUntil(t, desk, "notice", &note)
	if !note.Error || !strings.Contains(note.Text, "verified") {
		t.Errorf("notice = %+v, want a refusal for want of verification", note)
	}
	if store.LoadPrefs().RemoteArtifacts.HasDevice("d1") {
		t.Fatal("an unverified device was allowed")
	}

	e.ver.verify("d1", remote.KeyOriginDesk, e.key)
	sendCmd(t, desk, command{Cmd: "setRemoteArtifacts", Kind: "on", ID: "d1"})
	readUntil(t, desk, "notice", &note)
	waitFor(t, func() bool { return store.LoadPrefs().RemoteArtifacts.HasDevice("d1") })

	// Both a kind and a device, or neither, is not understood.
	sendCmd(t, desk, command{Cmd: "setRemoteArtifacts", Kind: "on", ID: "d2", Text: "recordings"})
	readUntil(t, desk, "notice", &note)
	if !note.Error || store.LoadPrefs().RemoteArtifacts.HasDevice("d2") {
		t.Errorf("a command naming both: %+v", note)
	}
}

func TestRevokingADeviceClosesItsSocketsAtOnce(t *testing.T) {
	e := newArtifactEnv(t)
	e.allow(t)
	desk := dialControl(t, e.srv)
	nextHello(t, desk)
	conn, sess := e.open(t)
	if r := ask2(t, conn, sess, map[string]any{"op": "hello"}); r["op"] != "hello" {
		t.Fatal(r)
	}
	sendCmd(t, desk, command{Cmd: "revokeArtifactDevice", ID: "d1"})
	if code, _ := closedWith(t, conn); code != artifactCloseRefused {
		t.Errorf("closed with %d", code)
	}
	if store.LoadPrefs().RemoteArtifacts.HasDevice("d1") {
		waitFor(t, func() bool { return !store.LoadPrefs().RemoteArtifacts.HasDevice("d1") })
	}
	// And it cannot come back.
	conn2, _, _ := e.rawDial(t, e.header("d1"))
	if code, reason := closedWith(t, conn2); code != artifactCloseRefused || reason != artifactReasonDevice {
		t.Errorf("after revoking: closed with %d %q", code, reason)
	}
}

func TestStopRemoteArtifactsClosesEverySocketAndKeepsTheSettings(t *testing.T) {
	e := newArtifactEnv(t)
	e.allow(t)
	desk := dialControl(t, e.srv)
	nextHello(t, desk)
	conn, sess := e.open(t)
	conn2, sess2 := e.open(t)
	ask2(t, conn, sess, map[string]any{"op": "hello"})
	ask2(t, conn2, sess2, map[string]any{"op": "hello"})
	sendCmd(t, desk, command{Cmd: "stopRemoteArtifacts"})
	for _, c := range []*websocket.Conn{conn, conn2} {
		if code, _ := closedWith(t, c); code != artifactCloseRefused {
			t.Errorf("closed with %d", code)
		}
	}
	if !store.LoadPrefs().RemoteArtifacts.KindOn("recordings") {
		t.Error("stopping changed the settings")
	}
}

func TestSwitchingAKindOffClosesSocketsAtOnce(t *testing.T) {
	e := newArtifactEnv(t)
	e.allow(t)
	desk := dialControl(t, e.srv)
	nextHello(t, desk)
	conn, sess := e.open(t)
	ask2(t, conn, sess, map[string]any{"op": "hello"})
	sendCmd(t, desk, command{Cmd: "setRemoteArtifacts", Kind: "off", Text: "recordings"})
	if code, _ := closedWith(t, conn); code != artifactCloseRefused {
		t.Errorf("closed with %d", code)
	}
}

func TestArtifactsStopEndpointNeedsTheTokenAndPost(t *testing.T) {
	e := newArtifactEnv(t)
	e.allow(t)
	conn, sess := e.open(t)
	ask2(t, conn, sess, map[string]any{"op": "hello"})
	base := "http://" + e.srv.Addr()

	// Through the relay there is no token, so none of these reach it.
	resp, err := http.Post(e.ts.URL+"/remote/artifacts/stop", "text/plain", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("through the tunnel without a token = %d, want 403", resp.StatusCode)
	}
	resp, err = http.Get(base + "/remote/artifacts/stop?t=" + e.srv.Token())
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET = %d, want 405", resp.StatusCode)
	}
	// Still open.
	if r := ask2(t, conn, sess, map[string]any{"op": "hello"}); r["op"] != "hello" {
		t.Fatalf("the socket was closed by a refused request: %v", r)
	}

	savePrefs(t, func(p *store.Prefs) { p.RemoteArtifacts.AllOff() })
	if err := RequestArtifactsStop(base, e.srv.Token()); err != nil {
		t.Fatal(err)
	}
	if code, _ := closedWith(t, conn); code != artifactCloseRefused {
		t.Errorf("closed with %d", code)
	}
}

// ---------------------------------------------------------------------------
// What a window reached through the relay is told
// ---------------------------------------------------------------------------

func TestHelloAdvertisesArtifactsOnlyToAnAllowedDevice(t *testing.T) {
	e := newArtifactEnv(t)
	hello := func(device string) map[string]any {
		t.Helper()
		c, err := dialRemoteControlDevice(e.ts, e.ts.URL, device)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.CloseNow() })
		return readRemoteMsg(t, c, "hello")
	}
	if _, has := hello("d1")["artifacts"]; has {
		t.Error("artifacts advertised while everything is off")
	}
	e.allow(t)
	// Reread the preferences the way a restart would.
	ask(e.srv, func() bool { e.srv.prefs = store.LoadPrefs(); return true })
	h := hello("d1")
	if got, _ := h["artifacts"].(map[string]any); got["v"] != float64(1) {
		t.Errorf("artifacts = %v, want {v:1}", h["artifacts"])
	}
	if _, has := hello("d2")["artifacts"]; has {
		t.Error("artifacts advertised to a device that is not allowed")
	}
}

func TestRemoteWindowsAreNotSentTheArtifactSettings(t *testing.T) {
	e := newArtifactEnv(t)
	e.allow(t)
	ask(e.srv, func() bool { e.srv.prefs = store.LoadPrefs(); return true })
	c, err := dialRemoteControlDevice(e.ts, e.ts.URL, "d1")
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	h := readRemoteMsg(t, c, "hello")
	b, _ := json.Marshal(h["prefs"])
	if strings.Contains(string(b), "remoteArtifacts") || strings.Contains(string(b), "d1") {
		t.Errorf("a remote window was sent the artifact settings: %s", b)
	}
	// The desk is.
	desk := dialControl(t, e.srv)
	var dh struct {
		Prefs store.Prefs `json:"prefs"`
	}
	readUntil(t, desk, "hello", &dh)
	if !dh.Prefs.RemoteArtifacts.HasDevice("d1") {
		t.Errorf("the desk was not sent the artifact settings: %+v", dh.Prefs.RemoteArtifacts)
	}
}

func TestRemoteWindowIsToldWhenArtifactsComeAndGo(t *testing.T) {
	e := newArtifactEnv(t)
	c, err := dialRemoteControlDevice(e.ts, e.ts.URL, "d1")
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	readRemoteMsg(t, c, "hello")

	e.allow(t)
	ask(e.srv, func() bool { e.srv.prefs = store.LoadPrefs(); e.srv.broadcastPrefs(); return true })
	m := readRemoteMsg(t, c, "artifacts")
	if m["available"] != true {
		t.Errorf("artifacts = %v, want available", m)
	}
	savePrefs(t, func(p *store.Prefs) { p.RemoteArtifacts.AllOff() })
	ask(e.srv, func() bool { e.srv.prefs = store.LoadPrefs(); e.srv.broadcastPrefs(); return true })
	m = readRemoteMsg(t, c, "artifacts")
	if m["available"] != false {
		t.Errorf("artifacts = %v, want gone", m)
	}
}
