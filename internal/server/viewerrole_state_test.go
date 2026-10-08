package server

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/remote"
	"github.com/jmwri/flockdeck/internal/store"
)

func prefsPath(t *testing.T) string {
	t.Helper()
	dir, err := store.Dir()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "prefs.json")
}

// saveUnrelatedPref has the desk change a preference that has nothing to do
// with devices and waits for it to be saved.
func saveUnrelatedPref(t *testing.T, srv *Server, theme string) {
	t.Helper()
	desk := dialControl(t, srv)
	defer desk.CloseNow()
	sendCmd(t, desk, command{Cmd: "theme", Text: theme})
	var prefs prefsMsg
	readUntil(t, desk, "prefs", &prefs)
}

// The file that holds the roles being deleted while Flockdeck runs must not
// make every device full: the roles in force are the running program's.
func TestDeletingThePrefsFileDoesNotLiftARestriction(t *testing.T) {
	srv, _ := newTestServer(t)
	setAccess(t, srv, "viewer-dev", "viewer", false)
	if err := os.Remove(prefsPath(t)); err != nil {
		t.Fatal(err)
	}
	saveUnrelatedPref(t, srv, "light")
	if got := srv.deviceRole("viewer-dev"); got != store.RoleViewer {
		t.Fatalf("after the file was deleted and another setting saved, the device is %q, want viewer", got)
	}
	if got := srv.deviceRole("other"); got != store.RoleFull {
		t.Errorf("a device with no record is %q after the file was deleted, want full", got)
	}
	p, err := store.ReadPrefs()
	if err != nil || p.AccessFor("viewer-dev").EffectiveRole() != store.RoleViewer {
		t.Errorf("the roles were not written back: %+v, %v", p.Devices, err)
	}
}

