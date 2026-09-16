package server

import (
	"context"
	"crypto/ecdh"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/jmwri/flockdeck/internal/e2e"
	"github.com/jmwri/flockdeck/internal/remote"
)

// e2eFakeRemote is remote access whose end-to-end handshake is the real
// thing (internal/e2e), for hostID -- everything else about it is
// fakeRemote's usual stand-in. It lets a test play the browser's side of a
// handshake against handlePTY's real host-side code, without a relay or a
// real device anywhere in the picture.
type e2eFakeRemote struct {
	fakeRemote
	// capable is the set of device ids E2ECapable answers true for.
	capable   map[string]bool
	hostPriv  *ecdh.PrivateKey
	devicePub map[string]*ecdh.PublicKey
}

func (f *e2eFakeRemote) E2ECapable(_ context.Context, deviceID string) bool {
	return f.capable[deviceID]
}

func (f *e2eFakeRemote) E2ERespond(_ context.Context, deviceID string, hello []byte) (*e2e.Session, []byte, error) {
	pub := f.devicePub[deviceID]
	if pub == nil {
		return nil, nil, remote.ErrNoE2EKey
	}
	return e2e.RespondHostHandshake(f.hostPriv, pub, hello)
}

// sealCtl and sealKeys are the device's side of e2eConn's own tagging
// convention (internal/server/e2e.go): a text control message or a binary
// keystroke, tagged and then sealed as one internal/e2e frame.
func sealCtl(t *testing.T, sess *e2e.Session, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	plain := append([]byte{1}, b...)
	return sess.Seal(plain)
}

func sealKeys(sess *e2e.Session, s string) []byte {
	plain := append([]byte{0}, []byte(s)...)
	return sess.Seal(plain)
}

// openFrame is the device's side of reading one of the host's sealed
// frames back: its tag and its payload.
func openFrame(t *testing.T, sess *e2e.Session, frame []byte) (tag byte, payload []byte) {
	t.Helper()
	plain, err := sess.Open(frame)
	if err != nil {
		t.Fatalf("open a frame the host sealed: %v", err)
	}
	if len(plain) == 0 {
		t.Fatal("an empty frame")
	}
	return plain[0], plain[1:]
}

// TestPTYEndToEndEncryptsWhenBothSidesHaveAKey covers the whole point of the
// feature: a browser and this host that both have a key on file run a real
// internal/e2e handshake as the terminal socket's first two frames, and
// every frame after -- a keystroke up, the shell's output down -- is
// sealed, never once appearing on the wire as its own plaintext.
func TestPTYEndToEndEncryptsWhenBothSidesHaveAKey(t *testing.T) {
	srv, ws := newTestServer(t)
	paneID, ok := ask(srv, func() string { return ws.CurrentTab().Focus })
	if !ok || paneID == "" {
		t.Fatal("could not find the test pane")
	}

	hostPriv, err := e2e.GenerateStaticKey()
	if err != nil {
		t.Fatal(err)
	}
	devicePriv, err := e2e.GenerateStaticKey()
	if err != nil {
		t.Fatal(err)
	}
	srv.SetRemote(&e2eFakeRemote{
		capable:   map[string]bool{"d1": true},
		hostPriv:  hostPriv,
		devicePub: map[string]*ecdh.PublicKey{"d1": devicePriv.PublicKey()},
	})

	ts := remoteServer(t, srv)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	h := http.Header{}
	h.Set("Origin", ts.URL)
	h.Set("Flockdeck-Remote-Device", "d1")
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/ws/pty?id="+paneID,
		&websocket.DialOptions{HTTPHeader: h})
	if err != nil {
		t.Fatalf("dial pty through the tunnel: %v", err)
	}
	conn.SetReadLimit(16 << 20)
	defer conn.CloseNow()

	// The device's side of the handshake: send the hello, read the host's
	// response, and reach the same session the host does.
	dh, hello, err := e2e.StartDeviceHandshake(devicePriv, hostPriv.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Write(ctx, websocket.MessageBinary, hello); err != nil {
		t.Fatalf("send hello: %v", err)
	}
	_, response, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read the handshake response: %v", err)
	}
	sess, err := dh.Finish(response)
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}

	// A resize, sealed as a text-tagged frame, and a keystroke, sealed as a
	// binary-tagged one -- exactly what a real terminal socket interleaves.
	if err := conn.Write(ctx, websocket.MessageBinary, sealCtl(t, sess, map[string]any{
		"resize": map[string]int{"cols": 80, "rows": 24},
	})); err != nil {
		t.Fatalf("send resize: %v", err)
	}
	marker := fmt.Sprintf("e2e-marker-%d", time.Now().UnixNano())
	if err := conn.Write(ctx, websocket.MessageBinary, sealKeys(sess, "echo "+marker+"\n")); err != nil {
		t.Fatalf("send keystrokes: %v", err)
	}

	var seen strings.Builder
	deadline := time.Now().Add(20 * time.Second)
	for !strings.Contains(seen.String(), marker) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for the marker; saw:\n%s", seen.String())
		}
		typ, frame, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if typ != websocket.MessageBinary {
			t.Fatalf("a %v frame on an encrypted socket", typ)
		}
		// Never once the plaintext marker on the wire, sealed or not: this
		// is the point of the whole feature, checked against the exact
		// bytes coder/websocket handed back before this test's own Open
		// even runs.
		if strings.Contains(string(frame), marker) {
			t.Fatal("the marker appeared on the wire unsealed")
		}
		tag, payload := openFrame(t, sess, frame)
		if tag == 0 {
			seen.Write(payload)
		}
	}

	waitFor(t, func() bool {
		return !remoteTermViewers.insecure(paneID)
	})
}

