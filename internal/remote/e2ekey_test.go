package remote

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/e2e"
)

// e2eFakeRelay is enough of a relay to test key registration and the roster
// lookup against: it answers POST /api/v1/host/e2e-key and GET
// /api/v1/host/devices, remembering the key it was given.
type e2eFakeRelay struct {
	*httptest.Server
	mu           sync.Mutex
	setKey       string   // the last key registered with SetE2EKey
	setCalls     int      // how many times e2e-key was called
	devicesCalls int      // how many times GET /api/v1/host/devices was called
	devices      []Device // what GET /api/v1/host/devices answers
}

func newE2EFakeRelay(t *testing.T, devices ...Device) *e2eFakeRelay {
	t.Helper()
	f := &e2eFakeRelay{devices: devices}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/host/e2e-key", func(w http.ResponseWriter, r *http.Request) {
		var req e2eKeyRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if _, err := e2e.DecodePublicKey(req.PublicKey); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":"not a P-256 key"}`)
			return
		}
		f.mu.Lock()
		f.setKey = req.PublicKey
		f.setCalls++
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /api/v1/host/devices", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		devs := f.devices
		f.devicesCalls++
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Roster{Devices: devs})
	})
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	return f
}

func (f *e2eFakeRelay) lastKey() (string, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.setKey, f.setCalls
}

func testManager(relayURL string) *Manager {
	m := NewManager("test", func(net.Listener) error { return nil }, nil)
	m.load = func() (*Config, error) { return nil, nil }
	m.cfg = &Config{Relay: relayURL, HostID: "h1", AccountID: "a1", Token: "fdh_test"}
	return m
}

// A machine with no saved identity makes one the first time it is asked,
// and keeps using that same one after.
func TestEnsureE2EIdentityPersists(t *testing.T) {
	isolate(t)
	priv1, err := ensureE2EIdentity()
	if err != nil {
		t.Fatal(err)
	}
	priv2, err := ensureE2EIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if e2e.EncodePublicKey(priv1.PublicKey()) != e2e.EncodePublicKey(priv2.PublicKey()) {
		t.Fatal("ensureE2EIdentity made a new key the second time")
	}
	// And it survives a fresh load from disk, as a restarted Flockdeck does.
	loaded, err := loadE2EIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if loaded == nil || e2e.EncodePublicKey(loaded.PublicKey()) != e2e.EncodePublicKey(priv1.PublicKey()) {
		t.Fatal("loadE2EIdentity did not round-trip the saved key")
	}
}

// A machine that has never made one has none to load.
func TestLoadE2EIdentityAbsent(t *testing.T) {
	isolate(t)
	priv, err := loadE2EIdentity()
	if priv != nil || err != nil {
		t.Fatalf("loadE2EIdentity with nothing saved = %v, %v; want nil, nil", priv, err)
	}
}

// EnsureE2EKey makes an identity and registers its public half with the
// relay, in the encoding internal/e2e uses.
func TestEnsureE2EKeyRegisters(t *testing.T) {
	isolate(t)
	relay := newE2EFakeRelay(t)
	m := testManager(relay.URL)

	if err := m.EnsureE2EKey(context.Background()); err != nil {
		t.Fatalf("EnsureE2EKey: %v", err)
	}
	priv, err := m.hostE2EIdentity()
	if err != nil {
		t.Fatal(err)
	}
	gotKey, calls := relay.lastKey()
	if calls != 1 {
		t.Fatalf("e2e-key was called %d times, want 1", calls)
	}
	if want := e2e.EncodePublicKey(priv.PublicKey()); gotKey != want {
		t.Fatalf("registered key %q, want %q", gotKey, want)
	}
	at, regErr := m.E2EKeyStatus()
	if regErr != nil || at.IsZero() {
		t.Fatalf("E2EKeyStatus = %v, %v; want a time and no error", at, regErr)
	}

	// Calling it again is fine, and simply registers the same key again --
	// what a rotation, or the relay having forgotten it, both look like.
	if err := m.EnsureE2EKey(context.Background()); err != nil {
		t.Fatalf("EnsureE2EKey a second time: %v", err)
	}
	if _, calls := relay.lastKey(); calls != 2 {
		t.Fatalf("e2e-key was called %d times, want 2", calls)
	}
}

// A device that has not registered a key -- an older client -- makes
// E2ECapable answer false rather than error, and E2ERespond fail with
// ErrNoE2EKey rather than attempt a handshake.
func TestE2ECapableFalseWithoutADeviceKey(t *testing.T) {
	isolate(t)
	relay := newE2EFakeRelay(t, Device{ID: "d1", Name: "old phone"})
	m := testManager(relay.URL)

	if m.E2ECapable(context.Background(), "d1", KeyOriginUsual) {
		t.Error("E2ECapable said yes for a device with no registered key")
	}
	if m.E2ECapable(context.Background(), "unknown", KeyOriginUsual) {
		t.Error("E2ECapable said yes for a device that does not exist")
	}
	if _, _, err := m.E2ERespond(context.Background(), "d1", KeyOriginUsual, []byte("not a real hello")); err != ErrNoE2EKey {
		t.Errorf("E2ERespond = %v, want ErrNoE2EKey", err)
	}
}