// A file damaged while running is set aside and read as nothing; that must
// neither widen the roles nor make every other device a viewer.
func TestDamagingThePrefsFileDoesNotChangeAnyRole(t *testing.T) {
	srv, _ := newTestServer(t)
	setAccess(t, srv, "viewer-dev", "viewer", false)
	if err := os.WriteFile(prefsPath(t), []byte("{ not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	saveUnrelatedPref(t, srv, "light")
	if got := srv.deviceRole("viewer-dev"); got != store.RoleViewer {
		t.Errorf("the restricted device is %q after the file was damaged, want viewer", got)
	}
	if got := srv.deviceRole("other"); got != store.RoleFull {
		t.Errorf("another device is %q after the file was damaged, want full: the roles were known", got)
	}
}

// A restriction is applied whether or not it can be saved, and the notice
// says which. A folder where the preferences file should be makes it unreadable
// and unwritable.
func TestARestrictionIsAppliedEvenWhenItCannotBeSaved(t *testing.T) {
	srv, _ := newTestServer(t)
	listDevice(srv, "dev-1")
	path := prefsPath(t)
	_ = os.Remove(path)
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	desk := dialControl(t, srv)
	sendCmd(t, desk, command{Cmd: "setDeviceRole", ID: "dev-1", Kind: "viewer"})
	var note noticeMsg
	readUntil(t, desk, "notice", &note)
	if got := srv.deviceRole("dev-1"); got != store.RoleViewer {
		t.Fatalf("the restriction was not applied because it could not be saved: %q", got)
	}
	if !strings.Contains(note.Text, "applied now, but could not be saved") || !note.Error {
		t.Errorf("the notice reads %+v, want it to say it is applied now but could not be saved", note)
	}
	// And it can be changed again in the same state.
	sendCmd(t, desk, command{Cmd: "setDeviceRole", ID: "dev-1", Kind: "viewer", Watch: true})
	waitFor(t, func() bool { return srv.deviceAccess("dev-1").WatchPanes })
}

func TestARecordIsDroppedWhenADeviceIsUnpaired(t *testing.T) {
	srv, _ := newTestServer(t)
	setAccess(t, srv, "gone", "viewer", false)
	setAccess(t, srv, "kept", "viewer", false)
	srv.forgetRole("gone")
	waitFor(t, func() bool { return srv.deviceRole("gone") == store.RoleFull })
	if got := srv.deviceRole("kept"); got != store.RoleViewer {
		t.Errorf("another device's record went too: %q", got)
	}
	if p, _ := store.ReadPrefs(); len(p.Devices) != 1 {
		t.Errorf("saved records = %+v, want only the other device", p.Devices)
	}
}

// A device missing from a roster that was read is forgotten. A roster with no
// devices in it is what a proxy, a captive portal or a relay that has failed
// sends, and must lift nothing.
func TestOnlyANonEmptyRosterPrunesRoleRecords(t *testing.T) {
	srv, _ := newTestServer(t)
	setAccess(t, srv, "a", "viewer", false)
	setAccess(t, srv, "b", "viewer", true)

	srv.forgetRolesExcept(nil)
	srv.forgetRolesExcept([]remote.Device{})
	ask(srv, func() bool { return true }) // let the workspace goroutine run what was queued
	if srv.deviceRole("a") != store.RoleViewer || srv.deviceRole("b") != store.RoleViewer {
		t.Fatalf("an empty roster lifted a restriction: a=%q b=%q", srv.deviceRole("a"), srv.deviceRole("b"))
	}

	srv.forgetRolesExcept([]remote.Device{{ID: "b"}, {ID: "c"}})
	waitFor(t, func() bool { return srv.deviceRole("a") == store.RoleFull })
	if got := srv.deviceAccess("b"); got.EffectiveRole() != store.RoleViewer || !got.WatchPanes {
		t.Errorf("a device still on the roster lost its record: %+v", got)
	}
}

// A relay that cannot be asked is not a relay that says the device exists.
func TestARosterErrorLimitsNothing(t *testing.T) {
	srv, _ := newTestServer(t)
	setAccess(t, srv, "a", "viewer", false)
	tr := rosterOf(srv)
	tr.mu.Lock()
	tr.err = context.DeadlineExceeded
	tr.mu.Unlock()
	desk := dialControl(t, srv)
	sendCmd(t, desk, command{Cmd: "setDeviceRole", ID: "b", Kind: "viewer"})
	var note noticeMsg
	readUntil(t, desk, "notice", &note)
	if !note.Error {
		t.Errorf("a role set while the relay could not be asked was answered %+v", note)
	}
	if got := srv.deviceRole("b"); got != store.RoleFull {
		t.Errorf("a device was limited without the relay confirming it: %q", got)
	}
	if got := srv.deviceRole("a"); got != store.RoleViewer {
		t.Errorf("an existing record changed: %q", got)
	}
}

// Only a device the relay lists can be limited; a limit can be lifted from one
// it no longer lists.
func TestOnlyAListedDeviceCanBeLimited(t *testing.T) {
	srv, _ := newTestServer(t)
	listDevice(srv, "real")
	desk := dialControl(t, srv)
	sendCmd(t, desk, command{Cmd: "setDeviceRole", ID: "made-up", Kind: "viewer"})
	var note noticeMsg
	readUntil(t, desk, "notice", &note)
	if !note.Error || !strings.Contains(note.Text, "not paired") {
		t.Errorf("limiting an unlisted device was answered %+v", note)
	}
	if p, _ := store.ReadPrefs(); len(p.Devices) != 0 {
		t.Errorf("an unlisted device got a record: %+v", p.Devices)
	}

	setAccess(t, srv, "real", "viewer", false)
	tr := rosterOf(srv)
	tr.mu.Lock()
	tr.ids = nil
	tr.mu.Unlock()
	setAccess(t, srv, "real", "full", false)
	if p, _ := store.ReadPrefs(); len(p.Devices) != 0 {
		t.Errorf("a limit could not be lifted after the device left the roster: %+v", p.Devices)
	}
}

// A table that was never published fails closed.
func TestAnUnpublishedAccessTableIsAViewer(t *testing.T) {
	var s Server
	if got := s.deviceRole("any"); got != store.RoleViewer {
		t.Errorf("with no table a device is %q, want viewer", got)
	}
}

// The relay's ids are 16 characters of lower-case base32; any id it makes must
// be accepted, or the device could never be limited.
func TestTheRelaysDeviceIDsAreValid(t *testing.T) {
	for _, id := range []string{"mfrggzdfmztwq2lk", "a2b3c4d5e6f7g2h3", "dev-1", "Zm9v_YmFy-Q"} {
		if !validDeviceID(id) {
			t.Errorf("%q was refused as a device id", id)
		}
	}
	for _, id := range []string{"", "a b", "a/b", "a+b", "a=", `..\x`, strings.Repeat("a", 129)} {
		if validDeviceID(id) {
			t.Errorf("%q was accepted as a device id", id)
		}
	}
}

// pushDevice is a push subscription with real keys, so a message sealed to it
// is well formed.
func pushDevice(t *testing.T, id string) remote.PushDevice {
	t.Helper()
	priv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	auth := make([]byte, 16)
	_, _ = rand.Read(auth)
	enc := base64.RawURLEncoding
	return remote.PushDevice{DeviceID: id, P256dh: enc.EncodeToString(priv.PublicKey().Bytes()), Auth: enc.EncodeToString(auth)}
}

// A push names a pane, its project and a link to it, so it goes only where
// that could be seen: full devices and watchers. A viewer that sees artifacts
// only is sent nothing, and neither is any device while the roles are unknown.
func TestPushNotificationsReachOnlyDevicesThatMaySeePanes(t *testing.T) {
	srv, _ := newTestServer(t)
	devices := []string{"full-dev", "viewer-dev", "watch-dev", "new-dev"}
	setAccess(t, srv, "viewer-dev", "viewer", false)
	setAccess(t, srv, "watch-dev", "viewer", true)

	var mu sync.Mutex
	var posted []string
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/push/devices"):
			var list []remote.PushDevice
			for _, id := range devices {
				list = append(list, pushDevice(t, id))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"devices": list})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/notify"):
			var req struct {
				Messages []remote.PushMessage `json:"messages"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			mu.Lock()
			for _, m := range req.Messages {
				posted = append(posted, m.DeviceID)
			}
			mu.Unlock()
			_, _ = fmt.Fprintf(w, `{"sent":%d}`, len(req.Messages))
		default:
			http.NotFound(w, r)
		}
	}))
	defer relay.Close()
	srv.SetRemote(&fakeRemote{ok: true, st: remote.Status{Name: "desk", HostID: "h1"}, client: &remote.Client{Relay: relay.URL, Token: "fdh_test"}})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.notifyRelay(ctx, remote.Notification{Title: "api needs you", URL: "/d/h1/p1", Tag: "flockdeck-h1"}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	got := slices.Clone(posted)
	mu.Unlock()
	slices.Sort(got)
	if want := []string{"full-dev", "new-dev", "watch-dev"}; !slices.Equal(got, want) {
		t.Errorf("messages were sealed for %v, want %v", got, want)
	}

	// While the record of roles cannot be trusted every device is a viewer,
	// and a push for no named device goes nowhere.
	ask(srv, func() bool { srv.prefs.KeepAccessUnknown(); srv.publishAccess(); return true })
	for _, id := range append(devices, "") {
		if srv.mayPush(id) {
			t.Errorf("a push may go to %q while the roles are unknown", id)
		}
	}
}

// The wiring: unpairing a device from the desk drops its record, a roster that
// lists devices drops the records of the ones missing from it, and a roster
// that lists none drops nothing.
func TestTheRelaysAnswersReachTheRoleRecords(t *testing.T) {
	srv, _ := newTestServer(t)
	setAccess(t, srv, "gone", "viewer", false)
	setAccess(t, srv, "kept", "viewer", false)
	setAccess(t, srv, "vanished", "viewer", false)

	var mu sync.Mutex
	roster := []remote.Device{{ID: "gone"}, {ID: "kept"}, {ID: "vanished"}}
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/v1/host/devices/"):
			// The roster keeps listing it for now, as a relay that is slow to
			// forget would: the record has to go because it was unpaired here.
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/host/devices":
			_ = json.NewEncoder(w).Encode(map[string]any{"devices": roster, "hosts": []remote.Host{}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer relay.Close()
	srv.SetRemote(&fakeRemote{ok: true, st: remote.Status{Name: "desk", HostID: "h1", State: remote.StateConnected},
		client: &remote.Client{Relay: relay.URL, Token: "fdh_test"}})
	desk := dialControl(t, srv)
	defer desk.CloseNow()

	sendCmd(t, desk, command{Cmd: "remoteRevoke", ID: "gone"})
	waitFor(t, func() bool { return srv.deviceRole("gone") == store.RoleFull })
	if srv.deviceRole("kept") != store.RoleViewer {
		t.Error("unpairing one device dropped another's record")
	}

	// Nothing listed: a failed relay or a proxy in the way. Nothing dropped.
	mu.Lock()
	roster = nil
	mu.Unlock()
	var msg remoteDevicesMsg
	sendCmd(t, desk, command{Cmd: "remoteDevices"})
	readUntil(t, desk, "remoteDevices", &msg)
	ask(srv, func() bool { return true })
	time.Sleep(200 * time.Millisecond)
	if srv.deviceRole("kept") != store.RoleViewer || srv.deviceRole("vanished") != store.RoleViewer {
		t.Fatal("an empty roster lifted a restriction")
	}

	// Listed devices, one of them not a limited one: the limited ones missing
	// from it go.
	mu.Lock()
	roster = []remote.Device{{ID: "kept"}, {ID: "someone"}}
	mu.Unlock()
	sendCmd(t, desk, command{Cmd: "remoteDevices"})
	readUntil(t, desk, "remoteDevices", &msg)
	waitFor(t, func() bool { return srv.deviceRole("vanished") == store.RoleFull })
	if srv.deviceRole("kept") != store.RoleViewer {
		t.Error("a device still on the roster lost its record")
	}
}