// TestPTYRefusesAHandshakeThatFailsRatherThanFallingBack covers the
// downgrade a relay in the middle would want: once both sides are supposed
// to have a key on file, a first frame that does not parse as a handshake
// hello at all -- a relay that garbled it, or simply a bug -- closes the
// socket instead of quietly treating those bytes as a plaintext keystroke.
// Falling back here, rather than in E2ECapable's own clean "no key on
// either side" case, is exactly the strip a relay in the middle would want.
func TestPTYRefusesAHandshakeThatFailsRatherThanFallingBack(t *testing.T) {
	srv, ws := newTestServer(t)
	paneID, ok := ask(srv, func() string { return ws.CurrentTab().Focus })
	if !ok || paneID == "" {
		t.Fatal("could not find the test pane")
	}

	hostPriv, err := e2e.GenerateStaticKey()
	if err != nil {
		t.Fatal(err)
	}
	devicePriv, err := e2e.GenerateStaticKey()
	if err != nil {
		t.Fatal(err)
	}
	srv.SetRemote(&e2eFakeRemote{
		capable:   map[string]bool{"d1": true},
		hostPriv:  hostPriv,
		devicePub: map[string]*ecdh.PublicKey{"d1": devicePriv.PublicKey()},
	})

	ts := remoteServer(t, srv)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	h := http.Header{}
	h.Set("Origin", ts.URL)
	h.Set("Flockdeck-Remote-Device", "d1")
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/ws/pty?id="+paneID,
		&websocket.DialOptions{HTTPHeader: h})
	if err != nil {
		t.Fatalf("dial pty through the tunnel: %v", err)
	}
	conn.SetReadLimit(16 << 20)
	defer conn.CloseNow()

	// Neither a valid P-256 point nor anything internal/e2e's handshake
	// could make sense of -- what a browser that predates the wire format,
	// or a relay mangling the first frame, would produce.
	if err := conn.Write(ctx, websocket.MessageBinary, []byte("not a handshake hello")); err != nil {
		t.Fatalf("send a bogus hello: %v", err)
	}
	if _, _, err := conn.Read(ctx); err == nil {
		t.Fatal("the socket kept going after a hello that could not be parsed")
	}
}

// TestPTYFallsBackToPlaintextWithoutADeviceKey covers an older browser that
// has never registered an end-to-end key: E2ECapable says no, so handlePTY
// never attempts a handshake at all, and the terminal is served exactly as
// it always was.
func TestPTYFallsBackToPlaintextWithoutADeviceKey(t *testing.T) {
	srv, ws := newTestServer(t)
	paneID, ok := ask(srv, func() string { return ws.CurrentTab().Focus })
	if !ok || paneID == "" {
		t.Fatal("could not find the test pane")
	}
	// A RemoteAccess is present, but nothing is capable -- an enrolled
	// machine talking to a relay whose roster says this device has no key.
	srv.SetRemote(&e2eFakeRemote{capable: map[string]bool{}})

	ts := remoteServer(t, srv)
	conn := dialRemotePTYDevice(t, ts, paneID, "old-phone")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	marker := fmt.Sprintf("plain-marker-%d", time.Now().UnixNano())
	if err := conn.Write(ctx, websocket.MessageBinary, []byte("echo "+marker+"\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	var seen strings.Builder
	deadline := time.Now().Add(20 * time.Second)
	for !strings.Contains(seen.String(), marker) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for the marker; saw:\n%s", seen.String())
		}
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		seen.Write(data)
	}

	waitFor(t, func() bool {
		return remoteTermViewers.insecure(paneID)
	})
}
