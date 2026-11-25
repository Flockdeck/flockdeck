package server

import (
	"context"
	"crypto/ecdh"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/jmwri/flockdeck/internal/e2e"
	"github.com/jmwri/flockdeck/internal/remote"
	"github.com/jmwri/flockdeck/internal/store"
)

// A full device whose key the desk has not marked verified may watch a
// terminal and is not let type into it, which is the rule /ws/sealed applies
// before it says anything at all. That a verified device does type is
// TestPTYEndToEndEncryptsWhenBothSidesHaveAKey.
func TestPTYTakesNoInputFromAnUnverifiedFullDevice(t *testing.T) {
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
	key := devicePriv.PublicKey().Bytes()
	fake := &approvalsFake{
		e2eFakeRemote: e2eFakeRemote{
			capable:   map[string]bool{"d1": true},
			hostPriv:  hostPriv,
			devicePub: map[string]*ecdh.PublicKey{"d1": devicePriv.PublicKey()},
		},
		keys:   map[string][]byte{"d1": key},
		states: map[string]remote.VerifyState{},
	}
	srv.SetRemote(fake)
	putAccess(t, srv, "d1", store.DeviceAccess{Role: store.RoleFull})

	ts := remoteServer(t, srv)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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

	dh, hello, err := e2e.StartDeviceHandshake(devicePriv, hostPriv.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Write(ctx, websocket.MessageBinary, hello); err != nil {
		t.Fatal(err)
	}
	_, response, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read the handshake response: %v", err)
	}
	sess, err := dh.Finish(response)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Write(ctx, websocket.MessageBinary, sealCtl(t, sess, map[string]any{
		"resize": map[string]int{"cols": 80, "rows": 24},
	})); err != nil {
		t.Fatal(err)
	}

	// Typed before the key is verified: thrown away.
	blocked := fmt.Sprintf("blockedmark%d", time.Now().UnixNano())
	if err := conn.Write(ctx, websocket.MessageBinary, sealKeys(sess, "echo "+blocked+"\n")); err != nil {
		t.Fatal(err)
	}
	// Whatever the pane prints in the next while is read; a typed line would
	// have been echoed by then.
	var seen strings.Builder
	quiet, stop := context.WithTimeout(ctx, 2*time.Second)
	for {
		_, frame, err := conn.Read(quiet)
		if err != nil {
			break
		}
		if tag, payload := openFrame(t, sess, frame); tag == 0 {
			seen.Write(payload)
		}
	}
	stop()
	if strings.Contains(seen.String(), blocked) {
		t.Fatalf("what an unverified device typed reached the pane: %s", seen.String())
	}
}

func TestPTYReadOnlyReasonIsInFixedWords(t *testing.T) {
	r, m := ptyReadOnlyReason(store.DeviceAccess{Role: store.RoleFull}, false)
	if r != "verify" || !strings.Contains(m, "verified") {
		t.Errorf("an unverified full device was given %q, %q", r, m)
	}
	r, _ = ptyReadOnlyReason(store.DeviceAccess{Role: store.RoleViewer, WatchPanes: true}, true)
	if r != "role" {
		t.Errorf("a viewer was given %q, want role", r)
	}
}