// A device that has registered a key makes E2ECapable answer true, and
// E2ERespond complete a real handshake that a device's own
// StartDeviceHandshake, run against this host's now-registered public key,
// agrees with.
func TestE2ERespondCompletesAHandshake(t *testing.T) {
	isolate(t)
	devicePriv, err := e2e.GenerateStaticKey()
	if err != nil {
		t.Fatal(err)
	}
	relay := newE2EFakeRelay(t, Device{ID: "d1", Name: "phone", PublicKey: e2e.EncodePublicKey(devicePriv.PublicKey())})
	m := testManager(relay.URL)

	if err := m.EnsureE2EKey(context.Background()); err != nil {
		t.Fatal(err)
	}
	hostPriv, err := m.hostE2EIdentity()
	if err != nil {
		t.Fatal(err)
	}

	if !m.E2ECapable(context.Background(), "d1", KeyOriginUsual) {
		t.Fatal("E2ECapable said no for a device with a registered key")
	}

	dh, hello, err := e2e.StartDeviceHandshake(devicePriv, hostPriv.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	hostSession, response, err := m.E2ERespond(context.Background(), "d1", KeyOriginUsual, hello)
	if err != nil {
		t.Fatalf("E2ERespond: %v", err)
	}
	deviceSession, err := dh.Finish(response)
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}

	sealed := deviceSession.Seal([]byte("echo hi"))
	got, err := hostSession.Open(sealed)
	if err != nil || string(got) != "echo hi" {
		t.Fatalf("host opening what the device sealed = %q, %v", got, err)
	}
}

// A device's two keys are answered with independently: registering one says
// nothing about the other, in either direction, and a handshake against the
// wrong origin's key fails exactly as a device with no key at all does
// rather than completing with the wrong one.
func TestE2EOriginPicksTheRightKey(t *testing.T) {
	isolate(t)
	usualPriv, err := e2e.GenerateStaticKey()
	if err != nil {
		t.Fatal(err)
	}
	deskPriv, err := e2e.GenerateStaticKey()
	if err != nil {
		t.Fatal(err)
	}
	relay := newE2EFakeRelay(t, Device{
		ID: "d1", Name: "phone",
		PublicKey:     e2e.EncodePublicKey(usualPriv.PublicKey()),
		DeskPublicKey: e2e.EncodePublicKey(deskPriv.PublicKey()),
	})
	m := testManager(relay.URL)
	if err := m.EnsureE2EKey(context.Background()); err != nil {
		t.Fatal(err)
	}
	hostPriv, err := m.hostE2EIdentity()
	if err != nil {
		t.Fatal(err)
	}

	if !m.E2ECapable(context.Background(), "d1", KeyOriginUsual) {
		t.Error("E2ECapable said no for the usual origin with its own key registered")
	}
	if !m.E2ECapable(context.Background(), "d1", KeyOriginDesk) {
		t.Error("E2ECapable said no for the desk origin with its own key registered")
	}

	// A handshake against the desk origin completes only against the
	// device's desk key -- the device's own StartDeviceHandshake, run with
	// its desk private key, agreeing with what E2ERespond(..., KeyOriginDesk,
	// ...) derives.
	dh, hello, err := e2e.StartDeviceHandshake(deskPriv, hostPriv.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	hostSession, response, err := m.E2ERespond(context.Background(), "d1", KeyOriginDesk, hello)
	if err != nil {
		t.Fatalf("E2ERespond(KeyOriginDesk): %v", err)
	}
	deviceSession, err := dh.Finish(response)
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	sealed := deviceSession.Seal([]byte("echo hi"))
	if got, err := hostSession.Open(sealed); err != nil || string(got) != "echo hi" {
		t.Fatalf("host opening what the device sealed = %q, %v", got, err)
	}

	// Only one of them registered, the other origin is exactly the "no key
	// on file" case, not answered with the one that does exist.
	onlyUsual := newE2EFakeRelay(t, Device{ID: "d2", PublicKey: e2e.EncodePublicKey(usualPriv.PublicKey())})
	m2 := testManager(onlyUsual.URL)
	if !m2.E2ECapable(context.Background(), "d2", KeyOriginUsual) {
		t.Error("E2ECapable said no for the origin that does have a key")
	}
	if m2.E2ECapable(context.Background(), "d2", KeyOriginDesk) {
		t.Error("E2ECapable said yes for the origin with no key of its own")
	}
	if _, _, err := m2.E2ERespond(context.Background(), "d2", KeyOriginDesk, []byte("not a real hello")); err != ErrNoE2EKey {
		t.Errorf("E2ERespond(KeyOriginDesk) with no desk key = %v, want ErrNoE2EKey", err)
	}
}

// The roster is cached rather than fetched on every call, so opening several
// panes in quick succession costs the relay one call, not one per pane.
func TestE2ERosterIsCached(t *testing.T) {
	isolate(t)
	devicePriv, _ := e2e.GenerateStaticKey()
	relay := newE2EFakeRelay(t, Device{ID: "d1", PublicKey: e2e.EncodePublicKey(devicePriv.PublicKey())})
	m := testManager(relay.URL)
	old := e2eRosterTTL
	e2eRosterTTL = time.Hour
	t.Cleanup(func() { e2eRosterTTL = old })

	for i := 0; i < 5; i++ {
		if !m.E2ECapable(context.Background(), "d1", KeyOriginUsual) {
			t.Fatal("E2ECapable said no")
		}
	}
	relay.mu.Lock()
	calls := relay.devicesCalls
	relay.mu.Unlock()
	if calls != 1 {
		t.Fatalf("GET /api/v1/host/devices was called %d times, want 1", calls)
	}
}
