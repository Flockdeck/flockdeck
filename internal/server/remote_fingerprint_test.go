package server

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jmwri/flockdeck/internal/e2e"
	"github.com/jmwri/flockdeck/internal/remote"
)

// The remote access dialog is where a person compares this desktop's and a
// paired device's out-of-band fingerprint (internal/e2e.Fingerprint,
// closing the gap internal/e2e's own doc describes): each device the roster
// lists comes with the code it and this machine's own registered key share,
// computed here rather than asked of the relay, which never holds a
// private key and could not compute it even if it wanted to lie about it.
func TestRemoteDevicesCarriesEachDevicesFingerprint(t *testing.T) {
	selfPriv, err := e2e.GenerateStaticKey()
	if err != nil {
		t.Fatal(err)
	}
	devicePriv, err := e2e.GenerateStaticKey()
	if err != nil {
		t.Fatal(err)
	}
	selfKey := e2e.EncodePublicKey(selfPriv.PublicKey())
	deviceKey := e2e.EncodePublicKey(devicePriv.PublicKey())
	want := e2e.Fingerprint(devicePriv.PublicKey(), selfPriv.PublicKey())

	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, fmt.Sprintf(
			`{"devices":[{"id":"d1","name":"phone","publicKey":%q},{"id":"d2","name":"old phone"}],`+
				`"hosts":[{"id":"h1","name":"desk","self":true,"publicKey":%q}]}`,
			deviceKey, selfKey))
	}))
	defer relay.Close()
	srv, _ := newTestServer(t)
	srv.SetRemote(&relayedRemote{client: remote.NewClient(&remote.Config{Relay: relay.URL, Token: "fdh_test"}, "v")})
	conn := dialControl(t, srv)
	sendCmd(t, conn, command{Cmd: "remoteDevices"})

	var msg struct {
		Devices []struct {
			ID          string `json:"id"`
			Fingerprint string `json:"fingerprint"`
		} `json:"devices"`
	}
	readUntil(t, conn, "remoteDevices", &msg)
	if len(msg.Devices) != 2 {
		t.Fatalf("got %d devices, want 2", len(msg.Devices))
	}
	got := map[string]string{}
	for _, d := range msg.Devices {
		got[d.ID] = d.Fingerprint
	}
	if got["d1"] != want {
		t.Errorf("d1's fingerprint = %q, want %q", got["d1"], want)
	}
	if got["d2"] != "" {
		t.Errorf(`d2 (no registered key) got a fingerprint anyway: %q`, got["d2"])
	}
}

// A desktop that has not yet registered its own end-to-end key -- an
// enrolment mid-upgrade, or one whose relay call has simply not landed yet
// -- has nothing of its own to compare a device's key against, so every
// device comes back with no fingerprint rather than one computed against
// an empty key.
func TestRemoteDevicesOmitsFingerprintWithNoSelfKey(t *testing.T) {
	devicePriv, err := e2e.GenerateStaticKey()
	if err != nil {
		t.Fatal(err)
	}
	deviceKey := e2e.EncodePublicKey(devicePriv.PublicKey())

	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, fmt.Sprintf(
			`{"devices":[{"id":"d1","name":"phone","publicKey":%q}],"hosts":[{"id":"h1","name":"desk","self":true}]}`,
			deviceKey))
	}))
	defer relay.Close()
	srv, _ := newTestServer(t)
	srv.SetRemote(&relayedRemote{client: remote.NewClient(&remote.Config{Relay: relay.URL, Token: "fdh_test"}, "v")})
	conn := dialControl(t, srv)
	sendCmd(t, conn, command{Cmd: "remoteDevices"})

	var msg struct {
		Devices []struct {
			Fingerprint string `json:"fingerprint"`
		} `json:"devices"`
	}
	readUntil(t, conn, "remoteDevices", &msg)
	if len(msg.Devices) != 1 {
		t.Fatalf("got %d devices, want 1", len(msg.Devices))
	}
	if msg.Devices[0].Fingerprint != "" {
		t.Errorf("got a fingerprint with no self key on file: %q", msg.Devices[0].Fingerprint)
	}
}
